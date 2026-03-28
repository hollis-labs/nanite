package api

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/hollis-labs/conduit/internal/provider"
	"github.com/hollis-labs/conduit/internal/secrets"
	"github.com/hollis-labs/conduit/internal/store"
)

func (a *API) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var update store.ProviderUpdate
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if err := a.Store.UpdateProvider(id, update); err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to update provider: "+err.Error())
		return
	}

	p, err := a.Store.GetProvider(id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "provider not found")
		return
	}
	a.jsonResp(w, http.StatusOK, p)
}

// handleSetProviderAPIKey accepts {"api_key": "sk-..."} and stores it in the OS keychain.
func (a *API) handleSetProviderAPIKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var body struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	keyName := secrets.ProviderKeyName(id)
	if body.APIKey == "" {
		secrets.Delete(keyName)
	} else {
		if err := secrets.Set(keyName, body.APIKey); err != nil {
			a.errorResp(w, http.StatusInternalServerError, "failed to store API key in keychain: "+err.Error())
			return
		}
	}

	a.jsonResp(w, http.StatusOK, map[string]any{
		"provider_id": id,
		"has_key":     body.APIKey != "",
	})
}

// handleGetProviderStatus returns the provider's config plus runtime status:
// whether it's registered in the provider registry and whether it has an API key.
func (a *API) handleGetProviderStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	p, err := a.Store.GetProvider(id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "provider not found")
		return
	}

	hasKey := secrets.Has(secrets.ProviderKeyName(id))
	registered := a.Engine != nil && a.Engine.Providers.Has(p.ProviderType)

	a.jsonResp(w, http.StatusOK, map[string]any{
		"provider":   p,
		"has_api_key": hasKey,
		"registered": registered,
	})
}

// CLIDetectionResult describes the auto-detection status for one CLI adapter.
type CLIDetectionResult struct {
	Name         string `json:"name"`          // adapter name (claude, codex, gemini, copilot, aider)
	ProviderType string `json:"provider_type"` // pty-claude, pty-codex, etc.
	Detected     bool   `json:"detected"`
	Path         string `json:"path"`          // resolved path if found
	EnvVar       string `json:"env_var"`       // env var for override
}

// handleDetectCLI runs auto-detection for all CLI adapters using the same
// Detect() logic that the runtime uses at startup.
func (a *API) handleDetectCLI(w http.ResponseWriter, r *http.Request) {
	type cliSpec struct {
		adapter provider.CLIAdapter
		provType, provID, envVar string
	}

	specs := []cliSpec{
		{provider.NewClaudeAdapter(), "pty", "pty-001", "CLAUDE_CLI_PATH"},
		{provider.NewCodexAdapter(), "pty-codex", "pty-codex-001", "CODEX_CLI_PATH"},
		{provider.NewGeminiAdapter(), "pty-gemini", "pty-gemini-001", "GEMINI_CLI_PATH"},
		{provider.NewCopilotAdapter(), "pty-copilot", "pty-copilot-001", "GH_COPILOT_PATH"},
		{provider.NewAiderAdapter(), "pty-aider", "pty-aider-001", "AIDER_CLI_PATH"},
	}

	results := make([]CLIDetectionResult, 0, len(specs))
	for _, s := range specs {
		result := CLIDetectionResult{
			Name:         s.adapter.Name(),
			ProviderType: s.provType,
			EnvVar:       s.envVar,
		}

		// Check if a custom path is stored in provider settings.
		if p, err := a.Store.GetProvider(s.provID); err == nil && p.Settings != "" && p.Settings != "{}" {
			var settings map[string]string
			if json.Unmarshal([]byte(p.Settings), &settings) == nil {
				if cp, ok := settings["cli_path"]; ok && cp != "" {
					result.Path = cp
					result.Detected = isExecutable(cp)
					results = append(results, result)
					continue
				}
			}
		}

		// Use the adapter's own Detect() — same logic as runtime registration.
		if path, ok := s.adapter.Detect(); ok {
			result.Path = path
			result.Detected = true
		}
		results = append(results, result)
	}

	a.jsonResp(w, http.StatusOK, results)
}

// handleGetAllProviderStatuses returns all providers with their runtime status.
func (a *API) handleGetAllProviderStatuses(w http.ResponseWriter, r *http.Request) {
	providers, err := a.Store.ListProviders()
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	type providerStatus struct {
		store.ProviderConfig
		HasAPIKey  bool `json:"has_api_key"`
		Registered bool `json:"registered"`
	}

	out := make([]providerStatus, 0, len(providers))
	for _, p := range providers {
		hasKey := secrets.Has(secrets.ProviderKeyName(p.ID))
		registered := a.Engine != nil && a.Engine.Providers.Has(p.ProviderType)

		// For CLI providers, also check pty- and sub- variants.
		if !registered && strings.HasPrefix(p.ProviderType, "pty-") {
			registered = a.Engine != nil && (a.Engine.Providers.Has(p.ProviderType) || a.Engine.Providers.Has("sub-"+strings.TrimPrefix(p.ProviderType, "pty-")))
		}

		out = append(out, providerStatus{
			ProviderConfig: p,
			HasAPIKey:      hasKey,
			Registered:     registered,
		})
	}

	a.jsonResp(w, http.StatusOK, out)
}

// isExecutable checks if a file exists and is executable.
// Works for both absolute paths and PATH-relative binaries.
func isExecutable(path string) bool {
	// Absolute or relative path — check the file directly.
	if strings.Contains(path, "/") || strings.Contains(path, "\\") {
		info, err := os.Stat(path)
		if err != nil {
			return false
		}
		return !info.IsDir() && info.Mode()&0111 != 0
	}
	// Bare binary name — search PATH.
	_, err := exec.LookPath(path)
	return err == nil
}
