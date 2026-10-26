package main

import (
	"embed"
	"net/http"
	"path"
	"strings"
)

// The /local page uses the installer's typeface (Barlow, SIL OFL, fonts/OFL.txt) so it looks
// like the June USB screens and the app. The oven is often offline, so the fonts ship inside
// june-local instead of coming from a font CDN.
//
//go:embed fonts/*.ttf fonts/OFL.txt
var localFonts embed.FS

func handleLocalFont(w http.ResponseWriter, r *http.Request) {
	name := path.Base(r.URL.Path)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	b, err := localFonts.ReadFile("fonts/" + name)
	if err != nil || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(name, ".ttf") {
		w.Header().Set("Content-Type", "font/ttf")
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	// The files only change with a new june-local, and the page links them by name.
	w.Header().Set("Cache-Control", "public, max-age=604800")
	_, _ = w.Write(b)
}
