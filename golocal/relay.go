package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

var (
	cameraEnabledFlag   atomic.Bool
	cameraOverrideUntil atomic.Int64
	lastCookFrameNano   atomic.Int64
)

// cameraBridgeEnabled reports whether the stock-socket camera bridge should
// publish idle live-view frames. During a cook the stock cameracapturerd
// pipeline is busy with MediaCapturer and, for toast, FoodRecognizer's
// CookMonitorService; cook-time frames normally come from MediaCapturer's
// /1/videos/.../start_image uploads instead. If those uploads stop arriving
// (for example an older install whose MediaCapturer still pins June's cert),
// fall back to the bridge at a low rate so live view still works.
func cameraBridgeEnabled() bool {
	if !cameraViewerActive() {
		return false
	}
	if !cameraEnabledFlag.Load() {
		return true
	}
	last := lastCookFrameNano.Load()
	return last == 0 || time.Since(time.Unix(0, last)) > 8*time.Second
}

// cameraViewerActive reports whether something (Home Assistant, a test
// script, or another client) explicitly asked for live frames.
func cameraViewerActive() bool {
	return time.Now().UnixNano() < cameraOverrideUntil.Load()
}

// dammTable is the Damm check-digit table the app's SpongyCastleSrpVerifier uses.
var dammTable = [10][10]int{
	{0, 3, 1, 7, 5, 9, 8, 6, 4, 2},
	{7, 0, 9, 2, 1, 5, 4, 8, 6, 3},
	{4, 2, 0, 6, 8, 7, 1, 3, 5, 9},
	{1, 7, 5, 0, 9, 8, 3, 4, 2, 6},
	{6, 1, 2, 3, 0, 4, 5, 9, 7, 8},
	{3, 6, 7, 4, 2, 0, 9, 5, 8, 1},
	{5, 8, 6, 9, 7, 2, 0, 1, 3, 4},
	{8, 9, 4, 5, 3, 6, 2, 0, 1, 7},
	{9, 4, 3, 8, 6, 1, 7, 2, 0, 5},
	{2, 5, 8, 1, 4, 3, 6, 7, 9, 0},
}

func damm(digits string) int {
	interim := 0
	for _, ch := range digits {
		interim = dammTable[interim][int(ch-'0')]
	}
	return interim
}

func fullPinOverride(serverCode string) string {
	b, err := os.ReadFile("/tmp/june_full_pin_map.json")
	if err != nil {
		return serverCode
	}
	var m map[string]string
	if json.Unmarshal(b, &m) != nil {
		return serverCode
	}
	if v, ok := m[serverCode]; ok {
		return v
	}
	return serverCode
}

// expiresSeconds converts the ms-epoch expiry to whole seconds, bounded to a signed
// 32-bit int, because the oven's PairingPin.mExpires is a Java Integer.
func expiresSeconds(ms int64, context string) int64 {
	secs := ms / 1000
	if secs >= 1<<31 {
		logLine("[pair] %s: expires %ds overflows signed 32-bit — clamping to %d", context, secs, int64(1<<31-1))
		secs = 1<<31 - 1
	}
	return secs
}

func pinEnvelope(rec *Pairing) map[string]any {
	return map[string]any{
		"success": true,
		"pin": map[string]any{
			"code":       rec.Code,
			"expires":    expiresSeconds(rec.Expires, "oven POST response"),
			"request_id": rec.RequestID,
		},
	}
}

func issueToken() map[string]any {
	return map[string]any{
		"access_token":  randomToken(32),
		"refresh_token": randomToken(32),
		"expires_in":    31536000,
		"token_type":    "bearer",
		"scope":         "oven companion",
	}
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// describe parses a raw frame for logging/routing without re-marshaling it.
func describe(raw []byte) (map[string]any, string) {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Sprintf("non-JSON %dB", len(raw))
	}
	code, _ := obj["message_code"].(float64)
	name := codeNames[int64(code)]
	if name == "" {
		name = "?"
	}
	target, _ := obj["target"].(map[string]any)
	var tid string
	if target != nil {
		tid, _ = target["id"].(string)
	}
	dev, _ := obj["device_id"].(string)
	order, _ := obj["order"].(float64)
	return obj, fmt.Sprintf("code=%.0f %s order=%.0f dev=%s target=%s", code, name, order, dev, tid)
}

// relay forwards the exact bytes received. See README "byte-exact relay".
func relay(hub *Hub, peer *Peer, raw []byte) {
	obj, desc := describe(raw)
	hub.mu.Lock()
	hub.framesSeen++
	hub.mu.Unlock()
	if peer.role == "oven" {
		lastOvenFrameNano.Store(time.Now().UnixNano())
	}

	logLine("[%s->%s] %s", peer.role, counterRole(peer.role), desc)
	writeJSONL(map[string]any{"dir": peer.role, "path": peer.path, "raw": string(raw)})

	if obj != nil {
		if peer.role == "oven" {
			if dev, _ := obj["device_id"].(string); dev != "" {
				hub.setOvenID(dev)
			}
			code, _ := obj["message_code"].(float64)
			data, _ := obj["data"].(map[string]any)
			switch int64(code) {
			case 10018:
				if data != nil {
					if st, ok := data["state"].(string); ok && st == "idle" {
						cameraEnabledFlag.Store(false)
					}
					if b, err := json.Marshal(data); err == nil {
						hub.setDeviceState(b)
					}
				}
				hub.setDeviceStateRaw(raw)
			case 10013, 10014:
				cameraEnabledFlag.Store(true)
				if int64(code) == 10013 && data != nil {
					if b, err := json.Marshal(data); err == nil {
						hub.setCookingState(b)
					}
				}
				var plan any
				if data != nil {
					if p, ok := data["cook_plan_data"]; ok {
						plan = p
					} else if int64(code) == 10014 {
						plan = data
					}
				}
				if plan != nil {
					if b, err := json.Marshal(plan); err == nil {
						hub.setCookPlan(b)
					}
				}
			case 10017:
				cameraEnabledFlag.Store(false)
			case 10020:
				if data != nil {
					order, _ := data["request_order"].(float64)
					status, _ := data["status"].(string)
					noteAck(int64(order), status)
				}
			case 10026:
				capturePairingFromOven(hub, data)
			}
		} else {
			if dev, _ := obj["device_id"].(string); dev != "" {
				noteClientSeen(dev)
				ccode, _ := obj["message_code"].(float64)
				corder, _ := obj["order"].(float64)
				cmode := ""
				if cdata, _ := obj["data"].(map[string]any); cdata != nil {
					cmode, _ = cdata["primitive_type"].(string)
				}
				switch int64(ccode) {
				case 11001, 11002, 11004, 11005, 11006, 11007, 11009, 11010, 11013, 11015, 11016:
					noteCommand(int(ccode), int64(corder), dev, cmode)
				}
				hub.mu.Lock()
				if _, exists := hub.companionsInfo[dev]; !exists {
					name, _ := obj["device_name"].(string)
					if name == "" {
						name = "companion"
					}
					hub.companionsInfo[dev] = CompanionInfo{Name: name, Platform: "android", Version: "unknown"}
					hub.mu.Unlock()
					hub.saveCompanions()
				} else {
					hub.mu.Unlock()
				}
			}
		}
	}

	if hub.observeOnly {
		return
	}

	var code int64
	if obj != nil {
		if c, ok := obj["message_code"].(float64); ok {
			code = int64(c)
		}
	}

	targets := hub.targetsFor(peer)
	if len(targets) == 0 {
		role := "oven not connected"
		if peer.role != "oven" {
			role = "no companion connected"
		}
		logLine("[%s] no counterpart connected — frame dropped (%s)", peer.addr, role)
		return
	}
	forward := raw
	if peer.role == "oven" && obj != nil && code == 10011 && mediaURL != "" {
		forward = rewriteVideoFrameURL(raw, mediaURL)
	}
	for _, t := range targets {
		_ = t.sendRaw(forward)
	}
	// The real cloud reflects every oven message back to the sender.
	if peer.role == "oven" {
		_ = peer.sendRaw(raw)
	}
}

func rewriteVideoFrameURL(raw []byte, base string) []byte {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw
	}
	data, _ := obj["data"].(map[string]any)
	if data == nil {
		return raw
	}
	u, _ := data["image_url"].(string)
	if u == "" {
		return raw
	}
	parsed, err := url.Parse(u)
	if err != nil || parsed.Path == "" {
		return raw
	}
	data["image_url"] = strings.TrimRight(base, "/") + parsed.Path
	b, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return b
}

func counterRole(role string) string {
	if role == "oven" {
		return "companion"
	}
	return "oven"
}

func capturePairingFromOven(hub *Hub, data map[string]any) {
	if data == nil {
		return
	}
	ki, _ := data["key_info"].(map[string]any)
	pin, _ := data["pin"].(map[string]any)
	var code string
	if pin != nil {
		code, _ = pin["code"].(string)
	}
	var rec *Pairing
	if code != "" {
		rec = hub.findPairing(code)
	}
	if rec == nil {
		live := hub.livePairings()
		if len(live) == 1 {
			rec = live[0]
		}
	}
	if rec == nil {
		b, _ := json.Marshal(data)
		logLine("[pair] 10026 from oven but no matching pairing record: %s", truncate(string(b), 300))
		return
	}
	if ki != nil {
		if a, ok := ki["A"].(string); ok {
			hub.mu.Lock()
			rec.A = a
			hub.mu.Unlock()
			logLine("[pair] got oven SRP A for code %s (%d b64 chars)", rec.Code, len(a))
		}
		if oi, ok := ki["oven_info"].(string); ok {
			hub.mu.Lock()
			rec.OvenInfo = oi
			hub.mu.Unlock()
			logLine("[pair] got sealed oven_info for code %s — pairing complete oven-side", rec.Code)
			pushOvenInfoToCompanions(hub, rec)
		}
	}
}

func pushOvenInfoToCompanions(hub *Hub, rec *Pairing) {
	order := int(time.Now().UnixMilli()) & 0x7FFFFFFF
	push := map[string]any{
		"v":            2,
		"message_code": 10026,
		"order":        order,
		"time":         time.Now().UnixMilli(),
		"device_id":    hub.getOvenID(),
		"data": map[string]any{
			"pin": map[string]any{
				"code":       fullPinOverride(rec.Code),
				"expires":    expiresSeconds(rec.Expires, "companion oven_info push"),
				"request_id": rec.RequestID,
			},
			"key_info": map[string]any{"oven_info": rec.OvenInfo},
		},
	}
	for _, comp := range pairingTargets(hub, rec) {
		ok := comp.sendJSON(push)
		logLine("[pair] pushed oven_info to companion %s ok=%v device_id=%s", comp.addr, ok, hub.getOvenID())
	}
}

func pushVideoFrameToCompanions(hub *Hub, imageURL string) {
	frame := map[string]any{
		"v":            2,
		"message_code": 10011,
		"order":        int(time.Now().UnixMilli()) & 0x7FFFFFFF,
		"time":         time.Now().UnixMilli(),
		"device_id":    hub.getOvenID(),
		"data":         map[string]any{"image_url": imageURL},
	}
	b, err := json.Marshal(frame)
	if err != nil {
		return
	}
	for _, comp := range hub.snapshotCompanions() {
		_ = comp.sendRaw(b)
		logLine("[video] pushed 10011 image_url=%s to companion %s", imageURL, comp.addr)
	}
}

func pushAtoCompanions(hub *Hub, rec *Pairing) {
	push := map[string]any{
		"v":            2,
		"message_code": 10026,
		"order":        int(time.Now().UnixMilli()) & 0x7FFFFFFF,
		"time":         time.Now().UnixMilli(),
		"device_id":    hub.getOvenID(),
		"data": map[string]any{
			"pin": map[string]any{
				"code":       fullPinOverride(rec.Code),
				"expires":    expiresSeconds(rec.Expires, "companion A push"),
				"request_id": rec.RequestID,
			},
			"key_info": map[string]any{"A": rec.A},
		},
	}
	for _, comp := range pairingTargets(hub, rec) {
		ok := comp.sendJSON(push)
		logLine("[pair] pushed A to companion %s ok=%v device_id=%s", comp.addr, ok, hub.getOvenID())
	}
}

// pairingTargets returns the companions that own the pairing session. Older
// builds broadcast to every companion, which cross-talked two simultaneous
// pairings (phone app + Home Assistant). Match the REST requester's IP to the
// WebSocket peer; fall back to broadcast only when no peer matches.
func pairingTargets(hub *Hub, rec *Pairing) []*Peer {
	comps := hub.snapshotCompanions()
	if rec.RequesterAddr == "" || len(comps) == 0 {
		return comps
	}
	matched := make([]*Peer, 0, 1)
	for _, comp := range comps {
		if sameHost(comp.addr, rec.RequesterAddr) {
			matched = append(matched, comp)
		}
	}
	if len(matched) == 0 {
		logLine("[pair] no companion from %s; broadcasting to %d companion(s)", rec.RequesterAddr, len(comps))
		return comps
	}
	return matched
}

func sameHost(a, b string) bool {
	ha, _, errA := net.SplitHostPort(a)
	hb, _, errB := net.SplitHostPort(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return ha == hb
}

// ownerUserID returns a stable local user id for the owner-link, deterministic per
// email so restarts do not mint a new identity.
func ownerUserID(email string) string {
	if email == "" {
		return newUUID()
	}
	h := sha256.Sum256([]byte("june-local-owner:" + email))
	return fmt.Sprintf("%x", h[:16])
}

func pushPairingToOven(hub *Hub, rec *Pairing) {
	if hub.oven == nil {
		logLine("[pair] push skipped: oven socket not connected")
		return
	}
	ki := map[string]any{}
	if rec.B != "" {
		ki["B"] = rec.B
	}
	if rec.Salt != "" {
		ki["salt"] = rec.Salt
	}
	if rec.CompanionInfo != "" {
		ki["companion_info"] = rec.CompanionInfo
	}
	frame := map[string]any{
		"v":            2,
		"message_code": 11025,
		"order":        int(time.Now().UnixMilli()) & 0x7FFFFFFF,
		"time":         time.Now().UnixMilli(),
		"signature":    "",
		"device_name":  "cloud",
		"device_id":    "cloud",
		"data": map[string]any{
			"pin": map[string]any{
				"code":       rec.Code,
				"expires":    expiresSeconds(rec.Expires, "oven push"),
				"request_id": rec.RequestID,
			},
			"key_info": ki,
		},
		"target": map[string]any{"id": hub.getOvenID()},
	}
	logLine("[pair] push 10026 -> oven for pin %s expires=%ds", rec.Code, expiresSeconds(rec.Expires, "oven push"))
	_ = hub.oven.sendJSON(frame)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
