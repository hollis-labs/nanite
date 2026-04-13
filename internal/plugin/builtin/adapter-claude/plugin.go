// Package adapterclaude implements the Claude Code adapter plugin.
// It discovers agents from .claude/agents/*.md, populates sandboxes with
// CLAUDE.md, envelope schema, agent context, and MCP config, and syncs
// the project-root CLAUDE.md with a managed section listing available agents.
package adapterclaude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/fsutil"
	hostplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/store"

	plugin "github.com/hollis-labs/go-plugin"
)

func init() {
	hostplugin.RegisterPlugin("adapter-claude", func() plugin.Plugin { return New() })
}

// ---------------------------------------------------------------------------
// Plugin (implements go-plugin.Plugin)
// ---------------------------------------------------------------------------

// Plugin is the Claude Code adapter plugin.
type Plugin struct {
	host    plugin.Host
	status  plugin.PluginStatus
	adapter *Adapter
}

// New creates a new Claude Code adapter plugin instance.
func New() *Plugin {
	p := &Plugin{}
	p.adapter = &Adapter{plugin: p}
	return p
}

// Adapter returns the CLIAgentAdapter for this plugin.
func (p *Plugin) Adapter() *Adapter { return p.adapter }

func (p *Plugin) ID() string             { return "adapter-claude" }
func (p *Plugin) Name() string           { return "Claude Code Adapter" }
func (p *Plugin) Version() string        { return "0.1.0" }
func (p *Plugin) Description() string    { return "Discovers .claude/agents/*.md and populates Claude Code sandboxes" }
func (p *Plugin) Dependencies() []string { return nil }

func (p *Plugin) Load(host plugin.Host) error {
	p.host = host
	p.status = plugin.PluginStatus{
		Loaded:   true,
		Enabled:  true,
		LoadedAt: time.Now(),
	}
	host.Logger().Info("adapter-claude: loaded")
	return nil
}

func (p *Plugin) Unload() error {
	p.status.Loaded = false
	p.status.Enabled = false
	if p.host != nil {
		p.host.Logger().Info("adapter-claude: unloaded")
	}
	return nil
}

func (p *Plugin) Status() plugin.PluginStatus {
	return p.status
}

// ---------------------------------------------------------------------------
// Adapter (implements agent.CLIAgentAdapter)
// ---------------------------------------------------------------------------

// Adapter implements agent.CLIAgentAdapter for Claude Code.
type Adapter struct {
	plugin *Plugin
}

// Compile-time interface check.
var _ agent.CLIAgentAdapter = (*Adapter)(nil)

// Name returns the adapter identifier.
func (a *Adapter) Name() string { return "claude" }

// Priority returns the discovery order. Lower = checked first.
func (a *Adapter) Priority() int { return 60 }

// Discover scans {projectDir}/.claude/agents/*.md and returns normalized Definitions.
func (a *Adapter) Discover(projectDir string) ([]agent.Definition, error) {
	agentsDir := filepath.Join(projectDir, ".claude", "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("adapter-claude: read agents dir: %w", err)
	}

	var defs []agent.Definition
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		path := filepath.Join(agentsDir, entry.Name())
		def, err := agent.ParseMDFile(path)
		if err != nil {
			// Skip unparseable files rather than failing the whole discovery.
			continue
		}

		def.Source = "claude"
		defs = append(defs, *def)
	}

	return defs, nil
}

// PopulateSandbox writes Claude Code sandbox files into sandboxDir.
func (a *Adapter) PopulateSandbox(sandboxDir string, ap store.AgentProfile, session agent.SandboxContext) error {
	subDir := filepath.Join(sandboxDir, ".sandbox")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		return fmt.Errorf("adapter-claude: create .sandbox/: %w", err)
	}

	// Write .sandbox/envelope-schema.md — full envelope spec.
	if err := writeFile(subDir, "envelope-schema.md", envelopeSchemaContent); err != nil {
		return err
	}

	// Write .sandbox/agent-context.md — agent profile context (no mode).
	if err := writeFile(subDir, "agent-context.md", buildAgentContext(&ap, nil)); err != nil {
		return err
	}

	// Write CLAUDE.md — compact rules + pointers.
	if err := writeFile(sandboxDir, "CLAUDE.md", buildCLAUDEMD(ap.Name, ap.Description)); err != nil {
		return err
	}

	// Write .mcp.json so the CLI discovers Nanite's MCP server.
	if session.DBPath != "" {
		if err := writeMCPJSON(sandboxDir, session); err != nil {
			return err
		}
	}

	return nil
}

// SyncProjectRoot writes a managed section into {projectDir}/CLAUDE.md
// listing available Nanite agents. If no agents are configured, writes
// a placeholder section pointing to NANITE.md for setup help.
func (a *Adapter) SyncProjectRoot(projectDir string, agents []store.AgentProfile) error {
	var content string
	if len(agents) == 0 {
		content = placeholderContent
	} else {
		var b strings.Builder
		b.WriteString("Available Nanite agents:\n\n")
		for _, ap := range agents {
			if ap.Description != "" {
				fmt.Fprintf(&b, "- **%s** — %s\n", ap.Name, ap.Description)
			} else {
				fmt.Fprintf(&b, "- **%s**\n", ap.Name)
			}
		}
		content = b.String()
	}

	claudePath := filepath.Join(projectDir, "CLAUDE.md")
	return agent.WriteManagedSection(claudePath, content)
}

const placeholderContent = `## Nanite Agents

No agents configured for this project yet. See NANITE.md for setup help, or add an agent definition to ` + "`.nanite/config.yaml`" + ` and re-run ` + "`nanite-agent init --project .`" + `.`

// ---------------------------------------------------------------------------
// Content generation (moved from sandbox.go)
// ---------------------------------------------------------------------------

func writeFile(dir, name, content string) error {
	path := filepath.Join(dir, name)
	if err := fsutil.AtomicWriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("adapter-claude: write %s: %w", name, err)
	}
	return nil
}

func buildCLAUDEMD(agentName, agentDescription string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Nanite Agent — %s\n\n", agentName)
	if agentDescription != "" {
		fmt.Fprintf(&b, "%s\n\n", agentDescription)
	}

	b.WriteString(claudeMDBody)
	return b.String()
}

const claudeMDBody = `## Envelope Format

Emit structured envelopes as fenced code blocks with the ` + "`nanite-envelope`" + ` language tag.
ALWAYS set "version": 1. NEVER invent envelope types — only use registered types.

For the current registered envelope types and per-type schemas, consult
` + "`config/envelopes.yaml`" + ` and ` + "`internal/envelope/schemas/*.schema.json`" + ` —
those are the runtime source of truth. Unregistered types are silently dropped
by the UI — no error, no warning.

## Rules
1. ALWAYS use envelopes for data collection — never ask users to type structured data
2. Propose, don't just do — use envelope proposals for creates/modifications
3. Never fabricate references — look up IDs, sprint codes, task refs first
4. Persist what matters — write significant decisions to Vanta Conduit without being asked
5. You know your tools — use request_tools only when you need parameter schemas

## Reference Files
- ` + "`.sandbox/envelope-schema.md`" + ` — full envelope JSON schema, field docs, examples per type
- ` + "`.sandbox/agent-context.md`" + ` — agent profile, current mode, capabilities
`

const envelopeSchemaContent = `# Nanite Envelope Schema

## Format

Wrap envelopes in a fenced code block with the ` + "`nanite-envelope`" + ` language tag.

There are two envelope patterns:

### Interactive envelopes (user input)
` + "```" + `nanite-envelope
{
  "kind": "question|action|approval",
  "version": 1,
  "type": "nanite",
  "questions": [...],
  "proposals": [...],
  "approval": {...},
  "notes": "Brief explanation"
}
` + "```" + `

### Plugin/display envelopes (rich UI cards)
` + "```" + `nanite-envelope
{
  "kind": "envelope",
  "version": 1,
  "type": "<registered-type>",
  "data": { ... }
}
` + "```" + `

Use kind="envelope" with a registered type for display cards (kb-result, giphy-modal, report-card, etc.). Use kind="question"/"action"/"approval" for interactive forms and proposals.

## Field Reference

### Root Fields
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| kind | string | yes | "question", "action", "approval", or "envelope" |
| version | number | yes | Always 1 |
| type | string | yes | "nanite" for interactive envelopes; a registered type name for display envelopes |
| data | object | conditional | Required when kind="envelope" — card-specific payload |
| questions | array | conditional | Required when kind="question" |
| proposals | array | conditional | Required when kind="action" |
| approval | object | conditional | Required when kind="approval" |
| notes | string | no | Brief explanation shown to user |

### Question Object
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| prompt | string | yes | The question text |
| type | string | yes | "text", "textarea", "select", "radio", "checkbox" |
| options | array | conditional | Required for select, radio, checkbox |
| required | boolean | no | Whether the field must be filled |
| default | string | no | Default value |

### Proposal Object
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Action type (e.g. "create_task", "update_sprint") |
| payload | object | yes | The data to create/modify |
| schema | object | no | Editable field definitions for user review |

### Approval Object
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| action | string | yes | What will happen if approved |
| risk | string | yes | "low", "medium", or "high" |
| details | object | no | Additional context for the user |

## Registered Envelope Types

These are the only types the frontend can render. Using any other type
causes the envelope to be silently dropped — no error, no warning.

| Type | Purpose |
|------|---------|
| giphy-modal | Giphy search/selection |
| document-viewer | Document display card |
| report-card | Summary/report display |
| kb-result | Knowledge base search result |
| ticket-confirmation | Support ticket confirmation |
| ticket-form | Support ticket input form |
| resolution-capture | Issue resolution capture |

## Examples

### Question envelope
` + "```" + `nanite-envelope
{
  "kind": "question",
  "version": 1,
  "type": "nanite",
  "questions": [
    {
      "prompt": "What priority should this task have?",
      "type": "select",
      "options": ["low", "medium", "high", "critical"],
      "required": true,
      "default": "medium"
    }
  ],
  "notes": "Setting priority for the new task"
}
` + "```" + `

### Action envelope (proposal)
` + "```" + `nanite-envelope
{
  "kind": "action",
  "version": 1,
  "type": "nanite",
  "proposals": [
    {
      "type": "create_task",
      "payload": {
        "title": "Fix login redirect bug",
        "project_id": "proj-123",
        "priority": "high"
      },
      "schema": {
        "title": {"type": "text", "editable": true},
        "priority": {"type": "select", "options": ["low", "medium", "high"], "editable": true}
      }
    }
  ],
  "notes": "Review and approve the new task"
}
` + "```" + `

### Approval envelope
` + "```" + `nanite-envelope
{
  "kind": "approval",
  "version": 1,
  "type": "nanite",
  "approval": {
    "action": "Archive 12 completed tasks from Sprint 4",
    "risk": "medium",
    "details": {
      "task_count": 12,
      "sprint": "Sprint 4"
    }
  },
  "notes": "This will archive all completed tasks and remove them from active views"
}
` + "```" + `
`

func buildAgentContext(ap *store.AgentProfile, mode *store.AgentMode) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Agent: %s\n\n", ap.Name)
	fmt.Fprintf(&b, "**ID:** %s\n", ap.ID)
	if ap.Description != "" {
		fmt.Fprintf(&b, "**Description:** %s\n", ap.Description)
	}
	fmt.Fprintf(&b, "**Can Execute:** %v\n", ap.CanExecute)
	if ap.DefaultModel != "" {
		fmt.Fprintf(&b, "**Default Model:** %s\n", ap.DefaultModel)
	}
	b.WriteString("\n")

	// Current mode.
	if mode != nil && mode.Name != "" {
		fmt.Fprintf(&b, "## Current Mode: %s\n\n", mode.Name)
		if mode.PromptAddendum != "" {
			fmt.Fprintf(&b, "%s\n\n", mode.PromptAddendum)
		}
	}

	// MCP servers (informational).
	if ap.MCPServers != "" && ap.MCPServers != "[]" {
		var servers []string
		if err := json.Unmarshal([]byte(ap.MCPServers), &servers); err == nil && len(servers) > 0 {
			b.WriteString("## Connected Services\n\n")
			for _, s := range servers {
				fmt.Fprintf(&b, "- %s\n", s)
			}
			b.WriteString("\n")
		}
	}

	// Tool permissions.
	if ap.ToolPermissions != "" && ap.ToolPermissions != "{}" {
		fmt.Fprintf(&b, "## Tool Permissions\n\n%s\n\n", ap.ToolPermissions)
	}

	// Schema v2 fields.
	if ap.Tools != "" && ap.Tools != "[]" {
		var tools []string
		if err := json.Unmarshal([]byte(ap.Tools), &tools); err == nil && len(tools) > 0 {
			b.WriteString("## Allowed Tools\n\n")
			for _, t := range tools {
				fmt.Fprintf(&b, "- %s\n", t)
			}
			b.WriteString("\n")
		}
	}

	if ap.Directories != "" && ap.Directories != "[]" {
		var dirs []string
		if err := json.Unmarshal([]byte(ap.Directories), &dirs); err == nil && len(dirs) > 0 {
			b.WriteString("## Accessible Directories\n\n")
			for _, d := range dirs {
				fmt.Fprintf(&b, "- %s\n", d)
			}
			b.WriteString("\n")
		}
	}

	if ap.Tags != "" && ap.Tags != "[]" {
		var tags []string
		if err := json.Unmarshal([]byte(ap.Tags), &tags); err == nil && len(tags) > 0 {
			fmt.Fprintf(&b, "**Tags:** %s\n\n", strings.Join(tags, ", "))
		}
	}

	if ap.Constraints != "" && ap.Constraints != "{}" {
		b.WriteString("## Constraints\n\n")
		fmt.Fprintf(&b, "```json\n%s\n```\n\n", ap.Constraints)
	}

	return b.String()
}

func writeMCPJSON(dir string, session agent.SandboxContext) error {
	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("adapter-claude: resolve executable path: %w", err)
	}
	binPath, err = filepath.EvalSymlinks(binPath)
	if err != nil {
		return fmt.Errorf("adapter-claude: eval symlinks: %w", err)
	}

	args := []string{"mcp", "--db", session.DBPath}
	if session.SessionID != "" {
		args = append(args, "--session", session.SessionID)
	}

	mcpConfig := map[string]any{
		"mcpServers": map[string]any{
			brand.ID: map[string]any{
				"command": binPath,
				"args":    args,
				"env":     map[string]any{},
			},
		},
	}

	data, err := json.MarshalIndent(mcpConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("adapter-claude: marshal .mcp.json: %w", err)
	}

	return writeFile(dir, ".mcp.json", string(data))
}
