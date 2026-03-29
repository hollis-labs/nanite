package api

import (
	"net/http"
)

// handleListKeybindings returns all plugin-registered keybindings.
// The frontend merges these with its core bindings.
func (a *API) handleListKeybindings(w http.ResponseWriter, r *http.Request) {
	if a.PluginHost == nil {
		a.jsonResp(w, http.StatusOK, map[string]any{
			"keybindings": []any{},
			"count":       0,
		})
		return
	}

	keybindings := a.PluginHost.GetKeybindings()
	a.jsonResp(w, http.StatusOK, map[string]any{
		"keybindings": keybindings,
		"count":       len(keybindings),
	})
}
