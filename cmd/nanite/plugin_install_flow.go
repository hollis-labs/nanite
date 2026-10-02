package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/plugin/catalog"
	"github.com/hollis-labs/nanite/internal/plugin/install"
)

// defaultCatalogURL is the primary catalog for nanite plugins.
// Overridden by NANITE_CATALOG_URL.
const defaultCatalogURL = "https://github.com/hollis-labs/plugins-catalog/releases/latest/download/catalog.yaml"

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

// noopLoader satisfies install.Loader for the CLI path — the host process
// (nanite) rediscovers the plugin on restart, so the CLI installer does
// not need to hand anything to an in-process host.
type noopLoader struct{}

func (noopLoader) Load(ctx context.Context, pluginID, pluginDir string) error { return nil }

// buildInstaller wires the state machine for CLI use via the shared
// install.NewInstaller constructor (AD-04 item 6 — cmd/nanite and
// internal/api both call this instead of hand-rolling their own Installer,
// which is exactly how the API's catalog-install path diverged from this
// one originally). The loader is a no-op; triggerActivation() handles the
// running-service refresh (hot-reload for a subprocess plugin,
// triggerRestart() for a builtin) after Run returns successfully.
func buildInstaller(emit install.EventFunc) (*install.Installer, *install.DirStaging) {
	inst, staging := install.NewInstaller(install.BuildOptions{
		Extractor:   &install.TarGzExtractor{SkipDevelopmentFiles: true},
		Loader:      noopLoader{},
		StagingRoot: resolveStagingRoot(),
		PluginsRoot: resolvePluginsDir(),
		Emit:        emit,
		Review:      reviewPluginInstall,
	})
	return inst, staging
}

// installLocalFromStateMachine runs the state machine for a local directory
// source. Returns the final install dir on success.
func installLocalFromStateMachine(ctx context.Context, absSrc, pluginID string) (string, error) {
	emit := printEvents()
	inst, _ := buildInstaller(emit)
	return inst.Install(ctx, &localDirSource{id: pluginID, absPath: absSrc})
}

// installFromCatalog fetches the catalog, finds the entry for pluginID,
// and runs the state machine. Returns (finalDir, true, nil) on success,
// ("", false, nil) if the plugin is not in the catalog (caller may fall
// back), and ("", false, err) on hard failures.
func installFromCatalog(ctx context.Context, pluginID string) (string, bool, error) {
	emit := printEvents()
	inst, _ := buildInstaller(emit)

	catURL := resolveCatalogURL()
	fetched, err := fetchCatalog(ctx, catURL)
	if err != nil {
		return "", false, fmt.Errorf("catalog: %w", err)
	}

	decoded, err := plugin.DecodeCatalog(fetched.YAML)
	if err != nil {
		return "", false, fmt.Errorf("catalog: %w", err)
	}
	var entry plugin.CatalogEntry
	ok := false
	for _, candidate := range decoded.Plugins {
		if candidate.ID == pluginID {
			entry, ok = candidate, true
			break
		}
	}
	if !ok {
		return "", false, nil
	}

	if !entry.Available {
		return "", false, fmt.Errorf("plugin %q has no archive for this platform", pluginID)
	}
	sha := stripSha256Prefix(entry.Checksum)
	if sha == "" {
		return "", false, fmt.Errorf("catalog entry %q has no sha256 checksum", pluginID)
	}

	src := &install.CatalogArchiveSource{
		ID:             pluginID,
		ArchiveURL:     entry.ArchiveURL,
		ManifestSHA256: entry.ManifestSHA256,
		Size:           entry.ArchiveSize,
		SHA256:         sha,
		Downloader:     &install.HTTPDownloader{},
	}
	final, err := inst.Install(ctx, src)
	if err != nil {
		return "", false, err
	}
	return final, true, nil
}

func fetchCatalog(ctx context.Context, catalogURL string) (*catalog.Catalog, error) {
	f := &catalog.Fetcher{
		CacheDir: resolveCatalogCacheDir(),
	}
	return f.Fetch(ctx, catalogURL)
}

// findCatalogEntry uses the same strict released contract as the host.
func findCatalogEntry(raw []byte, pluginID string) (plugin.CatalogEntry, bool) {
	cf, err := plugin.DecodeCatalog(raw)
	if err != nil {
		return plugin.CatalogEntry{}, false
	}
	for _, entry := range cf.Plugins {
		if entry.ID == pluginID {
			return entry, true
		}
	}
	return plugin.CatalogEntry{}, false
}

func stripSha256Prefix(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "sha256:")
	return s
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

func reviewPluginInstall(ctx context.Context, review plugin.InstallReview, previous *plugin.InstallApproval) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	fmt.Printf("\nReview %s (%s) v%s\nBundle: %s\n", review.Name, review.ID, review.Version, review.BundleDigest)
	if previous != nil {
		fmt.Printf("Previously accepted: v%s, bundle %s\n", previous.Review.Version, previous.Review.BundleDigest)
		for _, change := range installReviewChanges(previous.Review, review) {
			fmt.Println(change)
		}
	}
	fmt.Printf("Executable: %s %q\n", review.Entrypoint, review.Arguments)
	for _, capability := range review.Capabilities {
		fmt.Printf("Capability: %s — %s (optional: %t)\n", capability.Name, capability.Reason, capability.Optional)
		if len(capability.Metadata) != 0 {
			fmt.Printf("  Requested access: %s\n", compactCapabilityMetadata(capability.Metadata))
		}
	}
	for _, secret := range review.Secrets {
		fmt.Printf("Secret: %s (environment: %s, required: %t)\n", secret.Name, secret.Environment, secret.Required)
	}
	for _, environment := range review.Environment {
		fmt.Printf("Config environment: %s\n", environment)
	}
	for _, tool := range review.Tools {
		fmt.Printf("Tool: %s (effect: %s)\n", tool.Name, tool.Effect)
	}
	fmt.Printf("Type %s to approve this bundle: ", review.ID)
	entered, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("install review input: %w", err)
	}
	if strings.TrimSpace(entered) != review.ID {
		return "", fmt.Errorf("installation was not approved")
	}
	return review.Digest(), nil
}

func installReviewDeclarations(review plugin.InstallReview) map[string]string {
	declarations := map[string]string{"Executable": fmt.Sprintf("%s %q", review.Entrypoint, review.Arguments)}
	for _, capability := range review.Capabilities {
		declarations["Capability "+capability.Name] = fmt.Sprintf("%s (optional: %t); requested access: %s", capability.Reason, capability.Optional, compactCapabilityMetadata(capability.Metadata))
	}
	for _, secret := range review.Secrets {
		declarations["Secret "+secret.Name] = fmt.Sprintf("environment: %s, required: %t", secret.Environment, secret.Required)
	}
	for _, environment := range review.Environment {
		declarations["Config environment "+environment] = environment
	}
	for _, tool := range review.Tools {
		declarations["Tool "+tool.Name] = tool.Effect
	}
	return declarations
}

func installReviewChanges(previous, current plugin.InstallReview) []string {
	before, after := installReviewDeclarations(previous), installReviewDeclarations(current)
	keys := make([]string, 0, len(before)+len(after))
	for key := range before {
		keys = append(keys, key)
	}
	for key := range after {
		if _, exists := before[key]; !exists {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	var changes []string
	for _, key := range keys {
		old, had := before[key]
		next, has := after[key]
		switch {
		case !had:
			changes = append(changes, fmt.Sprintf("Added %s: %s", key, next))
		case !has:
			changes = append(changes, fmt.Sprintf("Removed %s: %s", key, old))
		case old != next:
			changes = append(changes, fmt.Sprintf("Changed %s: %s → %s", key, old, next))
		}
	}
	return changes
}

func compactCapabilityMetadata(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "none"
	}
	var output bytes.Buffer
	if err := json.Compact(&output, raw); err != nil {
		return "invalid declaration"
	}
	return output.String()
}
