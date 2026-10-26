package main

import (
	"archive/zip"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bundleZip(t *testing.T, dir string, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, b := range files {
		w, err := zw.Create(dir + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func fixtureFiles(f *updateServer) map[string][]byte {
	return map[string][]byte{"manifest.json": f.manifest, "manifest.sig": f.signature, "artifacts/Chef_signed.apk": f.artifact}
}

func TestStageFileVerifiesLikeADownload(t *testing.T) {
	fixture, priv := signedUpdate(t, nil, false)
	u := newTestUpdater(t, "http://127.0.0.1:1", publicKeyFor(t, priv))

	bad := []struct {
		name  string
		body  []byte
		error string
	}{
		{"not a zip", []byte("hello"), "not an update bundle"},
		{"no manifest", bundleZip(t, "", map[string][]byte{"x": {1}}), "no manifest.json"},
		{"missing apk", bundleZip(t, "", map[string][]byte{"manifest.json": fixture.manifest, "manifest.sig": fixture.signature}), "missing artifacts/Chef_signed.apk"},
		{"tampered apk", bundleZip(t, "", map[string][]byte{"manifest.json": fixture.manifest, "manifest.sig": fixture.signature, "artifacts/Chef_signed.apk": append(append([]byte{}, fixture.artifact...), 0)}), "size mismatch"},
	}
	other, _ := signedUpdate(t, nil, false) // signed by a different key
	bad = append(bad, struct {
		name  string
		body  []byte
		error string
	}{"wrong key", bundleZip(t, "", fixtureFiles(other)), "not from Project July"})
	for _, c := range bad {
		if _, err := u.StageFile(bytes.NewReader(c.body)); err == nil || !strings.Contains(err.Error(), c.error) {
			t.Fatalf("%s: got %v, want %q", c.name, err, c.error)
		}
		if _, err := os.Stat(u.readyFile); err == nil {
			t.Fatalf("%s: a rejected bundle left a ready marker", c.name)
		}
	}

	st, err := u.StageFile(bytes.NewReader(bundleZip(t, "stable/", fixtureFiles(fixture))))
	if err != nil {
		t.Fatal(err)
	}
	if st.State != "staged" || st.Pending == nil || st.Pending.Release != "2026.10.24-test" {
		t.Fatalf("unexpected status after upload: %+v", st)
	}
	staged, err := os.ReadFile(u.pending.ArtifactFile)
	if err != nil || !bytes.Equal(staged, fixture.artifact) {
		t.Fatalf("staged APK differs from the signed one: %v", err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(u.transactionsDir, ".upload-*"))
	if len(leftovers) != 0 {
		t.Fatalf("upload temp files left behind: %v", leftovers)
	}

	// After the boot applier promotes it, the same bundle is no longer newer.
	applyPending(t, u)
	u = reopenTestUpdater(t, u, publicKeyFor(t, priv))
	if _, err := u.StageFile(bytes.NewReader(bundleZip(t, "", fixtureFiles(fixture)))); err == nil || !strings.Contains(err.Error(), "not newer") {
		t.Fatalf("replayed bundle: got %v", err)
	}
}

// applyPending does what june_reaper.sh does on a successful boot apply.
func applyPending(t *testing.T, u *Updater) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(filepath.Dir(u.pending.ArtifactFile), "pending.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{u.appliedFile, u.persistentFile} {
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.RemoveAll(filepath.Dir(u.pending.ArtifactFile))
	_ = os.Remove(u.readyFile)
}

func TestRestoreBackupKeepsSequence(t *testing.T) {
	fixture, priv := signedUpdate(t, nil, false)
	u := newTestUpdater(t, "http://127.0.0.1:1", publicKeyFor(t, priv))
	if st := u.Status(); st.Restore != nil {
		t.Fatalf("restore offered before any wireless update: %+v", st.Restore)
	}
	if _, err := u.RestoreBackup(); err == nil {
		t.Fatal("restore without an applied update should fail")
	}

	if _, err := u.StageFile(bytes.NewReader(bundleZip(t, "", fixtureFiles(fixture)))); err != nil {
		t.Fatal(err)
	}
	applyPending(t, u)
	backup := testAPK(t)
	backup = append(backup[:0:0], backup...)
	if err := os.WriteFile(u.backupFile, backup, 0o644); err != nil {
		t.Fatal(err)
	}
	u = reopenTestUpdater(t, u, publicKeyFor(t, priv))
	st := u.Status()
	if st.Restore == nil || st.Restore.Undoes != "2026.10.24-test" {
		t.Fatalf("restore not offered after an update: %+v", st)
	}
	st, err := u.RestoreBackup()
	if err != nil {
		t.Fatal(err)
	}
	if st.State != "staged" || st.Pending == nil || st.Pending.Sequence != 2026102402 {
		t.Fatalf("restore should stage at the current sequence: %+v", st)
	}
	if u.pending.ArtifactID != restoreArtifactID {
		t.Fatalf("restore record id = %q", u.pending.ArtifactID)
	}
	staged, _ := os.ReadFile(u.pending.ArtifactFile)
	if !bytes.Equal(staged, backup) {
		t.Fatal("staged restore is not the backup")
	}

	applyPending(t, u)
	u = reopenTestUpdater(t, u, publicKeyFor(t, priv))
	if st := u.Status(); st.Restore != nil {
		t.Fatalf("restore offered again right after a restore: %+v", st.Restore)
	}
	if got := u.currentSequence(); got != 2026102402 {
		t.Fatalf("restore lowered the sequence floor to %d", got)
	}
}

func TestUploadAndRestoreNeedHTTPSAndPassword(t *testing.T) {
	_, priv := signedUpdate(t, nil, false)
	u := newTestUpdater(t, "http://127.0.0.1:1", publicKeyFor(t, priv))
	for _, path := range []string{"/local/update/upload", "/local/update/restore"} {
		do := func(secure bool, token string) int {
			r := httptest.NewRequest(http.MethodPost, path, strings.NewReader("x"))
			if secure {
				r.TLS = &tls.ConnectionState{}
			}
			if token != "" {
				r.Header.Set(updateControlHeader, token)
			}
			w := httptest.NewRecorder()
			u.handleLocalUpdate(w, r)
			return w.Code
		}
		if got := do(false, strings.Repeat("a", 64)); got != http.StatusUpgradeRequired {
			t.Fatalf("%s over HTTP: got %d", path, got)
		}
		if got := do(true, strings.Repeat("b", 64)); got != http.StatusUnauthorized {
			t.Fatalf("%s with a wrong password: got %d", path, got)
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader("x"))
		r.TLS = &tls.ConnectionState{}
		r.Header.Set(updateControlHeader, strings.Repeat("a", 64))
		u.handleLocalUpdate(w, r)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if w.Code != http.StatusBadGateway || out["success"] != false {
			t.Fatalf("%s with a junk body: got %d %s", path, w.Code, w.Body.String())
		}
	}
}
