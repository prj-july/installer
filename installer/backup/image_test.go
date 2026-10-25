package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeImage(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWriteAndVerify(t *testing.T) {
	dir := t.TempDir()
	img := writeImage(t, dir, "oven.img", "june-oven-image")
	opt := Options{ExpectedSize: int64(len("june-oven-image"))}

	if _, err := Verify(img, opt); err == nil {
		t.Fatal("verify succeeded without a sidecar")
	} else if s, ok := err.(Stop); !ok || s.Code != "sidecar" {
		t.Fatalf("want sidecar stop, got %v", err)
	}

	rep, err := WriteSidecar(img)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Verified || len(rep.SHA256) != 64 {
		t.Fatalf("checksum report: %+v", rep)
	}
	side, err := os.ReadFile(SidecarPath(img))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(side), "  oven.img\n") {
		t.Fatalf("sidecar should use the basename, got %q", side)
	}

	got, err := Verify(img, opt)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Verified || got.SHA256 != rep.SHA256 {
		t.Fatalf("verify mismatch: %+v", got)
	}
}

func TestVerifyWrongSize(t *testing.T) {
	dir := t.TempDir()
	img := writeImage(t, dir, "tiny.img", "xx")
	_, _ = WriteSidecar(img)
	_, err := Verify(img, Options{ExpectedSize: 7818182656})
	if err == nil {
		t.Fatal("expected size stop")
	}
	s, ok := err.(Stop)
	if !ok || s.Code != "size" {
		t.Fatalf("want size stop, got %v", err)
	}
	if !strings.Contains(s.Detail, "not a complete oven image") {
		t.Fatalf("plain-language size reason: %s", s.Detail)
	}
}

func TestVerifyBadChecksum(t *testing.T) {
	dir := t.TempDir()
	img := writeImage(t, dir, "oven.img", "payload-a")
	opt := Options{ExpectedSize: int64(len("payload-a"))}
	if _, err := WriteSidecar(img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(img, []byte("payload-b"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Verify(img, opt)
	if err == nil {
		t.Fatal("expected checksum stop")
	}
	s, ok := err.(Stop)
	if !ok || s.Code != "checksum" {
		t.Fatalf("want checksum stop, got %v", err)
	}
	if !strings.Contains(s.Detail, "Do not restore") {
		t.Fatalf("plain-language checksum reason: %s", s.Detail)
	}
}

func TestReadSidecarIgnoresOldPath(t *testing.T) {
	dir := t.TempDir()
	img := writeImage(t, dir, "261012-1200.img", "emmc")
	sum, err := HashFile(img)
	if err != nil {
		t.Fatal(err)
	}
	// Sidecar as the live installer used to write it: absolute path at backup time.
	old := sum + "  /mnt/june-backup/261012-1200.img\n"
	if err := os.WriteFile(SidecarPath(img), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Verify(img, Options{ExpectedSize: 4})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Verified {
		t.Fatal("sidecar with a stale path should still verify")
	}
}

func TestAllowUnverified(t *testing.T) {
	dir := t.TempDir()
	img := writeImage(t, dir, "oven.img", "raw")
	rep, err := Verify(img, Options{ExpectedSize: 3, AllowUnverified: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verified {
		t.Fatal("unverified image must not be marked verified")
	}
}
