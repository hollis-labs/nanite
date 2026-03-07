package server

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:ui_dist
var embeddedUI embed.FS

const placeholderHTML = `<!DOCTYPE html>
<html><head><title>Mentat Chat</title></head>
<body><div id="app-root">Loading...</div></body></html>`

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	// In dev mode, always return placeholder.
	if s.dev {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(placeholderHTML))
		return
	}

	// Try to serve from embedded FS.
	subFS, err := fs.Sub(embeddedUI, "ui_dist")
	if err != nil {
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
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(placeholderHTML))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
		return
	}
	f.Close()

	// Serve the file.
	http.FileServer(http.FS(subFS)).ServeHTTP(w, r)
}
