// Package install validates plugin archives and their manifests before they
// are loaded into the host.
//
// ValidateManifest enforces the plugin.yaml v1 JSON Schema (via santhosh-tekuri
// jsonschema), cross-reference consistency between declared registrations and
// on-disk assets, and plugin-archive hygiene checks (bundle files exist, agent
// profiles resolve, envelope schema files resolve, etc).
//
// DeveloperMode downgrades most "refuse" outcomes to warnings so that
// developers iterating on a plugin can still load it without shipping every
// asset.
package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	plugin "github.com/hollis-labs/nanite/internal/plugin"
)

// Severity describes how strict a failure is. Refuse-level failures block
// installation; warn-level failures are logged but allow install to proceed.
type Severity string

const (
	SeverityRefuse Severity = "refuse"
	SeverityWarn   Severity = "warn"
)

// Kind categorises an install-time failure for reporting / filtering.
type Kind string

const (
	KindSchema    Kind = "schema"
	KindCrossRef  Kind = "cross-ref"
	KindBundle    Kind = "bundle"
	KindAsset     Kind = "asset"
	KindPlatform  Kind = "platform"
	KindSignature Kind = "signature"
)

// InstallFailure is a single validation problem.
type InstallFailure struct {
	Kind     Kind     `json:"kind"`
	Field    string   `json:"field,omitempty"`
	Message  string   `json:"message"`
	Severity Severity `json:"severity"`
}

// InstallError aggregates InstallFailures. A non-nil InstallError may contain
// warning-only failures as well as refuse-level failures; callers should use
// HasRefusals to determine whether validation must block installation. A nil
// *InstallError means validation had zero failures of any severity.
type InstallError struct {
	Failures []InstallFailure
}

func (e *InstallError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "plugin install validation failed (%d issue(s)):", len(e.Failures))
	for _, f := range e.Failures {
		fmt.Fprintf(&b, "\n  [%s/%s] %s: %s", f.Severity, f.Kind, f.Field, f.Message)
	}
	return b.String()
}

// HasRefusals reports whether at least one failure is refuse-level.
func (e *InstallError) HasRefusals() bool {
	if e == nil {
		return false
	}
	for _, f := range e.Failures {
		if f.Severity == SeverityRefuse {
			return true
		}
	}
	return false
}

// Warnings returns the warn-level failures (if any).
func (e *InstallError) Warnings() []InstallFailure {
	if e == nil {
		return nil
	}
	var w []InstallFailure
	for _, f := range e.Failures {
		if f.Severity == SeverityWarn {
			w = append(w, f)
		}
	}
	return w
}

// ValidationOptions tunes the validator behavior.
type ValidationOptions struct {
	// DeveloperMode downgrades most refuse outcomes (missing bundle files,
	// missing envelope schemas, missing agent-profile files) to warnings.
	// Schema violations (malformed manifest) remain refuse in developer mode.
	DeveloperMode bool

	// AllowedPlatforms optionally restricts acceptable release.platforms values
	// to a known set. Empty means any platform string is accepted.
	AllowedPlatforms []string
}

// ValidateManifest validates the plugin.yaml at manifestPath and checks
// cross-references against files on disk rooted at pluginDir. Returns nil
// only when there are zero failures of any severity. When either refusals
// or warnings are present the returned *InstallError carries the full
// failure list; callers should branch on HasRefusals to decide whether to
// block installation.
func ValidateManifest(manifestPath, pluginDir string, opts ValidationOptions) *InstallError {
	v := &validator{
		manifestPath: manifestPath,
		pluginDir:    pluginDir,
		opts:         opts,
	}

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		v.refuse(KindSchema, "", fmt.Sprintf("read plugin.yaml: %v", err))
		return v.result()
	}
	v.run(data)
	return v.result()
}

// ValidateBytes validates raw plugin.yaml bytes (schema + decode only). Used
// when the manifest is already in memory (e.g. before archive extraction).
func ValidateBytes(manifestData []byte, opts ValidationOptions) *InstallError {
	v := &validator{opts: opts}
	v.runSchemaOnly(manifestData)
	return v.result()
}

// validator holds the state for a single validation pass.
type validator struct {
	manifestPath string
	pluginDir    string
	opts         ValidationOptions
	failures     []InstallFailure
}

func (v *validator) refuse(kind Kind, field, msg string) {
	v.failures = append(v.failures, InstallFailure{
		Kind: kind, Field: field, Message: msg, Severity: SeverityRefuse,
	})
}

func (v *validator) warn(kind Kind, field, msg string) {
	v.failures = append(v.failures, InstallFailure{
		Kind: kind, Field: field, Message: msg, Severity: SeverityWarn,
	})
}

// refuseOrWarn adds a refusal under normal mode, warning in developer mode.
func (v *validator) refuseOrWarn(kind Kind, field, msg string) {
	if v.opts.DeveloperMode {
		v.warn(kind, field, msg)
	} else {
		v.refuse(kind, field, msg)
	}
}

func (v *validator) result() *InstallError {
	if len(v.failures) == 0 {
		return nil
	}
	return &InstallError{Failures: v.failures}
}

// runSchemaOnly executes only the JSON Schema pass (for ValidateBytes).
func (v *validator) runSchemaOnly(data []byte) {
	_, _ = v.validateSchema(data)
}

// run executes the full validation pipeline. It short-circuits further checks
// when schema validation fails (garbage manifest → nothing else to check).
func (v *validator) run(data []byte) {
	manifest, ok := v.validateSchema(data)
	if !ok {
		return
	}
	v.validateCrossRefs(manifest)
	v.validateBundleAssets(manifest)
	v.validatePlatforms(manifest)
	v.validateShadcnVersion(manifest)
}

// validateShadcnVersion enforces ui.shadcn_version compatibility with the
// host. Empty is treated as "plugin does not use shared primitives" and
// passes silently. J.5 OQ9.
func (v *validator) validateShadcnVersion(m *plugin.PluginManifest) {
	if m == nil {
		return
	}
	if err := plugin.CheckShadcnCompat(m.UI.ShadcnVersion); err != nil {
		v.refuseOrWarn(KindSchema, "ui.shadcn_version", err.Error())
	}
}

// validateSchema decodes the yaml, runs it against the embedded schema, and
// (on success) also returns the strongly-typed PluginManifest so later phases
// can operate on it without re-parsing.
func (v *validator) validateSchema(data []byte) (*plugin.PluginManifest, bool) {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		v.refuse(KindSchema, "", fmt.Sprintf("parse yaml: %v", err))
		return nil, false
	}

	// Convert yaml any → json-compatible any for the schema library.
	jsonBytes, err := json.Marshal(raw)
	if err != nil {
		v.refuse(KindSchema, "", fmt.Sprintf("normalize yaml→json: %v", err))
		return nil, false
	}
	var jsonDoc any
	if err := json.Unmarshal(jsonBytes, &jsonDoc); err != nil {
		v.refuse(KindSchema, "", fmt.Sprintf("normalize yaml→json: %v", err))
		return nil, false
	}

	s, err := plugin.SchemaV1()
	if err != nil {
		v.refuse(KindSchema, "", fmt.Sprintf("load embedded schema: %v", err))
		return nil, false
	}
	if err := s.Validate(jsonDoc); err != nil {
		// Always refuse on schema violations — even in developer mode a
		// malformed manifest cannot be loaded.
		v.refuse(KindSchema, "", err.Error())
		return nil, false
	}

	var manifest plugin.PluginManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		v.refuse(KindSchema, "", fmt.Sprintf("decode manifest: %v", err))
		return nil, false
	}
	return &manifest, true
}

// envelopeTypePattern mirrors the schema pattern for envelope types.
var envelopeTypePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// validateCrossRefs checks that declared registrations are internally
// consistent (no duplicate ids/names, component references resolve, etc).
func (v *validator) validateCrossRefs(m *plugin.PluginManifest) {
	// Envelope types must be unique.
	seenEnv := map[string]bool{}
	for i, e := range m.Registers.Envelopes {
		field := fmt.Sprintf("registers.envelopes[%d]", i)
		if e.Type == "" {
			v.refuse(KindCrossRef, field+".type", "empty envelope type")
			continue
		}
		if !envelopeTypePattern.MatchString(e.Type) {
			v.refuse(KindCrossRef, field+".type", fmt.Sprintf("envelope type %q violates pattern %s", e.Type, envelopeTypePattern))
		}
		if seenEnv[e.Type] {
			v.refuse(KindCrossRef, field+".type", fmt.Sprintf("duplicate envelope type %q", e.Type))
		}
		seenEnv[e.Type] = true
		if e.Component == "" {
			v.refuse(KindCrossRef, field+".component", "envelope requires component")
		}
	}

	// Command names must be unique.
	seenCmd := map[string]bool{}
	for i, c := range m.Registers.Commands {
		field := fmt.Sprintf("registers.commands[%d]", i)
		if c.Name == "" {
			v.refuse(KindCrossRef, field+".name", "empty command name")
			continue
		}
		if seenCmd[c.Name] {
			v.refuse(KindCrossRef, field+".name", fmt.Sprintf("duplicate command %q", c.Name))
		}
		seenCmd[c.Name] = true
	}

	// Slot ids per-slot must be unique.
	seenSlot := map[string]bool{}
	for i, s := range m.Registers.Slots {
		field := fmt.Sprintf("registers.slots[%d]", i)
		key := s.Slot + "/" + s.ID
		if s.Slot == "" || s.ID == "" || s.Component == "" {
			v.refuse(KindCrossRef, field, "slot requires slot, id, component")
			continue
		}
		if seenSlot[key] {
			v.refuse(KindCrossRef, field, fmt.Sprintf("duplicate slot entry %q", key))
		}
		seenSlot[key] = true
	}

	// Keybinding ids must be unique.
	seenKB := map[string]bool{}
	for i, k := range m.Registers.Keybindings {
		field := fmt.Sprintf("registers.keybindings[%d]", i)
		if k.ID == "" {
			v.refuse(KindCrossRef, field+".id", "empty keybinding id")
			continue
		}
		if seenKB[k.ID] {
			v.refuse(KindCrossRef, field+".id", fmt.Sprintf("duplicate keybinding id %q", k.ID))
		}
		seenKB[k.ID] = true
	}

	// HTTP route (pattern,method) pairs unique.
	seenRoute := map[string]bool{}
	for i, r := range m.Registers.HttpRoutes {
		field := fmt.Sprintf("registers.http_routes[%d]", i)
		key := r.Method + " " + r.Pattern
		if r.Pattern == "" || r.Method == "" {
			v.refuse(KindCrossRef, field, "http_route requires pattern and method")
			continue
		}
		if seenRoute[key] {
			v.refuse(KindCrossRef, field, fmt.Sprintf("duplicate route %q", key))
		}
		seenRoute[key] = true
	}

	// MCP server names must be unique.
	seenMCP := map[string]bool{}
	for i, s := range m.Registers.McpServers {
		field := fmt.Sprintf("registers.mcp_servers[%d]", i)
		if s.Name == "" {
			v.refuse(KindCrossRef, field+".name", "empty mcp server name")
			continue
		}
		if seenMCP[s.Name] {
			v.refuse(KindCrossRef, field+".name", fmt.Sprintf("duplicate mcp server %q", s.Name))
		}
		seenMCP[s.Name] = true
	}

	// Agent profile ids must be unique.
	seenAP := map[string]bool{}
	for i, a := range m.Registers.AgentProfiles {
		field := fmt.Sprintf("registers.agent_profiles[%d]", i)
		if a.ID == "" || a.File == "" {
			v.refuse(KindCrossRef, field, "agent_profile requires id and file")
			continue
		}
		if seenAP[a.ID] {
			v.refuse(KindCrossRef, field+".id", fmt.Sprintf("duplicate agent profile %q", a.ID))
		}
		seenAP[a.ID] = true
	}

	// Card rule card_types must be unique and have exactly one matcher set.
	seenCR := map[string]bool{}
	for i, cr := range m.Registers.CardRules {
		field := fmt.Sprintf("registers.card_rules[%d]", i)
		if cr.CardType == "" {
			v.refuse(KindCrossRef, field+".card_type", "card rule requires card_type")
			continue
		}
		if seenCR[cr.CardType] {
			v.refuse(KindCrossRef, field+".card_type", fmt.Sprintf("duplicate card_type %q", cr.CardType))
		}
		seenCR[cr.CardType] = true
		if cr.Pattern == "" && cr.OutputSchema == "" {
			v.refuse(KindCrossRef, field, fmt.Sprintf("card_type %q: exactly one of pattern or output_schema is required", cr.CardType))
		}
		if cr.Pattern != "" && cr.OutputSchema != "" {
			v.refuse(KindCrossRef, field, fmt.Sprintf("card_type %q: pattern and output_schema are mutually exclusive", cr.CardType))
		}
	}
}

// validateBundleAssets checks that files referenced by the manifest exist
// inside pluginDir. Skipped when pluginDir is empty (ValidateBytes).
func (v *validator) validateBundleAssets(m *plugin.PluginManifest) {
	if v.pluginDir == "" {
		return
	}

	// Subprocess entrypoint.
	if m.Runtime == "subprocess" && m.Entrypoint != "" {
		// Entrypoint may be "./bin", "python3 plugin.py", etc. Only check
		// dot-prefixed relative paths for existence; PATH-resolved commands
		// can't be verified here. Reject parent-directory paths because
		// entrypoints are expected to stay inside pluginDir.
		tokens := strings.Fields(m.Entrypoint)
		if len(tokens) > 0 {
			t := tokens[0]
			switch {
			case strings.HasPrefix(t, "../") || t == "..":
				v.refuseOrWarn(KindBundle, "entrypoint", fmt.Sprintf("entrypoint %q must not escape plugin dir", t))
			case strings.HasPrefix(t, "./"):
				if !v.pathExists(t) {
					v.refuseOrWarn(KindBundle, "entrypoint", fmt.Sprintf("entrypoint %q not found under plugin dir", t))
				}
			}
		}
	}

	// Envelope schema files.
	for i, e := range m.Registers.Envelopes {
		if e.Schema == "" {
			continue
		}
		field := fmt.Sprintf("registers.envelopes[%d].schema", i)
		if !v.pathExists(e.Schema) {
			v.refuseOrWarn(KindAsset, field, fmt.Sprintf("envelope schema %q not found", e.Schema))
		}
	}

	// Agent profile files.
	for i, a := range m.Registers.AgentProfiles {
		field := fmt.Sprintf("registers.agent_profiles[%d].file", i)
		if !v.pathExists(a.File) {
			v.refuseOrWarn(KindAsset, field, fmt.Sprintf("agent profile %q not found", a.File))
		}
	}

	// UI bundle files.
	ui := m.UI
	if ui.BundleDir != "" || ui.Entry != "" || ui.Stylesheet != "" || ui.AssetsDir != "" {
		if ui.Entry == "" {
			v.refuseOrWarn(KindBundle, "ui.entry", "ui.entry required when ui bundle is declared")
		} else {
			entry := ui.Entry
			if ui.BundleDir != "" {
				entry = filepath.Join(ui.BundleDir, ui.Entry)
			}
			if !v.pathExists(entry) {
				v.refuseOrWarn(KindBundle, "ui.entry", fmt.Sprintf("ui entry %q not found", entry))
			}
		}
		if ui.Stylesheet != "" {
			sheet := ui.Stylesheet
			if ui.BundleDir != "" {
				sheet = filepath.Join(ui.BundleDir, ui.Stylesheet)
			}
			if !v.pathExists(sheet) {
				v.refuseOrWarn(KindBundle, "ui.stylesheet", fmt.Sprintf("ui stylesheet %q not found", sheet))
			}
		}
	}
}

// pathExists reports whether rel, resolved against v.pluginDir, names an
// existing file or directory. Absolute paths and paths that would escape
// pluginDir (via ".." segments or symlink-style tricks on the literal path)
// return false — validator never follows entries outside the plugin sandbox.
func (v *validator) pathExists(rel string) bool {
	if rel == "" {
		return false
	}
	cleanRel := filepath.Clean(rel)
	if filepath.IsAbs(cleanRel) {
		return false
	}
	pluginDir := filepath.Clean(v.pluginDir)
	p := filepath.Join(pluginDir, cleanRel)
	resolvedRel, err := filepath.Rel(pluginDir, p)
	if err != nil {
		return false
	}
	if resolvedRel == ".." || strings.HasPrefix(resolvedRel, ".."+string(filepath.Separator)) {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// validatePlatforms checks release.platforms against the optional allow-list.
func (v *validator) validatePlatforms(m *plugin.PluginManifest) {
	if len(v.opts.AllowedPlatforms) == 0 || len(m.Release.Platforms) == 0 {
		return
	}
	allowed := map[string]bool{}
	for _, p := range v.opts.AllowedPlatforms {
		allowed[p] = true
	}
	for i, p := range m.Release.Platforms {
		if !allowed[p] {
			v.warn(KindPlatform, fmt.Sprintf("release.platforms[%d]", i), fmt.Sprintf("unknown platform %q", p))
		}
	}
}
