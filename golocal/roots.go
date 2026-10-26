package main

// The updater runs on the oven as a static Go binary built for linux/arm, so
// crypto/x509 cannot use the Android trust store and the oven has no Linux CA
// bundle.  Without explicit roots every HTTPS manifest fetch fails with
// "certificate signed by unknown authority" (see the v0.9.1 incident).
//
// Embed a Mozilla-derived bundle and use it as the updater's RootCAs.  The
// release gate (scripts/verify-release.sh and roots_test.go) requires the
// bundle to contain GTS Root R4, which signs the Cloudflare chain used by
// downloads.project-july.org.

import (
	"crypto/x509"
	_ "embed"
	"encoding/pem"
	"strings"
	"sync"
)

//go:embed certs/roots.pem
var rootsPEM []byte

var (
	rootsOnce  sync.Once
	rootsPool  *x509.CertPool
	rootsCount int
	rootsGTS   bool
)

func embeddedRoots() *x509.CertPool {
	rootsOnce.Do(func() {
		pool := x509.NewCertPool()
		rest := rootsPEM
		for {
			var block *pem.Block
			block, rest = pem.Decode(rest)
			if block == nil {
				break
			}
			if block.Type != "CERTIFICATE" {
				continue
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				continue
			}
			pool.AddCert(cert)
			rootsCount++
			if strings.Contains(cert.Subject.CommonName, "GTS Root R4") ||
				strings.Contains(cert.Subject.String(), "GTS Root R4") {
				rootsGTS = true
			}
		}
		if rootsCount > 0 {
			rootsPool = pool
		}
	})
	return rootsPool
}

// caBundleInfo reports the embedded bundle state for /local/status.
func caBundleInfo() (count int, hasGTSRootR4 bool) {
	embeddedRoots()
	return rootsCount, rootsGTS
}
