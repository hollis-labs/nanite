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
	Name        string   `yaml:"name"`
	Slug        string   `yaml:"slug"`
	Description string   `yaml:"description"`
	Icon        string   `yaml:"icon"`
	Avatar      string   `yaml:"avatar"`
	Tags        []string `yaml:"tags"`

	// Behavior
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

	// SystemPrompt is the markdown body below the YAML frontmatter.
	SystemPrompt string `yaml:"-"`

	// Metadata set by the loader, not parsed from file.
	Source    string `yaml:"-"` // "builtin", "cli", "project", "user", "plugin", "nanite", "claude"
	SourceRef string `yaml:"-"` // file path or "embedded:default.md"
}

// ModeDefinition is an inline mode within an agent file.
type ModeDefinition struct {
	Slug           string         `yaml:"slug"`
	Name           string         `yaml:"name"`
	PromptAddendum string         `yaml:"promptAddendum"`
	ToolOverrides  map[string]any `yaml:"toolOverrides"`
}

// AgentConstraints configures iteration and time limits for the agent.
type AgentConstraints struct {
	MaxIterations  int `yaml:"maxIterations" json:"max_iterations,omitempty"`
	MaxTimeSeconds int `yaml:"maxTimeSeconds" json:"max_time_seconds,omitempty"`
	RetryBudget    int `yaml:"retryBudget" json:"retry_budget,omitempty"`
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
	return def, nil
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
