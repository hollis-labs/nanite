package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/store"
)

// catalogState holds dependencies for catalog API handlers.
type catalogState struct {
	store      *store.Store
	fetcher    *naniteplugin.CatalogFetcher
	pluginsDir string
	pluginHost *naniteplugin.Host
}

// RegisterCatalogRoutes adds catalog management endpoints to the mux.
func RegisterCatalogRoutes(mux *http.ServeMux, s *store.Store, pluginsDir string, host *naniteplugin.Host) *naniteplugin.CatalogFetcher {
	cacheDir := filepath.Join(pluginsDir, ".cache")
	fetcher := naniteplugin.NewCatalogFetcher(5*time.Minute, cacheDir)

	cs := &catalogState{
		store:      s,
		fetcher:    fetcher,
		pluginsDir: pluginsDir,
		pluginHost: host,
	}

	// Source management.
	mux.HandleFunc("GET /api/plugins/catalog/sources", cs.handleListSources)
	mux.HandleFunc("POST /api/plugins/catalog/sources", cs.handleAddSource)
	mux.HandleFunc("PUT /api/plugins/catalog/sources/{id}", cs.handleUpdateSource)
	mux.HandleFunc("DELETE /api/plugins/catalog/sources/{id}", cs.handleDeleteSource)
	mux.HandleFunc("PUT /api/plugins/catalog/sources/{id}/key", cs.handleSetSourceKey)

	// Catalog browsing and install.
	mux.HandleFunc("GET /api/plugins/catalog", cs.handleBrowseCatalog)
	mux.HandleFunc("POST /api/plugins/catalog/refresh", cs.handleRefreshCatalog)
	mux.HandleFunc("POST /api/plugins/catalog/install", cs.handleCatalogInstall)

	return fetcher
}

func (cs *catalogState) jsonResp(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (cs *catalogState) errorResp(w http.ResponseWriter, status int, msg string) {
	cs.jsonResp(w, status, map[string]string{"error": msg})
}

// --- Source management ---

func (cs *catalogState) handleListSources(w http.ResponseWriter, r *http.Request) {
	sources, err := cs.store.ListCatalogSources()
	if err != nil {
		cs.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	cs.jsonResp(w, http.StatusOK, sources)
}

func (cs *catalogState) handleAddSource(w http.ResponseWriter, r *http.Request) {
	var req AddSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		cs.errorResp(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.URL == "" {
		cs.errorResp(w, http.StatusBadRequest, "name and url are required")
		return
	}

	src, err := cs.store.CreateCatalogSource(req.Name, req.URL, "custom", req.Priority)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			cs.errorResp(w, http.StatusConflict, "a source with that URL already exists")
			return
		}
		cs.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	cs.fetcher.Invalidate()
	cs.jsonResp(w, http.StatusCreated, src)
}

func (cs *catalogState) handleUpdateSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req UpdateSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		cs.errorResp(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Load current to fill in unchanged fields.
	existing, err := cs.store.GetCatalogSource(id)
	if err != nil {
		cs.errorResp(w, http.StatusNotFound, "source not found")
		return
	}

	name := existing.Name
	if req.Name != "" {
		name = req.Name
	}
	url := existing.URL
	if req.URL != "" {
		url = req.URL
	}
	enabled := existing.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	priority := existing.Priority
	if req.Priority != nil {
		priority = *req.Priority
	}

	if err := cs.store.UpdateCatalogSource(id, name, url, enabled, priority); err != nil {
		cs.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	cs.fetcher.Invalidate()
	cs.jsonResp(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (cs *catalogState) handleDeleteSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := cs.store.DeleteCatalogSource(id); err != nil {
		cs.errorResp(w, http.StatusNotFound, err.Error())
		return
	}
	cs.fetcher.Invalidate()
	cs.jsonResp(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (cs *catalogState) handleSetSourceKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req SetSourceKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		cs.errorResp(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate key format if non-empty (64 hex chars = 32 bytes).
	if req.PublicKey != "" {
		if len(req.PublicKey) != 64 {
			cs.errorResp(w, http.StatusBadRequest, "public_key must be a 64-character hex string (32 bytes Ed25519)")
			return
		}
		if _, err := hex.DecodeString(req.PublicKey); err != nil {
			cs.errorResp(w, http.StatusBadRequest, "public_key must be valid hex encoding")
			return
		}
	}

	if err := cs.store.SetCatalogSourcePublicKey(id, req.PublicKey); err != nil {
		cs.errorResp(w, http.StatusNotFound, err.Error())
		return
	}

	cs.jsonResp(w, http.StatusOK, map[string]string{"status": "key updated"})
}

// --- Catalog browsing ---

// catalogBrowseEntry extends the merged catalog entry with install status.
type catalogBrowseEntry struct {
	naniteplugin.MergedCatalogEntry
	Installed        bool   `json:"installed"`
	InstalledVersion string `json:"installed_version,omitempty"`
	UpdateAvailable  bool   `json:"update_available,omitempty"`
}

func (cs *catalogState) handleBrowseCatalog(w http.ResponseWriter, r *http.Request) {
	sources, err := cs.store.ListCatalogSources()
	if err != nil {
		cs.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Convert store model to fetcher model.
	fetcherSources := make([]naniteplugin.CatalogSource, len(sources))
	for i, s := range sources {
		fetcherSources[i] = naniteplugin.CatalogSource{
			ID: s.ID, Name: s.Name, URL: s.URL, Priority: s.Priority, Enabled: s.Enabled, PublicKey: s.PublicKey,
		}
	}

	entries, err := cs.fetcher.Fetch(r.Context(), fetcherSources)
	if err != nil {
		cs.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Enrich with install status.
	result := make([]catalogBrowseEntry, 0, len(entries))
	for _, entry := range entries {
		be := catalogBrowseEntry{MergedCatalogEntry: entry}

		// Check if installed — filesystem manifest first, then loaded plugin host
		// (builtin plugins may not have a plugins-dir manifest).
		manifestPath := filepath.Join(cs.pluginsDir, entry.Name, "plugin.yaml")
		if fileExists(manifestPath) {
			be.Installed = true
			if m, err := naniteplugin.ParseManifest(manifestPath); err == nil {
				be.InstalledVersion = m.Version
				if m.Version != entry.Version {
					be.UpdateAvailable = true
				}
			}
		} else if p, ok := cs.pluginHost.GetPlugin(entry.Name); ok {
			be.Installed = true
			be.InstalledVersion = p.Version()
			if p.Version() != entry.Version {
				be.UpdateAvailable = true
			}
		}

		result = append(result, be)
	}

	cs.jsonResp(w, http.StatusOK, result)
}

func (cs *catalogState) handleRefreshCatalog(w http.ResponseWriter, r *http.Request) {
	cs.fetcher.Invalidate()
	cs.jsonResp(w, http.StatusOK, map[string]string{"status": "cache invalidated"})
}

// --- Catalog install ---

func (cs *catalogState) handleCatalogInstall(w http.ResponseWriter, r *http.Request) {
	var req CatalogInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		cs.errorResp(w, http.StatusBadRequest, "name is required")
		return
	}

	emitProgress := func(state, message string, progress float64) {
		if cs.pluginHost != nil {
			cs.pluginHost.EmitPluginInstallProgress(req.Name, state, message, progress)
		}
	}
	emitFailure := func(state string, err error) {
		emitProgress(state, fmt.Sprintf("failed in %s", state), 0)
		if cs.pluginHost != nil {
			cs.pluginHost.EmitPluginLoadFailed(req.Name, fmt.Sprintf("%s: %v", state, err))
		}
	}

	// Look up in catalog.
	sources, err := cs.store.ListCatalogSources()
	if err != nil {
		emitFailure("downloading", err)
		cs.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	fetcherSources := make([]naniteplugin.CatalogSource, len(sources))
	for i, s := range sources {
		fetcherSources[i] = naniteplugin.CatalogSource{
			ID: s.ID, Name: s.Name, URL: s.URL, Priority: s.Priority, Enabled: s.Enabled, PublicKey: s.PublicKey,
		}
	}

	entries, err := cs.fetcher.Fetch(r.Context(), fetcherSources)
	if err != nil {
		emitFailure("downloading", err)
		cs.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	var entry *naniteplugin.MergedCatalogEntry
	for _, e := range entries {
		if e.Name == req.Name {
			entry = &e
			break
		}
	}
	if entry == nil {
		cs.errorResp(w, http.StatusNotFound, fmt.Sprintf("plugin %q not found in catalog", req.Name))
		return
	}

	// Check if already installed.
	target := filepath.Join(cs.pluginsDir, entry.Name)
	if fileExists(filepath.Join(target, "plugin.yaml")) {
		cs.errorResp(w, http.StatusConflict, fmt.Sprintf("plugin %q is already installed", entry.Name))
		return
	}

	if entry.ArchiveURL == "" {
		cs.errorResp(w, http.StatusBadRequest, fmt.Sprintf("plugin %q has no archive_url in catalog", entry.Name))
		return
	}

	emitProgress("downloading", "fetching archive", 0)
	// Download the archive.
	tmpFile, err := os.CreateTemp("", "nanite-catalog-*.tar.gz")
	if err != nil {
		cs.errorResp(w, http.StatusInternalServerError, "failed to create temp file")
		return
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	dlReq, err := http.NewRequestWithContext(r.Context(), "GET", entry.ArchiveURL, nil)
	if err != nil {
		tmpFile.Close()
		emitFailure("downloading", err)
		cs.errorResp(w, http.StatusBadRequest, fmt.Sprintf("invalid archive URL: %v", err))
		return
	}

	resp, err := http.DefaultClient.Do(dlReq)
	if err != nil {
		tmpFile.Close()
		emitFailure("downloading", err)
		cs.errorResp(w, http.StatusBadGateway, fmt.Sprintf("download failed: %v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		tmpFile.Close()
		emitFailure("downloading", fmt.Errorf("http %d", resp.StatusCode))
		cs.errorResp(w, http.StatusBadGateway, fmt.Sprintf("download returned %d", resp.StatusCode))
		return
	}

	// Stream to disk with a 100MB limit.
	limited := io.LimitReader(resp.Body, 100<<20)
	if _, err := io.Copy(tmpFile, limited); err != nil {
		tmpFile.Close()
		emitFailure("downloading", err)
		cs.errorResp(w, http.StatusInternalServerError, fmt.Sprintf("download write failed: %v", err))
		return
	}
	tmpFile.Close()

	emitProgress("verifying", "verifying archive", 0)
	// Verify checksum if present.
	if err := naniteplugin.VerifyChecksum(tmpPath, entry.Checksum); err != nil {
		emitFailure("verifying", err)
		cs.errorResp(w, http.StatusBadRequest, fmt.Sprintf("checksum verification failed: %v", err))
		return
	}

	// Verify signature if the source has a trusted public key.
	sourcePublicKey := findSourcePublicKey(sources, entry.SourceID)
	if sourcePublicKey != "" && entry.Signature != "" {
		if err := naniteplugin.VerifySignature(tmpPath, sourcePublicKey, entry.Signature); err != nil {
			emitFailure("verifying", err)
			cs.errorResp(w, http.StatusBadRequest, fmt.Sprintf("signature verification failed: %v", err))
			return
		}
	} else if sourcePublicKey != "" && entry.Signature == "" {
		// Source has a key but plugin is unsigned — warn but allow.
		slog.Warn("catalog: plugin is unsigned (source has a trusted key)", "name", entry.Name, "source", entry.SourceName)
	}

	emitProgress("extracting", "extracting archive", 0)
	// Extract the archive.
	extractDir, err := os.MkdirTemp("", "nanite-catalog-extract-*")
	if err != nil {
		emitFailure("extracting", err)
		cs.errorResp(w, http.StatusInternalServerError, "failed to create extract dir")
		return
	}
	defer os.RemoveAll(extractDir)

	// Detect format by URL or content.
	if strings.HasSuffix(entry.ArchiveURL, ".zip") {
		err = extractZip(tmpPath, extractDir)
	} else {
		err = extractTarGz(tmpPath, extractDir)
	}
	if err != nil {
		emitFailure("extracting", err)
		cs.errorResp(w, http.StatusBadRequest, fmt.Sprintf("extract failed: %v", err))
		return
	}

	// Find plugin root (plugin.yaml at root or single subdir).
	pluginRoot := extractDir
	if !fileExists(filepath.Join(pluginRoot, "plugin.yaml")) {
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

	emitProgress("validating", "checking plugin manifest", 0)
	if !fileExists(filepath.Join(pluginRoot, "plugin.yaml")) {
		emitFailure("validating", fmt.Errorf("plugin.yaml missing"))
		cs.errorResp(w, http.StatusBadRequest, "downloaded archive does not contain plugin.yaml")
		return
	}

	// Save checksum for future verification.
	if entry.Checksum != "" {
		os.WriteFile(filepath.Join(pluginRoot, ".checksum"), []byte(entry.Checksum), 0644)
	}

	os.MkdirAll(cs.pluginsDir, 0755)
	if err := copyDir(pluginRoot, target); err != nil {
		os.RemoveAll(target)
		emitFailure("validating", err)
		cs.errorResp(w, http.StatusInternalServerError, fmt.Sprintf("install failed: %v", err))
		return
	}

	emitProgress("loading", "loading plugin into host", 0)
	// Hot-load into running host (reuses the existing helper from plugins.go).
	pms := &pluginManagerState{
		pluginsDir: cs.pluginsDir,
		pluginHost: cs.pluginHost,
		store:      cs.store,
	}
	pms.runPluginLoadIntoHost(filepath.Join(target, "plugin.yaml"), target)

	emitProgress("ready", "install complete", 1)

	cs.jsonResp(w, http.StatusOK, map[string]string{
		"status":  "installed",
		"plugin":  entry.Name,
		"version": entry.Version,
		"source":  entry.SourceName,
		"message": fmt.Sprintf("Plugin %q v%s installed from %s.", entry.Name, entry.Version, entry.SourceName),
	})
}

// findSourcePublicKey looks up the public key for a source by ID.
func findSourcePublicKey(sources []store.CatalogSource, sourceID string) string {
	for _, s := range sources {
		if s.ID == sourceID {
			return s.PublicKey
		}
	}
	return ""
}

// checksumFile computes the sha256 checksum of a file.
func checksumFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil)), nil
}
