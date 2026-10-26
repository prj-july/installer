package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func restWithBody(method, path, body string) *http.Request {
	if body == "" {
		return httptest.NewRequest(method, path, nil)
	}
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	return r
}

func serveREST(t *testing.T, cfg *Config, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	handleREST(newHub(true, ""), cfg, w, r)
	return w
}

func TestLocalSettingsPersistAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")
	store := newLocalSettingsStore(path)
	if store.Get().LocalUIDisabled {
		t.Fatal("a missing settings file must leave the phone page enabled")
	}
	if err := store.SetLocalUIDisabled(true); err != nil {
		t.Fatal(err)
	}
	// A new store is what a server restart or reboot does: it reads the file.
	reopened := newLocalSettingsStore(path)
	if !reopened.Get().LocalUIDisabled {
		t.Fatal("the disable flag did not survive a restart")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		LocalUIDisabled bool `json:"local_ui_disabled"`
	}
	if err := json.Unmarshal(b, &file); err != nil || !file.LocalUIDisabled {
		t.Fatalf("settings file = %s (%v)", b, err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".settings-*.json"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary settings files left behind: %v", leftovers)
	}

	if err := reopened.SetLocalUIDisabled(false); err != nil {
		t.Fatal(err)
	}
	if newLocalSettingsStore(path).Get().LocalUIDisabled {
		t.Fatal("re-enabling did not persist")
	}

	// A corrupt file must not lock anyone out of /local.
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if newLocalSettingsStore(path).Get().LocalUIDisabled {
		t.Fatal("a corrupt settings file disabled the phone page")
	}
}

func TestOvenPageShowsFullVersion(t *testing.T) {
	oldV, oldC, oldB := appVersion, appCommit, appBuild
	appVersion, appCommit, appBuild = "9.9.9-test", "abc1234", "2026-10-26T01:00:00Z"
	defer func() { appVersion, appCommit, appBuild = oldV, oldC, oldB }()

	cfg := &Config{Timeout: 30, Updater: &Updater{status: UpdateStatus{State: "disabled"}}}
	w, _ := serveOven(t, cfg, ovenRequest("GET", "/local/oven", "127.0.0.1:1"))
	if w.Code != http.StatusOK {
		t.Fatalf("oven page: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{appVersion, appCommit, appBuild} {
		if !strings.Contains(body, want) {
			t.Fatalf("oven page does not show %q", want)
		}
	}
	if strings.Contains(body, "{{") {
		t.Fatalf("oven page still has an unfilled placeholder: %s", body)
	}
}

func TestLocalUIDisableBlocksPhonesButNotTheOven(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := newLocalSettingsStore(path).SetLocalUIDisabled(true); err != nil {
		t.Fatal(err)
	}
	// Read it back through a fresh store, as a server restart would.
	cfg := &Config{
		Timeout:       30,
		SettingsFile:  path,
		LocalSettings: newLocalSettingsStore(path),
		Updater:       &Updater{status: UpdateStatus{State: "disabled"}},
	}

	phone := func(method, path, body string) *http.Request {
		r := restWithBody(method, path, body)
		r.RemoteAddr = "192.168.1.5:40000"
		return r
	}
	oven := func(method, path, body string) *http.Request {
		r := restWithBody(method, path, body)
		r.RemoteAddr = "127.0.0.1:1"
		return r
	}

	// The phone-facing page is gone, including the trailing-slash redirect.
	for _, p := range []string{"/local", "/local/"} {
		w := serveREST(t, cfg, phone(http.MethodGet, p, ""))
		if w.Code != http.StatusForbidden {
			t.Fatalf("GET %s from a phone while disabled: %d", p, w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, "June Local is turned off") || !strings.Contains(body, "Project July") {
			t.Fatalf("GET %s did not explain how to turn /local back on: %s", p, body)
		}
	}

	// The oven's Updates screen and its settings remain reachable over loopback.
	w, _ := serveOven(t, cfg, oven("GET", "/local/oven", ""))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), appVersion) {
		t.Fatalf("oven page while disabled: %d %s", w.Code, w.Body.String())
	}
	w, _ = serveOven(t, cfg, oven("GET", "/local/oven/settings", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("oven settings: %d %s", w.Code, w.Body.String())
	}
	var got struct {
		Success  bool            `json:"success"`
		Settings localUISettings `json:"settings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || !got.Settings.LocalUIDisabled {
		t.Fatalf("oven settings do not report the disabled state: %s", w.Body.String())
	}
	// Loopback is exempt, so the oven itself can still open /local.
	if w := serveREST(t, cfg, oven(http.MethodGet, "/local", "")); w.Code != http.StatusOK {
		t.Fatalf("GET /local from the oven while disabled: %d", w.Code)
	}

	// Status and cook are not part of the page and keep working for phones,
	// as do the oven's update/sign-in routes (covered by the other tests).
	if w := serveREST(t, cfg, phone(http.MethodGet, "/local/status", "")); w.Code != http.StatusOK {
		t.Fatalf("GET /local/status while disabled: %d", w.Code)
	}
	if w := serveREST(t, cfg, phone(http.MethodGet, "/local/cook", "")); w.Code != http.StatusOK {
		t.Fatalf("GET /local/cook while disabled: %d", w.Code)
	}
	if w, _ := serveOven(t, cfg, phone("GET", "/local/oven", "")); w.Code != http.StatusNotFound {
		t.Fatalf("oven page from a phone while disabled: %d", w.Code)
	}
	if w, _ := serveOven(t, cfg, phone("GET", "/local/oven/settings", "")); w.Code != http.StatusNotFound {
		t.Fatalf("oven settings from a phone while disabled: %d", w.Code)
	}
	if w, _ := serveOven(t, cfg, oven("PUT", "/local/oven/settings", "")); w.Code != http.StatusNotFound {
		t.Fatalf("PUT on oven settings: %d", w.Code)
	}

	// Re-enable from the oven's screen, with the same JSON the page sends.
	w, _ = serveOven(t, cfg, oven(http.MethodPost, "/local/oven/settings", `{"local_ui_disabled":false}`))
	if w.Code != http.StatusOK {
		t.Fatalf("re-enable: %d %s", w.Code, w.Body.String())
	}
	if newLocalSettingsStore(path).Get().LocalUIDisabled {
		t.Fatal("re-enable did not persist")
	}
	if w := serveREST(t, cfg, phone(http.MethodGet, "/local", "")); w.Code != http.StatusOK {
		t.Fatalf("GET /local from a phone after re-enable: %d", w.Code)
	}

	// A bad body and a cross-origin write are refused without changing the flag.
	if w, _ := serveOven(t, cfg, oven(http.MethodPost, "/local/oven/settings", `{}`)); w.Code != http.StatusBadRequest {
		t.Fatalf("empty settings body: %d", w.Code)
	}
	r := oven(http.MethodPost, "/local/oven/settings", `{"local_ui_disabled":true}`)
	r.Header.Set("Origin", "http://evil.example")
	if w, _ := serveOven(t, cfg, r); w.Code != http.StatusNotFound {
		t.Fatalf("cross-origin settings write: %d", w.Code)
	}
	if newLocalSettingsStore(path).Get().LocalUIDisabled {
		t.Fatal("a refused settings write changed the flag")
	}
}
