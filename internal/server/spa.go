package server

import (
	"embed"
	"io/fs"
	"net/http"
	pathpkg "path"
	"strings"
)

//go:embed all:ui_dist
var embeddedUI embed.FS

const placeholderHTML = `<!DOCTYPE html>
<html><head><title>Mentat Chat</title></head>
<body><div id="app-root">Loading...</div></body></html>`

func setSPACacheHeaders(w http.ResponseWriter, assetPath string) {
	switch {
	case assetPath == "", assetPath == "index.html":
		// HTML should always revalidate so rebuilt asset manifests take effect.
		w.Header().Set("Cache-Control", "no-cache")
	case strings.HasPrefix(assetPath, "assets/"):
		// Vite emits hashed asset filenames, so they are safe to cache hard.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	default:
		w.Header().Set("Cache-Control", "no-cache")
	}
}

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	// In dev mode, always return placeholder.
	if s.dev {
		setSPACacheHeaders(w, "index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(placeholderHTML))
		return
	}

	// Try to serve from embedded FS.
	subFS, err := fs.Sub(embeddedUI, "ui_dist")
	if err != nil {
		setSPACacheHeaders(w, "index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(placeholderHTML))
		return
	}

	// Strip leading slash for fs lookup.
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}

	// Try opening the requested file.
	f, err := subFS.Open(path)
	if err != nil {
		// File not found — serve index.html for SPA routing.
		data, err2 := fs.ReadFile(subFS, "index.html")
		if err2 != nil {
			setSPACacheHeaders(w, "index.html")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(placeholderHTML))
			return
		}
		setSPACacheHeaders(w, "index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
		return
	}
	f.Close()

	// Serve the file.
	setSPACacheHeaders(w, pathpkg.Clean(path))
	http.FileServer(http.FS(subFS)).ServeHTTP(w, r)
}
