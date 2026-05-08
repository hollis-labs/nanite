package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
)

// BuildCLAUDEMD returns the CLAUDE.md body planted in claude-provider boot
// dirs and refreshed on slot change in Phase 4c. Mirrors the existing
// adapter-claude implementation verbatim; the duplicate at
// internal/plugin/builtin/adapter-claude/plugin.go is removed in Phase 4c.
func BuildCLAUDEMD(agentName, agentDescription string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Nanite Agent — %s\n\n", agentName)
	if agentDescription != "" {
		fmt.Fprintf(&b, "%s\n\n", agentDescription)
	}

	b.WriteString(claudeMDBody)
	return b.String()
}

// claudeMDBody is the CLAUDE.md addendum (rules + reference pointers).
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

// BuildAgentContext returns the .sandbox/agent-context.md body for the given
// profile and (optional) mode. Exported so the chat service can regenerate
// the file on slot change without re-reaching into the agent package
// internals (Phase 4c slot-regeneration).
func BuildAgentContext(ap *store.AgentProfile, mode *store.AgentMode) string {
	var b strings.Builder

	if ap == nil {
		b.WriteString("# Agent: (unknown)\n\n")
		return b.String()
	}

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

	if mode != nil && mode.Name != "" {
		fmt.Fprintf(&b, "## Current Mode: %s\n\n", mode.Name)
		if mode.PromptAddendum != "" {
			fmt.Fprintf(&b, "%s\n\n", mode.PromptAddendum)
		}
	}

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

	if ap.ToolPermissions != "" && ap.ToolPermissions != "{}" {
		fmt.Fprintf(&b, "## Tool Permissions\n\n%s\n\n", ap.ToolPermissions)
	}

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
