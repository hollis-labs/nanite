package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// AppConfig holds application-level tunables loaded from config/nanite.yaml.
type AppConfig struct {
	Presence  PresenceConfig  `yaml:"presence"`
	Artifacts ArtifactsConfig `yaml:"artifacts"`
	HTTP      HTTPConfig      `yaml:"http"`
}

// HTTPConfig controls HTTP server timeouts and body-size limits. All values
// are optional; zero or unset values fall back to conservative defaults at
// Server construction time. See server.New for the fallback policy.
type HTTPConfig struct {
	ReadTimeoutSeconds       int `yaml:"read_timeout_seconds"`
	ReadHeaderTimeoutSeconds int `yaml:"read_header_timeout_seconds"`
	WriteTimeoutSeconds      int `yaml:"write_timeout_seconds"`
	IdleTimeoutSeconds       int `yaml:"idle_timeout_seconds"`
	// MaxRequestBodyBytes caps the body of any mutating (POST/PUT/PATCH/DELETE)
	// request that is not explicitly whitelisted for a larger cap (e.g.
	// multipart artifact upload). A value <=0 falls back to the default.
	MaxRequestBodyBytes int64 `yaml:"max_request_body_bytes"`
	// MaxUploadBodyBytes caps the body of multipart artifact/plugin uploads.
	// Override separately from MaxRequestBodyBytes so routine JSON endpoints
	// stay tight while file-upload endpoints get the headroom they need.
	MaxUploadBodyBytes int64 `yaml:"max_upload_body_bytes"`
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
		HTTP: HTTPConfig{
			ReadTimeoutSeconds:       30,
			ReadHeaderTimeoutSeconds: 10,
			WriteTimeoutSeconds:      60,
			IdleTimeoutSeconds:       120,
			MaxRequestBodyBytes:      10 << 20, // 10 MiB
			MaxUploadBodyBytes:       32 << 20, // 32 MiB (matches pre-existing multipart cap)
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
