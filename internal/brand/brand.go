// Package brand is the single source of truth for application identity.
// Future rebrands should only need to change these constants (plus the Go
// module path in go.mod and the cmd/ directory name, which are structural).
package brand

import (
	"fmt"
	"os"
	"path/filepath"
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
func UserHomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("brand: resolve user home: %w", err)
	}
	return filepath.Join(home, "."+ID), nil
}

// PluginDataDir returns the absolute, persistent per-plugin data root
// under the user's brand directory (e.g. ~/.nanite/plugin-data/<id>).
// The directory is not created by this call; subprocess init code
// os.MkdirAll's it before sending the path to a plugin.
func PluginDataDir(pluginID string) (string, error) {
	base, err := UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "plugin-data", pluginID), nil
}

// PluginCacheDir returns the absolute, ephemeral per-plugin cache root
// under the user's brand directory (e.g. ~/.nanite/plugin-cache/<id>).
// The directory is not created by this call; subprocess init code
// os.MkdirAll's it before sending the path to a plugin.
func PluginCacheDir(pluginID string) (string, error) {
	base, err := UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "plugin-cache", pluginID), nil
}
