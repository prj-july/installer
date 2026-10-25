// Package backup verifies June oven eMMC images and plans a restore without
// writing anything unless the caller asks for it.
package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DefaultExpectedSize is the June oven eMMC size the installer already uses
// (7818182656 bytes, about 7.8 GB / 7.3 GiB).
const DefaultExpectedSize = int64(7818182656)

// Stop is a plain-language refusal. Callers print Title and Detail and must
// not write to the oven.
type Stop struct {
	Code   string
	Title  string
	Detail string
}

func (s Stop) Error() string {
	if s.Detail == "" {
		return s.Title
	}
	return s.Title + "\n" + s.Detail
}

func stop(code, title, detail string) Stop {
	return Stop{Code: code, Title: title, Detail: detail}
}

// Options control size checks. ExpectedSize 0 means DefaultExpectedSize.
type Options struct {
	ExpectedSize     int64
	AllowUnverified  bool
	AllowMissingSize bool
}

func (o Options) expected() int64 {
	if o.ExpectedSize > 0 {
		return o.ExpectedSize
	}
	return DefaultExpectedSize
}

// Report is the result of verifying one backup image.
type Report struct {
	Image    string `json:"image"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	Sidecar  string `json:"sidecar,omitempty"`
	Verified bool   `json:"verified"`
}

// SidecarPath is the SHA256 file written next to the image.
func SidecarPath(image string) string {
	return image + ".sha256"
}

// Verify checks that image exists, is the expected size, and matches its
// .sha256 sidecar. The sidecar may name a different path (the installer used
// to write the mount-time path); only the hex digest is compared.
func Verify(image string, opt Options) (Report, error) {
	image = filepath.Clean(image)
	st, err := os.Stat(image)
	if err != nil {
		return Report{}, stop("missing",
			"Backup image not found",
			fmt.Sprintf("%s does not exist or cannot be read.\nPick a .img file made by this installer.", image))
	}
	if st.IsDir() {
		return Report{}, stop("missing",
			"Backup image not found",
			fmt.Sprintf("%s is a folder, not a backup file.", image))
	}

	rep := Report{Image: image, Size: st.Size(), Sidecar: SidecarPath(image)}
	wantSize := opt.expected()
	if !opt.AllowMissingSize && st.Size() != wantSize {
		return rep, stop("size",
			"This backup file is the wrong size",
			fmt.Sprintf("%s is %d bytes; a June oven backup is %d bytes (about 7.8 GB).\nThis is not a complete oven image. Do not restore it.",
				filepath.Base(image), st.Size(), wantSize))
	}

	sum, err := HashFile(image)
	if err != nil {
		return rep, stop("checksum",
			"Could not read the backup image",
			fmt.Sprintf("Reading %s failed: %v", image, err))
	}
	rep.SHA256 = sum

	want, sidecarErr := ReadSidecar(SidecarPath(image))
	if sidecarErr != nil {
		if opt.AllowUnverified {
			return rep, nil
		}
		return rep, stop("sidecar",
			"This backup has no SHA256 file",
			fmt.Sprintf("Expected %s next to the image.\nRefusing to restore an unverified backup. Make a new backup, or write the sidecar with:\n  june-backup checksum -image %s",
				filepath.Base(SidecarPath(image)), image))
	}
	if !strings.EqualFold(want, sum) {
		return rep, stop("checksum",
			"The backup file does not match its SHA256",
			fmt.Sprintf("Recorded: %s\nComputed: %s\nThe file is damaged, incomplete, or was changed after it was made. Do not restore it.",
				want, sum))
	}
	rep.Verified = true
	return rep, nil
}

// HashFile returns the hex SHA256 of path.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ReadSidecar returns the hex digest from a sha256sum-format file. The
// filename column is ignored so a sidecar still works after the USB is
// remounted at a different path.
func ReadSidecar(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(b))
	if line == "" {
		return "", fmt.Errorf("empty sidecar")
	}
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", fmt.Errorf("empty sidecar")
	}
	sum := strings.ToLower(fields[0])
	if len(sum) != 64 {
		return "", fmt.Errorf("sidecar is not a SHA256 digest")
	}
	for _, c := range sum {
		if c < '0' || (c > '9' && c < 'a') || c > 'f' {
			return "", fmt.Errorf("sidecar is not a SHA256 digest")
		}
	}
	return sum, nil
}

// WriteSidecar writes path.sha256 in sha256sum format using the basename so
// `sha256sum -c` still works after the drive is remounted.
func WriteSidecar(image string) (Report, error) {
	image = filepath.Clean(image)
	sum, err := HashFile(image)
	if err != nil {
		return Report{}, stop("checksum",
			"Could not read the backup image",
			fmt.Sprintf("Reading %s failed: %v", image, err))
	}
	st, err := os.Stat(image)
	if err != nil {
		return Report{}, err
	}
	line := sum + "  " + filepath.Base(image) + "\n"
	side := SidecarPath(image)
	if err := os.WriteFile(side, []byte(line), 0o644); err != nil {
		return Report{}, stop("sidecar",
			"Could not write the SHA256 file",
			fmt.Sprintf("Writing %s failed: %v", side, err))
	}
	return Report{Image: image, Size: st.Size(), SHA256: sum, Sidecar: side, Verified: true}, nil
}
