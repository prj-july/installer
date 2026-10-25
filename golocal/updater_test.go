package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	updatelib "june-golocal/internal/update"
)

type updateServer struct {
	manifest      []byte
	signature     []byte
	artifact      []byte
	artifactGets  atomic.Int32
	manifestError int
}

func (s *updateServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/manifest.json":
		if s.manifestError != 0 {
			http.Error(w, "failed", s.manifestError)
			return
		}
		_, _ = w.Write(s.manifest)
	case "/manifest.json.sig":
		_, _ = w.Write(s.signature)
	case "/artifacts/Chef_signed.apk":
		s.artifactGets.Add(1)
		_, _ = w.Write(s.artifact)
	default:
		http.NotFound(w, r)
	}
}

func testAPK(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"AndroidManifest.xml", "classes.dex"} {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func signedUpdate(t *testing.T, mutate func(*updatelib.Manifest), badSignature bool) (*updateServer, ed25519.PrivateKey) {
	t.Helper()
	artifact := testAPK(t)
	sum := sha256.Sum256(artifact)
	m := updatelib.Manifest{
		Schema: updatelib.ManifestSchema, Channel: "stable", Release: "2026.10.24-test",
		Sequence: 2026102402, PublishedAt: "2026-10-24T12:00:00Z",
		MinAppVersion: "0.8.1", Notes: "test release",
		Artifacts: []updatelib.Artifact{{
			ID: "chef", Type: "apk", File: "artifacts/Chef_signed.apk",
			Size: int64(len(artifact)), SHA256: hex.EncodeToString(sum[:]),
			Target: updatelib.TargetJuneOvenAndroid5, AppID: "com.junelife.device.chef",
		}},
	}
	if mutate != nil {
		mutate(&m)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	_, priv, err := updatelib.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, data)
	if badSignature {
		sig[0] ^= 0xff
	}
	return &updateServer{
		manifest: data, signature: updatelib.EncodeSignature(sig), artifact: artifact,
	}, priv
}

func newTestUpdater(t *testing.T, serverURL, publicKey string) *Updater {
	t.Helper()
	root := t.TempDir()
	cfg := updaterConfigFile{
		Schema: 1, Enabled: true, Channel: "stable",
		ManifestURL: serverURL + "/manifest.json", SignatureURL: serverURL + "/manifest.json.sig",
		PublicKey: publicKey, Target: updatelib.TargetJuneOvenAndroid5,
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "updater.json")
	if err := os.WriteFile(configPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := newUpdater(updaterOptions{
		ConfigPath: configPath, DataDir: filepath.Join(root, "updates"),
		TokenFile:      filepath.Join(root, "updates", "control-token"),
		PersistentFile: filepath.Join(root, "system", "chef_update.json"),
		AppVersion:     "0.8.1", AllowInsecure: true, SkipPlatform: true,
		TokenOverride: strings.Repeat("a", 64),
		Now: func() time.Time {
			return time.Date(2026, 10, 24, 13, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func publicKeyFor(t *testing.T, priv ed25519.PrivateKey) string {
	t.Helper()
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatal("private key has unexpected public type")
	}
	return updatelib.EncodePublicKey(pub)
}

func reopenTestUpdater(t *testing.T, previous *Updater, publicKey string) *Updater {
	t.Helper()
	root := filepath.Dir(previous.dataDir)
	u, err := newUpdater(updaterOptions{
		ConfigPath: filepath.Join(root, "updater.json"), DataDir: previous.dataDir,
		TokenFile: previous.tokenFile, PersistentFile: previous.persistentFile,
		AppVersion: "0.8.1", AllowInsecure: true, SkipPlatform: true,
		TokenOverride: previous.token,
		Now: func() time.Time {
			return time.Date(2026, 10, 24, 13, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestUpdaterStagesSignedArtifactAtomicallyAndIdempotently(t *testing.T) {
	fixture, priv := signedUpdate(t, nil, false)
	srv := httptest.NewServer(fixture)
	defer srv.Close()
	u := newTestUpdater(t, srv.URL, publicKeyFor(t, priv))

	st, err := u.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != "update-available" || st.Available == nil {
		t.Fatalf("unexpected check status: %+v", st)
	}
	st, err = u.Stage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != "staged" || st.Pending == nil {
		t.Fatalf("unexpected stage status: %+v", st)
	}
	ready, err := os.ReadFile(u.readyFile)
	if err != nil {
		t.Fatal(err)
	}
	txnName := strings.TrimSpace(string(ready))
	if !validTransactionName(txnName) {
		t.Fatalf("invalid commit marker %q", txnName)
	}
	txnDir := filepath.Join(u.transactionsDir, txnName)
	if _, err := os.Stat(filepath.Join(txnDir, "chef_cookmodes.apk")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(txnDir, "pending.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(txnDir, "pending.sha256")); err != nil {
		t.Fatal(err)
	}
	if got := fixture.artifactGets.Load(); got != 1 {
		t.Fatalf("artifact downloaded %d times, want 1", got)
	}
	if _, err := u.Stage(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fixture.artifactGets.Load(); got != 1 {
		t.Fatalf("idempotent stage downloaded artifact %d times, want 1", got)
	}
}

func TestUpdaterRejectsBadSignatureHashTargetAndTraversal(t *testing.T) {
	tests := []struct {
		name          string
		mutate        func(*updatelib.Manifest)
		badSignature  bool
		mutateFixture func(*updateServer)
		want          string
	}{
		{"bad signature", nil, true, nil, "signature verification failed"},
		{"hash mismatch", nil, false, func(s *updateServer) {
			// Keep the signed size but alter the artifact bytes.
			s.artifact = append([]byte(nil), s.artifact...)
			s.artifact[len(s.artifact)-1] ^= 0xff
		}, "SHA-256 mismatch"},
		{"wrong target", func(m *updatelib.Manifest) {
			m.Artifacts[0].Target = "june-kernel"
		}, false, nil, "targets"},
		{"path traversal", func(m *updatelib.Manifest) {
			m.Artifacts[0].File = "../../system/app/Chef.apk"
		}, false, nil, "unsafe file path"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture, priv := signedUpdate(t, tc.mutate, tc.badSignature)
			if tc.mutateFixture != nil {
				tc.mutateFixture(fixture)
			}
			srv := httptest.NewServer(fixture)
			defer srv.Close()
			u := newTestUpdater(t, srv.URL, publicKeyFor(t, priv))
			_, err := u.Stage(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got error %v, want substring %q", err, tc.want)
			}
			if _, statErr := os.Stat(u.readyFile); !os.IsNotExist(statErr) {
				t.Fatalf("rejected update left pending metadata: %v", statErr)
			}
		})
	}
}

func TestUpdateControlRequiresTokenAndRedactsHeader(t *testing.T) {
	fixture, priv := signedUpdate(t, nil, false)
	srv := httptest.NewServer(fixture)
	defer srv.Close()
	u := newTestUpdater(t, srv.URL, publicKeyFor(t, priv))

	unauthorized := httptest.NewRequest(http.MethodPost, "/local/update/check", nil)
	unauthorized.TLS = &tls.ConnectionState{}
	rec := httptest.NewRecorder()
	u.handleLocalUpdate(rec, unauthorized)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want 401", rec.Code)
	}

	authorized := httptest.NewRequest(http.MethodPost, "/local/update/check", nil)
	authorized.TLS = &tls.ConnectionState{}
	authorized.Header.Set(updateControlHeader, u.token)
	rec = httptest.NewRecorder()
	u.handleLocalUpdate(rec, authorized)
	if rec.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, body=%s", rec.Code, rec.Body.String())
	}

	h := make(http.Header)
	h.Set(updateControlHeader, u.token)
	h.Set("Authorization", "Bearer secret")
	got := headerMap(h)
	if got[strings.ToLower(updateControlHeader)] != "<redacted>" || got["authorization"] != "<redacted>" {
		t.Fatalf("sensitive headers were not redacted: %#v", got)
	}
}

func TestUpdaterHTTPFailureIsReportedWithoutStaging(t *testing.T) {
	fixture, priv := signedUpdate(t, nil, false)
	fixture.manifestError = http.StatusBadGateway
	srv := httptest.NewServer(fixture)
	defer srv.Close()
	u := newTestUpdater(t, srv.URL, publicKeyFor(t, priv))
	st, err := u.Check(context.Background())
	if err == nil {
		t.Fatal("expected HTTP failure")
	}
	if st.State != "error" || !strings.Contains(st.LastError, "HTTP 502") {
		t.Fatalf("unexpected error status: %+v", st)
	}
}

func TestPersistentSequenceSurvivesDataReset(t *testing.T) {
	fixture, priv := signedUpdate(t, nil, false)
	srv := httptest.NewServer(fixture)
	defer srv.Close()
	key := publicKeyFor(t, priv)
	u := newTestUpdater(t, srv.URL, key)
	if err := writeJSONAtomic(u.persistentFile, updateRecord{
		Release: "2026.10.24-newer", Sequence: 2026102403,
		ArtifactSHA256: strings.Repeat("b", 64),
	}, 0o644); err != nil {
		t.Fatal(err)
	}

	reopened := reopenTestUpdater(t, u, key)
	if reopened.Status().CurrentSequence != 2026102403 {
		t.Fatalf("persistent sequence not loaded: %+v", reopened.Status())
	}
	st, err := reopened.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != "up-to-date" {
		t.Fatalf("older signed release was offered after data reset: %+v", st)
	}
}

func TestInstallerMinimumSequenceBlocksReplay(t *testing.T) {
	fixture, priv := signedUpdate(t, nil, false)
	srv := httptest.NewServer(fixture)
	defer srv.Close()
	u := newTestUpdater(t, srv.URL, publicKeyFor(t, priv))
	u.config.MinSequence = 2026102402
	st, err := u.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != "up-to-date" {
		t.Fatalf("installer minimum sequence did not block replay: %+v", st)
	}
}

func TestCorruptAppliedRecordFailsClosed(t *testing.T) {
	fixture, priv := signedUpdate(t, nil, false)
	srv := httptest.NewServer(fixture)
	defer srv.Close()
	key := publicKeyFor(t, priv)
	u := newTestUpdater(t, srv.URL, key)
	if err := os.WriteFile(u.appliedFile, []byte(`{"release":`), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(u.dataDir)
	_, err := newUpdater(updaterOptions{
		ConfigPath: filepath.Join(root, "updater.json"), DataDir: u.dataDir,
		TokenFile: u.tokenFile, PersistentFile: u.persistentFile,
		AppVersion: "0.8.1", AllowInsecure: true, SkipPlatform: true,
		TokenOverride: u.token,
	})
	if err == nil || !strings.Contains(err.Error(), "read applied update") {
		t.Fatalf("corrupt applied record did not fail closed: %v", err)
	}
}

func TestUpdateTokenFileIsProtected(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "token")
	token := strings.Repeat("b", 64)
	if err := os.WriteFile(path, []byte(token+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadOrCreateUpdateToken(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != token {
		t.Fatalf("got token %q", got)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("token mode = %o, want 600", st.Mode().Perm())
	}

	link := filepath.Join(root, "token-link")
	if err := os.Symlink(path, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := loadOrCreateUpdateToken(link, ""); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink token was accepted: %v", err)
	}
}

type trackingReader struct {
	read bool
}

func (r *trackingReader) Read([]byte) (int, error) {
	r.read = true
	return 0, io.EOF
}

func TestUpdateRouteDoesNotReadRequestBody(t *testing.T) {
	fixture, priv := signedUpdate(t, nil, false)
	srv := httptest.NewServer(fixture)
	defer srv.Close()
	u := newTestUpdater(t, srv.URL, publicKeyFor(t, priv))
	reader := &trackingReader{}
	req := httptest.NewRequest(http.MethodPost, "/local/update/check", reader)
	rec := httptest.NewRecorder()
	handleREST(nil, &Config{Updater: u}, rec, req)
	if reader.read {
		t.Fatal("update route read the unauthenticated request body")
	}
	if rec.Code != http.StatusUpgradeRequired {
		t.Fatalf("status = %d, want 426; body=%s", rec.Code, rec.Body.String())
	}
}
