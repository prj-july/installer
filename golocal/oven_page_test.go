package main

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func resetSignin() {
	signin.Lock()
	signin.code, signin.expires, signin.attempts = "", time.Time{}, 0
	signin.Unlock()
}

func ovenRequest(method, path, remote string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.RemoteAddr = remote
	return r
}

func serveOven(t *testing.T, cfg *Config, r *http.Request) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	w := httptest.NewRecorder()
	handled := handleOvenRoute(newHub(false, ""), cfg, w, r)
	return w, handled
}

func TestFromOvenItself(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:5123": true, "[::1]:5123": true, "127.8.9.10:1": true,
		"192.168.1.5:5123": false, "[fe80::1]:80": false, "garbage": false, "": false,
	} {
		if got := fromOvenItself(&http.Request{RemoteAddr: addr}); got != want {
			t.Errorf("fromOvenItself(%q) = %t, want %t", addr, got, want)
		}
	}
}

func TestOvenRoutesOnlyAnswerTheOven(t *testing.T) {
	cfg := &Config{Timeout: 30, Updater: &Updater{status: UpdateStatus{State: "disabled"}}}
	if _, handled := serveOven(t, cfg, ovenRequest("GET", "/local", "127.0.0.1:1")); handled {
		t.Fatal("/local is not an oven route")
	}
	if _, handled := serveOven(t, cfg, ovenRequest("GET", "/local/ovenx", "127.0.0.1:1")); handled {
		t.Fatal("/local/ovenx is not an oven route")
	}
	for _, rq := range []struct{ method, path string }{
		{"GET", "/local/oven"}, {"GET", "/local/oven/signin"},
		{"POST", "/local/oven/update/check"}, {"POST", "/local/oven/update/stage"}, {"POST", "/local/oven/update/restore"},
	} {
		w, handled := serveOven(t, cfg, ovenRequest(rq.method, rq.path, "192.168.1.5:40000"))
		if !handled || w.Code != http.StatusNotFound {
			t.Fatalf("%s %s from the LAN: handled=%t code=%d", rq.method, rq.path, handled, w.Code)
		}
	}

	w, _ := serveOven(t, cfg, ovenRequest("GET", "/local/oven", "127.0.0.1:1"))
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("oven page: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	if body := w.Body.String(); strings.Contains(body, "{{VERSION}}") || !strings.Contains(body, appVersion) {
		t.Fatal("oven page did not get its version")
	}

	r := ovenRequest("GET", "/local/oven", "127.0.0.1:1")
	r.Header.Set("Origin", "http://evil.example")
	if w, _ := serveOven(t, cfg, r); w.Code != http.StatusNotFound {
		t.Fatalf("cross-origin oven request: got %d", w.Code)
	}
}

func TestOvenUpdateNeedsNoPassword(t *testing.T) {
	fixture, priv := signedUpdate(t, nil, false)
	srv := httptest.NewServer(fixture)
	defer srv.Close()
	cfg := &Config{Timeout: 30, Updater: newTestUpdater(t, srv.URL, publicKeyFor(t, priv))}

	post := func(action string) (*httptest.ResponseRecorder, map[string]any) {
		w, _ := serveOven(t, cfg, ovenRequest("POST", "/local/oven/update/"+action, "127.0.0.1:1"))
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w, out
	}
	w, out := post("check")
	if w.Code != http.StatusOK || out["update"].(map[string]any)["state"] != "update-available" {
		t.Fatalf("check from the oven: %d %s", w.Code, w.Body.String())
	}
	w, out = post("stage")
	if w.Code != http.StatusOK || out["update"].(map[string]any)["state"] != "staged" {
		t.Fatalf("stage from the oven: %d %s", w.Code, w.Body.String())
	}
	if w, _ := post("nope"); w.Code != http.StatusNotFound {
		t.Fatalf("unknown action: got %d", w.Code)
	}
	if w, _ := serveOven(t, cfg, ovenRequest("GET", "/local/oven/update/check", "127.0.0.1:1")); w.Code != http.StatusNotFound {
		t.Fatalf("GET on an update action: got %d", w.Code)
	}
}

var signinCodeFormat = regexp.MustCompile(`^[2-9A-HJKMNP-Z]{4}-[2-9A-HJKMNP-Z]{4}$`)

func issueSignin(t *testing.T, cfg *Config) map[string]any {
	t.Helper()
	w, _ := serveOven(t, cfg, ovenRequest("GET", "/local/oven/signin", "127.0.0.1:1"))
	if w.Code != http.StatusOK {
		t.Fatalf("signin: %d %s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestOvenSigninCode(t *testing.T) {
	resetSignin()
	_, priv := signedUpdate(t, nil, false)
	cfg := &Config{Timeout: 30, Updater: newTestUpdater(t, "http://127.0.0.1:1", publicKeyFor(t, priv))}
	first := issueSignin(t, cfg)
	code, _ := first["code"].(string)
	if !signinCodeFormat.MatchString(code) {
		t.Fatalf("code %q is not XXXX-XXXX", code)
	}
	if svg, _ := first["qr_svg"].(string); !strings.HasPrefix(svg, "<svg") {
		t.Fatalf("no QR: %.40q", svg)
	}
	if n, _ := first["expires_in"].(float64); n <= 0 {
		t.Fatalf("expires_in = %v", first["expires_in"])
	}
	if again := issueSignin(t, cfg); again["code"] != code {
		t.Fatalf("a still-fresh code was replaced: %v then %v", code, again["code"])
	}

	disabled := createUpdater(&Config{UpdaterConfig: filepath.Join(t.TempDir(), "missing.json")})
	cfg = &Config{Timeout: 30, Updater: disabled}
	if w, _ := serveOven(t, cfg, ovenRequest("GET", "/local/oven/signin", "127.0.0.1:1")); w.Code != http.StatusConflict {
		t.Fatalf("signin with updates off: got %d", w.Code)
	}
}

func TestUpdateSignin(t *testing.T) {
	resetSignin()
	_, priv := signedUpdate(t, nil, false)
	u := newTestUpdater(t, "http://127.0.0.1:1", publicKeyFor(t, priv))
	cfg := &Config{Timeout: 30, Updater: u}

	exchange := func(method, body, origin string, secure bool) (int, map[string]any) {
		r := httptest.NewRequest(method, "/local/update/signin", strings.NewReader(body))
		r.Host = "oven.local"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if secure {
			r.TLS = &tls.ConnectionState{}
		}
		w := httptest.NewRecorder()
		handleUpdateSignin(u, w, r)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	code := issueSignin(t, cfg)["code"].(string)
	good := `{"code":"` + code + `"}`
	if c, _ := exchange("GET", good, "", true); c != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", c)
	}
	if c, _ := exchange("POST", good, "https://evil.example", true); c != http.StatusForbidden {
		t.Fatalf("cross-origin: %d", c)
	}
	if c, _ := exchange("POST", good, "", false); c != http.StatusUpgradeRequired {
		t.Fatalf("plain HTTP: %d", c)
	}

	// The right code, lower case and without the dash, works once.
	plain := strings.ToLower(strings.ReplaceAll(code, "-", ""))
	c, out := exchange("POST", `{"code":"`+plain+`"}`, "https://oven.local", true)
	if c != http.StatusOK || out["token"] != u.token {
		t.Fatalf("right code: %d %v", c, out)
	}
	if c, _ := exchange("POST", good, "", true); c != http.StatusUnauthorized {
		t.Fatalf("second use of a code: %d", c)
	}

	// Five wrong tries burn the code.
	resetSignin()
	code = issueSignin(t, cfg)["code"].(string)
	for i := 0; i < signinCodeAttempts; i++ {
		if c, _ := exchange("POST", `{"code":"2222-2222"}`, "", true); c != http.StatusUnauthorized {
			t.Fatalf("wrong code %d: %d", i, c)
		}
	}
	if c, _ := exchange("POST", `{"code":"`+code+`"}`, "", true); c != http.StatusUnauthorized {
		t.Fatalf("right code after %d wrong tries: %d", signinCodeAttempts, c)
	}

	// An expired code is refused.
	resetSignin()
	code = issueSignin(t, cfg)["code"].(string)
	signin.Lock()
	signin.expires = time.Now().Add(-time.Second)
	signin.Unlock()
	if c, _ := exchange("POST", `{"code":"`+code+`"}`, "", true); c != http.StatusUnauthorized {
		t.Fatalf("expired code: %d", c)
	}
	if c, _ := exchange("POST", `not json`, "", true); c != http.StatusBadRequest {
		t.Fatalf("bad body: %d", c)
	}
}
