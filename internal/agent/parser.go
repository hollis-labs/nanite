package agent

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Definition is the typed representation of an agent MD file.
// YAML frontmatter fields map to struct tags; the markdown body
// after the closing --- becomes SystemPrompt.
type Definition struct {
	// Identity
	// ID is the durable agent identity stamped into the managed file's
	// frontmatter (`id:`). When present it is the canonical primary key the
	// DB projection and all FK children (reflexes, known tools/skills,
	// procedures, boot plans) key off — so the slug can change freely
	// without orphaning anything. Empty for embedded internal profiles and
	// freshly hand-dropped/imported files; the boot reconcile pass mints or
	// adopts a UUID and writes it back to writable managed files. See
	// internal/service AgentConfigService + ingest reconciliation.
	ID          string   `yaml:"id,omitempty"`
	Name        string   `yaml:"name"`
	Slug        string   `yaml:"slug"`
	Description string   `yaml:"description"`
	Icon        string   `yaml:"icon"`
	Avatar      string   `yaml:"avatar"`
	Tags        []string `yaml:"tags"`

	// Behavior
	//
	// Model: leave blank. A blank value means "inherit whatever the system
	// default is at request time" — the chat-engine resolver
	// (store.ResolveProviderAndModel, CW-20260526-0003) re-evaluates it on
	// every call, walking explicit request → user_settings.default_model →
	// providers.default_model. Hardcoding a specific model ID here bypasses
	// that SSOT and *will* eventually 404 once the pinned model is retired
	// (CW-20260815-0021 — ten profiles independently made this mistake).
	// Only set Model when a profile has a genuine, deliberate reason to run
	// on something other than the system default; AutoIngestAgents logs a
	// loud warning for every profile that does.
	Model          string           `yaml:"model"`
	Tools          []string         `yaml:"tools"`
	PermissionMode string           `yaml:"permissionMode"`
	MaxTurns       int              `yaml:"maxTurns"`
	Skills         []string         `yaml:"skills"`
	MCPServers     []string         `yaml:"mcpServers"`
	Memory         string           `yaml:"memory"`
	Effort         string           `yaml:"effort"`
	Isolation      string           `yaml:"isolation"`
	Directories    []string         `yaml:"directories"`
	Constraints    AgentConstraints `yaml:"constraints"`

	// Modes (inline)
	Modes []ModeDefinition `yaml:"modes"`

	// ToolPermissions, when set, replaces the implicit allow_list derived from
	// Tools. Lets file-based agents express deny rules, allow-list patterns,
	// and call-budget caps without needing an agent_profiles row.
	ToolPermissions *AgentToolPermissions `yaml:"toolPermissions,omitempty"`

	// ParentDispatchAllowlist enumerates the role slugs this agent (as a
	// parent) may dispatch via task_execute. Surfaced into task_execute's
	// rendered description via the Tool Broker Describe hook
	// (CW-20260512-0105 W1B). When omitted, the agent has no dispatch
	// permission and the description renders the baseline body.
	// Added by CW-20260512-0107 (SP-20260512-0008 W2A).
	ParentDispatchAllowlist []string `yaml:"parentDispatchAllowlist,omitempty"`

	// RoleTools — pre-seed list of tool names for agent_known_tools.
	RoleTools []string `yaml:"roleTools,omitempty"`

	// ContextPolicy declares how the agent's live context should cycle.
	// It is intentionally open-shaped so project-managed agents can carry
	// richer policy keys without a Go release for each new field.
	ContextPolicy map[string]any `yaml:"contextPolicy,omitempty"`

	// Durable marks the profile as durable/file-managed intent.
	Durable bool `yaml:"durable,omitempty"`

	// ActivationMode, Class, and DefaultState mirror the multi-agent columns.
	ActivationMode string `yaml:"activationMode,omitempty"`
	Class          string `yaml:"class,omitempty"`
	DefaultState   string `yaml:"defaultState,omitempty"`

	// Procedures declares file-SOT procedures to upsert during ingest.
	Procedures []ProcedureDefinition `yaml:"procedures,omitempty"`

	// SystemPrompt is the markdown body below the YAML frontmatter.
	SystemPrompt string `yaml:"-"`

	// Metadata set by the loader, not parsed from file.
	Source    string `yaml:"-"` // "builtin", "cli", "project", "user", "plugin", "nanite", "claude"
	SourceRef string `yaml:"-"` // file path or "embedded:default.md"
}

type ProcedureDefinition struct {
	Name     string `yaml:"name"`
	Body     string `yaml:"body,omitempty"`
	BodyFile string `yaml:"body_file,omitempty"`
	Scope    string `yaml:"scope,omitempty"`
}

// ModeDefinition is an inline mode within an agent file.
type ModeDefinition struct {
	Slug           string         `yaml:"slug"`
	Name           string         `yaml:"name"`
	PromptAddendum string         `yaml:"promptAddendum"`
	ToolOverrides  map[string]any `yaml:"toolOverrides"`
}

// AgentConstraints is the frontmatter slot for per-agent runtime
// constraints. CW-20260512-0123 (SP-20260512-0011 W3) removed the
// `maxIterations` / `maxTimeSeconds` / `retryBudget` keys from the
// supported schema — those fields were heavy per-call deadline /
// retry restrictions that the user mandate ("get rid of the heavy
// restrictions on agents, timeouts, etc.") deleted in favor of the
// subagent reaper as the authoritative hung-run safety net.
//
// The struct was left empty after that removal — which meant every
// field added to the real runtime-facing internal/chat.AgentConstraints
// since (the Phase-4 chat-loop breakers, and CW-20260520-0001's
// SubagentCompletionPolicy) was silently unreachable from a file-based
// agent's frontmatter: yaml.Unmarshal tolerates the unknown keys, but
// they parsed to nothing, so ToProfile() had nothing to carry into
// store.AgentProfile.Constraints. Found 2026-08-16 investigating why the
// Orchestrator role's auto_summarize policy could never take effect —
// mirrors internal/chat.AgentConstraints's fields exactly (kept as a
// separate, local type — not an import of internal/chat — matching this
// file's existing pattern for AgentToolPermissions, which mirrors
// toolclient.ToolPermissions locally to avoid a heavier dependency).
type AgentConstraints struct {
	MaxTurns                 int    `yaml:"maxTurns,omitempty" json:"max_turns,omitempty"`
	HardCeiling              int    `yaml:"hardCeiling,omitempty" json:"hard_ceiling,omitempty"`
	ConsecutiveFailCap       int    `yaml:"consecutiveFailCap,omitempty" json:"consecutive_fail_cap,omitempty"`
	RunawayFailCap           int    `yaml:"runawayFailCap,omitempty" json:"runaway_fail_cap,omitempty"`
	IdleTimeoutSeconds       int    `yaml:"idleTimeoutSeconds,omitempty" json:"idle_timeout_seconds,omitempty"`
	SubagentCompletionPolicy string `yaml:"subagentCompletionPolicy,omitempty" json:"subagent_completion_policy,omitempty"`
	// MessageWakePolicy (CW-20260816-0065) mirrors
	// internal/chat.AgentConstraints.MessageWakePolicy — see that field's
	// doc comment for the full value vocabulary and resolution path. This
	// field was missing here until the code-review pass that added it
	// (frontmatter `constraints: messageWakePolicy: ...` was silently
	// dropped by yaml.Unmarshal before this fix, since ToProfile() only
	// carries fields present on this local struct).
	MessageWakePolicy string `yaml:"messageWakePolicy,omitempty" json:"message_wake_policy,omitempty"`
}

// AgentToolPermissions mirrors toolclient.ToolPermissions in shape but is
// declared here so agent frontmatter parsing does not depend on toolclient.
// YAML tags use canonical snake_case so frontmatter matches the JSON shape
// stored in agent_profiles.tool_permissions.
type AgentToolPermissions struct {
	AllowList          []string `yaml:"allow_list" json:"allow_list,omitempty"`
	DenyList           []string `yaml:"deny_list" json:"deny_list,omitempty"`
	MaxCallsPerTurn    int      `yaml:"max_calls_per_turn" json:"max_calls_per_turn,omitempty"`
	AllowDelegation    bool     `yaml:"allow_delegation" json:"allow_delegation,omitempty"`
	AllowCodeExecution bool     `yaml:"allow_code_execution" json:"allow_code_execution,omitempty"`
}

var frontmatterDelim = []byte("---")

// ParseMD parses a markdown file with YAML frontmatter into a Definition.
// The file format is:
//
//	---
//	name: Agent Name
//	slug: agent-name
//	...
//	---
//	Markdown body becomes the system prompt.
func ParseMD(data []byte) (*Definition, error) {
	fm, body, err := splitFrontmatter(data)
	if err != nil {
		return nil, err
	}

	var def Definition
	if err := yaml.Unmarshal(fm, &def); err != nil {
		return nil, fmt.Errorf("agent: invalid YAML frontmatter: %w", err)
	}

	if def.Slug == "" {
		return nil, fmt.Errorf("agent: slug is required in frontmatter (or use ParseMDFile for filename fallback)")
	}

	def.SystemPrompt = strings.TrimSpace(string(body))
	return &def, nil
}

// ParseMDFile reads a file from disk and parses it as an agent definition.
// If the parsed definition has no slug, the filename (without extension) is used.
func ParseMDFile(path string) (*Definition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("agent: read %s: %w", path, err)
	}

	def, err := ParseMD(data)
	if err != nil {
		return nil, fmt.Errorf("agent: parse %s: %w", path, err)
	}

	// Fall back to filename-derived slug when frontmatter omits it.
	if def.Slug == "" {
		def.Slug = SlugFromFilename(path)
	}

	def.SourceRef = path
	if err := resolveProcedureBodyFiles(def, filepath.Dir(path), os.ReadFile); err != nil {
		return nil, fmt.Errorf("agent: parse %s: %w", path, err)
	}
	return def, nil
}

func resolveProcedureBodyFiles(def *Definition, baseDir string, readFile func(string) ([]byte, error)) error {
	for i := range def.Procedures {
		p := &def.Procedures[i]
		if p.BodyFile == "" {
			continue
		}
		bodyPath := filepath.Join(baseDir, p.BodyFile)
		body, err := readFile(bodyPath)
		if err != nil {
			return fmt.Errorf("procedure %q body_file %s: %w", p.Name, p.BodyFile, err)
		}
		p.Body = string(body)
		p.BodyFile = ""
	}
	return nil
}

// SlugFromFilename derives a slug from a markdown filename.
// e.g. "code-agent.md" -> "code-agent"
func SlugFromFilename(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// splitFrontmatter splits data into YAML frontmatter and markdown body.
// Returns an error if the data does not start with ---.
func splitFrontmatter(data []byte) (frontmatter, body []byte, err error) {
	data = bytes.TrimLeft(data, "\n\r")

	if !bytes.HasPrefix(data, frontmatterDelim) {
		return nil, nil, fmt.Errorf("agent: file does not start with --- frontmatter delimiter")
	}

	// Skip the opening delimiter line.
	rest := data[len(frontmatterDelim):]
	if _, after, ok := bytes.Cut(rest, []byte("\n")); ok {
		rest = after
	} else {
		return nil, nil, fmt.Errorf("agent: no content after opening --- delimiter")
	}

	// Find the closing delimiter — must appear at the start of a line.
	before, after, found := bytes.Cut(rest, append([]byte("\n"), frontmatterDelim...))
	if !found {
		return nil, nil, fmt.Errorf("agent: missing closing --- delimiter")
	}

	frontmatter = before

	// Body starts after the closing delimiter line.
	if _, bodyPart, ok := bytes.Cut(after, []byte("\n")); ok {
		body = bodyPart
	}

	return frontmatter, body, nil
}
