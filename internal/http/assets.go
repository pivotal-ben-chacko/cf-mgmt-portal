package http

import (
	"embed"
	"net/http"
	"strings"
)

//go:embed assets
var assetsFS embed.FS

// handleAsset serves embedded static assets (logo, icons) from /assets/<name>.
// Public (the header/home render before sign-in) and cacheable. The flat
// single-segment path (no slashes) prevents traversal outside the embedded dir.
func handleAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/assets/")
	if name == "" || strings.ContainsAny(name, "/\\") {
		http.NotFound(w, r)
		return
	}
	b, err := assetsFS.ReadFile("assets/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ct := "application/octet-stream"
	switch {
	case strings.HasSuffix(name, ".svg"):
		ct = "image/svg+xml"
	case strings.HasSuffix(name, ".png"):
		ct = "image/png"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(b)
}
