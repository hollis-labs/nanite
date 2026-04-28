package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// extractEnvelopeJSON pulls the JSON payload out of the
// <!--ENVELOPE_DATA:...:ENVELOPE_DATA--> marker emitted by the show* tools.
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

// TestCallShowReport_PropagatesTargetAndMode is the J8 Gap A regression:
// CW-20260428-0007 — when the agent passes target/mode, those fields must
// land on the top-level envelope JSON so the FE applyEnvelopePanelEffects
// helper can drive drawer visibility from a tool result.
func TestCallShowReport_PropagatesTargetAndMode(t *testing.T) {
	st := newSelfTools(t)
	res, err := st.callShowReport(map[string]any{
		"title":   "Sprint Health",
		"metrics": `[{"label":"Done","value":7}]`,
		"sources": `[{"tool_use_id":"tu_1","tool_name":"clockwork_task_list"}]`,
		"target":  "bottom_chat_drawer",
		"mode":    "planning",
	})
	if err != nil {
		t.Fatalf("callShowReport returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("callShowReport returned error result: %s", readToolText(t, res))
	}
	env := extractEnvelopeJSON(t, readToolText(t, res))
	if got := env["target"]; got != "bottom_chat_drawer" {
		t.Fatalf("target: want bottom_chat_drawer got %v (env=%v)", got, env)
	}
	if got := env["mode"]; got != "planning" {
		t.Fatalf("mode: want planning got %v (env=%v)", got, env)
	}
	if got := env["type"]; got != "report-card" {
		t.Fatalf("type: want report-card got %v", got)
	}
}

// TestCallShowReport_OmitsEmptyTargetAndMode confirms the existing inline-
// rendering shape stays bytewise unchanged when the agent omits target/mode.
// Important so old call sites don't start emitting empty fields the FE
// would otherwise key off.
func TestCallShowReport_OmitsEmptyTargetAndMode(t *testing.T) {
	st := newSelfTools(t)
	res, err := st.callShowReport(map[string]any{
		"title":   "Sprint Health",
		"metrics": `[{"label":"Done","value":7}]`,
		"sources": `[{"tool_use_id":"tu_1","tool_name":"clockwork_task_list"}]`,
	})
	if err != nil {
		t.Fatalf("callShowReport returned error: %v", err)
	}
	env := extractEnvelopeJSON(t, readToolText(t, res))
	if _, ok := env["target"]; ok {
		t.Fatalf("envelope should not carry target when arg is omitted: %v", env)
	}
	if _, ok := env["mode"]; ok {
		t.Fatalf("envelope should not carry mode when arg is omitted: %v", env)
	}
}

// TestCallShowDocument_PropagatesTargetAndMode mirrors the report-card
// regression for the document-viewer envelope.
func TestCallShowDocument_PropagatesTargetAndMode(t *testing.T) {
	st := newSelfTools(t)
	res, err := st.callShowDocument(map[string]any{
		"title":   "Status Note",
		"content": "## update\nbody",
		"sources": `[{"tool_use_id":"tu_1","tool_name":"clockwork_task_list"}]`,
		"target":  "bottom_chat_drawer",
		"mode":    "planning",
	})
	if err != nil {
		t.Fatalf("callShowDocument returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("callShowDocument returned error result: %s", readToolText(t, res))
	}
	env := extractEnvelopeJSON(t, readToolText(t, res))
	if got := env["target"]; got != "bottom_chat_drawer" {
		t.Fatalf("target: want bottom_chat_drawer got %v", got)
	}
	if got := env["mode"]; got != "planning" {
		t.Fatalf("mode: want planning got %v", got)
	}
	if got := env["type"]; got != "document-viewer" {
		t.Fatalf("type: want document-viewer got %v", got)
	}
}

// TestCallShowGiphy_PropagatesTargetAndMode covers the giphy demo path
// (no GIPHY_API_KEY) — the live HTTP path runs the same buildShowEnvelope.
func TestCallShowGiphy_PropagatesTargetAndMode(t *testing.T) {
	t.Setenv("GIPHY_API_KEY", "")
	st := newSelfTools(t)
	res, err := st.callShowGiphy(map[string]any{
		"query":  "celebration",
		"target": "bottom_chat_drawer",
		"mode":   "planning",
	})
	if err != nil {
		t.Fatalf("callShowGiphy returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("callShowGiphy returned error result: %s", readToolText(t, res))
	}
	env := extractEnvelopeJSON(t, readToolText(t, res))
	if got := env["target"]; got != "bottom_chat_drawer" {
		t.Fatalf("target: want bottom_chat_drawer got %v", got)
	}
	if got := env["mode"]; got != "planning" {
		t.Fatalf("mode: want planning got %v", got)
	}
	if got := env["type"]; got != "giphy-modal" {
		t.Fatalf("type: want giphy-modal got %v", got)
	}
}
