package plugin

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LoadType controls whether a plugin's tools are available automatically or
// require explicit opt-in. The zero value ("") is treated as LoadTypeAuto.
type LoadType string

const (
	LoadTypeAuto  LoadType = "auto"   // tools available by default
	LoadTypeOptIn LoadType = "opt-in" // tools hidden until explicitly enabled
)

// IsOptIn returns true if the load type requires explicit enablement.
func (lt LoadType) IsOptIn() bool {
	return lt == LoadTypeOptIn
}

// Effective returns the canonical value, treating the zero value as auto.
func (lt LoadType) Effective() LoadType {
	if lt == "" {
		return LoadTypeAuto
	}
	return lt
}

// ToolLoadOverride allows per-tool loadType in the plugin manifest.
type ToolLoadOverride struct {
	LoadType LoadType `yaml:"load_type" json:"load_type"`
}

// PluginManifest represents the parsed plugin.yaml file.
//
// The schema is versioned via SchemaVersion. v1 adds explicit registration
// declarations (Registers.*), richer requires (structured), UI bundle metadata,
// and release archive metadata so that plugin.yaml is the authoritative source
// of truth for how a plugin registers itself with the host.
type PluginManifest struct {
	// SchemaVersion identifies the plugin.yaml schema version.
	// v1 is the current schema. Unset/0 is treated as legacy (pre-v1).
	SchemaVersion int `yaml:"schema_version"`

	// Identity. In the v1 schema ID is the canonical plugin identifier — it
	// is required, must match ^[a-z0-9][a-z0-9-]*$, and is what the host uses
	// for registry lookups, config keys, and ownership tracking. Name is a
	// human-readable display label only. Legacy v0 manifests omit ID, so
	// identity lookups should go through Identifier() which falls back to
	// Name when ID is empty.
	Name        string `yaml:"name"`
	ID          string `yaml:"id"`
	Version     string `yaml:"version"`
	Description string `yaml:"description"`
	Author      string `yaml:"author"`
	URL         string `yaml:"url"`
	ShortDesc   string `yaml:"short_desc"`
	License     string `yaml:"license"`
	Homepage    string `yaml:"homepage"`
	Repository  string `yaml:"repository"`

	// Protocol is the plugin subprocess JSON-RPC protocol version this plugin
	// targets. 0 means unset (legacy). Current is 1.
	Protocol int `yaml:"protocol"`

	// NaniteCompat expresses the host version range the plugin supports.
	NaniteCompat NaniteCompat `yaml:"nanite_compat"`

	// Legacy-style config schema. Still used by ConfigReader.
	Config map[string]ConfigEntry `yaml:"config"`

	// Dependencies lists other plugin IDs this plugin depends on (legacy
	// flat list; v1 prefers Requires.Plugins).
	Dependencies []string `yaml:"dependencies"`

	// Requires captures structured dependency requirements. v1 schema.
	Requires ManifestRequires `yaml:"requires"`

	// Registers holds all declarative registrations. See the Registers* types.
	Registers ManifestRegisters `yaml:"registers"`

	// UI describes the plugin's frontend bundle (if any).
	UI ManifestUI `yaml:"ui"`

	// Release describes the published archive(s) for this plugin version.
	Release ManifestRelease `yaml:"release"`

	// Runtime specifies how the plugin is executed.
	// "builtin" (default) — compiled into the binary.
	// "subprocess" — runs as a separate process communicating via JSON-RPC.
	Runtime string `yaml:"runtime"`

	// Entrypoint is the executable command for subprocess plugins.
	// Relative paths are resolved from the plugin directory.
	// Example: "./my-plugin" or "python3 plugin.py"
	Entrypoint string `yaml:"entrypoint"`

	// LoadType sets the default tool loading behavior for this plugin.
	// "auto" (default): all tools available immediately.
	// "opt-in": tools hidden until enabled by user/agent/project/session config.
	LoadType LoadType `yaml:"load_type"`

	// ToolOverrides allows per-tool loadType that overrides the plugin default.
	// Keys are bare tool names (not prefixed).
	ToolOverrides map[string]ToolLoadOverride `yaml:"tool_overrides"`
}

// NaniteCompat expresses the nanite host version range a plugin supports.
type NaniteCompat struct {
	Min string `yaml:"min"`
	Max string `yaml:"max"`
}

// ManifestRequires captures structured dependency requirements.
type ManifestRequires struct {
	McpServers []string `yaml:"mcp_servers"`
	Plugins    []string `yaml:"plugins"`
	Features   []string `yaml:"features"`
}

// ManifestRegisters declares all of the plugin's registrations. The loader
// translates each list into the corresponding host Register* call.
type ManifestRegisters struct {
	Envelopes     []EnvelopeRegistration      `yaml:"envelopes"`
	Commands      []CommandRegistration       `yaml:"commands"`
	Slots         []SlotRegistration          `yaml:"slots"`
	Components    []ComponentRegistration     `yaml:"components"`
	Keybindings   []KeybindingRegistration    `yaml:"keybindings"`
	Events        []EventRegistration         `yaml:"events"`
	Crud          []CRUDRegistration          `yaml:"crud"`
	HttpRoutes    []HTTPRouteRegistration     `yaml:"http_routes"`
	McpServers    []MCPServerRegistration     `yaml:"mcp_servers"`
	AgentProfiles []AgentProfileRegistration  `yaml:"agent_profiles"`
	// CardRules declares Stage 1 card detection rules the plugin contributes.
	// Each rule is evaluated against agent output text; the first matching rule
	// (by regex pattern or output schema) emits its card_type. Plugin rules run
	// AFTER built-in rules and can only add new card types, not override builtins.
	CardRules     []CardRuleRegistration      `yaml:"card_rules"`
	// Panels declares right-rail v2 panels this plugin contributes (J9).
	// Each entry adds a panel to the host's panel registry and makes it
	// available in the right-rail tab strip. Panel rendering in v1 is a
	// placeholder — the render function for plugin panels is a follow-up.
	// Trust gate: install-time only (H1); runtime registration is not supported.
	Panels        []PanelRegistration         `yaml:"panels"`
}

// PanelRegistration declares a right-rail panel the plugin contributes.
// Mirrors the card_rules shape: declarative at install time, registered into
// the host panel registry at plugin load, unregistered at plugin unload.
//
// ID must match ^[a-z][a-z0-9-]*$ (same constraint as card_type).
// Title is the human-readable tab label.
// DefaultVisible controls whether the panel appears in the tab strip by
// default (before user prefs override it). Plugin panels default to false.
// Icon is an optional Lucide icon name (e.g. "layers", "file-text"). When
// omitted the frontend falls back to the generic Layers icon.
// Order is a numeric sort hint; plugin panels default to 100+.
type PanelRegistration struct {
	ID             string `yaml:"id"`
	Title          string `yaml:"title"`
	DefaultVisible bool   `yaml:"default_visible"`
	Icon           string `yaml:"icon,omitempty"`
	Order          int    `yaml:"order,omitempty"`
	Description    string `yaml:"description,omitempty"`
}

// CardRuleRegistration declares a single Stage 1 card detection rule.
//
// Exactly one of Pattern or OutputSchema must be set:
//   - Pattern: a RE2-compatible regular expression matched against the full
//     agent output text. The first match wins; the captured card_type is emitted.
//   - OutputSchema: a relative path (inside the plugin dir) to a JSON Schema
//     file. The output is parsed as JSON and validated against the schema;
//     a successful validation emits card_type.
//
// CardType is the envelope type string to emit when the rule matches (must
// match ^[a-z][a-z0-9-]*$, same constraint as envelope types).
//
// Description is optional human-readable documentation for the rule.
type CardRuleRegistration struct {
	CardType     string `yaml:"card_type"`
	Pattern      string `yaml:"pattern,omitempty"`
	OutputSchema string `yaml:"output_schema,omitempty"`
	Description  string `yaml:"description,omitempty"`
}

// EnvelopeRegistration declares a chat envelope type the plugin emits.
// Component names the React component that renders this envelope; Schema
// is a relative path to the JSON Schema describing Data payloads.
type EnvelopeRegistration struct {
	Type      string `yaml:"type"`
	Component string `yaml:"component"`
	Version   int    `yaml:"version"`
	Schema    string `yaml:"schema"`
}

// CommandRegistration declares a slash command.
type CommandRegistration struct {
	Name        string                 `yaml:"name"`
	Description string                 `yaml:"description"`
	Args        []CommandArgDecl       `yaml:"args"`
	Aliases     []string               `yaml:"aliases"`
	Hidden      bool                   `yaml:"hidden"`
	Handler     string                 `yaml:"handler"`
	Metadata    map[string]interface{} `yaml:"metadata"`
}

// CommandArgDecl describes a single command argument.
type CommandArgDecl struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type"`
	Required    bool   `yaml:"required"`
	Description string `yaml:"description"`
	Default     string `yaml:"default"`
}

// SlotRegistration declares a UI slot contribution.
type SlotRegistration struct {
	Slot      string                 `yaml:"slot"`
	ID        string                 `yaml:"id"`
	Component string                 `yaml:"component"`
	Priority  int                    `yaml:"priority"`
	Props     map[string]interface{} `yaml:"props"`
}

// ComponentRegistration declares a reusable UI component the plugin exposes.
type ComponentRegistration struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type"`
	Description string `yaml:"description"`
	Export      string `yaml:"export"`
}

// KeybindingRegistration declares a keyboard shortcut.
type KeybindingRegistration struct {
	ID          string `yaml:"id"`
	Keys        string `yaml:"keys"`
	Description string `yaml:"description"`
	Command     string `yaml:"command"`
	When        string `yaml:"when"`
}

// EventRegistration declares which host event types this plugin subscribes to.
type EventRegistration struct {
	Types    []string `yaml:"types"`
	Handler  string   `yaml:"handler"`
	Priority int      `yaml:"priority"`
}

// CRUDRegistration declares a CRUD resource handler.
type CRUDRegistration struct {
	Resource string   `yaml:"resource"`
	Methods  []string `yaml:"methods"`
}

// HTTPRouteRegistration declares an HTTP route contribution.
type HTTPRouteRegistration struct {
	Pattern string `yaml:"pattern"`
	Method  string `yaml:"method"`
	Handler string `yaml:"handler"`
}

// MCPServerRegistration declares an MCP server the plugin exposes.
type MCPServerRegistration struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Tools       []string `yaml:"tools"`
}

// AgentProfileRegistration declares an agent profile shipped with the plugin.
// File is a relative path to the YAML file containing the profile.
type AgentProfileRegistration struct {
	ID   string `yaml:"id"`
	File string `yaml:"file"`
}

// ManifestUI describes the plugin's frontend bundle.
type ManifestUI struct {
	BundleDir    string `yaml:"bundle_dir"`
	Entry        string `yaml:"entry"`
	Stylesheet   string `yaml:"stylesheet"`
	AssetsDir    string `yaml:"assets_dir"`
	ReactVersion string `yaml:"react_version"`
	// ShadcnVersion is a semver range declaring which host shadcn primitive
	// API the plugin was built against. Plugins that import
	// `@nanite/ui/<primitive>` via the importmap (J.5 OQ9) should set this
	// so breaking primitive changes surface at install time rather than as
	// runtime render errors. Empty means "no shared primitives used" and the
	// install-time check is skipped.
	ShadcnVersion string `yaml:"shadcn_version"`
}

// ManifestRelease describes the published artifact(s) for this plugin version.
type ManifestRelease struct {
	ArchiveURL   string   `yaml:"archive_url"`
	ChecksumURL  string   `yaml:"checksum_url"`
	SignatureURL string   `yaml:"signature_url"`
	Platforms    []string `yaml:"platforms"`
}

// Identifier returns the canonical plugin identifier: ID when set (v1),
// otherwise Name (legacy v0 manifests). Use this anywhere the value is
// consumed for identity — registry lookups, config keys, log messages that
// reference the plugin, ownership tracking. Display-oriented callers should
// keep using Name directly.
func (pm *PluginManifest) Identifier() string {
	if pm.ID != "" {
		return pm.ID
	}
	return pm.Name
}

// EffectiveLoadType returns the resolved loadType for a specific tool.
// Per-tool override takes priority over the plugin-level default.
func (pm *PluginManifest) EffectiveLoadType(toolName string) LoadType {
	if override, ok := pm.ToolOverrides[toolName]; ok {
		return override.LoadType.Effective()
	}
	return pm.LoadType.Effective()
}

// ConfigEntry describes a single configuration value in plugin.yaml.
type ConfigEntry struct {
	Type        string `yaml:"type"`
	Required    bool   `yaml:"required"`
	EnvVar      string `yaml:"env_var"`
	Default     string `yaml:"default"`
	Description string `yaml:"description"`
}

// PluginConfig holds the resolved configuration for a single plugin.
type PluginConfig struct {
	pluginID string
	schema   map[string]ConfigEntry
	// overrides loaded from a per-plugin config.yaml file (optional)
	overrides map[string]string
}

// NewPluginConfig builds a PluginConfig by parsing the plugin.yaml in the given
// directory.  If the file does not exist the config is empty (no error).
func NewPluginConfig(pluginID, pluginDir string) (*PluginConfig, error) {
	pc := &PluginConfig{
		pluginID:  pluginID,
		schema:    make(map[string]ConfigEntry),
		overrides: make(map[string]string),
	}

	// Parse plugin.yaml for schema.
	manifestPath := filepath.Join(pluginDir, "plugin.yaml")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return pc, nil // no manifest — empty config
		}
		return nil, fmt.Errorf("read plugin.yaml: %w", err)
	}

	var manifest PluginManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse plugin.yaml: %w", err)
	}
	pc.schema = manifest.Config

	// Load optional per-plugin config.yaml overrides.
	configPath := filepath.Join(pluginDir, "config.yaml")
	if cfgData, err := os.ReadFile(configPath); err == nil {
		var overrides map[string]string
		if err := yaml.Unmarshal(cfgData, &overrides); err == nil {
			pc.overrides = overrides
		}
	}

	return pc, nil
}

// ParseManifest reads and parses a plugin.yaml file.
func ParseManifest(path string) (*PluginManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m PluginManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &m, nil
}

// Get resolves a config value.  Resolution order:
//  1. Environment variable (from schema env_var)
//  2. Override from config.yaml
//  3. Default from schema
//  4. Error if required
func (pc *PluginConfig) Get(key string) (string, error) {
	entry, ok := pc.schema[key]
	if !ok {
		return "", fmt.Errorf("unknown config key %q for plugin %s", key, pc.pluginID)
	}

	// 1. Env var
	if entry.EnvVar != "" {
		if v := os.Getenv(entry.EnvVar); v != "" {
			return v, nil
		}
	}

	// 2. Override file
	if v, ok := pc.overrides[key]; ok && v != "" {
		return v, nil
	}

	// 3. Default
	if entry.Default != "" {
		return entry.Default, nil
	}

	// 4. Required?
	if entry.Required {
		return "", fmt.Errorf("required config key %q not set for plugin %s (env: %s)", key, pc.pluginID, entry.EnvVar)
	}

	return "", nil
}
