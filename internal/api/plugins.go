package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	conduitplugin "github.com/hollis-labs/conduit/internal/plugin"
	"github.com/hollis-labs/conduit/internal/store"
	fplugin "github.com/hollis-labs/fragments-engine/plugin"
)

// PluginInfo is the JSON representation of a plugin in the management API.
type PluginInfo struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	ShortDesc   string `json:"short_desc,omitempty"`
	Author      string `json:"author,omitempty"`
	URL         string `json:"url,omitempty"`
	Status      string `json:"status"`
	Type        string `json:"type"`
	Installed   bool   `json:"installed"`
}

// pluginManagerState holds the state needed by plugin management handlers.
type pluginManagerState struct {
	pluginsDir string
	reposPath  string
	store      *store.Store
	pluginHost *conduitplugin.Host
}

// RegisterPluginManagementRoutes adds plugin management endpoints to the mux.
func RegisterPluginManagementRoutes(mux *http.ServeMux, pluginsDir string, s *store.Store, host *conduitplugin.Host) {
	pms := &pluginManagerState{
		pluginsDir: pluginsDir,
		reposPath:  filepath.Join(pluginsDir, "repos.yaml"),
		store:      s,
		pluginHost: host,
	}

	mux.HandleFunc("GET /api/plugins/managed", pms.handleListManaged)
	mux.HandleFunc("POST /api/plugins/install", pms.handleInstall)
	mux.HandleFunc("POST /api/plugins/uninstall", pms.handleUninstall)
	mux.HandleFunc("POST /api/plugins/disable", pms.handleDisable)
	mux.HandleFunc("POST /api/plugins/enable", pms.handleEnable)
}

func (pms *pluginManagerState) jsonResp(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
	// Flush to ensure the client receives the response before any restart.
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func (pms *pluginManagerState) errorResp(w http.ResponseWriter, status int, msg string) {
	pms.jsonResp(w, status, map[string]string{"error": msg})
}

// handleListManaged returns all plugins: installed (active/disabled/no-binary) plus
// available from repos.yaml that are not yet installed.
func (pms *pluginManagerState) handleListManaged(w http.ResponseWriter, r *http.Request) {
	result := []PluginInfo{}
	seen := map[string]bool{}

	// 1. Scan installed plugins (including disabled ones).
	entries, _ := os.ReadDir(pms.pluginsDir)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		status := conduitplugin.PluginStatus(pms.pluginsDir, name)
		if status == "not-installed" {
			continue
		}

		// Parse manifest (active or disabled).
		manifestPath := filepath.Join(pms.pluginsDir, name, "plugin.yaml")
		if status == "disabled" {
			manifestPath = filepath.Join(pms.pluginsDir, name, "plugin.yaml.disabled")
		}
		manifest, err := conduitplugin.ParseManifest(manifestPath)
		if err != nil {
			continue
		}

		info := PluginInfo{
			Name:        name,
			Version:     manifest.Version,
			Description: manifest.Description,
			ShortDesc:   manifest.ShortDesc,
			Author:      manifest.Author,
			URL:         manifest.URL,
			Status:      status,
			Type:        "user", // default; overridden below if in repos
			Installed:   true,
		}
		result = append(result, info)
		seen[name] = true
	}

	// 2. Load repos.yaml to get type info and find uninstalled plugins.
	repos, err := conduitplugin.LoadRepos(pms.reposPath)
	if err == nil {
		// Update type for installed plugins that are in repos.
		for i, info := range result {
			for _, repo := range repos {
				if repo.Name == info.Name {
					result[i].Type = repo.Type
					// Backfill description from repo if manifest is sparse.
					if result[i].Description == "" {
						result[i].Description = repo.Description
					}
					break
				}
			}
		}

		// Add uninstalled plugins from repos.
		for _, repo := range repos {
			if seen[repo.Name] {
				continue
			}
			result = append(result, PluginInfo{
				Name:        repo.Name,
				Description: repo.Description,
				Status:      "available",
				Type:        repo.Type,
				Installed:   false,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

type pluginActionReq struct {
	Name string `json:"name"`
}

func (pms *pluginManagerState) handleInstall(w http.ResponseWriter, r *http.Request) {
	var req pluginActionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		pms.errorResp(w, http.StatusBadRequest, "name is required")
		return
	}

	target := filepath.Join(pms.pluginsDir, req.Name)

	// Check if already installed.
	if _, err := os.Stat(filepath.Join(target, "plugin.yaml")); err == nil {
		pms.errorResp(w, http.StatusConflict, fmt.Sprintf("plugin %q is already installed", req.Name))
		return
	}

	os.MkdirAll(pms.pluginsDir, 0755)

	// Determine repo URL — check repos.yaml first, fall back to default org.
	repoURL := fmt.Sprintf("git@github.com:hollis-labs/%s.git", req.Name)
	if repos, err := conduitplugin.LoadRepos(pms.reposPath); err == nil {
		for _, repo := range repos {
			if repo.Name == req.Name {
				repoURL = fmt.Sprintf("git@github.com:%s.git", repo.Repo)
				break
			}
		}
	}

	cmd := exec.Command("git", "clone", "--depth", "1", repoURL, target)
	output, err := cmd.CombinedOutput()
	if err != nil {
		pms.errorResp(w, http.StatusInternalServerError,
			fmt.Sprintf("clone failed: %v — %s", err, string(output)))
		return
	}

	// Verify plugin.yaml exists in the cloned repo.
	if _, err := os.Stat(filepath.Join(target, "plugin.yaml")); err != nil {
		os.RemoveAll(target)
		pms.errorResp(w, http.StatusBadRequest, "cloned repo does not contain plugin.yaml")
		return
	}

	// Hot-load the plugin into the running host so agent profile appears immediately.
	pms.runPluginLoadIntoHost(filepath.Join(target, "plugin.yaml"), target)

	pms.jsonResp(w, http.StatusOK, map[string]string{
		"status":  "installed",
		"plugin":  req.Name,
		"message": fmt.Sprintf("Plugin %q installed and activated.", req.Name),
	})
}

func (pms *pluginManagerState) handleUninstall(w http.ResponseWriter, r *http.Request) {
	var req pluginActionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		pms.errorResp(w, http.StatusBadRequest, "name is required")
		return
	}

	// Check repo type — core plugins cannot be uninstalled via API.
	if repos, err := conduitplugin.LoadRepos(pms.reposPath); err == nil {
		for _, repo := range repos {
			if repo.Name == req.Name && repo.Type == "core" {
				pms.errorResp(w, http.StatusForbidden,
					fmt.Sprintf("plugin %q is a core plugin and cannot be uninstalled", req.Name))
				return
			}
		}
	}

	target := filepath.Join(pms.pluginsDir, req.Name)
	// Check if installed (active or disabled).
	activeManifest := filepath.Join(target, "plugin.yaml")
	disabledManifest := filepath.Join(target, "plugin.yaml.disabled")
	hasActive := fileExists(activeManifest)
	hasDisabled := fileExists(disabledManifest)
	if !hasActive && !hasDisabled {
		pms.errorResp(w, http.StatusNotFound, fmt.Sprintf("plugin %q is not installed", req.Name))
		return
	}

	// Clean up runtime artifacts (agent profiles, etc.) and unload from host.
	if hasActive {
		pms.runPluginUninstallCleanup(activeManifest)
		pms.unloadPluginFromHost(activeManifest)
	} else {
		pms.runPluginUninstallCleanup(disabledManifest)
	}

	if err := os.RemoveAll(target); err != nil {
		pms.errorResp(w, http.StatusInternalServerError,
			fmt.Sprintf("failed to remove plugin: %v", err))
		return
	}

	pms.jsonResp(w, http.StatusOK, map[string]string{
		"status":  "uninstalled",
		"plugin":  req.Name,
		"message": fmt.Sprintf("Plugin %q uninstalled. Restart Conduit to apply.", req.Name),
	})

	// No auto-restart — server can't restart itself safely.
}

func (pms *pluginManagerState) handleDisable(w http.ResponseWriter, r *http.Request) {
	var req pluginActionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		pms.errorResp(w, http.StatusBadRequest, "name is required")
		return
	}

	// Clean up agent profile and unload from running host so changes are immediate.
	manifestPath := filepath.Join(pms.pluginsDir, req.Name, "plugin.yaml")
	pms.runPluginUninstallCleanup(manifestPath)
	pms.unloadPluginFromHost(manifestPath)

	if err := conduitplugin.DisablePlugin(pms.pluginsDir, req.Name); err != nil {
		pms.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}

	pms.jsonResp(w, http.StatusOK, map[string]string{
		"status":  "disabled",
		"plugin":  req.Name,
		"message": fmt.Sprintf("Plugin %q disabled.", req.Name),
	})
}

func (pms *pluginManagerState) handleEnable(w http.ResponseWriter, r *http.Request) {
	var req pluginActionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		pms.errorResp(w, http.StatusBadRequest, "name is required")
		return
	}

	if err := conduitplugin.EnablePlugin(pms.pluginsDir, req.Name); err != nil {
		pms.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}

	// Hot-load the plugin into the running host so agent profile appears immediately.
	target := filepath.Join(pms.pluginsDir, req.Name)
	pms.runPluginLoadIntoHost(filepath.Join(target, "plugin.yaml"), target)

	pms.jsonResp(w, http.StatusOK, map[string]string{
		"status":  "enabled",
		"plugin":  req.Name,
		"message": fmt.Sprintf("Plugin %q enabled and activated.", req.Name),
	})
}


func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// runPluginUninstallCleanup runs the plugin's Uninstall() method to clean up
// DB artifacts (agent profiles, etc.) so changes are visible immediately.
func (pms *pluginManagerState) runPluginUninstallCleanup(manifestPath string) {
	manifest, err := conduitplugin.ParseManifest(manifestPath)
	if err != nil {
		return
	}
	constructor, ok := conduitplugin.LookupConstructor(manifest.Name)
	if !ok {
		return
	}
	p := constructor()
	if u, ok := p.(fplugin.Uninstallable); ok {
		// Use a minimal host backed by the live store
		host := conduitplugin.NewHostWithStore(pms.store)
		if err := u.Uninstall(host); err != nil {
			log.Printf("plugin-api: uninstall cleanup for %s: %v", manifest.Name, err)
		}
	}
}

// unloadPluginFromHost removes a plugin from the running host's registry
// so it can be re-loaded later (e.g. after disable → enable).
func (pms *pluginManagerState) unloadPluginFromHost(manifestPath string) {
	if pms.pluginHost == nil {
		return
	}
	manifest, err := conduitplugin.ParseManifest(manifestPath)
	if err != nil {
		return
	}
	if err := pms.pluginHost.UnloadPlugin(manifest.Name); err != nil {
		log.Printf("plugin-api: unload %s: %v", manifest.Name, err)
	}
}

// runPluginLoadIntoHost loads a plugin into the running host so its
// agent profiles and MCP tools become available immediately.
func (pms *pluginManagerState) runPluginLoadIntoHost(manifestPath, pluginDir string) {
	if pms.pluginHost == nil {
		return
	}
	manifest, err := conduitplugin.ParseManifest(manifestPath)
	if err != nil {
		return
	}
	constructor, ok := conduitplugin.LookupConstructor(manifest.Name)
	if !ok {
		return
	}
	// Build config and load into running host
	cfg, err := conduitplugin.NewPluginConfig(manifest.Name, pluginDir)
	if err != nil {
		log.Printf("plugin-api: config for %s: %v", manifest.Name, err)
		return
	}
	pms.pluginHost.SetPluginConfig(manifest.Name, cfg)
	p := constructor()
	if err := pms.pluginHost.LoadPlugin(p); err != nil {
		log.Printf("plugin-api: hot-load %s: %v", manifest.Name, err)
	} else {
		log.Printf("plugin-api: hot-loaded plugin %s", manifest.Name)
	}
}
