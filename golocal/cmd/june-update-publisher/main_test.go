package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	updatelib "june-golocal/internal/update"
)

func TestPublishAndVerifyRepository(t *testing.T) {
	root := t.TempDir()
	apk := filepath.Join(root, "Chef_test.apk")
	f, err := os.Create(apk)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, name := range []string{"AndroidManifest.xml", "classes.dex"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	pub, priv, err := updatelib.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(root, "release.pem")
	publicPath := filepath.Join(root, "release.pub")
	pemBytes, err := updatelib.MarshalPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privatePath, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publicPath, []byte(updatelib.EncodePublicKey(pub)), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(root, "dist")
	args := []string{
		"-private-key", privatePath, "-out", out,
		"-release", "2026.10.24-test", "-sequence", "42",
		"-published-at", "2026-10-24T12:00:00Z",
		"-artifact", "chef=" + apk,
	}
	if err := publish(args); err != nil {
		t.Fatal(err)
	}
	if err := verify([]string{
		"-public-key", publicPath,
		"-manifest", filepath.Join(out, "manifest.json"),
		"-signature", filepath.Join(out, "manifest.sig"),
		"-channel", "stable", "-target", updatelib.TargetJuneOvenAndroid5,
		"-app-version", "0.8.1",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "artifacts", "Chef_test.apk")); err != nil {
		t.Fatal(err)
	}
	err = publish(args)
	if err == nil || !strings.Contains(err.Error(), "is not newer") {
		t.Fatalf("expected local sequence downgrade protection, got %v", err)
	}

	bundlePath := filepath.Join(root, "release.zip")
	if err := bundle([]string{"-repo", out, "-out", bundlePath}); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "manifest.json,manifest.sig,artifacts/Chef_test.apk" {
		t.Fatalf("bundle holds %v", names)
	}
}
