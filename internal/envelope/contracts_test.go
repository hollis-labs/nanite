package envelope

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"

	"github.com/hollis-labs/go-envelopes"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// knownTypes is the authoritative list of envelope types from the backend registry.
// This list must stay in sync with internal/chat/envelope.go registeredTypes.
var knownTypes = []string{
	// Core primitives
	"session-task",
	// G-4 — subagent spawn approval.
	"subagent-spawn-approval",
	"document-viewer",
	"report-card",
	"error-report",
	"approval-card",
	"proposal-card",
	// Phase 7 reusable primitives
	"info-card",
	"list-card",
	"metric-card",
	"progress-card",
	"confirmation-card",
	"table-card",
	"timeline-card",
	"diff-card",
	// C2 — compact downloadable-artifact card
	"artifact-mini",
	// Plugin: giphy
	"giphy-modal",
	// KB + ticket primitives
	"kb-result",
	"ticket-form",
	"ticket-confirmation",
	"resolution-capture",
	// CW-20260417-0485 — chat-loop terminal pause envelope.
	"chat-loop-terminated",
	// CW-20260504-0001 — chat-loop soft budget warning (max_turns crossed).
	"chat-loop-budget-soft-warning",
	// CW-20260420-0018 — MCP elicitation/create mid-tool user prompt.
	"elicitation-prompt",
}

// examplePayloads provides a valid payload for each envelope type.
var examplePayloads = map[string]string{
	"session-task": `{
		"task_id": "task-001",
		"title": "Implement feature X",
		"status": "in_progress",
		"description": "Working on the main implementation."
	}`,
	"document-viewer": `{
		"title": "Architecture Overview",
		"content": "# Overview\nThis document describes...",
		"format": "markdown",
		"sections": ["Introduction", "Design"],
		"download_filename": "architecture.md",
		"download_enabled": true
	}`,
	"report-card": `{
		"title": "Sprint Report",
		"generated_at": "2026-04-05T10:00:00Z",
		"metrics": [
			{"label": "Tasks Done", "value": "12", "percent": 80, "color": "emerald"},
			{"label": "Velocity", "value": "24pts", "color": "blue"}
		],
		"summary": "Good progress this sprint.",
		"actions": [{"label": "View Details", "action": "show_details", "id": "sprint-1"}]
	}`,
	"error-report": `{
		"code": "provider_error",
		"message": "Rate limit exceeded",
		"details": {"raw": "429 Too Many Requests"},
		"giphy_query": "computer frustration",
		"timestamp": "2026-04-05T10:00:00Z"
	}`,
	"approval-card": `{
		"description": "Delete production database backup",
		"risk_level": "high",
		"details": "This action cannot be undone."
	}`,
	"proposal-card": `{
		"type": "create_task",
		"payload": {"title": "New task", "priority": "P2"},
		"schema": {
			"title": {"type": "text", "label": "Title", "required": true},
			"priority": {"type": "select", "label": "Priority", "options": ["P1", "P2", "P3"]}
		}
	}`,
	"info-card": `{
		"title": "Deployment Complete",
		"body": "The new version has been deployed to production.",
		"variant": "success"
	}`,
	"list-card": `{
		"title": "Next Steps",
		"items": [
			{"label": "Review changes", "description": "Check the diff"},
			{"label": "Run tests", "action": {"label": "Run", "type": "run_tests"}}
		],
		"ordered": true
	}`,
	"metric-card": `{
		"label": "Response Time",
		"value": "142",
		"unit": "ms",
		"trend": "down",
		"previous": "198",
		"description": "P95 latency over the last hour."
	}`,
	"progress-card": `{
		"title": "Migration Progress",
		"progress": 65,
		"status": "Migrating tables...",
		"description": "Step 3 of 5",
		"steps": [
			{"label": "Schema backup", "done": true},
			{"label": "Table migration", "done": true},
			{"label": "Data migration", "done": false}
		]
	}`,
	"confirmation-card": `{
		"title": "Delete workspace?",
		"message": "All projects and data in this workspace will be permanently deleted.",
		"confirm_label": "Delete",
		"cancel_label": "Keep",
		"risk": "high"
	}`,
	"table-card": `{
		"title": "Task Summary",
		"columns": [
			{"key": "id", "label": "ID", "sortable": true},
			{"key": "title", "label": "Title"},
			{"key": "status", "label": "Status", "sortable": true}
		],
		"rows": [
			{"id": "T-1", "title": "Fix bug", "status": "done"},
			{"id": "T-2", "title": "Add tests", "status": "in_progress"}
		],
		"caption": "2 tasks total"
	}`,
	"timeline-card": `{
		"title": "Deployment Timeline",
		"events": [
			{"timestamp": "2026-04-05T09:00:00Z", "label": "Build started", "status": "completed"},
			{"timestamp": "2026-04-05T09:05:00Z", "label": "Tests passed", "status": "completed"},
			{"timestamp": "2026-04-05T09:10:00Z", "label": "Deploying", "status": "active"}
		]
	}`,
	"diff-card": `{
		"title": "Config Change",
		"before": {"label": "Previous", "content": "timeout: 30s"},
		"after": {"label": "Updated", "content": "timeout: 60s"},
		"format": "code"
	}`,
	"artifact-mini": `{
		"artifact_id": "art-001",
		"name": "report.zip",
		"mime_type": "application/zip",
		"size_bytes": 1024,
		"origin": "auto"
	}`,
	"giphy-modal": `{
		"title": "Great Job!",
		"gif_url": "https://media.giphy.com/media/example/giphy.gif",
		"source": "GIPHY",
		"query": "celebration"
	}`,
	"kb-result": `{
		"results": [
			{
				"id": "KB-001",
				"title": "VPN Connection Drops",
				"category": "networking",
				"severity": "medium",
				"tags": ["vpn", "connectivity"],
				"body": "Try resetting the VPN client...",
				"source": "helix"
			}
		],
		"query": "vpn keeps disconnecting",
		"total_results": 1
	}`,
	"ticket-form": `{
		"prefilled": {
			"title": "VPN issue",
			"category": "networking",
			"priority": "medium"
		},
		"categories": ["networking", "hardware", "software", "access"]
	}`,
	"ticket-confirmation": `{
		"ticket": {
			"id": "TKT-2026-0042",
			"title": "VPN drops repeatedly",
			"description": "VPN disconnects every 15 minutes.",
			"category": "networking",
			"priority": "medium",
			"status": "open",
			"requester": "user@example.com",
			"routing": "IT Service Desk — Network Team",
			"created_at": "2026-04-05T10:30:00Z"
		}
	}`,
	"resolution-capture": `{
		"ticket_id": "TKT-2026-0042",
		"issue_summary": "VPN drops repeatedly during video calls",
		"categories": ["networking", "hardware", "software", "access"]
	}`,
	"subagent-spawn-approval": `{"run_id":"r-1","role":"file-backend","prompt":"summarize messaging","mode":"interactive"}`,
	"chat-loop-terminated": `{
		"reason": "hard circuit-breaker tripped: 10 consecutive tool failures",
		"code": "runaway_tool_failures",
		"iteration": 10,
		"consecutive_failures": 10,
		"last_error": "ARG_VALIDATION_FAILED: /limit: got string, want number",
		"last_tool": "list_tasks",
		"timestamp": "2026-04-17T17:48:44Z"
	}`,
	"chat-loop-budget-soft-warning": `{
		"max_turns": 10,
		"iteration": 10,
		"reason": "iteration crossed soft max_turns budget; agent continuing",
		"timestamp": "2026-05-04T00:50:00Z"
	}`,
	"elicitation-prompt": `{
		"elicitation_id": "e1c2d3a4-0000-0000-0000-000000000001",
		"message": "Are you sure you want to send this directive to all agents?",
		"schema_type": "boolean",
		"schema_title": "Confirm directive broadcast",
		"schema_description": "This will send a directive message to every connected agent in the session.",
		"tool_call_id": "toolu_01XY",
		"origin": "server",
		"timeout_at": "2026-04-27T10:05:00Z"
	}`,
}

// invalidPayloads provides a payload expected to fail validation for each envelope type.
var invalidPayloads = map[string]string{
	// G-4 — subagent spawn approval: missing required fields role/prompt/mode.
	"subagent-spawn-approval": `{"run_id":"r-1"}`,
}

// loadSchemaFiles returns all schema files from the go-envelopes lib's
// embedded manifest. Pre-Cap-5 this read from a local embed.FS that has
// since been removed; the lib is now the single source of truth.
func loadSchemaFiles(t *testing.T) map[string][]byte {
	t.Helper()
	schemas := make(map[string][]byte)
	libFS := envelopes.EmbeddedFS()
	entries, err := fs.ReadDir(libFS, "manifest/schemas")
	if err != nil {
		t.Fatalf("read manifest/schemas dir: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".schema.json") {
			continue
		}
		data, err := fs.ReadFile(libFS, "manifest/schemas/"+entry.Name())
		if err != nil {
			t.Fatalf("read schema %s: %v", entry.Name(), err)
		}
		// Extract type from filename: "info-card.schema.json" -> "info-card"
		typeName := strings.TrimSuffix(entry.Name(), ".schema.json")
		schemas[typeName] = data
	}
	return schemas
}

func TestEnvelopeSchemas_AllTypesHaveSchemas(t *testing.T) {
	schemas := loadSchemaFiles(t)

	for _, typeName := range knownTypes {
		if _, ok := schemas[typeName]; !ok {
			t.Errorf("envelope type %q has no schema file (expected schemas/%s.schema.json)", typeName, typeName)
		}
	}

	// Also check reverse: no orphan schemas without a known type.
	knownSet := make(map[string]bool, len(knownTypes))
	for _, typeName := range knownTypes {
		knownSet[typeName] = true
	}
	for typeName := range schemas {
		if !knownSet[typeName] {
			t.Errorf("schema file %s.schema.json exists but type %q is not in knownTypes", typeName, typeName)
		}
	}
}

func TestEnvelopeSchemas_ValidJSON(t *testing.T) {
	schemas := loadSchemaFiles(t)

	for typeName, data := range schemas {
		t.Run(typeName, func(t *testing.T) {
			// Verify it's valid JSON.
			var parsed map[string]any
			if err := json.Unmarshal(data, &parsed); err != nil {
				t.Fatalf("schema is not valid JSON: %v", err)
			}

			// Verify required JSON Schema fields.
			if _, ok := parsed["$id"]; !ok {
				t.Error("schema missing $id field")
			}
			if _, ok := parsed["title"]; !ok {
				t.Error("schema missing title field")
			}
			if _, ok := parsed["type"]; !ok {
				t.Error("schema missing type field")
			}

			// Verify it compiles as a valid JSON Schema.
			compiler := jsonschema.NewCompiler()
			if err := compiler.AddResource(typeName+".schema.json", unmarshalAny(t, data)); err != nil {
				t.Fatalf("add resource: %v", err)
			}
			if _, err := compiler.Compile(typeName + ".schema.json"); err != nil {
				t.Fatalf("schema does not compile: %v", err)
			}
		})
	}
}

func TestEnvelopeSchemas_ExamplePayloads(t *testing.T) {
	schemas := loadSchemaFiles(t)

	for typeName, schemaData := range schemas {
		t.Run(typeName, func(t *testing.T) {
			exampleJSON, ok := examplePayloads[typeName]
			if !ok {
				t.Skipf("no example payload for %q (add one to examplePayloads)", typeName)
			}

			// Compile schema.
			compiler := jsonschema.NewCompiler()
			if err := compiler.AddResource(typeName+".schema.json", unmarshalAny(t, schemaData)); err != nil {
				t.Fatalf("add resource: %v", err)
			}
			schema, err := compiler.Compile(typeName + ".schema.json")
			if err != nil {
				t.Fatalf("compile schema: %v", err)
			}

			// Parse and validate example payload.
			var payload any
			if err := json.Unmarshal([]byte(exampleJSON), &payload); err != nil {
				t.Fatalf("example payload is not valid JSON: %v", err)
			}

			if err := schema.Validate(payload); err != nil {
				t.Errorf("example payload does not validate:\n%v", err)
			}
		})
	}
}

func TestEnvelopeSchemas_InvalidPayloads(t *testing.T) {
	schemas := loadSchemaFiles(t)

	for typeName, badJSON := range invalidPayloads {
		t.Run(typeName, func(t *testing.T) {
			schemaData, ok := schemas[typeName]
			if !ok {
				t.Fatalf("no schema file for %q", typeName)
			}

			compiler := jsonschema.NewCompiler()
			if err := compiler.AddResource(typeName+".schema.json", unmarshalAny(t, schemaData)); err != nil {
				t.Fatalf("add resource: %v", err)
			}
			schema, err := compiler.Compile(typeName + ".schema.json")
			if err != nil {
				t.Fatalf("compile schema: %v", err)
			}

			var payload any
			if err := json.Unmarshal([]byte(badJSON), &payload); err != nil {
				t.Fatalf("invalid payload is not valid JSON: %v", err)
			}

			if err := schema.Validate(payload); err == nil {
				t.Errorf("expected validation error for invalid payload, got none")
			}
		})
	}
}

// unmarshalAny unmarshals JSON bytes into an any value for the jsonschema compiler.
func unmarshalAny(t *testing.T, data []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return v
}
