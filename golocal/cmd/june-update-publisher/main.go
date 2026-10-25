// june-update-publisher creates the Ed25519 keys and signed repository used by
// June Local's pull updater. The release private key is never copied into the
// installer or oven image.
package main

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	updatelib "june-golocal/internal/update"
)

const usage = `usage:
  june-update-publisher keygen -private KEY.pem -public KEY.txt
  june-update-publisher verify -public-key KEY.txt -manifest manifest.json -signature manifest.sig
  june-update-publisher publish -private-key KEY.pem -out DIR \
    -release ID -sequence N -artifact chef=Chef.apk [options]
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	case "publish":
		err = publish(os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	publicPath := fs.String("public-key", "", "Ed25519 public key file (base64)")
	manifestPath := fs.String("manifest", "", "signed manifest.json")
	signaturePath := fs.String("signature", "", "base64 manifest.sig")
	channel := fs.String("channel", "", "expected channel (optional)")
	target := fs.String("target", "", "expected target (optional)")
	appVersion := fs.String("app-version", "", "client version for min-app-version checks")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *publicPath == "" || *manifestPath == "" || *signaturePath == "" {
		return errors.New("-public-key, -manifest and -signature are required")
	}
	pubText, err := os.ReadFile(*publicPath)
	if err != nil {
		return err
	}
	pub, err := updatelib.ParsePublicKey(string(pubText))
	if err != nil {
		return err
	}
	manifestBytes, err := os.ReadFile(*manifestPath)
	if err != nil {
		return err
	}
	signatureBytes, err := os.ReadFile(*signaturePath)
	if err != nil {
		return err
	}
	sig, err := updatelib.DecodeSignature(signatureBytes)
	if err != nil {
		return err
	}
	if err := updatelib.VerifySignature(pub, manifestBytes, sig); err != nil {
		return err
	}
	m, err := updatelib.ParseManifest(manifestBytes, *channel, *target, *appVersion, time.Now().UTC())
	if err != nil {
		return err
	}
	fmt.Printf("verified %s sequence=%d channel=%s artifacts=%d\n", m.Release, m.Sequence, m.Channel, len(m.Artifacts))
	return nil
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	privatePath := fs.String("private", "", "output Ed25519 private key (PKCS#8 PEM)")
	publicPath := fs.String("public", "", "output Ed25519 public key (base64)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *privatePath == "" || *publicPath == "" {
		return errors.New("both -private and -public are required")
	}
	pub, priv, err := updatelib.GenerateSigningKey()
	if err != nil {
		return err
	}
	pemBytes, err := updatelib.MarshalPrivateKey(priv)
	if err != nil {
		return err
	}
	if err := writeExclusive(*privatePath, pemBytes, 0o600); err != nil {
		return err
	}
	if err := writeExclusive(*publicPath, []byte(updatelib.EncodePublicKey(pub)+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("private key: %s (mode 0600, keep offline)\n", *privatePath)
	fmt.Printf("public key:  %s\n", *publicPath)
	fmt.Printf("public value: %s\n", updatelib.EncodePublicKey(pub))
	return nil
}

type artifactFlags []string

func (a *artifactFlags) String() string { return strings.Join(*a, ",") }
func (a *artifactFlags) Set(v string) error {
	*a = append(*a, v)
	return nil
}

func publish(args []string) error {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	privatePath := fs.String("private-key", "", "release Ed25519 private key")
	outDir := fs.String("out", "dist", "repository output directory")
	release := fs.String("release", "", "human-readable release id")
	channel := fs.String("channel", "stable", "release channel")
	sequence := fs.Int64("sequence", 0, "monotonically increasing release sequence")
	publishedAt := fs.String("published-at", time.Now().UTC().Format(time.RFC3339), "RFC3339 publication time")
	expiresAt := fs.String("expires-at", "", "optional RFC3339 expiry")
	minAppVersion := fs.String("min-app-version", "", "minimum june-local version")
	target := fs.String("target", updatelib.TargetJuneOvenAndroid5, "artifact target")
	notes := fs.String("notes", "", "release notes")
	notesFile := fs.String("notes-file", "", "read release notes from this file")
	appID := fs.String("app-id", "com.junelife.device.chef", "Android package id")
	versionCode := fs.Int64("version-code", 0, "Android version code")
	copyArtifacts := fs.Bool("copy", true, "copy artifacts into DIR/artifacts")
	allowReplace := fs.Bool("allow-replace", false, "allow a local manifest with an equal or newer sequence to be replaced")
	var artifacts artifactFlags
	fs.Var(&artifacts, "artifact", "artifact in id=path form (chef=Chef.apk)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *privatePath == "" || *outDir == "" || *release == "" || *sequence <= 0 || len(artifacts) == 0 {
		return errors.New("-private-key, -out, -release, -sequence and at least one -artifact are required")
	}
	priv, err := updatelib.LoadPrivateKey(*privatePath)
	if err != nil {
		return fmt.Errorf("load private key: %w", err)
	}
	if *notesFile != "" {
		b, err := os.ReadFile(*notesFile)
		if err != nil {
			return err
		}
		*notes = string(b)
	}
	if err := os.MkdirAll(filepath.Join(*outDir, "artifacts"), 0o755); err != nil {
		return err
	}

	m := updatelib.Manifest{
		Schema: updatelib.ManifestSchema, Channel: *channel, Release: *release,
		Sequence: *sequence, PublishedAt: *publishedAt, ExpiresAt: *expiresAt,
		MinAppVersion: *minAppVersion, Notes: *notes,
	}
	for _, spec := range artifacts {
		id, source, ok := strings.Cut(spec, "=")
		if !ok || id == "" || source == "" {
			return fmt.Errorf("artifact %q must be id=path", spec)
		}
		if id != "chef" {
			return fmt.Errorf("unsupported artifact id %q", id)
		}
		info, err := os.Stat(source)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("artifact %s is not a regular file", source)
		}
		if err := validateAPK(source); err != nil {
			return err
		}
		hash, err := fileSHA256(source)
		if err != nil {
			return err
		}
		name := filepath.Base(source)
		relative := "artifacts/" + name
		if !updatelib.SafeRelativePath(relative) {
			return fmt.Errorf("unsafe artifact filename %q", name)
		}
		a := updatelib.Artifact{
			ID: id, Type: "apk", File: relative, Size: info.Size(), SHA256: hash,
			Target: *target, AppID: *appID, VersionCode: *versionCode,
		}
		m.Artifacts = append(m.Artifacts, a)
		if *copyArtifacts {
			if err := copyAtomic(source, filepath.Join(*outDir, filepath.FromSlash(relative)), 0o644); err != nil {
				return err
			}
		}
	}
	if err := updatelib.ValidateManifest(&m, *channel, *target, "", time.Now().UTC()); err != nil {
		return err
	}
	if existingPath := filepath.Join(*outDir, "manifest.json"); !*allowReplace {
		if existingBytes, err := os.ReadFile(existingPath); err == nil {
			var existing updatelib.Manifest
			if err := json.Unmarshal(existingBytes, &existing); err != nil {
				return fmt.Errorf("existing local manifest: %w", err)
			}
			if m.Sequence <= existing.Sequence {
				return fmt.Errorf("release sequence %d is not newer than local sequence %d; use -allow-replace only for a deliberate correction", m.Sequence, existing.Sequence)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	manifestBytes, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	manifestBytes = append(manifestBytes, '\n')
	signature := ed25519.Sign(priv, manifestBytes)
	if err := updatelib.VerifySignature(priv.Public().(ed25519.PublicKey), manifestBytes, signature); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(*outDir, "manifest.json"), manifestBytes, 0o644); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(*outDir, "manifest.sig"), updatelib.EncodeSignature(signature), 0o644); err != nil {
		return err
	}
	fmt.Printf("published %s sequence=%d channel=%s to %s\n", m.Release, m.Sequence, m.Channel, *outDir)
	for _, a := range m.Artifacts {
		fmt.Printf("  %s %s %d bytes sha256=%s\n", a.ID, a.File, a.Size, a.SHA256)
	}
	return nil
}

func validateAPK(path string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("%s is not a ZIP/APK: %w", path, err)
	}
	defer zr.Close()
	haveManifest, haveDex := false, false
	for _, f := range zr.File {
		switch f.Name {
		case "AndroidManifest.xml":
			haveManifest = true
		case "classes.dex":
			haveDex = true
		}
	}
	if !haveManifest || !haveDex {
		return fmt.Errorf("%s is missing AndroidManifest.xml or classes.dex", path)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
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

func copyAtomic(source, dest string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeReaderAtomic(dest, in, mode)
}

func writeAtomic(path string, b []byte, mode os.FileMode) error {
	return writeReaderAtomic(path, strings.NewReader(string(b)), mode)
}

func writeReaderAtomic(path string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func writeExclusive(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
