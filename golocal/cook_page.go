package main

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"
)

//go:embed cook_page.html
var cookPageHTML string

func handleLocalCook(hub *Hub, w http.ResponseWriter, r *http.Request) {
	view := cookView(hub)
	path := r.URL.Path
	wantPage := path == "/local/cook.html" || (path == "/local/cook" && acceptHTML(r))
	if wantPage {
		serveCookPage(w, view)
		return
	}
	writeJSON(w, 200, view)
}

func acceptHTML(r *http.Request) bool {
	a := r.Header.Get("Accept")
	return strings.Contains(a, "text/html") && !strings.Contains(a, "application/json")
}

func serveCookPage(w http.ResponseWriter, view CookView) {
	jb, _ := json.Marshal(view)
	jsonText := strings.ReplaceAll(string(jb), "</", "<\\/")
	page := strings.ReplaceAll(cookPageHTML, "{{JSON}}", jsonText)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(page))
}
