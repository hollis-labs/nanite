package api

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/plugin/install"
	"github.com/hollis-labs/nanite/internal/service"
)

// catalogState holds dependencies for catalog API handlers.
type catalogState struct {
	sources           *service.CatalogSourceService
	cleanup           *service.PluginCleanupService
	fetcher           *naniteplugin.CatalogFetcher
	pluginsDir        string
	pluginHost        *naniteplugin.Host
	archiveDownloader *install.HTTPDownloader
}

// RegisterCatalogRoutes adds catalog management endpoints to the mux.
func RegisterCatalogRoutes(mux *http.ServeMux, sources *service.CatalogSourceService, cleanup *service.PluginCleanupService, pluginsDir string, host *naniteplugin.Host) *naniteplugin.CatalogFetcher {
	cacheDir := filepath.Join(pluginsDir, ".cache")
	fetcher := naniteplugin.NewCatalogFetcher(5*time.Minute, cacheDir)

	cs := &catalogState{
		sources: sources, cleanup: cleanup,
		fetcher:    fetcher,
		pluginsDir: pluginsDir,
		pluginHost: host,
	}

	// Source management.
	mux.HandleFunc("GET /api/plugins/catalog/sources", cs.handleListSources)
	mux.HandleFunc("POST /api/plugins/catalog/sources", cs.handleAddSource)
	mux.HandleFunc("PUT /api/plugins/catalog/sources/{id}", cs.handleUpdateSource)
	mux.HandleFunc("DELETE /api/plugins/catalog/sources/{id}", cs.handleDeleteSource)

	// Catalog browsing and install.
	mux.HandleFunc("GET /api/plugins/catalog", cs.handleBrowseCatalog)
	mux.HandleFunc("POST /api/plugins/catalog/refresh", cs.handleRefreshCatalog)
	mux.HandleFunc("POST /api/plugins/catalog/install", cs.handleCatalogInstall)

	return fetcher
}

func (cs *catalogState) jsonResp(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Debug("api: write catalog JSON response failed", "err", err)
	}
}

func (cs *catalogState) errorResp(w http.ResponseWriter, status int, msg string) {
	cs.jsonResp(w, status, map[string]string{"error": msg})
}

// --- Source management ---

func (cs *catalogState) handleListSources(w http.ResponseWriter, r *http.Request) {
	sources, err := cs.sources.List(r.Context())
	if err != nil {
		cs.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	cs.jsonResp(w, http.StatusOK, catalogSourceViews(sources))
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

	src, err := cs.sources.Create(r.Context(), req.Name, req.URL, req.Priority)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			cs.errorResp(w, http.StatusConflict, "a source with that URL already exists")
			return
		}
		cs.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	cs.fetcher.Invalidate()
	cs.jsonResp(w, http.StatusCreated, catalogSourceView(src))
}

func (cs *catalogState) handleUpdateSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req UpdateSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		cs.errorResp(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := cs.sources.Patch(r.Context(), id, service.CatalogSourcePatch{Name: req.Name, URL: req.URL, Enabled: req.Enabled, Priority: req.Priority}); err != nil {
		var missing *service.CatalogSourceMissingError
		if errors.As(err, &missing) {
			cs.errorResp(w, http.StatusNotFound, "source not found")
		} else {
			cs.errorResp(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	cs.fetcher.Invalidate()
	cs.jsonResp(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (cs *catalogState) handleDeleteSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := cs.sources.Delete(r.Context(), id); err != nil {
		cs.errorResp(w, http.StatusNotFound, err.Error())
		return
	}
	cs.fetcher.Invalidate()
	cs.jsonResp(w, http.StatusOK, map[string]string{"status": "deleted"})
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
	fetcherSources, err := cs.sources.FetchInputs(r.Context())
	if err != nil {
		cs.errorResp(w, http.StatusInternalServerError, err.Error())
		return
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
		manifestPath := filepath.Join(cs.pluginsDir, entry.ID, "plugin.yaml")
		if fileExists(manifestPath) {
			be.Installed = true
			if m, err := naniteplugin.ParseManifest(manifestPath); err == nil {
				be.InstalledVersion = m.Version
				if m.Version != entry.Version {
					be.UpdateAvailable = true
				}
			}
		} else if p, ok := cs.pluginHost.GetPlugin(entry.ID); ok {
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
// handleCatalogInstall installs a catalog archive through checksum verification,
// bounded extraction, manifest validation and staged placement.
func (cs *catalogState) handleCatalogInstall(w http.ResponseWriter, r *http.Request) {
	var req CatalogInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		cs.errorResp(w, http.StatusBadRequest, "name is required")
		return
	}

	if err := install.ValidatePluginID(req.Name); err != nil {
		cs.errorResp(w, http.StatusBadRequest, "invalid plugin name: "+err.Error())
		return
	}

	// Look up in catalog.
	fetcherSources, err := cs.sources.FetchInputs(r.Context())
	if err != nil {
		cs.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	entries, err := cs.fetcher.Fetch(r.Context(), fetcherSources)
	if err != nil {
		cs.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	var entry *naniteplugin.MergedCatalogEntry
	for _, e := range entries {
		if e.ID == req.Name {
			entry = &e
			break
		}
	}
	if entry == nil {
		cs.errorResp(w, http.StatusNotFound, fmt.Sprintf("plugin %q not found in catalog", req.Name))
		return
	}

	// Confine the install target. entry.ID is untrusted — it comes
	// straight from a catalog.yaml fetched over HTTP from a configured
	// (and possibly attacker-influenced) source URL. Validate it against
	// the SAME allowlist install.DirStaging.Commit enforces internally
	// (^[a-z][a-z0-9-]{1,62}$) before doing anything else with it. This is
	// not a second, different confinement mechanism (AD-04 item 1
	// explicitly says not to add a pathsafe.ResolveUnder call here) — it's
	// an early exit using the pipeline's own validator, so a bad name is
	// rejected before any network I/O rather than only late inside Commit
	// (audit finding GO-PLUGIN-002).
	if err := install.ValidatePluginID(entry.ID); err != nil {
		cs.errorResp(w, http.StatusBadRequest, fmt.Sprintf("invalid plugin name %q: %v", entry.ID, err))
		return
	}

	// Check if already installed. Safe to filepath.Join now that entry.ID
	// has passed ValidatePluginID above (no "..", no separators, no
	// absolute-path prefix are possible in a validated plugin id).
	target := filepath.Join(cs.pluginsDir, entry.ID)
	if fileExists(filepath.Join(target, "plugin.yaml")) {
		cs.errorResp(w, http.StatusConflict, fmt.Sprintf("plugin %q is already installed", entry.ID))
		return
	}

	if !entry.Available || entry.ArchiveURL == "" {
		cs.errorResp(w, http.StatusBadRequest, fmt.Sprintf("plugin %q has no archive_url in catalog", entry.ID))
		return
	}

	src := &install.CatalogArchiveSource{
		ID:             entry.ID,
		ArchiveURL:     entry.ArchiveURL,
		ManifestSHA256: entry.ManifestSHA256,
		Size:           entry.ArchiveSize,
		SHA256:         stripChecksumPrefix(entry.Checksum),
		Downloader:     cs.archiveDownloader,
	}

	inst, _ := install.NewInstaller(install.BuildOptions{
		Extractor: &catalogExtractor{archiveURL: entry.ArchiveURL},
		Loader: hostLoader{pms: &pluginManagerState{
			pluginsDir: cs.pluginsDir,
			pluginHost: cs.pluginHost,
			cleanup:    cs.cleanup,
		}},
		StagingRoot: filepath.Join(cs.pluginsDir, ".staging"),
		PluginsRoot: cs.pluginsDir,
		Emit:        cs.catalogInstallEmit(entry.ID),
	})

	if _, err := inst.Install(r.Context(), src); err != nil {
		cs.errorResp(w, catalogInstallErrorStatus(err), fmt.Sprintf("install failed: %v", err))
		return
	}

	cs.jsonResp(w, http.StatusOK, map[string]string{
		"status":  "installed",
		"plugin":  entry.ID,
		"version": entry.Version,
		"source":  entry.SourceName,
		"message": fmt.Sprintf("Plugin %q v%s installed from %s.", entry.ID, entry.Version, entry.SourceName),
	})
}

// checksumFile computes the sha256 checksum of a file.
func checksumFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = f.Close() // Read-only file close is best-effort cleanup; read errors are handled separately.
	}()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil)), nil
}
