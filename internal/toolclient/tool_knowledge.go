package toolclient

import (
	"fmt"
	"strings"
)

// ToolEntry describes a single tool with a short description and use cases.
// This is a knowledge record, not a schema — it tells the LLM what exists
// and when to reach for it, without burning tokens on full JSON schemas.
type ToolEntry struct {
	Name             string   `json:"name"`
	Category         string   `json:"category"`
	ShortDescription string   `json:"short_description"` // max 80 chars
	Server           string   `json:"server"`             // MCP server name (hadron, conduit, etc.)
	UseCases         []string `json:"use_cases"`          // 2-3 brief use-case phrases
}

// ToolKnowledge is a curated catalog of available tools organized by category.
type ToolKnowledge struct {
	Categories map[string][]ToolEntry `json:"categories"`
}

// ForIntent returns tool entries whose names, descriptions, or use cases
// match any keyword in the given intent string. Matching is case-insensitive.
func (tk *ToolKnowledge) ForIntent(intent string) []ToolEntry {
	keywords := strings.Fields(strings.ToLower(intent))
	if len(keywords) == 0 {
		return nil
	}

	var matches []ToolEntry
	seen := make(map[string]bool)

	for _, entries := range tk.Categories {
		for _, entry := range entries {
			if seen[entry.Name] {
				continue
			}
			if entryMatchesKeywords(entry, keywords) {
				matches = append(matches, entry)
				seen[entry.Name] = true
			}
		}
	}
	return matches
}

// entryMatchesKeywords returns true if any keyword appears in the entry's
// name, description, or use cases.
func entryMatchesKeywords(entry ToolEntry, keywords []string) bool {
	corpus := strings.ToLower(entry.Name + " " + entry.ShortDescription)
	for _, uc := range entry.UseCases {
		corpus += " " + strings.ToLower(uc)
	}
	for _, kw := range keywords {
		if strings.Contains(corpus, kw) {
			return true
		}
	}
	return false
}

// Summary returns a compact, human-readable text summary of the tool catalog
// suitable for inclusion in a system prompt. It lists category headers with
// tool names only — no schemas, no full descriptions.
func (tk *ToolKnowledge) Summary() string {
	// Fixed category order for deterministic output.
	order := []string{
		"project-management",
		"automation",
		"context",
		"developer",
		"general",
	}

	var sb strings.Builder
	sb.WriteString("Tool Catalog\n")

	for _, cat := range order {
		entries, ok := tk.Categories[cat]
		if !ok || len(entries) == 0 {
			continue
		}
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name
		}
		fmt.Fprintf(&sb, "\n[%s] %s\n", cat, strings.Join(names, ", "))
	}

	// Include any categories not in the predefined order.
	for cat, entries := range tk.Categories {
		if containsStr(order, cat) || len(entries) == 0 {
			continue
		}
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name
		}
		fmt.Fprintf(&sb, "\n[%s] %s\n", cat, strings.Join(names, ", "))
	}

	return sb.String()
}

// containsStr checks if a string slice contains a given string.
func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// DefaultToolKnowledge returns the curated tool catalog covering all known
// MCP servers (Hadron, Vanta Conduit) plus built-in developer and general tools.
func DefaultToolKnowledge() *ToolKnowledge {
	return &ToolKnowledge{
		Categories: map[string][]ToolEntry{
			// ── Automation & CI/CD (Hadron) ────────────────────────
			"automation": {
				{
					Name:             "hadron_run_enqueue",
					Category:         "automation",
					ShortDescription: "Enqueue a blueprint run for execution",
					Server:           "hadron",
					UseCases:         []string{"running a build", "triggering a pipeline", "executing automation"},
				},
				{
					Name:             "hadron_run_get",
					Category:         "automation",
					ShortDescription: "Get status and output of a run",
					Server:           "hadron",
					UseCases:         []string{"checking build status", "reading run output", "monitoring execution"},
				},
				{
					Name:             "hadron_runs_list",
					Category:         "automation",
					ShortDescription: "List recent runs with status",
					Server:           "hadron",
					UseCases:         []string{"viewing run history", "finding failed builds", "audit trail"},
				},
				{
					Name:             "hadron_blueprints_list",
					Category:         "automation",
					ShortDescription: "List available automation blueprints",
					Server:           "hadron",
					UseCases:         []string{"discovering available automations", "finding a build blueprint"},
				},
				{
					Name:             "hadron_blueprint_get",
					Category:         "automation",
					ShortDescription: "Get blueprint details and parameters",
					Server:           "hadron",
					UseCases:         []string{"understanding what a blueprint does", "checking required inputs"},
				},
				{
					Name:             "hadron_pipeline_enqueue",
					Category:         "automation",
					ShortDescription: "Enqueue a multi-stage pipeline",
					Server:           "hadron",
					UseCases:         []string{"running full CI/CD pipeline", "multi-step automation"},
				},
				{
					Name:             "hadron_schedule_create",
					Category:         "automation",
					ShortDescription: "Create a scheduled automation run",
					Server:           "hadron",
					UseCases:         []string{"setting up cron jobs", "scheduling nightly builds"},
				},
				{
					Name:             "hadron_schedules_list",
					Category:         "automation",
					ShortDescription: "List scheduled automations",
					Server:           "hadron",
					UseCases:         []string{"viewing scheduled jobs", "managing recurring runs"},
				},
				{
					Name:             "hadron_health",
					Category:         "automation",
					ShortDescription: "Check Hadron service health",
					Server:           "hadron",
					UseCases:         []string{"troubleshooting automation failures", "service status check"},
				},
			},

			// ── Context & Knowledge (Vanta Conduit) ───────────────────────
			"context": {
				{
					Name:             "context_write",
					Category:         "context",
					ShortDescription: "Write a context packet to the knowledge store",
					Server:           "conduit",
					UseCases:         []string{"saving information for later", "storing research results", "persisting knowledge"},
				},
				{
					Name:             "context_view",
					Category:         "context",
					ShortDescription: "Read a context packet by key",
					Server:           "conduit",
					UseCases:         []string{"retrieving stored knowledge", "looking up saved context"},
				},
				{
					Name:             "context_pack",
					Category:         "context",
					ShortDescription: "Bundle multiple context items into a pack",
					Server:           "conduit",
					UseCases:         []string{"organizing related context", "creating knowledge bundles"},
				},
				{
					Name:             "context_head",
					Category:         "context",
					ShortDescription: "Get the latest version of a context key",
					Server:           "conduit",
					UseCases:         []string{"checking current state", "reading latest value"},
				},
				{
					Name:             "context_history",
					Category:         "context",
					ShortDescription: "View version history for a context key",
					Server:           "conduit",
					UseCases:         []string{"tracking changes over time", "auditing context edits"},
				},
				{
					Name:             "context_namespaces_list",
					Category:         "context",
					ShortDescription: "List available context namespaces",
					Server:           "conduit",
					UseCases:         []string{"discovering knowledge areas", "browsing stored context"},
				},
				{
					Name:             "context_broker_plan",
					Category:         "context",
					ShortDescription: "Plan context assembly for a given intent",
					Server:           "conduit",
					UseCases:         []string{"preparing context for a task", "optimizing context selection"},
				},
				{
					Name:             "context_broker_fetch",
					Category:         "context",
					ShortDescription: "Fetch assembled context from a plan",
					Server:           "conduit",
					UseCases:         []string{"loading planned context", "retrieving curated knowledge"},
				},
				{
					Name:             "context_audit",
					Category:         "context",
					ShortDescription: "Audit context usage and freshness",
					Server:           "conduit",
					UseCases:         []string{"checking stale context", "knowledge maintenance"},
				},
				{
					Name:             "context_typed_write",
					Category:         "context",
					ShortDescription: "Write typed/structured context data",
					Server:           "conduit",
					UseCases:         []string{"storing structured data", "writing typed records"},
				},
				{
					Name:             "context_typed_view",
					Category:         "context",
					ShortDescription: "View typed/structured context data",
					Server:           "conduit",
					UseCases:         []string{"reading structured records", "querying typed data"},
				},
			},

			// ── Developer Tools ────────────────────────────────────
			"developer": {
				{
					Name:             "cerberus_start",
					Category:         "developer",
					ShortDescription: "Start a managed service via Cerberus",
					Server:           "cerberus",
					UseCases:         []string{"starting a dev service", "launching a local server"},
				},
				{
					Name:             "cerberus_stop",
					Category:         "developer",
					ShortDescription: "Stop a managed service",
					Server:           "cerberus",
					UseCases:         []string{"stopping a running service", "shutting down a process"},
				},
				{
					Name:             "cerberus_status",
					Category:         "developer",
					ShortDescription: "Check service status",
					Server:           "cerberus",
					UseCases:         []string{"is the service running?", "health check on local services"},
				},
				{
					Name:             "cerberus_logs",
					Category:         "developer",
					ShortDescription: "Tail logs from a managed service",
					Server:           "cerberus",
					UseCases:         []string{"debugging a service", "reading error output", "viewing recent logs"},
				},
				{
					Name:             "cerberus_build",
					Category:         "developer",
					ShortDescription: "Build a project via Cerberus",
					Server:           "cerberus",
					UseCases:         []string{"compiling code", "running a build step"},
				},
				{
					Name:             "cerberus_restart",
					Category:         "developer",
					ShortDescription: "Restart a managed service",
					Server:           "cerberus",
					UseCases:         []string{"applying config changes", "recovering from a crash"},
				},
				{
					Name:             "cerberus_health",
					Category:         "developer",
					ShortDescription: "Check Cerberus service manager health",
					Server:           "cerberus",
					UseCases:         []string{"verifying dev environment", "troubleshooting service manager"},
				},
			},

			// ── General / Utility ──────────────────────────────────
			"general": {
				{
					Name:             "hadron_workspace_get",
					Category:         "general",
					ShortDescription: "Get workspace configuration from Hadron",
					Server:           "hadron",
					UseCases:         []string{"checking workspace settings", "environment inspection"},
				},
				{
					Name:             "hadron_workspaces_list",
					Category:         "general",
					ShortDescription: "List available Hadron workspaces",
					Server:           "hadron",
					UseCases:         []string{"discovering workspaces", "multi-project overview"},
				},
				{
					Name:             "context_status_promote",
					Category:         "general",
					ShortDescription: "Promote context status (draft -> active)",
					Server:           "conduit",
					UseCases:         []string{"publishing draft context", "promoting knowledge to active"},
				},
				{
					Name:             "context_promote_request",
					Category:         "general",
					ShortDescription: "Request promotion of context with review",
					Server:           "conduit",
					UseCases:         []string{"requesting context review", "promotion workflow"},
				},
				// ── UI Triggers ─────────────────────────────────────
				{
					Name:             "sprint_planning_open",
					Category:         "general",
					ShortDescription: "Open sprint planning modal to review sprints and tasks",
					Server:           "self",
					UseCases:         []string{"sprint review", "plan work", "manage tasks", "backlog review", "current sprint"},
				},
				// ── Self-service & Builders ──────────────────────────
				{
					Name:             "builder_start",
					Category:         "general",
					ShortDescription: "Start interactive builder for agents, skills, or templates",
					Server:           "self",
					UseCases:         []string{"create agent step-by-step", "interactive form", "guided creation", "interview", "collect data"},
				},
				{
					Name:             "builder_step",
					Category:         "general",
					ShortDescription: "Submit a value for the current builder step",
					Server:           "self",
					UseCases:         []string{"builder flow", "step-by-step form", "guided input"},
				},
				{
					Name:             "skill_create",
					Category:         "general",
					ShortDescription: "Create a new skill binding tools to a category",
					Server:           "self",
					UseCases:         []string{"new skill", "tool binding", "skill creation"},
				},
				{
					Name:             "agent_create",
					Category:         "general",
					ShortDescription: "Create a new agent profile with system prompt",
					Server:           "self",
					UseCases:         []string{"new agent", "agent creation", "add persona"},
				},
				{
					Name:             "skill_list",
					Category:         "general",
					ShortDescription: "List all skills with name, slug, and category",
					Server:           "self",
					UseCases:         []string{"browse skills", "skill inventory"},
				},
				{
					Name:             "agent_list",
					Category:         "general",
					ShortDescription: "List all agent profiles",
					Server:           "self",
					UseCases:         []string{"browse agents", "agent inventory"},
				},
			},
		},
	}
}
