package main

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"html"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// The oven's own Updates screen. Chef's Settings opens http://127.0.0.1/local/oven in a
// WebView (apk-patches/chef-cookmodes). Being at the oven is the authorization: these
// routes answer only requests from the oven itself, so they need no update password.
//
//   GET  /local/oven                          the screen
//   POST /local/oven/update/check|stage|restore
//   GET  /local/oven/signin                   a one-time code + QR for /local on a phone
//
// The phone side is POST /local/update/signin {"code"}: over HTTPS, it trades a fresh code
// for the update password once, so a phone that scanned the oven's QR can update without
// anyone typing 64 characters.

//go:embed oven_page.html
var ovenPageHTML string

const (
	signinCodeTTL      = 10 * time.Minute
	signinCodeAttempts = 5
	signinAlphabet     = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"
)

var signin struct {
	sync.Mutex
	code     string
	expires  time.Time
	attempts int
}

func fromOvenItself(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// handleOvenRoute serves the /local/oven routes and reports whether it handled the path.
func handleOvenRoute(hub *Hub, cfg *Config, w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	if path != "/local/oven" && !strings.HasPrefix(path, "/local/oven/") {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	if !fromOvenItself(r) || !sameRequestOrigin(r) {
		// Anyone else uses /local with the update password.
		http.NotFound(w, r)
		return true
	}
	switch {
	case path == "/local/oven" && r.Method == http.MethodGet:
		page := ovenPageHTML
		page = strings.ReplaceAll(page, "{{VERSION}}", html.EscapeString(appVersion))
		page = strings.ReplaceAll(page, "{{COMMIT}}", html.EscapeString(appCommit))
		page = strings.ReplaceAll(page, "{{BUILD}}", html.EscapeString(appBuild))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	case path == "/local/oven/settings" && r.Method == http.MethodGet:
		handleOvenSettingsGet(cfg, w)
	case path == "/local/oven/settings" && r.Method == http.MethodPost:
		handleOvenSettingsSet(cfg, w, r)
	case path == "/local/oven/signin" && r.Method == http.MethodGet:
		handleOvenSignin(hub, cfg, w)
	case strings.HasPrefix(path, "/local/oven/update/") && r.Method == http.MethodPost:
		handleOvenUpdate(cfg.Updater, strings.TrimPrefix(path, "/local/oven/update/"), w, r)
	default:
		http.NotFound(w, r)
	}
	return true
}

// handleOvenSettingsGet backs the "Phone access to /local" card on the oven's
// Updates screen.
func handleOvenSettingsGet(cfg *Config, w http.ResponseWriter) {
	st := localUISettings{}
	if cfg != nil && cfg.LocalSettings != nil {
		st = cfg.LocalSettings.Get()
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "settings": st})
}

// handleOvenSettingsSet turns the phone-facing /local page on or off. It is
// only reachable over loopback (see handleOvenRoute), so being at the oven is
// the authorization, exactly like the update controls.
func handleOvenSettingsSet(cfg *Config, w http.ResponseWriter, r *http.Request) {
	if cfg == nil || cfg.LocalSettings == nil {
		writeJSON(w, http.StatusConflict, map[string]any{"success": false, "error": "settings are not available on this oven"})
		return
	}
	var req struct {
		LocalUIDisabled *bool `json:"local_ui_disabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil || req.LocalUIDisabled == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "send {\"local_ui_disabled\": true or false}"})
		return
	}
	if err := cfg.LocalSettings.SetLocalUIDisabled(*req.LocalUIDisabled); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	if *req.LocalUIDisabled {
		logLine("[local] phone access to /local turned off from the oven screen")
	} else {
		logLine("[local] phone access to /local turned back on from the oven screen")
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "settings": cfg.LocalSettings.Get()})
}

func handleOvenUpdate(u *Updater, action string, w http.ResponseWriter, r *http.Request) {
	var (
		status UpdateStatus
		err    error
	)
	switch action {
	case "check":
		status, err = u.Check(r.Context())
	case "stage":
		status, err = u.Stage(r.Context())
	case "restore":
		status, err = u.RestoreBackup()
	default:
		http.NotFound(w, r)
		return
	}
	logLine("[update] %s from the oven screen", action)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"success": false, "error": err.Error(), "update": status})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "update": status})
}

// handleOvenSignin returns the current one-time code, making a new one when the old one is
// used up or has under two minutes left, and a QR that opens /local with it.
func handleOvenSignin(hub *Hub, cfg *Config, w http.ResponseWriter) {
	if cfg.Updater == nil || !cfg.Updater.Status().Enabled || cfg.Updater.token == "" {
		writeJSON(w, http.StatusConflict, map[string]any{"success": false, "error": "updates are not set up on this oven"})
		return
	}
	signin.Lock()
	if signin.code == "" || time.Until(signin.expires) < 2*time.Minute {
		signin.code = newSigninCode()
		signin.expires = time.Now().Add(signinCodeTTL)
		signin.attempts = 0
	}
	code, expires := signin.code, signin.expires
	signin.Unlock()

	netStatus := networkStatus(cfg)
	host := netStatus.MDNS
	if host == "" {
		host = "june.local"
	}
	link := "https://" + host + "/local#updates&code=" + code
	m, err := qrEncode(link)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "code": code[:4] + "-" + code[4:], "address": "https://" + host + "/local",
		"expires_in": int(time.Until(expires).Seconds()), "qr_svg": qrSVG(m),
	})
}

func newSigninCode() string {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	var b strings.Builder
	for _, c := range raw {
		b.WriteByte(signinAlphabet[int(c)%len(signinAlphabet)])
	}
	return b.String()
}

// handleUpdateSignin is POST /local/update/signin: HTTPS and same-origin like the other
// update controls, but authorized by a code read off the oven's screen instead of the
// password. A code works once, for ten minutes, and five wrong tries burn it.
func handleUpdateSignin(u *Updater, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "method not allowed"})
		return
	}
	if !sameRequestOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "error": "cross-origin request refused"})
		return
	}
	if r.TLS == nil {
		writeJSON(w, http.StatusUpgradeRequired, map[string]any{"success": false, "error": "open https://<oven>/local to sign in"})
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "send the code from the oven"})
		return
	}
	given := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(req.Code))

	signin.Lock()
	defer signin.Unlock()
	if signin.code == "" || time.Now().After(signin.expires) {
		signin.code = ""
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "error": "that code has expired; open the Project July tile on the oven for a new one"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(given), []byte(signin.code)) != 1 {
		signin.attempts++
		if signin.attempts >= signinCodeAttempts {
			signin.code = ""
		}
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "error": "that code is not the one on the oven's screen"})
		return
	}
	signin.code = ""
	if u == nil || u.token == "" {
		writeJSON(w, http.StatusConflict, map[string]any{"success": false, "error": "updates are not set up on this oven"})
		return
	}
	logLine("[update] phone signed in with a code from the oven screen")
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "token": u.token})
}
