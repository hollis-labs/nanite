package api

import (
	"net/http"
	"path"
	"strings"

	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
)

// handlePluginBundle serves browser assets beneath the declared bundle's
// directory. The executable and host configuration are never browser assets.
func (pms *pluginManagerState) handlePluginBundle(w http.ResponseWriter, r *http.Request) {
	directory, err := pms.resolvePluginTarget(r.PathValue("name"))
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	declaration, err := naniteplugin.ParseManifest(directory + "/plugin.yaml")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if declaration.UI.Entry == "" {
		http.NotFound(w, r)
		return
	}
	relative := r.PathValue("file")
	root := path.Dir(declaration.UI.Entry)
	contained := root == "." || strings.HasPrefix(relative, root+"/")
	extension := strings.ToLower(path.Ext(relative))
	asset := false
	switch extension {
	case ".js", ".mjs", ".css", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".ico", ".woff", ".woff2", ".ttf":
		asset = true
	}
	declared := relative == declaration.UI.Entry || relative == declaration.UI.Stylesheet
	if !declared && (!contained || !asset) {
		http.NotFound(w, r)
		return
	}
	target, err := naniteplugin.ResolveBundleFile(directory, relative, false)
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	http.ServeFile(w, r, target)
}

func (pms *pluginManagerState) handlePluginSchema(w http.ResponseWriter, r *http.Request) {
	directory, err := pms.resolvePluginTarget(r.PathValue("name"))
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	declaration, err := naniteplugin.ParseManifest(directory + "/plugin.yaml")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	for _, envelope := range declaration.Registers.Envelopes {
		if envelope.Type != r.PathValue("type") {
			continue
		}
		target, err := naniteplugin.ResolveBundleFile(directory, envelope.Schema, false)
		if err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		http.ServeFile(w, r, target)
		return
	}
	http.NotFound(w, r)
}
