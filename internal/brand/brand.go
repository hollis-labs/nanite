// Package brand is the single source of truth for application identity.
// Future rebrands should only need to change these constants (plus the Go
// module path in go.mod and the cmd/ directory name, which are structural).
package brand

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hollis-labs/go-apppaths/paths"
)

const (
	// Name is the human-readable display name used in UI, logs, and docs.
	Name = "Nanite"

	// ID is the lowercase machine identifier used for config directories,
	// keyring service names, event sources, and internal identifiers.
	ID = "nanite"

	// BinaryName is the CLI binary name.
	BinaryName = "nanite"

	// EnvPrefix is prepended to all environment variable names.
	// Usage: brand.EnvPrefix + "AUTH_USER"
	EnvPrefix = "NANITE_"

	// ServiceName is the deployment service name (e.g., in Cerberus).
	ServiceName = "nanite-api"

	// OTelService is the OpenTelemetry service identifier.
	OTelService = "nanite"

	// UserAgent is the HTTP User-Agent header value.
	UserAgent = "Nanite/1.0"

	// ConfigFileName is the base name of the app config file (without extension).
	ConfigFileName = "nanite"

	// DefaultDBName is the default database filename.
	DefaultDBName = "nanite.db"
)

// Env returns a full environment variable name for the given suffix.
// Example: brand.Env("AUTH_USER") → "NANITE_AUTH_USER"
func Env(suffix string) string {
	return EnvPrefix + suffix
}

// UserHomeDir returns the canonical per-user brand directory (e.g.
// ~/.nanite). The directory is not created — callers that need the path
// to exist should os.MkdirAll it with the permissions appropriate to
// their use case.
//
// SCOPE NOTE (CW-20260517-0061): UserHomeDir still points at the legacy
// ~/.nanite dotdir. The go-apppaths migration deliberately scoped to the DB
// and plugin-data/plugin-cache dirs only; the wholesale ~/.nanite evacuation
// (skills, roles, sandboxes, workspaces, registry/, ...) has ~19 callers and
// is a much larger blast radius — tracked as a follow-up under CW-20260517-0058.
func UserHomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("brand: resolve user home: %w", err)
	}
	return filepath.Join(home, "."+ID), nil
}

// pluginLayout resolves the go-apppaths XDG layout for the plugin-dir
// helpers. ID is the go-apppaths appName — the single source of truth (the
// internal/config.ResolveLayout resolver wraps the same call; brand cannot
// import internal/config without an import cycle, so it resolves directly).
// WithoutMaterialize keeps these pure path computations: the subprocess init
// code os.MkdirAll's the per-plugin dir before handing it to a plugin.
func pluginLayout() (paths.Layout, error) {
	return paths.Resolve(ID, paths.WithoutMaterialize())
}

// PluginDataDir returns the absolute, persistent per-plugin data root under
// the go-apppaths XDG data root (e.g. ~/.local/share/nanite/plugin-data/<id>).
// The directory is not created by this call; subprocess init code
// os.MkdirAll's it before sending the path to a plugin.
//
// CW-20260517-0061: repointed off the legacy ~/.nanite dotdir onto the
// go-apppaths DataDir.
func PluginDataDir(pluginID string) (string, error) {
	layout, err := pluginLayout()
	if err != nil {
		return "", fmt.Errorf("brand: resolve plugin-data layout: %w", err)
	}
	return filepath.Join(layout.DataDir(), "plugin-data", pluginID), nil
}

// PluginCacheDir returns the absolute, ephemeral per-plugin cache root under
// the go-apppaths XDG cache root (e.g. ~/.cache/nanite/plugin-cache/<id>).
// The directory is not created by this call; subprocess init code
// os.MkdirAll's it before sending the path to a plugin.
//
// CW-20260517-0061: repointed off the legacy ~/.nanite dotdir onto the
// go-apppaths CacheDir.
func PluginCacheDir(pluginID string) (string, error) {
	layout, err := pluginLayout()
	if err != nil {
		return "", fmt.Errorf("brand: resolve plugin-cache layout: %w", err)
	}
	return filepath.Join(layout.CacheDir(), "plugin-cache", pluginID), nil
}
