package main

// The one june-local setting that is not part of the updater config or the
// identity record: whether /local's phone-facing page is served to clients
// other than the oven itself.
//
// It lives in /data/june because /system/etc/june is the read-only system
// partition and a factory reset or reboot must not silently turn the phone
// page back on. The file is small and this is its only writer, so it is
// rewritten whole rather than merged with unknown keys.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

const defaultSettingsFile = "/data/june/settings.json"

type localUISettings struct {
	LocalUIDisabled bool `json:"local_ui_disabled"`
}

type localSettingsStore struct {
	mu     sync.RWMutex
	path   string
	loaded bool
	st     localUISettings
}

func newLocalSettingsStore(path string) *localSettingsStore {
	return &localSettingsStore{path: path}
}

// Get returns the settings, reading them once on first use and caching the
// result. A missing or unreadable file means the phone page stays enabled:
// the safe default is not to lock FnG out of /local.
func (s *localSettingsStore) Get() localUISettings {
	if s == nil {
		return localUISettings{}
	}
	s.mu.RLock()
	if s.loaded {
		st := s.st
		s.mu.RUnlock()
		return st
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		s.st = readLocalUISettings(s.path)
		s.loaded = true
	}
	return s.st
}

// SetLocalUIDisabled persists the flag and only then updates the cached value,
// so a failed write leaves the running server behaving as it did before.
func (s *localSettingsStore) SetLocalUIDisabled(disabled bool) error {
	if s == nil || s.path == "" {
		return errors.New("no settings file is configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		s.st = readLocalUISettings(s.path)
		s.loaded = true
	}
	st := s.st
	st.LocalUIDisabled = disabled
	if err := writeLocalUISettings(s.path, st); err != nil {
		return err
	}
	s.st = st
	return nil
}

func readLocalUISettings(path string) localUISettings {
	var st localUISettings
	if path == "" {
		return st
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	if err := json.Unmarshal(b, &st); err != nil {
		// A corrupt file must not make the phone page unreachable.
		return localUISettings{}
	}
	return st
}

func writeLocalUISettings(path string, st localUISettings) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(dir, ".settings-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}

// localUIPhonesBlocked reports whether this request must not see the
// phone-facing /local page: the persisted switch is off and the request did
// not come from the oven itself. Loopback is always exempt so the oven's own
// Updates screen can turn the page back on.
func localUIPhonesBlocked(cfg *Config, r *http.Request) bool {
	if cfg == nil || cfg.LocalSettings == nil || !cfg.LocalSettings.Get().LocalUIDisabled {
		return false
	}
	return !fromOvenItself(r)
}

const localUIDisabledHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>June Local</title>
<style>
html,body{margin:0;height:100%;background:#08090b;color:#f4f4f5;font:400 20px/1.4 sans-serif}
main{max-width:32em;margin:0 auto;padding:12vh 24px}
h1{font-size:30px;line-height:1.2;margin:0 0 18px}
p{margin:0 0 14px;color:#a1a1aa}
b{color:#f4f4f5}
</style>
</head>
<body><main>
<h1>June Local is turned off</h1>
<p>Phones can no longer open <b>/local</b> on this oven.</p>
<p>To turn it back on, go to the oven and open the <b>Project July</b> tile on its home screen, then press <b>Turn phone access back on</b>.</p>
</main></body>
</html>`

func serveLocalUIDisabled(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(localUIDisabledHTML))
}
