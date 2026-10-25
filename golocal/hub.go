package main

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Codes the app does not signature-check (pairing/video are cloud-signed or unsigned).
var unverifiedCodes = map[int64]bool{
	10026: true, 10027: true, 10011: true, 10010: true, 10022: true,
}

var codeNames = map[int64]string{
	10011: "VideoFrame", 10012: "Notification", 10013: "CookingState",
	10014: "CookPlanStarted", 10017: "CookPlanEnded", 10018: "DeviceState",
	10020: "Acknowledgement", 10022: "Unpaired", 10026: "PairingInfo",
	10027: "PairingSessionInvalidated", 10029: "RecipeSessionEnded",
	10033: "CookAdjustmentCandidate", 10034: "CookAdjustmentOfferDismissed",
	11002: "Preheat", 11004: "CancelCooking", 11005: "SetTemperatureCavity",
	11006: "SetTimer", 11007: "SetTemperatureTargetProbe", 11011: "Ping",
	11012: "Unpair", 11013: "KeepCooking", 11015: "SetCookMode",
	11016: "StartCookPlan", 11017: "SetRecipeState", 11018: "SetCookPlanPrefs",
	11019: "SetTemperatureAccessory", 11031: "MidCookCustomization",
	11032: "PostCookCustomization", 11035: "CookAdjustmentOfferResponse",
	12001: "UploadOvenBugReport",
}

// Hub holds connected sockets plus pairing/session state. One oven, any companions.
type Hub struct {
	mu                 sync.Mutex
	oven               *Peer
	companions         []*Peer
	companionsInfo     map[string]CompanionInfo
	associations       map[string]string
	pairings           map[string]*Pairing
	devices            map[string]DeviceInfo
	lastStatus         StatusSnapshot
	lastDeviceStateRaw []byte
	lastCookingState   json.RawMessage
	ovenID             string
	framesSeen         int
	observeOnly        bool
	companionState     string
}

type CompanionInfo struct {
	Name            string `json:"name"`
	Platform        string `json:"platform"`
	PlatformVersion string `json:"platform_version"`
	Version         string `json:"version"`
}

type DeviceInfo struct {
	Token any    `json:"token"`
	Name  string `json:"name"`
}

type StatusSnapshot struct {
	ConnectionState string          `json:"connection_state"`
	DeviceState     json.RawMessage `json:"device_state"`
	CookPlan        json.RawMessage `json:"cook_plan"`
	CookingState    json.RawMessage `json:"cooking_state,omitempty"`
	DeviceStateAt   time.Time       `json:"-"`
	CookEndedAt     time.Time       `json:"-"`
}

type Pairing struct {
	Code          string
	RequestID     string
	Expires       int64 // epoch ms
	RequesterAddr string
	A             string
	B             string
	Salt          string
	CompanionInfo string
	OvenInfo      string
	Created       float64
}

// Peer is one connected WebSocket.
type Peer struct {
	ws       *wsConn
	addr     string
	path     string
	role     string
	hub      *Hub
	deviceID string
	alive    bool
}

func newHub(observeOnly bool, companionState string) *Hub {
	h := &Hub{
		companionsInfo: map[string]CompanionInfo{},
		associations:   map[string]string{},
		pairings:       map[string]*Pairing{},
		devices:        map[string]DeviceInfo{},
		observeOnly:    observeOnly,
		companionState: companionState,
		// The oven identifies itself with the first signed 10018 frame; use
		// --oven-id only when a deployment needs a value before that.
		ovenID: "",
	}
	h.lastStatus.ConnectionState = "disconnected"
	h.lastStatus.DeviceState = json.RawMessage(`{"state":"idle"}`)
	h.lastStatus.CookPlan = json.RawMessage("null")
	h.loadCompanions()
	h.loadAssociations()
	return h
}

func (h *Hub) loadCompanions() {
	if h.companionState == "" {
		return
	}
	b, err := os.ReadFile(h.companionState)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &h.companionsInfo)
}

func (h *Hub) saveCompanions() {
	if h.companionState == "" {
		return
	}
	b, err := json.MarshalIndent(h.companionsInfo, "", " ")
	if err != nil {
		return
	}
	tmp := h.companionState + ".tmp"
	_ = os.WriteFile(tmp, b, 0o644)
	_ = os.Rename(tmp, h.companionState)
}

// Associations persist the oven <-> companion links created by the oven's
// POST /2/devices/{id}/associate call. They live in a separate file so the
// pre-seeded companions_state.json format stays compatible.
func (h *Hub) assocStatePath() string {
	if h.companionState == "" {
		return ""
	}
	return h.companionState + ".associations.json"
}

func (h *Hub) loadAssociations() {
	p := h.assocStatePath()
	if p == "" {
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &h.associations)
}

func (h *Hub) saveAssociations() {
	p := h.assocStatePath()
	if p == "" {
		return
	}
	h.mu.Lock()
	b, err := json.MarshalIndent(h.associations, "", " ")
	h.mu.Unlock()
	if err != nil {
		return
	}
	tmp := p + ".tmp"
	_ = os.WriteFile(tmp, b, 0o644)
	_ = os.Rename(tmp, p)
}

func (h *Hub) associate(deviceID, companionID string) {
	if deviceID == "" || companionID == "" {
		return
	}
	h.mu.Lock()
	if h.associations == nil {
		h.associations = map[string]string{}
	}
	h.associations[deviceID] = companionID
	h.mu.Unlock()
	h.saveAssociations()
}

func (h *Hub) attach(p *Peer) {
	h.mu.Lock()
	var replay []byte
	if p.role == "oven" {
		if h.oven != nil {
			logLine("[hub] NOTE second oven-side socket from %s; replacing the previous one", p.addr)
		}
		h.oven = p
		h.lastStatus.ConnectionState = "connected"
	} else {
		h.companions = append(h.companions, p)
		if h.oven != nil {
			replay = h.lastDeviceStateRaw
		}
	}
	logLine("[hub] + %s %s path=%s (oven=%v companions=%d)", p.role, p.addr, p.path, h.oven != nil, len(h.companions))
	h.mu.Unlock()

	if replay != nil {
		_ = p.sendRaw(replay)
		logLine("[hub] replayed cached DeviceState (%dB) to companion %s", len(replay), p.addr)
	}
}

func (h *Hub) detach(p *Peer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.oven == p {
		h.oven = nil
		h.lastStatus.ConnectionState = "disconnected"
		h.endCookLocked()
		// A dropped oven socket can lose the 10018 idle / 10017 end frames.
		// Fail safe: do not let the camera bridge stay off (or a future
		// writer think a cook is still active) after the oven goes away.
		cameraEnabledFlag.Store(false)
	}
	for i, c := range h.companions {
		if c == p {
			h.companions = append(h.companions[:i], h.companions[i+1:]...)
			break
		}
	}
	logLine("[hub] - %s %s", p.role, p.addr)
}

func (h *Hub) targetsFor(p *Peer) []*Peer {
	h.mu.Lock()
	defer h.mu.Unlock()
	if p.role == "oven" {
		out := make([]*Peer, len(h.companions))
		copy(out, h.companions)
		return out
	}
	if h.oven != nil {
		return []*Peer{h.oven}
	}
	return nil
}

func (h *Hub) snapshotCompanions() []*Peer {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*Peer, len(h.companions))
	copy(out, h.companions)
	return out
}

func (h *Hub) newPairing(ttlSeconds int) *Pairing {
	code := fmt.Sprintf("%05d", randInt(100000))
	rec := &Pairing{
		Code:      code,
		RequestID: newUUID(),
		Expires:   time.Now().Add(time.Duration(ttlSeconds) * time.Second).UnixMilli(),
		Created:   float64(time.Now().UnixNano()) / 1e9,
	}
	h.mu.Lock()
	h.pairings[code] = rec
	h.mu.Unlock()
	return rec
}

func (h *Hub) findPairing(pin string) *Pairing {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.pairings[pin]; ok {
		return r
	}
	if len(pin) > 5 {
		return h.pairings[pin[:5]]
	}
	return nil
}

func (h *Hub) livePairings() []*Pairing {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := float64(time.Now().UnixNano()) / 1e9
	var out []*Pairing
	for _, r := range h.pairings {
		if float64(r.Expires)/1000 > now {
			out = append(out, r)
		}
	}
	return out
}

func (h *Hub) setOvenID(id string) {
	if id == "" {
		return
	}
	h.mu.Lock()
	h.ovenID = id
	h.mu.Unlock()
}

func (h *Hub) getOvenID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ovenID
}

func (h *Hub) setDeviceState(raw json.RawMessage) {
	h.mu.Lock()
	h.lastStatus.DeviceState = raw
	h.lastStatus.DeviceStateAt = time.Now()
	h.mu.Unlock()
}

func (h *Hub) setCookingState(raw json.RawMessage) {
	h.mu.Lock()
	h.lastStatus.CookingState = raw
	h.lastCookingState = raw
	h.mu.Unlock()
}

// endCook marks the cached cook as finished (10017, 10018 idle, or the oven
// going away) and drops its live telemetry. The plan is kept for the last-cook
// view and for /1/messaging/device/.../status.
func (h *Hub) endCook() {
	h.mu.Lock()
	h.endCookLocked()
	h.mu.Unlock()
}

func (h *Hub) endCookLocked() {
	h.lastStatus.CookingState = nil
	h.lastCookingState = nil
	if h.lastStatus.CookEndedAt.IsZero() {
		h.lastStatus.CookEndedAt = time.Now()
	}
}

func (h *Hub) applyFixture(connection string, device, plan, cooking json.RawMessage) {
	h.mu.Lock()
	if connection != "" {
		h.lastStatus.ConnectionState = connection
	}
	if len(device) > 0 && string(device) != "null" {
		h.lastStatus.DeviceState = device
		h.lastStatus.DeviceStateAt = time.Now()
	}
	if len(plan) > 0 {
		h.lastStatus.CookPlan = plan
	}
	if len(cooking) > 0 && string(cooking) != "null" {
		h.lastStatus.CookingState = cooking
		h.lastCookingState = cooking
	}
	h.mu.Unlock()
	if connection == "connected" {
		lastOvenFrameNano.Store(time.Now().UnixNano())
	}
}

func (h *Hub) setDeviceStateRaw(raw []byte) {
	h.mu.Lock()
	h.lastDeviceStateRaw = raw
	h.mu.Unlock()
}

func (h *Hub) setCookPlan(raw json.RawMessage) {
	h.mu.Lock()
	h.lastStatus.CookPlan = raw
	h.lastStatus.CookEndedAt = time.Time{}
	h.mu.Unlock()
}

func (h *Hub) statusSnapshot() StatusSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastStatus
}

// randInt returns a crypto-random integer in [0,n).
func randInt(n int) int {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return int(time.Now().UnixNano() % int64(n))
	}
	return int(binary.BigEndian.Uint64(b[:]) % uint64(n))
}

func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
