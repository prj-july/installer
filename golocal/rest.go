package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func writeJSON(w http.ResponseWriter, status int, obj any) {
	b, err := json.Marshal(obj)
	if err != nil {
		http.Error(w, "marshal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Connection", "close")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func handleREST(hub *Hub, cfg *Config, w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	parts := []string{}
	for _, p := range strings.Split(path, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}

	bodyBytes, _ := io.ReadAll(r.Body)
	body := string(bodyBytes)
	mediaUpload := r.Method == "POST" &&
		(path == "/internal/video" || (len(parts) >= 3 && parts[0] == "1" && parts[1] == "videos"))
	// camerad polls this while it waits for a viewer; keep it out of the
	// request log and capture JSONL so a quiet oven stays quiet.
	quietPoll := r.Method == "GET" && path == "/internal/camera-bridge-enabled"

	if !quietPoll {
		noteHAUserAgent(r.UserAgent())
		logLine("[rest] %s %s ua=%s", r.Method, path, r.UserAgent())
		if mediaUpload {
			logLine("[rest]   body: <%d bytes binary>", len(bodyBytes))
		} else if body != "" {
			logLine("[rest]   body: %s", truncate(body, 2000))
		}
		loggedBody := body
		if mediaUpload {
			loggedBody = fmt.Sprintf("<%d bytes binary>", len(bodyBytes))
		}
		writeJSONL(map[string]any{
			"dir": "rest", "method": r.Method, "path": path,
			"headers": headerMap(r.Header), "body": loggedBody,
		})
	}

	jbody := func() map[string]any {
		var m map[string]any
		_ = json.Unmarshal([]byte(body), &m)
		if m == nil {
			return map[string]any{}
		}
		return m
	}

	// Human/phone-friendly local-mode verification. Normalize the trailing slash
	// so /local/ does not fall through to the oven REST catch-all below.
	if r.Method == http.MethodGet && (path == "/local/" || path == "/local/status/" || path == "/local/ca.crt/") {
		http.Redirect(w, r, strings.TrimSuffix(path, "/"), http.StatusMovedPermanently)
		return
	}
	if r.Method == "GET" && path == "/local/status" {
		handleLocalStatus(hub, cfg, w, r)
		return
	}
	if r.Method == "GET" && (path == "/local/cook" || path == "/local/cook.json" || path == "/local/cook.html") {
		handleLocalCook(hub, w, r)
		return
	}
	if r.Method == "GET" && path == "/local" {
		handleLocalPage(hub, cfg, w, r)
		return
	}
	if r.Method == "GET" && path == "/local/ca.crt" {
		handleLocalCA(cfg, w, r)
		return
	}

	// Media / live-view JPEG
	if r.Method == "GET" && len(parts) == 2 && parts[0] == "media" && parts[1] == "view" {
		serveCameraView(w)
		return
	}
	if len(parts) >= 2 && parts[0] == "media" && parts[1] == "stream.mjpg" {
		serveMJPEG(w, r, cfg)
		return
	}
	if len(parts) >= 3 && parts[0] == "media" {
		serveFromDir(w, cfg.MediaDir, parts[1:], "image/jpeg")
		return
	}

	// POST /internal/video — local capture daemon publishes a JPEG and asks the
	// relay to push a 10011 VideoFrame to connected companions.
	if r.Method == "POST" && len(parts) == 2 && parts[0] == "internal" && parts[1] == "video" {
		imageURL, err := publishVideoJPEG(hub, cfg, bodyBytes)
		if err != nil {
			writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "image_url": imageURL})
		return
	}

	// POST /1/videos/{sessionId}/start_image — MediaCapturer's cook-time
	// still. Patching MediaCapturer's certificate pinning makes these arrive
	// at june-local instead of June's dead cloud; saving and rebroadcasting
	// them gives the companion and Home Assistant live frames during a cook
	// without adding another cameracapturerd client.
	if r.Method == "POST" && len(parts) == 4 && parts[0] == "1" && parts[1] == "videos" && parts[3] == "start_image" {
		jpeg, err := multipartJPEG(r.Header.Get("Content-Type"), bodyBytes)
		if err != nil {
			logLine("[video] start_image %s rejected: %v", parts[2], err)
			writeJSON(w, 400, map[string]any{"success": false, "error": err.Error()})
			return
		}
		imageURL, err := publishVideoJPEG(hub, cfg, jpeg)
		if err != nil {
			writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
			return
		}
		lastCookFrameNano.Store(time.Now().UnixNano())
		logLine("[video] start_image session=%s bytes=%d image_url=%s", parts[2], len(jpeg), imageURL)
		writeJSON(w, 200, map[string]any{"success": true, "image_url": imageURL})
		return
	}

	// Camera trigger control (camerad polls the enabled endpoint).
	if r.Method == "GET" && len(parts) == 2 && parts[0] == "internal" && parts[1] == "camera-enabled" {
		on := cameraEnabledFlag.Load() || time.Now().UnixNano() < cameraOverrideUntil.Load()
		lightFollowCamera(on)
		if on {
			w.WriteHeader(200)
			_, _ = w.Write([]byte("1"))
		} else {
			w.WriteHeader(200)
			_, _ = w.Write([]byte("0"))
		}
		return
	}

	// Camera bridge gate. camerad uses this rather than camera-enabled: the
	// bridge may only run when a viewer asked for frames and no cook is
	// active. During a cook the stock camera belongs to MediaCapturer and
	// FoodRecognizer, and cook-time frames arrive through start_image.
	if r.Method == "GET" && len(parts) == 2 && parts[0] == "internal" && parts[1] == "camera-bridge-enabled" {
		on := cameraBridgeEnabled()
		lightFollowCamera(on)
		if on {
			w.WriteHeader(200)
			_, _ = w.Write([]byte("1"))
		} else {
			w.WriteHeader(200)
			_, _ = w.Write([]byte("0"))
		}
		return
	}
	if r.Method == "GET" && len(parts) == 2 && parts[0] == "internal" && parts[1] == "camera-state" {
		writeJSON(w, 200, map[string]any{
			"cook_active": cameraEnabledFlag.Load(),
			"viewer":      cameraViewerActive(),
			"bridge":      cameraBridgeEnabled(),
		})
		return
	}
	if r.Method == "POST" && len(parts) == 3 && parts[0] == "internal" && parts[1] == "camera" {
		switch parts[2] {
		case "on":
			d := 60
			if s := r.URL.Query().Get("seconds"); s != "" {
				if n, err := strconv.Atoi(s); err == nil && n > 0 {
					d = n
				}
			}
			cameraOverrideUntil.Store(time.Now().Add(time.Duration(d) * time.Second).UnixNano())
			setInteriorLight(true)
			writeJSON(w, 200, map[string]any{"success": true, "seconds": d})
			return
		case "off":
			cameraOverrideUntil.Store(0)
			setInteriorLight(false)
			writeJSON(w, 200, map[string]any{"success": true})
			return
		}
	}
	if r.Method == "POST" && len(parts) == 3 && parts[0] == "internal" && parts[1] == "camera" && parts[2] == "calibration" {
		var c map[string]any
		if err := json.Unmarshal(bodyBytes, &c); err != nil {
			writeJSON(w, 400, map[string]any{"success": false, "error": err.Error()})
			return
		}
		b, err := json.Marshal(c)
		if err != nil {
			writeJSON(w, 500, map[string]any{"success": false, "error": err.Error()})
			return
		}
		if err := os.MkdirAll("/data/june", 0o755); err == nil {
			_ = os.WriteFile("/data/june/camerad.conf", b, 0o644)
		}
		writeJSON(w, 200, map[string]any{"success": true, "config": c})
		return
	}

	// POST /2/devices/register
	if r.Method == "POST" && len(parts) >= 3 && parts[0] == "2" && parts[1] == "devices" && parts[2] == "register" {
		d := jbody()
		devID, _ := d["device_id"].(string)
		if devID == "" {
			devID = newUUID()[:32]
		}
		tok := issueToken()
		name, _ := d["device_name"].(string)
		if name == "" {
			name = "companion"
		}
		platform, _ := d["platform"].(string)
		if platform == "" {
			platform = "android"
		}
		pv, _ := d["platform_version"].(string)
		ver, _ := d["version"].(string)
		if ver == "" {
			ver = "unknown"
		}
		hub.mu.Lock()
		hub.devices[devID] = DeviceInfo{Token: tok, Name: name}
		hub.companionsInfo[devID] = CompanionInfo{Name: name, Platform: platform, PlatformVersion: pv, Version: ver}
		hub.mu.Unlock()
		hub.saveCompanions()
		logLine("[rest] registered device %s", devID)
		writeJSON(w, 200, map[string]any{"success": true, "token": tok, "device_id": devID})
		return
	}

	// GET /2/auth/oauth/token — bare AuthToken shape
	if len(parts) >= 3 && parts[0] == "2" && parts[1] == "auth" && parts[2] == "oauth" {
		writeJSON(w, 200, issueToken())
		return
	}

	// POST /2/devices/pairing
	if r.Method == "POST" && len(parts) == 3 && parts[0] == "2" && parts[1] == "devices" && parts[2] == "pairing" {
		rec := hub.newPairing(300)
		hub.mu.Lock()
		rec.RequesterAddr = r.RemoteAddr
		hub.mu.Unlock()
		full := rec.Code + fmt.Sprintf("%02d", randInt(100))
		full += fmt.Sprintf("%d", damm(full))
		logLine("[pair] minted server code %s (companion displays e.g. %s)", rec.Code, full)
		writeJSON(w, 200, pinEnvelope(rec))
		return
	}

	// POST/DELETE /2/devices/pairing/{pin}/companion
	if len(parts) == 5 && parts[0] == "2" && parts[1] == "devices" && parts[2] == "pairing" && parts[4] == "companion" {
		pin := parts[3]
		rec := hub.findPairing(pin)
		if rec == nil {
			writeJSON(w, 404, map[string]any{"success": false, "error": "no such pairing"})
			return
		}
		if r.Method == "DELETE" {
			logLine("[pair] companion closed pairing %s", pin)
			writeJSON(w, 200, map[string]any{"success": true})
			return
		}
		d := jbody()
		ki, _ := d["key_info"].(map[string]any)
		if ki != nil {
			hub.mu.Lock()
			rec.B, _ = ki["B"].(string)
			rec.Salt, _ = ki["salt"].(string)
			rec.CompanionInfo, _ = ki["companion_info"].(string)
			hub.mu.Unlock()
		}
		logLine("[pair] companion posted B/salt/companion_info for %s", pin)
		pushPairingToOven(hub, rec)
		if rec.OvenInfo != "" {
			writeJSON(w, 200, map[string]any{"success": true, "key_info": map[string]any{"oven_info": rec.OvenInfo}})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true})
		return
	}

	// POST /2/devices/pairing/{pin}/oven
	if r.Method == "POST" && len(parts) == 5 && parts[0] == "2" && parts[1] == "devices" && parts[2] == "pairing" && parts[4] == "oven" {
		pin := parts[3]
		rec := hub.findPairing(pin)
		if rec == nil {
			logLine("[pair] oven posted for unknown pin %s; answering pin-success anyway", pin)
			writeJSON(w, 200, map[string]any{"success": true})
			return
		}
		ki, _ := jbody()["key_info"].(map[string]any)
		if ki != nil {
			if oi, ok := ki["oven_info"].(string); ok && oi != "" {
				hub.mu.Lock()
				rec.OvenInfo = oi
				hub.mu.Unlock()
				logLine("[pair] got sealed oven_info for code %s — pairing complete oven-side (REST)", rec.Code)
				pushOvenInfoToCompanions(hub, rec)
				writeJSON(w, 200, pinEnvelope(rec))
				return
			}
			if a, ok := ki["A"].(string); ok {
				hub.mu.Lock()
				rec.A = a
				hub.mu.Unlock()
				logLine("[pair] got oven SRP A for code %s (%d b64 chars)", rec.Code, len(a))
				pushAtoCompanions(hub, rec)
			}
		}
		writeJSON(w, 200, pinEnvelope(rec))
		return
	}

	// GET /2/devices/pairing/{pin} — poll fallback
	if r.Method == "GET" && len(parts) == 4 && parts[0] == "2" && parts[1] == "devices" && parts[2] == "pairing" {
		rec := hub.findPairing(parts[3])
		if rec == nil {
			writeJSON(w, 404, map[string]any{"success": false})
			return
		}
		out := pinEnvelope(rec)
		if rec.B != "" && rec.Salt != "" && rec.CompanionInfo != "" {
			out["key_info"] = map[string]any{"salt": rec.Salt, "B": rec.B, "companion_info": rec.CompanionInfo}
		}
		writeJSON(w, 200, out)
		return
	}

	// POST /2/devices/{id}/associate — persist the oven <-> companion link
	if r.Method == "POST" && len(parts) >= 4 && parts[0] == "2" && parts[1] == "devices" && parts[3] == "associate" {
		devID := parts[2]
		companionID, _ := jbody()["companion_id"].(string)
		if companionID != "" {
			hub.associate(devID, companionID)
			logLine("[pair] associated device %s with companion %s", devID, companionID)
		}
		writeJSON(w, 200, map[string]any{"success": true})
		return
	}

	// GET /2/devices/{id}/associated — direction-aware
	if r.Method == "GET" && len(parts) >= 3 && parts[0] == "2" && parts[1] == "devices" && parts[len(parts)-1] == "associated" {
		requester := parts[2]
		var devices []map[string]any
		hub.mu.Lock()
		if requester == hub.ovenID {
			// The oven loads its trusted-companion roster from this response
			// at every boot, so it must list every companion.  Keeping only
			// the last association (the phone that paired most recently)
			// hides the others and the oven then rejects them with "No
			// paired companion found"; pairing a second phone on 2026-10-11
			// left HA and every other phone locked out after the next
			// reboot.  hub.associations still records the last link for
			// reference, but it no longer filters this list.
			for cid, info := range hub.companionsInfo {
				name := info.Name
				if name == "" {
					name = "companion"
				}
				platform := info.Platform
				if platform == "" {
					platform = "android"
				}
				ver := info.Version
				if ver == "" {
					ver = "unknown"
				}
				devices = append(devices, map[string]any{
					"device_id": cid, "companion_id": cid, "name": name, "platform": platform,
					"platform_version": info.PlatformVersion, "version": ver,
				})
			}
		} else {
			if hub.ovenID != "" {
				devices = append(devices, map[string]any{
					"device_id": hub.ovenID, "oven_id": hub.ovenID, "name": "June Oven", "model": "unknown",
					"model_number": "meerkat", "serial": "unknown", "version": "local",
				})
			}
		}
		hub.mu.Unlock()
		writeJSON(w, 200, map[string]any{"success": true, "devices": devices})
		return
	}

	// GET /1/messaging/device/{ovenId}/status
	if r.Method == "GET" && len(parts) >= 3 && parts[0] == "1" && parts[1] == "messaging" && parts[2] == "device" && parts[len(parts)-1] == "status" {
		st := hub.statusSnapshot()
		online := st.ConnectionState == "connected"
		haConn := "offline"
		if online {
			haConn = "online"
		}
		dev := json.RawMessage(st.DeviceState)
		if len(dev) == 0 {
			dev = json.RawMessage("null")
		}
		devData := json.RawMessage(`{"data":` + string(dev) + `}`)
		cook := json.RawMessage(st.CookPlan)
		if len(cook) == 0 {
			cook = json.RawMessage("null")
		}
		cookData := json.RawMessage("null")
		if string(cook) != "null" {
			cookData = json.RawMessage(`{"data":` + string(cook) + `}`)
		}
		out := map[string]any{
			"connection_state":    st.ConnectionState,
			"online":              online,
			"ha_connection_state": haConn,
			"device_state":        dev,
			"device_state_data":   devData,
			"cook_plan":           cook,
			"cook_plan_data":      cookData,
			"success":             true,
		}
		writeJSON(w, 200, out)
		return
	}

	// POST /2/devices/{companionId}/user — owner link
	if r.Method == "POST" && len(parts) >= 3 && parts[0] == "2" && parts[1] == "devices" && parts[len(parts)-1] == "user" {
		d := jbody()
		email, _ := d["email"].(string)
		email = strings.ToLower(strings.TrimSpace(email))
		uid := ownerUserID(email)
		logLine("[rest] owner link email=%s -> user %s", email, uid)
		writeJSON(w, 200, map[string]any{"success": true, "user": map[string]any{"id": uid}})
		return
	}

	// GET /2/devices/{ovenId}/features
	if r.Method == "GET" && len(parts) >= 3 && parts[0] == "2" && parts[1] == "devices" && parts[len(parts)-1] == "features" {
		writeJSON(w, 200, map[string]any{"success": true, "oven_features": []any{}})
		return
	}

	// GET /2/devices/{ovenId}/info
	if r.Method == "GET" && len(parts) >= 3 && parts[0] == "2" && parts[1] == "devices" && parts[len(parts)-1] == "info" {
		writeJSON(w, 200, map[string]any{"success": true, "data": map[string]any{
			"temperature_unit": "F", "primitive_presets": []any{},
		}})
		return
	}

	// GET /2/devices/{companionId}/settings
	if r.Method == "GET" && len(parts) >= 3 && parts[0] == "2" && parts[1] == "devices" && parts[len(parts)-1] == "settings" {
		writeJSON(w, 200, map[string]any{
			"success": true, "notify_preheat_finished": true,
			"notify_cook_finished": true, "notify_cook_almost_finished": true,
		})
		return
	}

	// GET /2/purchase/features|products
	if r.Method == "GET" && len(parts) >= 2 && parts[0] == "2" && parts[1] == "purchase" {
		nowMs := time.Now().UnixMilli()
		if parts[len(parts)-1] == "features" {
			writeJSON(w, 200, map[string]any{"success": true, "features": []any{
				map[string]any{
					"feature": "premium", "product_id": "june_premium_local",
					"transaction_id": "local-txn-0001", "start_date": nowMs,
					"end_date": nowMs + 10*365*24*3600*1000,
					"oven_id":  hub.getOvenID(), "buyer_id": "local-owner",
				},
			}})
			return
		}
		if parts[len(parts)-1] == "products" {
			writeJSON(w, 200, map[string]any{
				"description": "Your first three months are complimentary.",
				"product_info": []any{
					map[string]any{"description": "{{price}}/month after trial ends (VISIBLE INTERNAL ONLY)", "feature": "premium", "product_id": "com.junelife.local.subscription.premium.month"},
					map[string]any{"description": "{{price}}/year after trial ends (VISIBLE INTERNAL ONLY)", "feature": "premium", "product_id": "com.junelife.local.subscription.premium.year"},
					map[string]any{"description": "{{price}}/month after trial ends", "feature": "premium", "product_id": "com.junelife.subscription.premium.month"},
				},
				"products": []string{
					"com.junelife.local.subscription.premium.month",
					"com.junelife.local.subscription.premium.year",
					"com.junelife.subscription.premium.month",
				},
				"success": true, "title": "Choose a Plan",
			})
			return
		}
	}

	// /1/presets — content mirror (catalog + bundles)
	if len(parts) >= 2 && parts[0] == "1" && parts[1] == "presets" {
		// The stock Chef app POSTs the catalog; probes and other clients often
		// GET it. Serve both methods so a GET never falls through to the
		// generic {"success":true} and looks like an empty catalog.
		if (r.Method == "POST" || r.Method == "GET") && parts[len(parts)-1] == "catalog" {
			serveCatalog(w, r, cfg)
			return
		}
		if r.Method == "GET" && len(parts) >= 4 && parts[2] == "bundle" {
			rel := append([]string{"bundle"}, parts[3:]...)
			serveFromDir(w, cfg.ContentRoot, rel, "application/x-jcpb")
			return
		}
	}

	// POST /1/analytics/log — 202
	if r.Method == "POST" && len(parts) >= 2 && parts[0] == "1" && parts[1] == "analytics" {
		writeJSON(w, 202, map[string]any{})
		return
	}

	// GET /2/devices/{id}/... fallback
	if len(parts) >= 2 && parts[0] == "2" && parts[1] == "devices" {
		writeJSON(w, 200, map[string]any{"success": true})
		return
	}

	// Unhandled — answer 200/success generously so the oven opens its socket.
	logLine("[rest] UNHANDLED (answering 200/success) — GUESS: oven-side route never seen")
	writeJSON(w, 200, map[string]any{"success": true})
}

// serveCatalog serves the cook-preset catalog with its absolute
// api.junelife.com bundle URLs rewritten to the host the client used. The
// companion apps fetch those bundles directly, so without this they go to
// June's dead cloud and guided modes (proof, slow cook, dehydrate, reheat,
// warm, broil, toast) cannot start.
func serveCatalog(w http.ResponseWriter, r *http.Request, cfg *Config) {
	b, err := os.ReadFile(filepath.Join(cfg.ContentRoot, "catalog.json"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"success": false})
		return
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = "127.0.0.1"
	}
	base := scheme + "://" + host
	out := strings.ReplaceAll(string(b), "https://api.junelife.com", base)
	out = strings.ReplaceAll(out, "http://api.junelife.com", base)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "close")
	w.WriteHeader(200)
	_, _ = w.Write([]byte(out))
}

func serveFromDir(w http.ResponseWriter, root string, rel []string, ctype string) {
	clean := filepath.Clean(filepath.Join(append([]string{root}, rel...)...))
	absRoot, _ := filepath.Abs(root)
	if !strings.HasPrefix(clean, absRoot+string(os.PathSeparator)) && clean != absRoot {
		writeJSON(w, 404, map[string]any{"success": false})
		return
	}
	b, err := os.ReadFile(clean)
	if err != nil {
		writeJSON(w, 404, map[string]any{"success": false})
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "close")
	w.WriteHeader(200)
	_, _ = w.Write(b)
}

func serveMJPEG(w http.ResponseWriter, r *http.Request, cfg *Config) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	fps := cfg.MJPEGFps
	if fps <= 0 {
		fps = 10
	}
	interval := time.Second / time.Duration(fps)
	path := filepath.Join(cfg.MediaDir, "prod", "images", "latest.jpg")

	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "close")

	// A watched MJPEG stream is a viewer: keep the short-lived camera gate
	// open while frames are being pulled, and let it lapse after the client
	// disconnects. This is what lets a phone browser watch the camera without
	// leaving the oven camera running all the time.
	touchViewer := func() {
		if cfg.MJPEGViewerGate {
			cameraOverrideUntil.Store(time.Now().Add(15 * time.Second).UnixNano())
		}
	}
	touchViewer()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	ctx := r.Context()
	var lastMod time.Time
	var lastSize int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			touchViewer()
			st, err := os.Stat(path)
			if err != nil {
				continue
			}
			// The stock camera runs at 1 fps. Do not re-send the same JPEG at
			// the MJPEG tick rate; a changed mtime/size means a new frame.
			if st.ModTime().Equal(lastMod) && st.Size() == lastSize {
				continue
			}
			b, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			lastMod, lastSize = st.ModTime(), st.Size()
			if _, err := w.Write([]byte("--frame\r\nContent-Type: image/jpeg\r\nContent-Length: " +
				fmt.Sprintf("%d", len(b)) + "\r\n\r\n")); err != nil {
				return
			}
			if _, err := w.Write(b); err != nil {
				return
			}
			if _, err := w.Write([]byte("\r\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// serveCameraView is a phone-friendly wrapper around the MJPEG endpoint. A
// direct multipart/x-mixed-replace URL is awkward in some mobile browsers;
// this page just embeds it in an <img>, which every mobile browser renders.
func serveCameraView(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>June Oven Camera</title>
<style>
html,body{margin:0;background:#0f172a;color:#e2e8f0;font-family:sans-serif}
main{max-width:42em;margin:0 auto;padding:1em}
img{width:100%%;height:auto;background:#000;border-radius:.5em;display:block}
p{color:#94a3b8;font-size:.9em}
a{color:#38bdf8}
</style></head>
<body><main>
<h1>June Oven Camera</h1>
<img src="/media/stream.mjpg" alt="June oven live view">
<p>Live MJPEG stream. The camera turns off about 15 seconds after this page
stops watching. <a href="/local">June Local status</a></p>
</main></body></html>`)
}

const maxVideoJPEGBytes = 10 << 20

// publishVideoJPEG atomically stores the newest frame in the media directory
// and pushes a 10011 VideoFrame to every connected companion. Both the local
// stock-socket bridge (idle live view) and MediaCapturer's cook-time
// start_image uploads use this path, so the companion and Home Assistant see
// one consistent frame source.
func publishVideoJPEG(hub *Hub, cfg *Config, jpeg []byte) (string, error) {
	if len(jpeg) == 0 {
		return "", fmt.Errorf("empty JPEG")
	}
	if len(jpeg) > maxVideoJPEGBytes {
		return "", fmt.Errorf("JPEG too large (%d bytes)", len(jpeg))
	}
	if len(jpeg) < 2 || jpeg[0] != 0xff || jpeg[1] != 0xd8 {
		return "", fmt.Errorf("body is not a JPEG")
	}

	dir := filepath.Join(cfg.MediaDir, "prod", "images")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, "latest-*.jpg")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(jpeg); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	if err := os.Rename(tmpName, filepath.Join(dir, "latest.jpg")); err != nil {
		os.Remove(tmpName)
		return "", err
	}

	id := fmt.Sprintf("%d", time.Now().UnixNano())
	base := cfg.MediaURL
	if base == "" {
		base = "https://api.junelife.com"
	}
	imageURL := strings.TrimRight(base, "/") + "/media/prod/images/latest.jpg?ts=" + id
	pushVideoFrameToCompanions(hub, imageURL)
	return imageURL, nil
}

// multipartJPEG extracts the "photo" file part from MediaCapturer's multipart
// upload. A raw image/jpeg body is accepted as well so the endpoint can be
// exercised with curl during installs and tests.
func multipartJPEG(contentType string, body []byte) ([]byte, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err == nil && strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return nil, fmt.Errorf("multipart body without boundary")
		}
		mr := multipart.NewReader(bytes.NewReader(body), boundary)
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			formName := part.FormName()
			fileName := part.FileName()
			data, err := io.ReadAll(io.LimitReader(part, maxVideoJPEGBytes+1))
			part.Close()
			if err != nil {
				return nil, err
			}
			if len(data) > maxVideoJPEGBytes {
				return nil, fmt.Errorf("image part too large")
			}
			if len(data) > 0 && (formName == "photo" || fileName != "") {
				return data, nil
			}
		}
		return nil, fmt.Errorf("multipart body has no photo part")
	}
	if strings.HasPrefix(mediaType, "image/") || (len(body) >= 2 && body[0] == 0xff && body[1] == 0xd8) {
		return body, nil
	}
	return nil, fmt.Errorf("unsupported content type %q", contentType)
}
