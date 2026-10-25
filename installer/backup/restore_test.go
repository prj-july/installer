package backup

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type bufDevice struct {
	bytes.Buffer
}

func (b *bufDevice) Close() error { return nil }

func fixture(t *testing.T, body string) (string, RestoreOptions) {
	t.Helper()
	dir := t.TempDir()
	img := writeImage(t, dir, "oven.img", body)
	if _, err := WriteSidecar(img); err != nil {
		t.Fatal(err)
	}
	opt := RestoreOptions{
		Options:   Options{ExpectedSize: int64(len(body))},
		BootDisks: func() []string { return []string{"/dev/sda"} },
	}
	return img, opt
}

func TestPlanRestoreDryRunDefault(t *testing.T) {
	img, opt := fixture(t, "june-emmc-bytes")
	plan, err := PlanRestore(img, opt)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Write || plan.WouldWrite {
		t.Fatalf("dry-run must not write: %+v", plan)
	}
	if !plan.Image.Verified {
		t.Fatal("dry-run should still verify the image")
	}
	if err := Execute(plan, opt); err != nil {
		t.Fatal(err)
	}
}

func TestPlanRestoreRefusesWriteWithoutConfirm(t *testing.T) {
	img, opt := fixture(t, "june-emmc-bytes")
	opt.Write = true
	opt.Device = filepath.Join(t.TempDir(), "emmc")
	_, err := PlanRestore(img, opt)
	if err == nil {
		t.Fatal("expected confirm stop")
	}
	s, ok := err.(Stop)
	if !ok || s.Code != "confirm" {
		t.Fatalf("want confirm stop, got %v", err)
	}
	if !strings.Contains(s.Detail, "Dry-run is the default") {
		t.Fatalf("confirm reason: %s", s.Detail)
	}
}

func TestPlanRestoreRefusesBootDisk(t *testing.T) {
	img, opt := fixture(t, "june-emmc-bytes")
	opt.Write = true
	opt.Confirm = RestoreConfirm
	opt.Device = "/dev/sda"
	opt.DeviceSize = func(string) (int64, error) { return opt.expected(), nil }
	_, err := PlanRestore(img, opt)
	if err == nil {
		t.Fatal("expected boot-disk stop")
	}
	if s, ok := err.(Stop); !ok || s.Code != "boot-disk" {
		t.Fatalf("want boot-disk stop, got %v", err)
	}
}

func TestPlanRestoreRefusesWrongDeviceSize(t *testing.T) {
	img, opt := fixture(t, "june-emmc-bytes")
	opt.Device = "/dev/sdb"
	opt.DeviceSize = func(string) (int64, error) { return 123456, nil }
	_, err := PlanRestore(img, opt)
	if err == nil {
		t.Fatal("expected device-size stop")
	}
	s, ok := err.(Stop)
	if !ok || s.Code != "device-size" {
		t.Fatalf("want device-size stop, got %v", err)
	}
	if !strings.Contains(s.Title, "not a June oven") {
		t.Fatalf("plain-language device reason: %s", s.Title)
	}
}

func TestExecuteWriteCopiesBytes(t *testing.T) {
	body := "june-emmc-bytes"
	img, opt := fixture(t, body)
	dev := &bufDevice{}
	opt.Write = true
	opt.Confirm = RestoreConfirm
	opt.Device = "/dev/june"
	opt.DeviceSize = func(string) (int64, error) { return int64(len(body)), nil }
	opt.OpenDevice = func(string) (io.WriteCloser, error) { return dev, nil }

	plan, err := PlanRestore(img, opt)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.WouldWrite {
		t.Fatal("plan should be ready to write")
	}
	if err := Execute(plan, opt); err != nil {
		t.Fatal(err)
	}
	if got := dev.String(); got != body {
		t.Fatalf("wrote %q, want %q", got, body)
	}
}

func TestExecuteDryRunDoesNotOpenDevice(t *testing.T) {
	img, opt := fixture(t, "june-emmc-bytes")
	opt.Device = "/dev/june"
	opt.DeviceSize = func(string) (int64, error) { return opt.expected(), nil }
	opt.OpenDevice = func(string) (io.WriteCloser, error) {
		t.Fatal("dry-run opened the device")
		return nil, nil
	}
	plan, err := PlanRestore(img, opt)
	if err != nil {
		t.Fatal(err)
	}
	if err := Execute(plan, opt); err != nil {
		t.Fatal(err)
	}
}

func TestLatestVerifiedSkipsBadImages(t *testing.T) {
	dir := t.TempDir()
	good := writeImage(t, dir, "good.img", "good-image")
	if _, err := WriteSidecar(good); err != nil {
		t.Fatal(err)
	}
	bad := writeImage(t, dir, "bad.img", "bad-image-x")
	if _, err := WriteSidecar(bad); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("tampered!!"), 0o644); err != nil {
		t.Fatal(err)
	}
	newer := writeImage(t, dir, "newer.img", "good-image") // same size as good
	if _, err := WriteSidecar(newer); err != nil {
		t.Fatal(err)
	}

	opt := Options{ExpectedSize: int64(len("good-image"))}
	rep, err := LatestVerified(dir, opt)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(rep.Image) != "newer.img" {
		t.Fatalf("latest verified = %s, want newer.img", rep.Image)
	}
}

func TestLatestVerifiedNoneGood(t *testing.T) {
	dir := t.TempDir()
	img := writeImage(t, dir, "broke.img", "xxxx")
	if _, err := WriteSidecar(img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(img, []byte("yyyy"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LatestVerified(dir, Options{ExpectedSize: 4})
	if err == nil {
		t.Fatal("expected checksum stop")
	}
	if s, ok := err.(Stop); !ok || s.Code != "checksum" {
		t.Fatalf("want checksum stop, got %v", err)
	}
}

type failCloseDevice struct {
	bufDevice
}

func (f *failCloseDevice) Close() error { return io.ErrShortWrite }

func TestExecuteReportsCloseError(t *testing.T) {
	body := "june-emmc-bytes"
	img, opt := fixture(t, body)
	opt.Write = true
	opt.Confirm = RestoreConfirm
	opt.Device = "/dev/june"
	opt.DeviceSize = func(string) (int64, error) { return int64(len(body)), nil }
	opt.OpenDevice = func(string) (io.WriteCloser, error) { return &failCloseDevice{}, nil }

	plan, err := PlanRestore(img, opt)
	if err != nil {
		t.Fatal(err)
	}
	err = Execute(plan, opt)
	if s, ok := err.(Stop); !ok || s.Code != "write" {
		t.Fatalf("want write stop on a failed close, got %v", err)
	}
}
