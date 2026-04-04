// Package brand is the single source of truth for application identity.
// Future rebrands should only need to change these constants (plus the Go
// module path in go.mod and the cmd/ directory name, which are structural).
package brand

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
