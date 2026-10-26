package main

import (
	_ "embed"
	"encoding/json"
	"html"
	"net/http"
	"os"
	"strings"
)

//go:embed local_page.html
var localPageHTML string

func handleLocalStatus(hub *Hub, cfg *Config, w http.ResponseWriter, r *http.Request) {
	ensureFreshStatus(hub, cfg)
	writeJSON(w, 200, currentStatus(hub, cfg))
}

func handleLocalPage(hub *Hub, cfg *Config, w http.ResponseWriter, r *http.Request) {
	ensureFreshStatus(hub, cfg)
	d := currentStatus(hub, cfg)
	jb, _ := json.Marshal(d)
	jsonText := strings.ReplaceAll(string(jb), "</", "<\\/")
	headline, sev := worstProblem(d)
	page := localPageHTML
	installerVersion := d.Install.InstallerVersion
	if installerVersion == "" {
		installerVersion = "unknown"
	}
	page = strings.ReplaceAll(page, "{{VERSION}}", html.EscapeString(appVersion))
	page = strings.ReplaceAll(page, "{{INSTALLER_VERSION}}", html.EscapeString(installerVersion))
	page = strings.ReplaceAll(page, "{{JSON}}", jsonText)
	page = strings.ReplaceAll(page, "{{HEADLINE}}", html.EscapeString(headline))
	page = strings.ReplaceAll(page, "{{HEADLINE_CLASS}}", sev)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(page))
}

func handleLocalCA(cfg *Config, w http.ResponseWriter, r *http.Request) {
	path := cfg.CA
	name := "june-local-ca.crt"
	if path == "" {
		path = cfg.Cert
		name = "june-local-chain.crt"
	}
	b, err := os.ReadFile(path)
	if err != nil && path != cfg.Cert {
		// Basic installs keep the factory chain in the leaf path and have no
		// separate CA file; serve that chain instead of a dead download.
		path = cfg.Cert
		name = "june-local-chain.crt"
		b, err = os.ReadFile(path)
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = w.Write(b)
}

func worstProblem(d StatusData) (string, string) {
	sev := "ok"
	msg := "All good"
	for _, p := range d.Problems {
		if p.Severity == "red" {
			return p.Title, "red"
		}
		if p.Severity == "amber" && sev != "red" {
			sev = "amber"
			msg = p.Title
		}
	}
	return msg, sev
}
