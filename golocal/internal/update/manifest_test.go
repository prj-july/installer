package update

import (
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func testManifest() Manifest {
	return Manifest{
		Schema: ManifestSchema, Channel: "stable", Release: "2026.10.24-foodack",
		Sequence: 2026102401, PublishedAt: "2026-10-24T12:00:00Z",
		MinAppVersion: "0.8.1", Notes: "Food gate acknowledgment",
		Artifacts: []Artifact{{
			ID: "chef", Type: "apk", File: "artifacts/Chef_signed.apk",
			Size: 42, SHA256: strings.Repeat("a", 64),
			Target: TargetJuneOvenAndroid5, AppID: "com.junelife.device.chef",
			VersionCode: 7,
		}},
	}
}

func TestSignAndParseManifest(t *testing.T) {
	pub, priv, err := GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	m := testManifest()
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	sig := ed25519.Sign(priv, data)
	if err := VerifySignature(pub, data, sig); err != nil {
		t.Fatal(err)
	}
	got, err := ParseManifest(data, "stable", TargetJuneOvenAndroid5, "0.8.1",
		time.Date(2026, 10, 24, 13, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if got.Release != m.Release || got.Artifacts[0].VersionCode != 7 {
		t.Fatalf("unexpected manifest: %+v", got)
	}
}

func TestSignatureTamperRejected(t *testing.T) {
	pub, priv, err := GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"schema":1}`)
	sig := ed25519.Sign(priv, data)
	data[0] ^= 1
	if err := VerifySignature(pub, data, sig); err == nil {
		t.Fatal("tampered manifest signature was accepted")
	}
}

func TestManifestRejectsUnsafeAndMismatchedArtifacts(t *testing.T) {
	now := time.Date(2026, 10, 24, 13, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		edit func(*Manifest)
		want string
	}{
		{"path traversal", func(m *Manifest) { m.Artifacts[0].File = "../../system/app/x.apk" }, "unsafe file path"},
		{"absolute path", func(m *Manifest) { m.Artifacts[0].File = "/system/app/x.apk" }, "unsafe file path"},
		{"wrong target", func(m *Manifest) { m.Artifacts[0].Target = "june-kernel" }, "targets"},
		{"bad hash", func(m *Manifest) { m.Artifacts[0].SHA256 = strings.Repeat("A", 64) }, "sha256"},
		{"expired", func(m *Manifest) { m.ExpiresAt = "2026-10-24T12:30:00Z" }, "expired"},
		{"newer minimum", func(m *Manifest) { m.MinAppVersion = "0.9.0" }, "is older than required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := testManifest()
			tc.edit(&m)
			err := ValidateManifest(&m, "stable", TargetJuneOvenAndroid5, "0.8.1", now)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got error %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestVersionAtLeastAndArtifactURL(t *testing.T) {
	if !VersionAtLeast("0.10.2", "0.8.99") {
		t.Fatal("0.10.2 should be at least 0.8.99")
	}
	if VersionAtLeast("0.8.1", "0.8.2") {
		t.Fatal("0.8.1 should not be at least 0.8.2")
	}
	a := testManifest().Artifacts[0]
	u, err := ResolveArtifactURL("https://downloads.example/updates/stable/manifest.json", a)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := u.String(), "https://downloads.example/updates/stable/artifacts/Chef_signed.apk"; got != want {
		t.Fatalf("got URL %q, want %q", got, want)
	}
}

func TestPrivateKeyRoundTrip(t *testing.T) {
	_, priv, err := GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	b, err := MarshalPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParsePrivateKey(b)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("signed bytes")
	if !ed25519.Verify(priv.Public().(ed25519.PublicKey), data, ed25519.Sign(got, data)) {
		t.Fatal("round-tripped private key does not verify")
	}
}
