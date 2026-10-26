package main

import "testing"

// The release gate: the embedded bundle must parse and contain the root that
// signs the Cloudflare chain used by downloads.project-july.org.
func TestEmbeddedRootsHasGTSRootR4(t *testing.T) {
	if embeddedRoots() == nil {
		t.Fatal("embedded roots are empty or unparseable")
	}
	count, hasGTS := caBundleInfo()
	if count < 1 {
		t.Fatal("embedded roots contain no certificates")
	}
	if !hasGTS {
		t.Fatal("embedded roots do not contain GTS Root R4")
	}
}
