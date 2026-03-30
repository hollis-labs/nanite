package api

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	conduitplugin "github.com/hollis-labs/conduit/internal/plugin"
	"github.com/hollis-labs/conduit/internal/plugin/subprocess"
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
	mux.HandleFunc("POST /api/plugins/install-local", pms.handleInstallLocal)
	mux.HandleFunc("POST /api/plugins/install-archive", pms.handleInstallArchive)
	mux.HandleFunc("POST /api/plugins/uninstall", pms.handleUninstall)
	mux.HandleFunc("POST /api/plugins/disable", pms.handleDisable)

	// Serve plugin UI bundles for dynamic ESM loading.
	// GET /api/plugins/{name}/ui/{file...} → plugins/{name}/ui/{file...}
	mux.HandleFunc("GET /api/plugins/{name}/ui/{file...}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		file := r.PathValue("file")
		// Prevent path traversal.
		if strings.Contains(file, "..") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		target := filepath.Join(pluginsDir, name, "ui", file)
		http.ServeFile(w, r, target)
	})
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

// handleInstallLocal installs a plugin from a local directory path.
// POST /api/plugins/install-local {"path": "/absolute/path/to/plugin"}
// The directory must contain a plugin.yaml. Contents are copied (not symlinked)
// into the plugins directory. No signature verification (local trust model).
func (pms *pluginManagerState) handleInstallLocal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
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

	manifest, err := conduitplugin.ParseManifest(srcManifest)
	if err != nil {
		pms.errorResp(w, http.StatusBadRequest, fmt.Sprintf("invalid plugin.yaml: %v", err))
		return
	}

	target := filepath.Join(pms.pluginsDir, manifest.Name)
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
	tmpFile, err := os.CreateTemp("", "conduit-plugin-*")
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
	extractDir, err := os.MkdirTemp("", "conduit-plugin-extract-*")
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

	manifest, err := conduitplugin.ParseManifest(filepath.Join(pluginRoot, "plugin.yaml"))
	if err != nil {
		pms.errorResp(w, http.StatusBadRequest, fmt.Sprintf("invalid plugin.yaml: %v", err))
		return
	}

	target := filepath.Join(pms.pluginsDir, manifest.Name)
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
// Supports both builtin (compiled-in) and subprocess plugins.
func (pms *pluginManagerState) runPluginLoadIntoHost(manifestPath, pluginDir string) {
	if pms.pluginHost == nil {
		return
	}
	manifest, err := conduitplugin.ParseManifest(manifestPath)
	if err != nil {
		return
	}

	// Build config.
	cfg, err := conduitplugin.NewPluginConfig(manifest.Name, pluginDir)
	if err != nil {
		log.Printf("plugin-api: config for %s: %v", manifest.Name, err)
		return
	}
	pms.pluginHost.SetPluginConfig(manifest.Name, cfg)

	var p fplugin.Plugin

	if manifest.Runtime == "subprocess" {
		// Subprocess plugin: create a SubprocessPlugin bridge.
		if manifest.Entrypoint == "" {
			log.Printf("plugin-api: subprocess plugin %s has no entrypoint", manifest.Name)
			return
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
		constructor, ok := conduitplugin.LookupConstructor(manifest.Name)
		if !ok {
			log.Printf("plugin-api: no constructor for %s (not compiled in)", manifest.Name)
			return
		}
		p = constructor()
	}

	if err := pms.pluginHost.LoadPlugin(p); err != nil {
		log.Printf("plugin-api: hot-load %s: %v", manifest.Name, err)
	} else {
		log.Printf("plugin-api: hot-loaded plugin %s", manifest.Name)
	}
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
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}

		target := filepath.Join(destDir, hdr.Name)

		// Guard against zip-slip (path traversal).
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal path in archive: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
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

	for _, f := range zr.File {
		target := filepath.Join(destDir, f.Name)

		// Guard against zip-slip.
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal path in archive: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, f.Mode()); err != nil {
				return err
			}
			continue
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
		_, err = io.Copy(out, rc)
		out.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
