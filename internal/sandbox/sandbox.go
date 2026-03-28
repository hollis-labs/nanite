package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/conduit/internal/store"
)

const baseDirName = ".conduit/sandboxes"
const sandboxSubDir = ".sandbox"

// Dir returns the sandbox directory path for a session, creating it and
// the .sandbox/ subdirectory if needed.
func Dir(sessionID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("sandbox: resolve home dir: %w", err)
	}
	dir := filepath.Join(home, baseDirName, sessionID)
	subDir := filepath.Join(dir, sandboxSubDir)
	if err := os.MkdirAll(subDir, 0755); err != nil {
		return "", fmt.Errorf("sandbox: create dir: %w", err)
	}
	return dir, nil
}

// PopulateOpts contains optional parameters for sandbox population.
type PopulateOpts struct {
	SessionID string // Conduit session ID (for MCP server args)
	DBPath    string // Absolute path to Conduit's SQLite database
}

// Populate writes all sandbox files: a compact CLAUDE.md with rules and
// pointers, detailed reference files in .sandbox/, and .mcp.json for
// MCP server discovery.
func Populate(dir string, agent *store.AgentProfile, mode *store.AgentMode, opts PopulateOpts) error {
	subDir := filepath.Join(dir, sandboxSubDir)

	// Write .sandbox/ reference files first (CLAUDE.md points to these).
	if err := writeFile(subDir, "envelope-schema.md", envelopeSchemaContent); err != nil {
		return err
	}
	if err := writeFile(subDir, "agent-context.md", buildAgentContext(agent, mode)); err != nil {
		return err
	}

	// Write the compact CLAUDE.md (rules + pointers).
	if err := writeFile(dir, "CLAUDE.md", buildCLAUDEMD(agent.Name, agent.Description)); err != nil {
		return err
	}

	// Write .mcp.json so the CLI discovers Conduit's MCP server.
	if opts.DBPath != "" {
		if err := writeMCPJSON(dir, opts); err != nil {
			return err
		}
	}

	return nil
}

// writeMCPJSON generates a .mcp.json pointing the CLI at the conduit mcp
// subcommand with session-specific arguments.
func writeMCPJSON(dir string, opts PopulateOpts) error {
	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("sandbox: resolve executable path: %w", err)
	}
	// Resolve symlinks to get the actual binary path.
	binPath, err = filepath.EvalSymlinks(binPath)
	if err != nil {
		return fmt.Errorf("sandbox: eval symlinks: %w", err)
	}

	args := []string{"mcp", "--db", opts.DBPath}
	if opts.SessionID != "" {
		args = append(args, "--session", opts.SessionID)
	}

	mcpConfig := map[string]any{
		"mcpServers": map[string]any{
			"conduit": map[string]any{
				"command": binPath,
				"args":    args,
				"env":     map[string]any{},
			},
		},
	}

	data, err := json.MarshalIndent(mcpConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("sandbox: marshal .mcp.json: %w", err)
	}

	return writeFile(dir, ".mcp.json", string(data))
}

func writeFile(dir, name, content string) error {
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("sandbox: write %s: %w", name, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// CLAUDE.md — compact rules + pointers (auto-read by CLI, survives compaction)
// ---------------------------------------------------------------------------

func buildCLAUDEMD(agentName, agentDescription string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Conduit Agent — %s\n\n", agentName)
	if agentDescription != "" {
		fmt.Fprintf(&b, "%s\n\n", agentDescription)
	}

	b.WriteString(claudeMDBody)
	return b.String()
}

const claudeMDBody = `## Envelope Format

Emit structured envelopes as fenced code blocks with the ` + "`conduit-envelope`" + ` language tag.
ALWAYS set "version": 1. NEVER invent envelope types — only use registered types.

Registered types: task-disposition, giphy-modal, document-viewer, report-card,
task-complete-notification, sprint-planning-review, kb-result,
ticket-confirmation, ticket-form, resolution-capture, error-report

Unregistered types are silently dropped by the UI — no error, no warning.

For full schema, field reference, and examples per type, read ` + "`.sandbox/envelope-schema.md`" + `.

## Rules
1. ALWAYS use envelopes for data collection — never ask users to type structured data
2. Propose, don't just do — use envelope proposals for creates/modifications
3. Never fabricate references — look up IDs, sprint codes, task refs first
4. Persist what matters — write significant decisions to Cortex without being asked
5. You know your tools — use request_tools only when you need parameter schemas

## Reference Files
- ` + "`.sandbox/envelope-schema.md`" + ` — full envelope JSON schema, field docs, examples per type
- ` + "`.sandbox/agent-context.md`" + ` — agent profile, current mode, capabilities
`

// ---------------------------------------------------------------------------
// .sandbox/envelope-schema.md — full envelope spec (loaded on demand by CLI)
// ---------------------------------------------------------------------------

const envelopeSchemaContent = `# Conduit Envelope Schema

## Format

Wrap envelopes in a fenced code block with the ` + "`conduit-envelope`" + ` language tag.

There are two envelope patterns:

### Interactive envelopes (user input)
` + "```" + `conduit-envelope
{
  "kind": "question|action|approval",
  "version": 1,
  "type": "conduit",
  "questions": [...],
  "proposals": [...],
  "approval": {...},
  "notes": "Brief explanation"
}
` + "```" + `

### Plugin/display envelopes (rich UI cards)
` + "```" + `conduit-envelope
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
| type | string | yes | "conduit" for interactive envelopes; a registered type name for display envelopes |
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
| task-disposition | Task status change proposal |
| giphy-modal | Giphy search/selection |
| document-viewer | Document display card |
| report-card | Summary/report display |
| task-complete-notification | Task completion notice |
| sprint-planning-review | Sprint plan review card |
| kb-result | Knowledge base search result |
| ticket-confirmation | Support ticket confirmation |
| ticket-form | Support ticket input form |
| resolution-capture | Issue resolution capture |

## Examples

### Question envelope
` + "```" + `conduit-envelope
{
  "kind": "question",
  "version": 1,
  "type": "conduit",
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
` + "```" + `conduit-envelope
{
  "kind": "action",
  "version": 1,
  "type": "conduit",
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
` + "```" + `conduit-envelope
{
  "kind": "approval",
  "version": 1,
  "type": "conduit",
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

// ---------------------------------------------------------------------------
// .sandbox/agent-context.md — agent profile + mode (loaded on demand by CLI)
// ---------------------------------------------------------------------------

func buildAgentContext(agent *store.AgentProfile, mode *store.AgentMode) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Agent: %s\n\n", agent.Name)
	fmt.Fprintf(&b, "**ID:** %s\n", agent.ID)
	if agent.Description != "" {
		fmt.Fprintf(&b, "**Description:** %s\n", agent.Description)
	}
	fmt.Fprintf(&b, "**Can Execute:** %v\n", agent.CanExecute)
	if agent.DefaultModel != "" {
		fmt.Fprintf(&b, "**Default Model:** %s\n", agent.DefaultModel)
	}
	b.WriteString("\n")

	// Current mode.
	if mode != nil && mode.Name != "" {
		fmt.Fprintf(&b, "## Current Mode: %s\n\n", mode.Name)
		if mode.PromptAddendum != "" {
			fmt.Fprintf(&b, "%s\n\n", mode.PromptAddendum)
		}
	}

	// MCP servers (informational — the CLI won't connect to these directly
	// until Phase 2, but it's useful context for the agent).
	if agent.MCPServers != "" && agent.MCPServers != "[]" {
		var servers []string
		if err := json.Unmarshal([]byte(agent.MCPServers), &servers); err == nil && len(servers) > 0 {
			b.WriteString("## Connected Services\n\n")
			for _, s := range servers {
				fmt.Fprintf(&b, "- %s\n", s)
			}
			b.WriteString("\n")
		}
	}

	// Tool permissions.
	if agent.ToolPermissions != "" && agent.ToolPermissions != "{}" {
		fmt.Fprintf(&b, "## Tool Permissions\n\n%s\n\n", agent.ToolPermissions)
	}

	// Schema v2 fields.
	if agent.Tools != "" && agent.Tools != "[]" {
		var tools []string
		if err := json.Unmarshal([]byte(agent.Tools), &tools); err == nil && len(tools) > 0 {
			b.WriteString("## Allowed Tools\n\n")
			for _, t := range tools {
				fmt.Fprintf(&b, "- %s\n", t)
			}
			b.WriteString("\n")
		}
	}

	if agent.Directories != "" && agent.Directories != "[]" {
		var dirs []string
		if err := json.Unmarshal([]byte(agent.Directories), &dirs); err == nil && len(dirs) > 0 {
			b.WriteString("## Accessible Directories\n\n")
			for _, d := range dirs {
				fmt.Fprintf(&b, "- %s\n", d)
			}
			b.WriteString("\n")
		}
	}

	if agent.Tags != "" && agent.Tags != "[]" {
		var tags []string
		if err := json.Unmarshal([]byte(agent.Tags), &tags); err == nil && len(tags) > 0 {
			fmt.Fprintf(&b, "**Tags:** %s\n\n", strings.Join(tags, ", "))
		}
	}

	if agent.Constraints != "" && agent.Constraints != "{}" {
		b.WriteString("## Constraints\n\n")
		fmt.Fprintf(&b, "```json\n%s\n```\n\n", agent.Constraints)
	}

	return b.String()
}
