package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/plugin/install"
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
	sources, err := cs.store.ListCatalogSources(r.Context())
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

	src, err := cs.store.CreateCatalogSource(r.Context(), req.Name, req.URL, "custom", req.Priority)
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
	existing, err := cs.store.GetCatalogSource(r.Context(), id)
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

	if err := cs.store.UpdateCatalogSource(r.Context(), id, name, url, enabled, priority); err != nil {
		cs.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	cs.fetcher.Invalidate()
	cs.jsonResp(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (cs *catalogState) handleDeleteSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := cs.store.DeleteCatalogSource(r.Context(), id); err != nil {
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

	if err := cs.store.SetCatalogSourcePublicKey(r.Context(), id, req.PublicKey); err != nil {
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
	sources, err := cs.store.ListCatalogSources(r.Context())
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
//
// handleCatalogInstall converges onto the CLI's install.Installer pipeline
// (AD-04, TASKS/audit-remediation/01-plugin-install-convergence/01-unify-
// plugin-catalog-install-pipeline.md) instead of the older, weaker
// download/verify/extract implementation that used to live here directly
// (internal/plugin.VerifyChecksum/VerifySignature — both now fully
// retired). The supporting install.Extractor/install.Loader adapters and
// the KeyLookup/checksum/signature-decoding helpers live in
// catalog_install.go.
func (cs *catalogState) handleCatalogInstall(w http.ResponseWriter, r *http.Request) {
	var req CatalogInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		cs.errorResp(w, http.StatusBadRequest, "name is required")
		return
	}

	// Look up in catalog.
	sources, err := cs.store.ListCatalogSources(r.Context())
	if err != nil {
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

	// Confine the install target. entry.Name is untrusted — it comes
	// straight from a catalog.yaml fetched over HTTP from a configured
	// (and possibly attacker-influenced) source URL. Validate it against
	// the SAME allowlist install.DirStaging.Commit enforces internally
	// (^[a-z][a-z0-9-]{1,62}$) before doing anything else with it. This is
	// not a second, different confinement mechanism (AD-04 item 1
	// explicitly says not to add a pathsafe.ResolveUnder call here) — it's
	// an early exit using the pipeline's own validator, so a bad name is
	// rejected before any network I/O rather than only late inside Commit
	// (audit finding GO-PLUGIN-002).
	if err := install.ValidatePluginID(entry.Name); err != nil {
		cs.errorResp(w, http.StatusBadRequest, fmt.Sprintf("invalid plugin name %q: %v", entry.Name, err))
		return
	}

	// Check if already installed. Safe to filepath.Join now that entry.Name
	// has passed ValidatePluginID above (no "..", no separators, no
	// absolute-path prefix are possible in a validated plugin id).
	target := filepath.Join(cs.pluginsDir, entry.Name)
	if fileExists(filepath.Join(target, "plugin.yaml")) {
		cs.errorResp(w, http.StatusConflict, fmt.Sprintf("plugin %q is already installed", entry.Name))
		return
	}

	if entry.ArchiveURL == "" {
		cs.errorResp(w, http.StatusBadRequest, fmt.Sprintf("plugin %q has no archive_url in catalog", entry.Name))
		return
	}

	sig, err := decodeCatalogSignature(entry.Signature)
	if err != nil {
		cs.errorResp(w, http.StatusBadRequest, fmt.Sprintf("plugin %q has an invalid signature encoding: %v", entry.Name, err))
		return
	}

	// entry.SourceID doubles as the install.Handle.SignerKeyID here: the
	// API's per-entry catalog model has no separate signer-key-id field the
	// way the CLI's single hardcoded signed catalog does, so the source
	// that carried the entry IS its signer identity. See catalogKeyLookup.
	src := &install.CatalogArchiveSource{
		ID:          entry.Name,
		ArchiveURL:  entry.ArchiveURL,
		SHA256:      stripChecksumPrefix(entry.Checksum),
		Signature:   sig,
		SignerKeyID: entry.SourceID,
		Downloader:  &install.HTTPDownloader{},
	}

	// AllowUnsigned is threaded from user_settings.allow_unsigned_plugins
	// (AD-25, TASKS/audit-remediation/01-plugin-install-convergence/02-wire-
	// allow-unsigned-plugins-setting.md). Only has an observable effect in a
	// devmode build — see SignatureVerifier.AllowUnsigned and
	// devmode.HostDevSigningBypass's own doc comments. A settings-read
	// failure fails safe to false (unsigned installs stay rejected), same as
	// the CLI's resolveAllowUnsignedPlugins.
	allowUnsigned := false
	if us, err := cs.store.GetUserSettings(r.Context()); err == nil {
		allowUnsigned = us.AllowUnsignedPlugins
	}

	inst, _ := install.NewInstaller(install.BuildOptions{
		KeyLookup:     catalogKeyLookup(sources),
		AllowUnsigned: allowUnsigned,
		Extractor:     &catalogExtractor{archiveURL: entry.ArchiveURL},
		Loader: hostLoader{pms: &pluginManagerState{
			pluginsDir: cs.pluginsDir,
			pluginHost: cs.pluginHost,
			store:      cs.store,
		}},
		StagingRoot: filepath.Join(cs.pluginsDir, ".staging"),
		PluginsRoot: cs.pluginsDir,
		Emit:        cs.catalogInstallEmit(entry.Name),
	})

	if _, err := inst.Install(r.Context(), src); err != nil {
		cs.errorResp(w, catalogInstallErrorStatus(err), fmt.Sprintf("install failed: %v", err))
		return
	}

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
