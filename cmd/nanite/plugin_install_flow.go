package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/plugin/catalog"
	"github.com/hollis-labs/nanite/internal/plugin/install"
)

// defaultCatalogURL is the primary signed catalog for nanite plugins.
// Overridden by NANITE_CATALOG_URL.
const defaultCatalogURL = "https://plugins.nanite.hollislabs.dev/catalog.yaml"

func resolveCatalogURL() string {
	if u := os.Getenv(brand.Env("CATALOG_URL")); u != "" {
		return u
	}
	return defaultCatalogURL
}

func resolveStagingRoot() string {
	if d := os.Getenv(brand.Env("PLUGIN_STAGING_DIR")); d != "" {
		return d
	}
	home, err := brand.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), brand.BinaryName+"-plugin-staging")
	}
	return filepath.Join(home, "plugin-staging")
}

func resolveCatalogCacheDir() string {
	if d := os.Getenv(brand.Env("PLUGIN_CATALOG_CACHE")); d != "" {
		return d
	}
	home, err := brand.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), brand.BinaryName+"-plugin-catalog")
	}
	return filepath.Join(home, "plugin-catalog")
}

// localDirSource implements install.Source by wrapping an already-on-disk
// plugin directory — the `nanite plugin install ./path` flow.
type localDirSource struct {
	id      string
	absPath string
}

func (s *localDirSource) PluginID() string { return s.id }
func (s *localDirSource) Download(ctx context.Context, stagingDir string, emit install.EventFunc) (install.Handle, error) {
	return install.Handle{Kind: "directory", Path: s.absPath}, nil
}

// catalogArchiveSource implements install.Source by downloading a signed
// archive from a catalog entry.
type catalogArchiveSource struct {
	id         string
	archiveURL string
	sha256     string
	signature  []byte
	signerKey  string
	downloader *install.HTTPDownloader
}

func (s *catalogArchiveSource) PluginID() string { return s.id }
func (s *catalogArchiveSource) Download(ctx context.Context, stagingDir string, emit install.EventFunc) (install.Handle, error) {
	path, err := s.downloader.Download(ctx, s.archiveURL, stagingDir, s.id, emit)
	if err != nil {
		return install.Handle{}, err
	}
	return install.Handle{
		Kind:           "archive",
		Path:           path,
		ExpectedSHA256: s.sha256,
		Signature:      s.signature,
		SignerKeyID:    s.signerKey,
	}, nil
}

// noopLoader satisfies install.Loader for the CLI path — the host process
// (nanite) rediscovers the plugin on restart, so the CLI installer does
// not need to hand anything to an in-process host.
type noopLoader struct{}

func (noopLoader) Load(ctx context.Context, pluginID, pluginDir string) error { return nil }

// buildInstaller wires the state machine for CLI use. The loader is a
// no-op; triggerActivation() handles the running-service refresh (hot-reload
// for a subprocess plugin, triggerRestart() for a builtin) after Run returns
// successfully.
func buildInstaller(emit install.EventFunc) (*install.Installer, *catalog.KeyRing, *install.DirStaging) {
	ring := catalog.NewKeyRing()
	staging := &install.DirStaging{
		StagingRoot: resolveStagingRoot(),
		PluginsRoot: resolvePluginsDir(),
	}
	return &install.Installer{
		Verifier:  &install.SignatureVerifier{KeyLookup: ring.LookupFunc()},
		Extractor: &install.TarGzExtractor{},
		Validator: &cliValidator{},
		Loader:    noopLoader{},
		Staging:   staging,
		Emit:      emit,
	}, ring, staging
}

// cliValidator adapts ValidateManifest into the install.Validator interface.
type cliValidator struct{}

func (cliValidator) Validate(ctx context.Context, pluginDir string) error {
	manifestPath := filepath.Join(pluginDir, "plugin.yaml")
	if err := pluginYAMLPresent(manifestPath); err != nil {
		return err
	}
	if verr := install.ValidateManifest(manifestPath, pluginDir, install.ValidationOptions{}); verr != nil {
		if verr.HasRefusals() {
			return fmt.Errorf("manifest validation failed: %s", verr.Error())
		}
	}
	return nil
}

func pluginYAMLPresent(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("plugin.yaml not found at %s", path)
	}
	return nil
}

// installLocalFromStateMachine runs the state machine for a local directory
// source. Returns the final install dir on success.
func installLocalFromStateMachine(ctx context.Context, absSrc, pluginID string) (string, error) {
	emit := printEvents()
	inst, _, _ := buildInstaller(emit)
	return inst.Install(ctx, &localDirSource{id: pluginID, absPath: absSrc})
}

// installFromCatalog fetches the catalog, finds the entry for pluginID,
// and runs the state machine. Returns (finalDir, true, nil) on success,
// ("", false, nil) if the plugin is not in the catalog (caller may fall
// back), and ("", false, err) on hard failures.
func installFromCatalog(ctx context.Context, pluginID string) (string, bool, error) {
	emit := printEvents()
	inst, ring, _ := buildInstaller(emit)

	catURL := resolveCatalogURL()
	signed, err := fetchCatalog(ctx, ring, catURL)
	if err != nil {
		return "", false, fmt.Errorf("catalog: %w", err)
	}

	entry, ok := findCatalogEntry(signed.YAML, pluginID)
	if !ok {
		return "", false, nil
	}

	sig, err := decodeHexSig(entry.Signature)
	if err != nil {
		return "", false, fmt.Errorf("catalog entry %q: %w", pluginID, err)
	}
	sha := stripSha256Prefix(entry.Checksum)
	if sha == "" {
		return "", false, fmt.Errorf("catalog entry %q has no sha256 checksum", pluginID)
	}
	keyID := entry.SignerKeyID
	if keyID == "" {
		keyID = "catalog-root"
	}

	src := &catalogArchiveSource{
		id:         pluginID,
		archiveURL: entry.ArchiveURL,
		sha256:     sha,
		signature:  sig,
		signerKey:  keyID,
		downloader: &install.HTTPDownloader{},
	}
	final, err := inst.Install(ctx, src)
	if err != nil {
		return "", false, err
	}
	return final, true, nil
}

func fetchCatalog(ctx context.Context, ring *catalog.KeyRing, catalogURL string) (*catalog.SignedCatalog, error) {
	f := &catalog.SignedFetcher{
		Ring:     ring,
		CacheDir: resolveCatalogCacheDir(),
	}
	return f.Fetch(ctx, catalogURL)
}

// catalogEntryLite is a parser-friendly subset of the catalog entry fields
// this CLI consumes. Declared locally so catalog-file format changes are
// centralized in one place.
type catalogEntryLite struct {
	Name         string `yaml:"name"`
	ArchiveURL   string `yaml:"archive_url"`
	Checksum     string `yaml:"checksum"`
	Signature    string `yaml:"signature"`
	SignerKeyID  string `yaml:"signer_key_id"`
}

type catalogFileLite struct {
	Plugins []catalogEntryLite `yaml:"plugins"`
}

func findCatalogEntry(yamlBytes []byte, pluginID string) (catalogEntryLite, bool) {
	var cf catalogFileLite
	if err := yaml.Unmarshal(yamlBytes, &cf); err != nil {
		return catalogEntryLite{}, false
	}
	for _, e := range cf.Plugins {
		if e.Name == pluginID {
			return e, true
		}
	}
	return catalogEntryLite{}, false
}

func stripSha256Prefix(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "sha256:")
	return s
}

func decodeHexSig(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("missing signature")
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}
	return b, nil
}

// printEvents returns an EventFunc that writes human-readable progress to
// stdout. Throttled by the progressWriter in download.go.
func printEvents() install.EventFunc {
	last := install.State("")
	return func(e install.Event) {
		if e.State != last {
			fmt.Printf("  [%s] %s\n", e.State, e.Message)
			last = e.State
		}
		if e.Err != nil {
			fmt.Printf("  failure: %v\n", e.Err)
		}
	}
}

// readInstalledManifest parses plugin.yaml at the installed-plugin dir.
// Returns nil with an error if the dir doesn't exist or lacks a manifest.
func readInstalledManifest(pluginsDir, id string) (*plugin.PluginManifest, error) {
	path := filepath.Join(pluginsDir, id, "plugin.yaml")
	return plugin.ParseManifest(path)
}

// pluginUpdate reinstalls pluginID from the catalog. On failure, the
// existing install is preserved (DirStaging.Commit keeps the old dir
// intact unless the new extract+validate+rename succeeds end-to-end).
func pluginUpdate(id string) {
	pluginsDir := resolvePluginsDir()
	oldManifest, err := readInstalledManifest(pluginsDir, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Plugin %q is not installed — run `%s plugin install %s` first\n",
			id, brand.BinaryName, id)
		os.Exit(1)
	}
	fmt.Printf("Updating %s (current %s) from catalog...\n", id, oldManifest.Version)

	final, found, err := installFromCatalog(context.Background(), id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Update failed: %v\n", err)
		os.Exit(1)
	}
	if !found {
		fmt.Fprintf(os.Stderr, "Plugin %q not in catalog (%s) — no update available\n", id, resolveCatalogURL())
		os.Exit(1)
	}

	newManifest, merr := plugin.ParseManifest(filepath.Join(final, "plugin.yaml"))
	if merr == nil {
		fmt.Printf("\nUpdated %s: %s → %s\n", id, oldManifest.Version, newManifest.Version)
	} else {
		fmt.Printf("\nUpdated %s\n", id)
		newManifest = nil
	}
	triggerActivation(id, newManifest)
}
