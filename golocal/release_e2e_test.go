package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPublishedRepositoryEndToEnd exercises the actual signed repository and
// 247 MB Chef APK. It is opt-in because it needs the generated release assets
// and several hundred MiB of temporary disk space.
func TestPublishedRepositoryEndToEnd(t *testing.T) {
	if os.Getenv("JUNE_RELEASE_E2E") != "1" {
		t.Skip("set JUNE_RELEASE_E2E=1 after generating dist/oven-updates/stable")
	}
	repoRoot := filepath.Clean("..")
	stable := filepath.Join(repoRoot, "dist", "oven-updates", "stable")
	configBytes, err := os.ReadFile(filepath.Join(repoRoot, "onoven", "updater.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg updaterConfigFile
	if err := json.Unmarshal(configBytes, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.MinSequence = 0 // exercise download/commit even though this release is the installer baseline
	srv := httptest.NewServer(http.FileServer(http.Dir(stable)))
	defer srv.Close()
	cfg.ManifestURL = srv.URL + "/manifest.json"
	cfg.SignatureURL = srv.URL + "/manifest.sig"
	configPath := filepath.Join(t.TempDir(), "updater.json")
	configBytes, err = json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, configBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	u, err := newUpdater(updaterOptions{
		ConfigPath: configPath, DataDir: filepath.Join(root, "updates"),
		TokenFile:      filepath.Join(root, "updates", "control-token"),
		PersistentFile: filepath.Join(root, "system", "chef_update.json"),
		AppVersion:     "0.8.1", AllowInsecure: true, SkipPlatform: true,
		TokenOverride: strings.Repeat("c", 64),
		Now:           time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err := u.Stage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != "staged" || st.Pending == nil {
		t.Fatalf("unexpected status: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(u.transactionsDir, strings.TrimSpace(readText(t, u.readyFile)), "chef_cookmodes.apk")); err != nil {
		t.Fatal(err)
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
