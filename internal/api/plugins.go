package api

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/pathsafe"
	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	"github.com/hollis-labs/nanite/internal/store"
	fplugin "github.com/hollis-labs/go-plugin"
)

// Archive extraction caps — defense against zip-bomb / tar-bomb plugin
// archives uploaded via handleInstallArchive. Breaches return a
// *archiveLimitError wrapping fmt.Errorf with exact bytes/counts so
// operators can raise caps deliberately if a legitimate plugin trips them.
const (
	// maxArchiveFileSize bounds a single decompressed file (100 MiB). A
	// Nanite plugin shipping a binary over this cap should split or
	// externalize it.
	maxArchiveFileSize int64 = 100 * 1024 * 1024
	// maxArchiveTotalSize bounds cumulative decompressed bytes across all
	// entries (500 MiB). Defends against many-small-files bombs that slip
	// under the per-file cap.
	maxArchiveTotalSize int64 = 500 * 1024 * 1024
	// maxArchiveFileCount bounds entry count (10,000). Defends against
	// inode-exhaustion bombs (millions of 0-byte entries).
	maxArchiveFileCount = 10_000
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
	pluginHost *naniteplugin.Host
}

// RegisterPluginManagementRoutes adds plugin management endpoints to the mux.
func RegisterPluginManagementRoutes(mux *http.ServeMux, pluginsDir string, s *store.Store, host *naniteplugin.Host) {
	pms := &pluginManagerState{
		pluginsDir: pluginsDir,
		reposPath:  filepath.Join(pluginsDir, "repos.yaml"),
		store:      s,
		pluginHost: host,
	}

	mux.HandleFunc("GET /api/plugins/managed", pms.handleListManaged)
	mux.HandleFunc("POST /api/plugins/install", pms.handleInstall)
	mux.HandleFunc("POST /api/plugins/install-local", pms.handleInstallLocal)
	mux.HandleFunc("POST /api/plugins/install-archive", pms.handleInstallArchive)
	mux.HandleFunc("POST /api/plugins/uninstall", pms.handleUninstall)
	mux.HandleFunc("POST /api/plugins/disable", pms.handleDisable)

	// Serve plugin UI bundles for dynamic ESM loading.
	// GET /api/plugins/{name}/ui/{file...} → plugins/{name}/ui/{file...}
	mux.HandleFunc("GET /api/plugins/{name}/ui/{file...}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		file := r.PathValue("file")

		// Resolve the allowed base directory and the requested path,
		// then verify the target stays within the plugin's ui/ directory.
		baseDir := filepath.Join(pluginsDir, name, "ui")
		target := filepath.Clean(filepath.Join(baseDir, file))
		if !strings.HasPrefix(target, filepath.Clean(baseDir)+string(filepath.Separator)) && target != filepath.Clean(baseDir) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		http.ServeFile(w, r, target)
	})
	mux.HandleFunc("POST /api/plugins/enable", pms.handleEnable)

	// B.7 consolidated registry endpoint. Separate file (plugins_registry.go)
	// keeps the envelope/slot/component/widget aggregation logic isolated from
	// the install/uninstall lifecycle handlers above.
	registerPluginsRegistryRoute(mux, host, pluginsDir)

	// B.8 plugin lifecycle SSE stream. Replaces the deleted dead endpoint
	// GET /api/plugins/events/stream. Filters the host event bus down to
	// the six lifecycle event types per plan §B.8.
	registerPluginsEventsRoute(mux, host)
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
		status := naniteplugin.PluginStatus(pms.pluginsDir, name)
		if status == "not-installed" {
			continue
		}

		// Parse manifest (active or disabled).
		manifestPath := filepath.Join(pms.pluginsDir, name, "plugin.yaml")
		if status == "disabled" {
			manifestPath = filepath.Join(pms.pluginsDir, name, "plugin.yaml.disabled")
		}
		manifest, err := naniteplugin.ParseManifest(manifestPath)
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

	// 2. Include compiled-in (builtin) plugins not found in pluginsDir.
	if pms.pluginHost != nil {
		for _, p := range pms.pluginHost.ListPlugins() {
			if seen[p.ID()] {
				continue
			}
			status := p.Status()
			statusStr := "active"
			if !status.Loaded {
				statusStr = "disabled"
			}
			result = append(result, PluginInfo{
				Name:        p.ID(),
				Version:     p.Version(),
				Description: p.Description(),
				Status:      statusStr,
				Type:        "core",
				Installed:   true,
			})
			seen[p.ID()] = true
		}
	}

	// 3. Load repos.yaml to get type info and find uninstalled plugins.
	repos, err := naniteplugin.LoadRepos(pms.reposPath)
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

	// Confine the plugin target path under pluginsDir. A name like
	// "../../etc/passwd" would otherwise place the cloned repo outside the
	// plugins directory (audit finding: Critical — path traversal in install).
	target, err := pathsafe.ResolveUnder(pms.pluginsDir, req.Name)
	if err != nil {
		var escErr *pathsafe.EscapeError
		if errors.As(err, &escErr) {
			pms.errorResp(w, http.StatusBadRequest, fmt.Sprintf("invalid plugin name: %v", escErr))
			return
		}
		pms.errorResp(w, http.StatusBadRequest, fmt.Sprintf("invalid plugin name: %v", err))
		return
	}

	// Check if already installed.
	if _, err := os.Stat(filepath.Join(target, "plugin.yaml")); err == nil {
		pms.errorResp(w, http.StatusConflict, fmt.Sprintf("plugin %q is already installed", req.Name))
		return
	}

	os.MkdirAll(pms.pluginsDir, 0755)

	// Determine repo URL — check repos.yaml first, fall back to default org.
	repoURL := fmt.Sprintf("git@github.com:hollis-labs/%s.git", req.Name)
	if repos, err := naniteplugin.LoadRepos(pms.reposPath); err == nil {
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

// handleInstallLocal installs a plugin from a local directory path.
// POST /api/plugins/install-local {"path": "/absolute/path/to/plugin"}
// The directory must contain a plugin.yaml. Contents are copied (not symlinked)
// into the plugins directory. No signature verification (local trust model).
func (pms *pluginManagerState) handleInstallLocal(w http.ResponseWriter, r *http.Request) {
	var req InstallLocalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		pms.errorResp(w, http.StatusBadRequest, "path is required")
		return
	}

	// Resolve and validate source path.
	srcDir, err := filepath.Abs(req.Path)
	if err != nil {
		pms.errorResp(w, http.StatusBadRequest, fmt.Sprintf("invalid path: %v", err))
		return
	}

	srcManifest := filepath.Join(srcDir, "plugin.yaml")
	if !fileExists(srcManifest) {
		pms.errorResp(w, http.StatusBadRequest, "directory does not contain plugin.yaml")
		return
	}

	manifest, err := naniteplugin.ParseManifest(srcManifest)
	if err != nil {
		pms.errorResp(w, http.StatusBadRequest, fmt.Sprintf("invalid plugin.yaml: %v", err))
		return
	}

	// Confine the plugin target path under pluginsDir. A manifest whose name
	// includes ".." would otherwise copy the plugin contents outside the
	// configured plugins directory (audit finding: Critical — path traversal
	// via local-install manifest name).
	target, err := pathsafe.ResolveUnder(pms.pluginsDir, manifest.Name)
	if err != nil {
		var escErr *pathsafe.EscapeError
		if errors.As(err, &escErr) {
			pms.errorResp(w, http.StatusBadRequest, fmt.Sprintf("invalid plugin name in manifest: %v", escErr))
			return
		}
		pms.errorResp(w, http.StatusBadRequest, fmt.Sprintf("invalid plugin name in manifest: %v", err))
		return
	}
	if fileExists(filepath.Join(target, "plugin.yaml")) {
		pms.errorResp(w, http.StatusConflict, fmt.Sprintf("plugin %q is already installed", manifest.Name))
		return
	}

	os.MkdirAll(pms.pluginsDir, 0755)

	// Copy the directory tree.
	if err := copyDir(srcDir, target); err != nil {
		os.RemoveAll(target)
		pms.errorResp(w, http.StatusInternalServerError, fmt.Sprintf("copy failed: %v", err))
		return
	}

	// Hot-load into running host.
	pms.runPluginLoadIntoHost(filepath.Join(target, "plugin.yaml"), target)

	pms.jsonResp(w, http.StatusOK, map[string]string{
		"status":  "installed",
		"plugin":  manifest.Name,
		"source":  "local",
		"message": fmt.Sprintf("Plugin %q installed from local directory.", manifest.Name),
	})
}

// handleInstallArchive installs a plugin from an uploaded archive.
// POST /api/plugins/install-archive (multipart form: "archive" file field)
// Accepts .tar.gz and .zip archives. The archive must contain a plugin.yaml
// either at the root or inside a single top-level directory.
// No signature verification (local trust model).
func (pms *pluginManagerState) handleInstallArchive(w http.ResponseWriter, r *http.Request) {
	// 32 MB max upload.
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		pms.errorResp(w, http.StatusBadRequest, fmt.Sprintf("parse form: %v", err))
		return
	}

	file, header, err := r.FormFile("archive")
	if err != nil {
		pms.errorResp(w, http.StatusBadRequest, "archive file is required")
		return
	}
	defer file.Close()

	// Write to a temp file so we can seek (needed for zip).
	tmpFile, err := os.CreateTemp("", "nanite-plugin-*")
	if err != nil {
		pms.errorResp(w, http.StatusInternalServerError, "failed to create temp file")
		return
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmpFile, file); err != nil {
		tmpFile.Close()
		pms.errorResp(w, http.StatusInternalServerError, "failed to save upload")
		return
	}
	tmpFile.Close()

	// Extract to a temp directory first, then validate.
	extractDir, err := os.MkdirTemp("", "nanite-plugin-extract-*")
	if err != nil {
		pms.errorResp(w, http.StatusInternalServerError, "failed to create temp dir")
		return
	}
	defer os.RemoveAll(extractDir)

	filename := header.Filename
	switch {
	case strings.HasSuffix(filename, ".tar.gz") || strings.HasSuffix(filename, ".tgz"):
		err = extractTarGz(tmpPath, extractDir)
	case strings.HasSuffix(filename, ".zip"):
		err = extractZip(tmpPath, extractDir)
	default:
		pms.errorResp(w, http.StatusBadRequest, "unsupported archive format (use .tar.gz or .zip)")
		return
	}
	if err != nil {
		pms.errorResp(w, http.StatusBadRequest, fmt.Sprintf("extract failed: %v", err))
		return
	}

	// Find plugin.yaml — either at root of extractDir or inside a single subdir.
	pluginRoot := extractDir
	if !fileExists(filepath.Join(pluginRoot, "plugin.yaml")) {
		// Check for single top-level directory (common in archives).
		entries, _ := os.ReadDir(extractDir)
		dirs := []string{}
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, e.Name())
			}
		}
		if len(dirs) == 1 {
			pluginRoot = filepath.Join(extractDir, dirs[0])
		}
	}

	if !fileExists(filepath.Join(pluginRoot, "plugin.yaml")) {
		pms.errorResp(w, http.StatusBadRequest, "archive does not contain plugin.yaml")
		return
	}

	manifest, err := naniteplugin.ParseManifest(filepath.Join(pluginRoot, "plugin.yaml"))
	if err != nil {
		pms.errorResp(w, http.StatusBadRequest, fmt.Sprintf("invalid plugin.yaml: %v", err))
		return
	}

	// Confine archive-derived plugin target under pluginsDir.
	target, err := pathsafe.ResolveUnder(pms.pluginsDir, manifest.Name)
	if err != nil {
		var escErr *pathsafe.EscapeError
		if errors.As(err, &escErr) {
			pms.errorResp(w, http.StatusBadRequest, fmt.Sprintf("invalid plugin name in manifest: %v", escErr))
			return
		}
		pms.errorResp(w, http.StatusBadRequest, fmt.Sprintf("invalid plugin name in manifest: %v", err))
		return
	}
	if fileExists(filepath.Join(target, "plugin.yaml")) {
		pms.errorResp(w, http.StatusConflict, fmt.Sprintf("plugin %q is already installed", manifest.Name))
		return
	}

	os.MkdirAll(pms.pluginsDir, 0755)

	if err := copyDir(pluginRoot, target); err != nil {
		os.RemoveAll(target)
		pms.errorResp(w, http.StatusInternalServerError, fmt.Sprintf("copy failed: %v", err))
		return
	}

	// Hot-load into running host.
	pms.runPluginLoadIntoHost(filepath.Join(target, "plugin.yaml"), target)

	pms.jsonResp(w, http.StatusOK, map[string]string{
		"status":  "installed",
		"plugin":  manifest.Name,
		"source":  "archive",
		"message": fmt.Sprintf("Plugin %q installed from archive.", manifest.Name),
	})
}

func (pms *pluginManagerState) handleUninstall(w http.ResponseWriter, r *http.Request) {
	var req pluginActionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		pms.errorResp(w, http.StatusBadRequest, "name is required")
		return
	}

	// Check repo type — core plugins cannot be uninstalled via API.
	if repos, err := naniteplugin.LoadRepos(pms.reposPath); err == nil {
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
		"message": fmt.Sprintf("Plugin %q uninstalled. Restart %s to apply.", req.Name, brand.Name),
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
	unloaded := pms.unloadPluginFromHost(manifestPath)

	if err := naniteplugin.DisablePlugin(pms.pluginsDir, req.Name); err != nil {
		pms.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}

	// B.8: only emit plugin.disabled when runtime state actually changed. The
	// file rename succeeded (DisablePlugin above), so disk state is "disabled"
	// either way — but if unloadPluginFromHost was a no-op or failed, the
	// runtime wasn't cleaned and subscribers would receive a misleading event.
	// UnloadPlugin already bumps the registry version on success, so no second
	// bump is needed here.
	if unloaded && pms.pluginHost != nil {
		pms.pluginHost.EmitPluginDisabled(req.Name)
	} else if !unloaded {
		slog.Warn("plugin-api: disable moved disk state but runtime unload did not occur",
			"name", req.Name)
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

	if err := naniteplugin.EnablePlugin(pms.pluginsDir, req.Name); err != nil {
		pms.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}

	// Hot-load the plugin into the running host so agent profile appears immediately.
	target := filepath.Join(pms.pluginsDir, req.Name)
	loaded := pms.runPluginLoadIntoHost(filepath.Join(target, "plugin.yaml"), target)

	// B.8: only emit plugin.enabled when the plugin actually loaded into the
	// running host. LoadPlugin already bumped the registry version on success.
	// On failure/no-op, emit plugin.load_failed so subscribers see the
	// disk-state/runtime-state mismatch — the file was renamed to enabled
	// but the runtime didn't come up.
	if pms.pluginHost != nil {
		if loaded {
			pms.pluginHost.EmitPluginEnabled(req.Name)
		} else {
			pms.pluginHost.EmitPluginLoadFailed(req.Name, "hot-load into host failed or was a no-op")
		}
	}

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
	manifest, err := naniteplugin.ParseManifest(manifestPath)
	if err != nil {
		return
	}
	constructor, ok := naniteplugin.LookupConstructor(manifest.Name)
	if !ok {
		return
	}
	p := constructor()
	if u, ok := p.(fplugin.Uninstallable); ok {
		// Use a minimal host backed by the live store
		host := naniteplugin.NewHostWithStore(pms.store)
		if err := u.Uninstall(host); err != nil {
			slog.Warn("plugin-api: uninstall cleanup failed", "name", manifest.Name, "err", err)
		}
	}
}

// unloadPluginFromHost removes a plugin from the running host's registry so
// it can be re-loaded later (e.g. after disable → enable). Returns true only
// when UnloadPlugin actually removed the plugin; returns false on no-op
// (nil host, manifest parse error) or failure (UnloadPlugin returned error).
// Callers use the return value to decide whether lifecycle events and
// registry-version bumps should fire, since those signal runtime state
// changes and must not fire when runtime state didn't actually change.
func (pms *pluginManagerState) unloadPluginFromHost(manifestPath string) bool {
	if pms.pluginHost == nil {
		return false
	}
	manifest, err := naniteplugin.ParseManifest(manifestPath)
	if err != nil {
		return false
	}
	if err := pms.pluginHost.UnloadPlugin(manifest.Name); err != nil {
		slog.Warn("plugin-api: unload failed", "name", manifest.Name, "err", err)
		return false
	}
	return true
}

// runPluginLoadIntoHost loads a plugin into the running host so its
// agent profiles and MCP tools become available immediately. Supports both
// builtin (compiled-in) and subprocess plugins. Returns true only when the
// plugin was actually loaded into the host; returns false on no-op (nil
// host, parse/config error, missing entrypoint, no constructor) or failure
// (LoadPlugin returned error). Callers use this to decide whether to emit
// plugin.enabled / bump registry (true) or plugin.load_failed (false).
func (pms *pluginManagerState) runPluginLoadIntoHost(manifestPath, pluginDir string) bool {
	if pms.pluginHost == nil {
		return false
	}
	manifest, err := naniteplugin.ParseManifest(manifestPath)
	if err != nil {
		return false
	}

	// Build config.
	cfg, err := naniteplugin.NewPluginConfig(manifest.Name, pluginDir)
	if err != nil {
		slog.Warn("plugin-api: config failed", "name", manifest.Name, "err", err)
		return false
	}
	pms.pluginHost.SetPluginConfig(manifest.Name, cfg)

	var p fplugin.Plugin

	if manifest.Runtime == "subprocess" {
		// Subprocess plugin: create a SubprocessPlugin bridge.
		if manifest.Entrypoint == "" {
			slog.Warn("plugin-api: subprocess plugin has no entrypoint", "name", manifest.Name)
			return false
		}
		command := manifest.Entrypoint
		parts := strings.Fields(command)
		cmd, args := parts[0], parts[1:]

		// Resolve relative entrypoint from plugin dir.
		if !filepath.IsAbs(cmd) {
			abs := filepath.Join(pluginDir, cmd)
			if fileExists(abs) {
				cmd = abs
			}
		}

		resolvedConfig := make(map[string]string)
		for key, entry := range manifest.Config {
			if entry.EnvVar != "" {
				if v := os.Getenv(entry.EnvVar); v != "" {
					resolvedConfig[key] = v
					continue
				}
			}
			if entry.Default != "" {
				resolvedConfig[key] = entry.Default
			}
		}

		mgrCfg := subprocess.DefaultManagerConfig(cmd, pluginDir)
		mgrCfg.Args = args
		p = subprocess.NewSubprocessPlugin(pluginDir, resolvedConfig, mgrCfg)
	} else {
		// Builtin plugin: use compiled-in constructor.
		constructor, ok := naniteplugin.LookupConstructor(manifest.Name)
		if !ok {
			slog.Warn("plugin-api: no constructor (not compiled in)", "name", manifest.Name)
			return false
		}
		p = constructor()
	}

	if err := pms.pluginHost.LoadPlugin(p); err != nil {
		slog.Warn("plugin-api: hot-load failed", "name", manifest.Name, "err", err)
		return false
	}
	slog.Info("plugin-api: hot-loaded plugin", "name", manifest.Name)
	return true
}

// --- File and archive helpers ---

// copyDir recursively copies src to dst.
func copyDir(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// extractedFileClose is the hook used by extractTarGz / extractZip to close
// the per-entry destination file. Tests override this to simulate a close
// failure (e.g. a buffered-fs flush error) without needing to mock the
// kernel. Production path just calls the file's Close method.
var extractedFileClose = func(f *os.File) error { return f.Close() }

// extractTarGz extracts a .tar.gz archive to the given directory.
func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var (
		totalBytes int64
		entryCount int
	)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}

		entryCount++
		if entryCount > maxArchiveFileCount {
			return fmt.Errorf("archive too many entries: %d > %d", entryCount, maxArchiveFileCount)
		}

		// Path confinement via pathsafe.ResolveUnder. Defends against
		// absolute paths, `..` segments, and symlink-pointed entries.
		target, err := pathsafe.ResolveUnder(destDir, hdr.Name)
		if err != nil {
			return fmt.Errorf("illegal path in archive: %s: %w", hdr.Name, err)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			// Per-file size cap using the declared header size. tar
			// headers carry Size; reject obvious bombs before opening
			// the destination file.
			if hdr.Size > maxArchiveFileSize {
				return fmt.Errorf("archive file too large: %s declared %d > %d", hdr.Name, hdr.Size, maxArchiveFileSize)
			}
			if totalBytes+hdr.Size > maxArchiveTotalSize {
				return fmt.Errorf("archive total size too large: %d + %d > %d", totalBytes, hdr.Size, maxArchiveTotalSize)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			// Defense-in-depth against mismatched-header bombs (Size in
			// header underreports actual stream length): cap the copy at
			// maxArchiveFileSize + 1 so a liar trips the per-file cap.
			//nolint:gosec // G110: copy is explicitly bounded by io.LimitReader below; bomb is rejected before it can exhaust disk/memory.
			written, err := io.Copy(out, io.LimitReader(tr, maxArchiveFileSize+1))
			closeErr := extractedFileClose(out)
			if err != nil {
				return err
			}
			if closeErr != nil {
				return fmt.Errorf("close extracted file: %w", closeErr)
			}
			if written > maxArchiveFileSize {
				return fmt.Errorf("archive file too large: %s exceeded %d during decompress", hdr.Name, maxArchiveFileSize)
			}
			totalBytes += written
			if totalBytes > maxArchiveTotalSize {
				return fmt.Errorf("archive total size too large: %d > %d", totalBytes, maxArchiveTotalSize)
			}
		}
	}
	return nil
}

// extractZip extracts a .zip archive to the given directory.
func extractZip(archivePath, destDir string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("zip: %w", err)
	}
	defer zr.Close()

	if len(zr.File) > maxArchiveFileCount {
		return fmt.Errorf("archive too many entries: %d > %d", len(zr.File), maxArchiveFileCount)
	}

	var totalBytes int64
	for _, f := range zr.File {
		// Path confinement via pathsafe.ResolveUnder.
		target, err := pathsafe.ResolveUnder(destDir, f.Name)
		if err != nil {
			return fmt.Errorf("illegal path in archive: %s: %w", f.Name, err)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, f.Mode()); err != nil {
				return err
			}
			continue
		}

		// Per-file cap on declared uncompressed size. zip header
		// UncompressedSize64 is authoritative for zip; an archive with a
		// mismatched header is rejected by the io.LimitReader sentinel
		// below.
		//nolint:gosec // G115: UncompressedSize64 is uint64; cap is 100 MiB so any value above maxArchiveFileSize trips the >= check before any int64 conversion risk.
		declared := f.UncompressedSize64
		if declared > uint64(maxArchiveFileSize) {
			return fmt.Errorf("archive file too large: %s declared %d > %d", f.Name, declared, maxArchiveFileSize)
		}
		if totalBytes+int64(declared) > maxArchiveTotalSize {
			return fmt.Errorf("archive total size too large: %d + %d > %d", totalBytes, declared, maxArchiveTotalSize)
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		// Bounded copy: reject at maxArchiveFileSize+1 bytes. Defends
		// against a compressed entry whose actual expansion exceeds the
		// declared UncompressedSize64 (header mismatch bombs).
		//nolint:gosec // G110: copy is explicitly bounded by io.LimitReader; bomb is rejected before disk/memory exhaustion.
		written, err := io.Copy(out, io.LimitReader(rc, maxArchiveFileSize+1))
		closeErr := extractedFileClose(out)
		rc.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return fmt.Errorf("close extracted file: %w", closeErr)
		}
		if written > maxArchiveFileSize {
			return fmt.Errorf("archive file too large: %s exceeded %d during decompress", f.Name, maxArchiveFileSize)
		}
		totalBytes += written
		if totalBytes > maxArchiveTotalSize {
			return fmt.Errorf("archive total size too large: %d > %d", totalBytes, maxArchiveTotalSize)
		}
	}
	return nil
}
