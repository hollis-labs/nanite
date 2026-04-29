package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/envelope"
)

// extractEnvelopeJSON pulls the JSON payload out of the
// <!--ENVELOPE_DATA:...:ENVELOPE_DATA--> marker emitted by nanite_show_card.
func extractEnvelopeJSON(t *testing.T, body string) map[string]any {
	t.Helper()
	const startTag = "<!--ENVELOPE_DATA:"
	const endTag = ":ENVELOPE_DATA-->"
	start := strings.Index(body, startTag)
	if start < 0 {
		t.Fatalf("envelope marker not found in body: %s", body)
	}
	tail := body[start+len(startTag):]
	end := strings.Index(tail, endTag)
	if end < 0 {
		t.Fatalf("envelope close marker not found in body: %s", body)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(tail[:end]), &env); err != nil {
		t.Fatalf("envelope JSON did not parse: %v (raw=%s)", err, tail[:end])
	}
	return env
}

// validShowCardPayloads pairs each v1 passive-renderable type with a payload
// the per-type schema accepts. These mirror examplePayloads in the envelope
// package's contract tests so the tool boundary and the schema test stay in
// agreement.
var validShowCardPayloads = map[string]map[string]any{
	"giphy-modal": {
		"title":   "Great Job!",
		"gif_url": "https://media.giphy.com/media/example/giphy.gif",
		"source":  "GIPHY",
		"query":   "celebration",
	},
	"document-viewer": {
		"title":             "Architecture Overview",
		"content":           "# Overview\nThis document describes...",
		"format":            "markdown",
		"sections":          []any{"Introduction", "Design"},
		"download_filename": "architecture.md",
		"download_enabled":  true,
	},
	"report-card": {
		"title":        "Sprint Report",
		"generated_at": "2026-04-05T10:00:00Z",
		"metrics": []any{
			map[string]any{"label": "Tasks Done", "value": "12", "percent": float64(80), "color": "emerald"},
			map[string]any{"label": "Velocity", "value": "24pts", "color": "blue"},
		},
		"summary": "Good progress this sprint.",
		"actions": []any{
			map[string]any{"label": "View Details", "action": "show_details", "id": "sprint-1"},
		},
	},
	"info-card": {
		"title":   "Deployment Complete",
		"body":    "The new version has been deployed to production.",
		"variant": "success",
	},
	"list-card": {
		"title": "Next Steps",
		"items": []any{
			map[string]any{"label": "Review changes", "description": "Check the diff"},
			map[string]any{"label": "Run tests", "action": map[string]any{"label": "Run", "type": "run_tests"}},
		},
		"ordered": true,
	},
	"metric-card": {
		"label":       "Response Time",
		"value":       "142",
		"unit":        "ms",
		"trend":       "down",
		"previous":    "198",
		"description": "P95 latency over the last hour.",
	},
	"progress-card": {
		"title":       "Migration Progress",
		"progress":    float64(65),
		"status":      "Migrating tables...",
		"description": "Step 3 of 5",
		"steps": []any{
			map[string]any{"label": "Schema backup", "done": true},
			map[string]any{"label": "Table migration", "done": true},
			map[string]any{"label": "Data migration", "done": false},
		},
	},
	"table-card": {
		"title": "Task Summary",
		"columns": []any{
			map[string]any{"key": "id", "label": "ID", "sortable": true},
			map[string]any{"key": "title", "label": "Title"},
			map[string]any{"key": "status", "label": "Status", "sortable": true},
		},
		"rows": []any{
			map[string]any{"id": "T-1", "title": "Fix bug", "status": "done"},
			map[string]any{"id": "T-2", "title": "Add tests", "status": "in_progress"},
		},
		"caption": "2 tasks total",
	},
	"timeline-card": {
		"title": "Deployment Timeline",
		"events": []any{
			map[string]any{"timestamp": "2026-04-05T09:00:00Z", "label": "Build started", "status": "completed"},
			map[string]any{"timestamp": "2026-04-05T09:05:00Z", "label": "Tests passed", "status": "completed"},
			map[string]any{"timestamp": "2026-04-05T09:10:00Z", "label": "Deploying", "status": "active"},
		},
	},
	"diff-card": {
		"title":  "Config Change",
		"before": map[string]any{"label": "Previous", "content": "timeout: 30s"},
		"after":  map[string]any{"label": "Updated", "content": "timeout: 60s"},
		"format": "code",
	},
	"artifact-mini": {
		"artifact_id": "art-001",
		"name":        "report.zip",
		"mime_type":   "application/zip",
		"size_bytes":  float64(1024),
		"origin":      "auto",
	},
}

// validSources covers the grounding gate enforced for prose-bearing cards.
const validSources = `[{"tool_use_id":"tu_test_1","tool_name":"clockwork_task_list"}]`

// TestCallShowCard_AllPassiveRenderables_RoundTrip is the table-driven
// regression for CW-20260428-0019 / A3: every type in the v1 allow-list must
// validate end-to-end and emit an envelope of the correct type.
func TestCallShowCard_AllPassiveRenderables_RoundTrip(t *testing.T) {
	st := newSelfTools(t)
	for _, envType := range envelope.PassiveRenderableTypes {
		envType := envType
		t.Run(envType, func(t *testing.T) {
			data, ok := validShowCardPayloads[envType]
			if !ok {
				t.Fatalf("missing valid payload for type %q (add one to validShowCardPayloads)", envType)
			}
			args := map[string]any{
				"type": envType,
				"data": cloneMap(data),
			}
			if envType == "report-card" || envType == "document-viewer" {
				args["sources"] = validSources
			}

			res, err := st.callShowCard(context.Background(), args)
			if err != nil {
				t.Fatalf("callShowCard returned error: %v", err)
			}
			if res.IsError {
				t.Fatalf("callShowCard returned error result for %q: %s", envType, readToolText(t, res))
			}
			env := extractEnvelopeJSON(t, readToolText(t, res))
			if got := env["type"]; got != envType {
				t.Fatalf("envelope type: want %q got %v", envType, got)
			}
			if env["kind"] != "envelope" {
				t.Fatalf("envelope kind: want envelope got %v", env["kind"])
			}
			if got := env["version"]; got != float64(1) {
				t.Fatalf("envelope version: want 1 got %v", got)
			}
			gotData, ok := env["data"].(map[string]any)
			if !ok {
				t.Fatalf("envelope.data is not an object: %T", env["data"])
			}
			// Sanity: a couple of required-by-schema fields survive into
			// the wire data so we know we're not silently dropping the
			// payload.
			for k := range data {
				if _, present := gotData[k]; !present {
					t.Errorf("envelope.data missing payload key %q", k)
				}
			}
		})
	}
}

// TestCallShowCard_RejectsTypeOutsideAllowList covers the four "not
// addressable through show_card" buckets called out in the ticket.
func TestCallShowCard_RejectsTypeOutsideAllowList(t *testing.T) {
	st := newSelfTools(t)
	rejected := []string{
		"approval-card",       // decision-flow
		"proposal-card",       // decision-flow
		"confirmation-card",   // decision-flow
		"question-form",       // decision-flow
		"chat-loop-terminated", // runtime-emitted
		"elicitation-prompt", // runtime-emitted
		"kb-result",           // plugin-shipped
		"ticket-form",         // plugin-shipped
		"made-up-type",        // unknown
	}
	for _, envType := range rejected {
		envType := envType
		t.Run(envType, func(t *testing.T) {
			res, err := st.callShowCard(context.Background(), map[string]any{
				"type": envType,
				"data": map[string]any{},
			})
			if err != nil {
				t.Fatalf("callShowCard returned transport error: %v", err)
			}
			if !res.IsError {
				t.Fatalf("expected error result for type %q, got success: %s", envType, readToolText(t, res))
			}
			body := readToolText(t, res)
			if !strings.Contains(body, "not addressable") {
				t.Errorf("error message should explain why %q is rejected, got: %s", envType, body)
			}
		})
	}
}

// TestCallShowCard_RejectsInvalidData asserts schema validation runs at the
// boundary for an in-list type, citing the missing required field.
func TestCallShowCard_RejectsInvalidData(t *testing.T) {
	st := newSelfTools(t)
	res, err := st.callShowCard(context.Background(), map[string]any{
		"type": "metric-card",
		"data": map[string]any{
			// metric-card requires `label` and `value`; provide only label.
			"label": "Latency",
		},
	})
	if err != nil {
		t.Fatalf("callShowCard returned transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected schema-validation error, got success: %s", readToolText(t, res))
	}
	body := readToolText(t, res)
	if !strings.Contains(body, "metric-card") {
		t.Errorf("error message should mention the type that failed: %s", body)
	}
	if !strings.Contains(body, "schema") {
		t.Errorf("error message should mention schema validation: %s", body)
	}
}

// TestCallShowCard_ReportCard_RequiresSources keeps the grounding gate
// inherited from the per-type tools.
func TestCallShowCard_ReportCard_RequiresSources(t *testing.T) {
	st := newSelfTools(t)
	args := map[string]any{
		"type": "report-card",
		"data": cloneMap(validShowCardPayloads["report-card"]),
		// No sources arg.
	}
	res, err := st.callShowCard(context.Background(), args)
	if err != nil {
		t.Fatalf("callShowCard returned transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected sources-required error for report-card, got: %s", readToolText(t, res))
	}
	if !strings.Contains(readToolText(t, res), "sources") {
		t.Errorf("error message should mention sources: %s", readToolText(t, res))
	}
}

// TestCallShowCard_DocumentViewer_RequiresSources mirrors the report-card
// gate for document-viewer.
func TestCallShowCard_DocumentViewer_RequiresSources(t *testing.T) {
	st := newSelfTools(t)
	args := map[string]any{
		"type": "document-viewer",
		"data": cloneMap(validShowCardPayloads["document-viewer"]),
	}
	res, err := st.callShowCard(context.Background(), args)
	if err != nil {
		t.Fatalf("callShowCard returned transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected sources-required error for document-viewer, got: %s", readToolText(t, res))
	}
}

// TestCallShowCard_PropagatesTargetAndMode keeps the Gap A regression.
func TestCallShowCard_PropagatesTargetAndMode(t *testing.T) {
	st := newSelfTools(t)
	args := map[string]any{
		"type":    "report-card",
		"data":    cloneMap(validShowCardPayloads["report-card"]),
		"sources": validSources,
		"target":  "bottom_chat_drawer",
		"mode":    "planning",
	}
	res, err := st.callShowCard(context.Background(), args)
	if err != nil {
		t.Fatalf("callShowCard returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("callShowCard returned error result: %s", readToolText(t, res))
	}
	env := extractEnvelopeJSON(t, readToolText(t, res))
	if got := env["target"]; got != "bottom_chat_drawer" {
		t.Errorf("target: want bottom_chat_drawer got %v", got)
	}
	if got := env["mode"]; got != "planning" {
		t.Errorf("mode: want planning got %v", got)
	}
}

// TestCallShowCard_OmitsEmptyTargetAndMode confirms the no-args-attached
// path stays bytewise minimal.
func TestCallShowCard_OmitsEmptyTargetAndMode(t *testing.T) {
	st := newSelfTools(t)
	args := map[string]any{
		"type": "info-card",
		"data": cloneMap(validShowCardPayloads["info-card"]),
	}
	res, err := st.callShowCard(context.Background(), args)
	if err != nil {
		t.Fatalf("callShowCard returned error: %v", err)
	}
	env := extractEnvelopeJSON(t, readToolText(t, res))
	if _, ok := env["target"]; ok {
		t.Errorf("envelope should not carry target when arg is omitted: %v", env)
	}
	if _, ok := env["mode"]; ok {
		t.Errorf("envelope should not carry mode when arg is omitted: %v", env)
	}
}

// TestCallShowCard_ReportCard_StampsGeneratedAtWhenMissing asserts the
// timestamp auto-fill behavior preserved from the pre-A3 nanite_show_report.
func TestCallShowCard_ReportCard_StampsGeneratedAtWhenMissing(t *testing.T) {
	st := newSelfTools(t)
	data := cloneMap(validShowCardPayloads["report-card"])
	delete(data, "generated_at")
	args := map[string]any{
		"type":    "report-card",
		"data":    data,
		"sources": validSources,
	}
	res, err := st.callShowCard(context.Background(), args)
	if err != nil {
		t.Fatalf("callShowCard returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("callShowCard returned error result: %s", readToolText(t, res))
	}
	env := extractEnvelopeJSON(t, readToolText(t, res))
	envData, _ := env["data"].(map[string]any)
	ts, ok := envData["generated_at"].(string)
	if !ok || ts == "" {
		t.Errorf("expected generated_at to be auto-stamped, got %v", envData["generated_at"])
	}
}

// TestCallShowCard_ListedAsTool covers the registry surface so dispatchers
// pick up the new tool name.
func TestCallShowCard_ListedAsTool(t *testing.T) {
	st := newSelfTools(t)
	tools, err := st.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools err: %v", err)
	}
	for _, tool := range tools {
		if tool.Name == "nanite_show_card" {
			return
		}
	}
	t.Errorf("nanite_show_card not registered in ListTools output")
}

// cloneMap returns a shallow copy so each test's mutation of `data` does not
// bleed into the shared validShowCardPayloads fixture (the handler stamps
// fields like generated_at and sources into data in place).
func cloneMap(src map[string]any) map[string]any {
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
