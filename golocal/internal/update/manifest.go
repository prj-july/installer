// Package update defines the signed metadata shared by the June oven updater
// and the release publisher. A manifest is signed as exact bytes; the .sig file
// is the base64 Ed25519 signature of manifest.json without any whitespace or
// canonicalization transform.
package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	ManifestSchema = 1
	// TargetJuneOvenAndroid5 is deliberately narrow. Boot, kernel and generic
	// Android artifacts need different safeguards and are not accepted by the
	// first updater release.
	TargetJuneOvenAndroid5 = "june-oven-android5"
	MaxManifestBytes       = 1 << 20
	MaxSignatureBytes      = 16 << 10
	MaxArtifactBytes       = 384 << 20
)

type Manifest struct {
	Schema        int        `json:"schema"`
	Channel       string     `json:"channel"`
	Release       string     `json:"release"`
	Sequence      int64      `json:"sequence"`
	PublishedAt   string     `json:"published_at"`
	ExpiresAt     string     `json:"expires_at,omitempty"`
	MinAppVersion string     `json:"min_app_version,omitempty"`
	Notes         string     `json:"notes,omitempty"`
	Artifacts     []Artifact `json:"artifacts"`
}

type Artifact struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	File        string `json:"file"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	Target      string `json:"target"`
	AppID       string `json:"app_id,omitempty"`
	VersionCode int64  `json:"version_code,omitempty"`
}

func ParseManifest(data []byte, expectedChannel, expectedTarget, appVersion string, now time.Time) (Manifest, error) {
	var m Manifest
	if len(data) == 0 || len(data) > MaxManifestBytes {
		return m, fmt.Errorf("manifest size is invalid")
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("parse manifest: %w", err)
	}
	if err := ValidateManifest(&m, expectedChannel, expectedTarget, appVersion, now); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func ValidateManifest(m *Manifest, expectedChannel, expectedTarget, appVersion string, now time.Time) error {
	if m == nil {
		return errors.New("manifest is missing")
	}
	if m.Schema != ManifestSchema {
		return fmt.Errorf("unsupported manifest schema %d", m.Schema)
	}
	if !validSlug(m.Channel, 32) {
		return errors.New("manifest channel is invalid")
	}
	if expectedChannel != "" && m.Channel != expectedChannel {
		return fmt.Errorf("manifest channel %q does not match configured channel %q", m.Channel, expectedChannel)
	}
	if !validSlug(m.Release, 80) {
		return errors.New("manifest release id is invalid")
	}
	if m.Sequence <= 0 {
		return errors.New("manifest sequence must be positive")
	}
	published, err := time.Parse(time.RFC3339, m.PublishedAt)
	if err != nil {
		return errors.New("manifest published_at must be RFC3339")
	}
	if m.ExpiresAt != "" {
		expires, err := time.Parse(time.RFC3339, m.ExpiresAt)
		if err != nil {
			return errors.New("manifest expires_at must be RFC3339")
		}
		if !expires.After(published) {
			return errors.New("manifest expires_at must be after published_at")
		}
		if !now.Before(expires) {
			return fmt.Errorf("manifest expired at %s", expires.UTC().Format(time.RFC3339))
		}
	}
	if m.MinAppVersion != "" {
		if _, err := parseVersion(m.MinAppVersion); err != nil {
			return fmt.Errorf("manifest min_app_version: %w", err)
		}
		if appVersion != "" && !VersionAtLeast(appVersion, m.MinAppVersion) {
			return fmt.Errorf("updater %s is older than required %s", appVersion, m.MinAppVersion)
		}
	}
	if len(m.Notes) > 4096 {
		return errors.New("manifest notes are too long")
	}
	if len(m.Artifacts) == 0 || len(m.Artifacts) > 16 {
		return errors.New("manifest must contain 1 to 16 artifacts")
	}

	seenID := map[string]bool{}
	seenFile := map[string]bool{}
	for i := range m.Artifacts {
		a := &m.Artifacts[i]
		if !oneOf(a.ID, "chef") {
			return fmt.Errorf("artifact %d has unsupported id %q", i, a.ID)
		}
		if a.Type != "apk" {
			return fmt.Errorf("artifact %s: only APK updates are supported", a.ID)
		}
		if !SafeRelativePath(a.File) {
			return fmt.Errorf("artifact %s: unsafe file path", a.ID)
		}
		if seenID[a.ID] || seenFile[a.File] {
			return fmt.Errorf("artifact %s: duplicate id or file", a.ID)
		}
		seenID[a.ID], seenFile[a.File] = true, true
		if a.Size <= 0 || a.Size > MaxArtifactBytes {
			return fmt.Errorf("artifact %s: size %d is outside 1..%d", a.ID, a.Size, MaxArtifactBytes)
		}
		rawHash, err := hex.DecodeString(a.SHA256)
		if err != nil || len(rawHash) != sha256.Size || strings.ToLower(a.SHA256) != a.SHA256 {
			return fmt.Errorf("artifact %s: sha256 must be 64 lowercase hex characters", a.ID)
		}
		if a.Target != expectedTarget {
			return fmt.Errorf("artifact %s targets %q, expected %q", a.ID, a.Target, expectedTarget)
		}
		if a.ID == "chef" && a.AppID != "" && a.AppID != "com.junelife.device.chef" {
			return fmt.Errorf("artifact chef has unexpected app_id %q", a.AppID)
		}
		if a.VersionCode < 0 {
			return fmt.Errorf("artifact %s has a negative version_code", a.ID)
		}
	}
	if !seenID["chef"] {
		return errors.New("manifest does not contain the chef artifact")
	}
	return nil
}

func ResolveArtifactURL(manifestURL string, a Artifact) (*url.URL, error) {
	base, err := url.Parse(manifestURL)
	if err != nil {
		return nil, fmt.Errorf("manifest URL: %w", err)
	}
	ref, err := url.Parse(a.File)
	if err != nil {
		return nil, fmt.Errorf("artifact URL: %w", err)
	}
	if ref.IsAbs() || ref.Host != "" || ref.User != nil {
		return nil, errors.New("artifact file must be relative to the signed manifest")
	}
	u := base.ResolveReference(ref)
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, errors.New("artifact URL must use HTTP(S)")
	}
	if u.User != nil {
		return nil, errors.New("artifact URL must not contain credentials")
	}
	return u, nil
}

func SafeRelativePath(name string) bool {
	if name == "" || len(name) > 240 || strings.ContainsAny(name, "\\?#") {
		return false
	}
	if strings.HasPrefix(name, "/") || path.Clean(name) != name || name == "." {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func VersionAtLeast(got, want string) bool {
	a, errA := parseVersion(got)
	b, errB := parseVersion(want)
	if errA != nil || errB != nil {
		return false
	}
	for i := 0; i < len(a); i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return true
}

func parseVersion(v string) ([3]int, error) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v = strings.SplitN(v, "-", 2)[0]
	v = strings.SplitN(v, "+", 2)[0]
	parts := strings.Split(v, ".")
	if len(parts) < 1 || len(parts) > 3 {
		return out, fmt.Errorf("invalid version %q", v)
	}
	for i, part := range parts {
		if part == "" {
			return out, fmt.Errorf("invalid version %q", v)
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return out, fmt.Errorf("invalid version %q", v)
		}
		out[i] = n
	}
	return out, nil
}

func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("updater public key is empty")
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(s)
	}
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("updater public key must be %d base64-encoded bytes", ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

func EncodePublicKey(key ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(key)
}

func DecodeSignature(s []byte) ([]byte, error) {
	s = []byte(strings.TrimSpace(string(s)))
	if len(s) == 0 || len(s) > MaxSignatureBytes {
		return nil, errors.New("signature file size is invalid")
	}
	sig, err := base64.StdEncoding.DecodeString(string(s))
	if err != nil {
		sig, err = base64.RawStdEncoding.DecodeString(string(s))
	}
	if err != nil || len(sig) != ed25519.SignatureSize {
		return nil, fmt.Errorf("signature must be %d base64-encoded bytes", ed25519.SignatureSize)
	}
	return sig, nil
}

func EncodeSignature(sig []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(sig) + "\n")
}

func VerifySignature(pub ed25519.PublicKey, data, sig []byte) error {
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("invalid Ed25519 public key")
	}
	if !ed25519.Verify(pub, data, sig) {
		return errors.New("manifest signature verification failed")
	}
	return nil
}

func GenerateSigningKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	return pub, priv, nil
}

func MarshalPrivateKey(priv ed25519.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func ParsePrivateKey(data []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("signing key must be a PKCS#8 PEM private key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("signing key is not Ed25519")
	}
	return priv, nil
}

func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParsePrivateKey(b)
}

func validSlug(s string, max int) bool {
	if s == "" || len(s) > max || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' || r == '+' {
			continue
		}
		return false
	}
	return true
}

func oneOf(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}
