package config

import (
	"fmt"
	"net"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// AppConfig holds application-level tunables loaded from config/nanite.yaml.
type AppConfig struct {
	Presence  PresenceConfig  `yaml:"presence"`
	Artifacts ArtifactsConfig `yaml:"artifacts"`
	Skills    SkillsConfig    `yaml:"skills"`
	HTTP      HTTPConfig      `yaml:"http"`
	OTel      OTelConfig      `yaml:"otel"`
	Logging   LoggingConfig   `yaml:"logging"`
	Recipes   RecipesConfig   `yaml:"recipes"`
}

// RecipesConfig controls durable-agent recipe catalog loading.
type RecipesConfig struct {
	// CatalogPaths is an ordered list of local recipe catalog files or
	// directories. Configured recipes override built-ins by ID; duplicate
	// IDs across configured catalogs are rejected at startup.
	CatalogPaths []string `yaml:"catalog_paths"`
}

// LoggingConfig controls the structured logging handler installed by
// internal/slogx at startup.
//
// Defaults (see DefaultAppConfig): JSON format, INFO level, PII
// redaction enabled. Unknown format/level values fall back to these
// defaults rather than erroring out so a typo never takes the process
// down.
type LoggingConfig struct {
	// Format is "json" or "text". Unknown values fall back to "json".
	Format string `yaml:"format"`
	// Level is "debug", "info", "warn", or "error". Case-insensitive.
	// Unknown values fall back to "info".
	Level string `yaml:"level"`
	// RedactPII, when true, installs the slogx.PIIRedactor handler
	// wrapper. Default true (set via DefaultAppConfig). Explicitly set
	// false only in tightly-controlled debug environments.
	RedactPII bool `yaml:"redact_pii"`
	// AddSource, when true, annotates records with file:line.
	AddSource bool `yaml:"add_source"`
}

// OTelConfig controls OpenTelemetry initialisation.
//
// The NANITE_OTEL_DISABLED=1 environment variable takes precedence over
// OTelConfig.Disabled — either set installs the no-op tracer provider
// via internal/otel.Init and skips exporter initialisation.
type OTelConfig struct {
	// Disabled, when true, installs a no-op tracer provider instead of
	// delegating to the external feotel exporter. Env var
	// NANITE_OTEL_DISABLED=1 wins over this field.
	Disabled bool `yaml:"disabled"`
	// Future: Endpoint, SamplingRate, etc.
}

// HTTPConfig controls HTTP server timeouts and body-size limits. All values
// are optional; zero or unset values fall back to conservative defaults at
// Server construction time. See server.New for the fallback policy.
type HTTPConfig struct {
	// BindAddress is the host or IP address used by the production HTTP
	// listener. Empty values resolve to 127.0.0.1 so an unconfigured server is
	// reachable only from the local machine. Values are host-only: ASCII DNS
	// hostnames and raw IPv4/raw unbracketed IPv6 addresses are accepted; ports,
	// brackets, and surrounding whitespace are rejected. Set explicitly to
	// 0.0.0.0 (or ::) only when remote access is intended and protected by the
	// deployment.
	BindAddress              string `yaml:"bind_address"`
	ReadTimeoutSeconds       int    `yaml:"read_timeout_seconds"`
	ReadHeaderTimeoutSeconds int    `yaml:"read_header_timeout_seconds"`
	WriteTimeoutSeconds      int    `yaml:"write_timeout_seconds"`
	IdleTimeoutSeconds       int    `yaml:"idle_timeout_seconds"`
	// MaxRequestBodyBytes caps the body of any mutating (POST/PUT/PATCH/DELETE)
	// request that is not explicitly whitelisted for a larger cap (e.g.
	// multipart artifact upload). A value <=0 falls back to the default.
	MaxRequestBodyBytes int64 `yaml:"max_request_body_bytes"`
	// MaxUploadBodyBytes caps the body of multipart artifact/plugin uploads.
	// Override separately from MaxRequestBodyBytes so routine JSON endpoints
	// stay tight while file-upload endpoints get the headroom they need.
	MaxUploadBodyBytes int64 `yaml:"max_upload_body_bytes"`
	// CORSAllowedOrigins is the exact-match allowlist consulted by the server's
	// CORS middleware when deciding whether to reflect the Origin header.
	//
	// Semantics:
	//   - Empty / unset: the server falls back to a development-oriented default
	//     of ["http://localhost:5173", "http://127.0.0.1:5173"]. This replaces
	//     the prior reflect-any behaviour (audit finding: Critical) so an
	//     unconfigured deployment is no longer open to arbitrary web origins.
	//   - Comparison is an exact string match against the Origin header. No
	//     substring, suffix, or regex matching is performed.
	//   - The special value "*" reflects any origin but, per the CORS spec,
	//     disables Access-Control-Allow-Credentials. Opt in explicitly by
	//     listing "*" as a sole entry when credentials are not required.
	CORSAllowedOrigins []string `yaml:"cors_allowed_origins"`
}

// DefaultHTTPBindAddress keeps an unconfigured server local to the host.
const DefaultHTTPBindAddress = "127.0.0.1"

// ResolveHTTPBindAddress validates and resolves HTTPConfig.BindAddress.
// The option is deliberately host-only because the listen port has its own
// typed setting. Accepted values are ASCII DNS-style hostnames, raw IPv4, and
// raw unbracketed IPv6. Dotted numeric values are interpreted as IPv4 and must
// parse as such. An empty value selects DefaultHTTPBindAddress.
func ResolveHTTPBindAddress(value string) (string, error) {
	if value == "" {
		return DefaultHTTPBindAddress, nil
	}
	if strings.TrimSpace(value) != value {
		return "", fmt.Errorf("http.bind_address %q must not contain surrounding whitespace", value)
	}
	if strings.ContainsAny(value, "[]") {
		return "", fmt.Errorf("http.bind_address %q must be an unbracketed host or raw IP address without a port", value)
	}
	if net.ParseIP(value) != nil {
		return value, nil
	}
	if strings.Contains(value, ":") {
		if _, _, err := net.SplitHostPort(value); err == nil {
			return "", fmt.Errorf("http.bind_address %q must be a host only without a port", value)
		}
		return "", fmt.Errorf("http.bind_address %q must be a valid raw unbracketed IPv6 address or hostname without a port", value)
	}
	if !validASCIIHostname(value) {
		return "", fmt.Errorf("http.bind_address %q must be a valid ASCII hostname or raw IPv4/IPv6 address", value)
	}
	return value, nil
}

func validASCIIHostname(value string) bool {
	if len(value) > 253 || numericDottedValue(value) {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || !asciiLetterOrDigit(label[0]) || !asciiLetterOrDigit(label[len(label)-1]) {
			return false
		}
		for i := 1; i < len(label)-1; i++ {
			if !asciiLetterOrDigit(label[i]) && label[i] != '-' {
				return false
			}
		}
	}
	return true
}

func numericDottedValue(value string) bool {
	if !strings.Contains(value, ".") {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] != '.' && (value[i] < '0' || value[i] > '9') {
			return false
		}
	}
	return true
}

func asciiLetterOrDigit(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// PresenceConfig controls presence broadcast behavior.
type PresenceConfig struct {
	CLIActiveThrottleSeconds int `yaml:"cli_active_throttle_seconds"`
}

// ArtifactsConfig controls automatic artifact detection.
type ArtifactsConfig struct {
	AutoDetectTools []string `yaml:"auto_detect_tools"`
	PathKeys        []string `yaml:"path_keys"`
	StorageDir      string   `yaml:"storage_dir"`
}

// SkillsConfig controls the skill system's on-disk storage.
type SkillsConfig struct {
	// VendorStorageDir is the filesystem root for the content-addressed
	// vendored skill store (internal/skillvendor.Store) — installed
	// SKILL.md packages (body + scripts/references/assets), keyed by
	// content address, immutable once written. Mirrors
	// ArtifactsConfig.StorageDir's own load/default/override pattern; see
	// docs/engineering/architecture/20-skills.md's "The model" section.
	VendorStorageDir string `yaml:"vendor_storage_dir"`
}

// DefaultAppConfig returns sensible defaults when no config file exists.
func DefaultAppConfig() *AppConfig {
	return &AppConfig{
		Presence: PresenceConfig{
			CLIActiveThrottleSeconds: 5,
		},
		Artifacts: ArtifactsConfig{
			AutoDetectTools: []string{"Write", "write", "write_file", "create_file", "Edit", "edit"},
			PathKeys:        []string{"file_path", "path", "filename"},
			StorageDir:      "data/artifacts",
		},
		Skills: SkillsConfig{
			VendorStorageDir: "data/skills/vendor",
		},
		HTTP: HTTPConfig{
			BindAddress:              DefaultHTTPBindAddress,
			ReadTimeoutSeconds:       30,
			ReadHeaderTimeoutSeconds: 10,
			WriteTimeoutSeconds:      60,
			IdleTimeoutSeconds:       120,
			MaxRequestBodyBytes:      10 << 20, // 10 MiB
			MaxUploadBodyBytes:       32 << 20, // 32 MiB (matches pre-existing multipart cap)
		},
		Logging: LoggingConfig{
			Format:    "json",
			Level:     "info",
			RedactPII: true,
		},
	}
}

// LoadAppConfig reads config/nanite.yaml and returns the parsed config.
// Returns defaults if the file doesn't exist.
func LoadAppConfig(path string) (*AppConfig, error) {
	cfg := DefaultAppConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read app config: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse app config: %w", err)
	}
	bindAddress, err := ResolveHTTPBindAddress(cfg.HTTP.BindAddress)
	if err != nil {
		return nil, fmt.Errorf("validate app config: %w", err)
	}
	cfg.HTTP.BindAddress = bindAddress

	return cfg, nil
}

// IsAutoDetectTool checks if a tool name matches the auto-detect list (case-insensitive).
func (c *ArtifactsConfig) IsAutoDetectTool(toolName string) bool {
	lower := strings.ToLower(toolName)
	for _, t := range c.AutoDetectTools {
		if strings.ToLower(t) == lower {
			return true
		}
	}
	return false
}

// ExtractFilePath extracts a file path from tool input using configured path keys.
func (c *ArtifactsConfig) ExtractFilePath(input map[string]any) string {
	for _, key := range c.PathKeys {
		if val, ok := input[key]; ok {
			if s, ok := val.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}
