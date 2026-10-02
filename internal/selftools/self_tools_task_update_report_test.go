package selftools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/selftools/reactions"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestCallTaskUpdateReport_FullChain_RenderCardAndInternalAPICall proves
// TASKS/harness-reactive-self-tools/07-worked-example-task-update-report.md's
// own Done-means requirement: the FULL handler -> reactions.Fire -> both
// reactions -> EmitReactionTrace chain, end to end, not each piece in
// isolation (those are already unit-tested individually by 03/04/05).
//
// The internal_api_call reaction's config.endpoint targets an
// httptest.NewServer as the reaction target
// (internal/api/example_task_updates.go) — this task's own documented
// choice (see this task file's Work Log) for proving the internal_api_call
// path inside a regression test, matching 03-reaction-engine-core.md's own
// test approach (internal/selftools/reactions/internal_api_call_test.go).
// The real route is exercised separately by the live dogfeed this task's
// Done means also requires.
func TestCallTaskUpdateReport_FullChain_RenderCardAndInternalAPICall(t *testing.T) {
	var gotMethod, gotContentType string
	var gotBody map[string]any
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer apiSrv.Close()

	s := newTestStore(t)
	ctx := context.Background()

	// Configure two operator reactions,
	// pointed at the httptest server instead of a real apiBaseURL — proves
	// callTaskUpdateReport's own Fire/marker/telemetry wiring independent
	// of the seed function (covered by its own tests below).
	renderCardConfig, err := json.Marshal(map[string]any{
		"envelope_type": "info-card",
		"template": map[string]any{
			"title": "Task update",
			"body":  "{{msg}}",
		},
	})
	if err != nil {
		t.Fatalf("marshal render_card config: %v", err)
	}
	internalAPICallConfig, err := json.Marshal(map[string]any{
		"endpoint": apiSrv.URL,
		"method":   "POST",
		"body_template": map[string]any{
			"id":  "{{id}}",
			"msg": "{{msg}}",
		},
	})
	if err != nil {
		t.Fatalf("marshal internal_api_call config: %v", err)
	}

	if err := s.InsertSelftoolReaction(ctx, store.SelftoolReaction{
		ToolName:       taskUpdateReportToolName,
		ReactionKindID: reactions.KindRenderCard,
		Config:         string(renderCardConfig),
		Enabled:        true,
	}); err != nil {
		t.Fatalf("insert render_card reaction: %v", err)
	}
	if err := s.InsertSelftoolReaction(ctx, store.SelftoolReaction{
		ToolName:       taskUpdateReportToolName,
		ReactionKindID: reactions.KindInternalAPICall,
		Config:         string(internalAPICallConfig),
		Enabled:        true,
	}); err != nil {
		t.Fatalf("insert internal_api_call reaction: %v", err)
	}

	st := newTestSelfToolsTransport(s)
	st.Reactions = reactions.NewEngine(s, apiSrv.Client(), nil)

	result, err := st.CallTool(ctx, taskUpdateReportToolName, map[string]any{
		"id":  "t-42",
		"msg": "build finished",
	})
	if err != nil {
		t.Fatalf("CallTool(task_update_report): %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("CallTool(task_update_report) returned an error result: %+v", result)
	}

	text := result.Content[0].Text

	// (a) render_card: marker embedded, resolves to an info-card envelope
	// with body correctly substituted from msg.
	if !strings.Contains(text, "<!--ENVELOPE_DATA:") {
		t.Fatalf("tool result text has no ENVELOPE_DATA marker: %q", text)
	}
	markerStart := strings.Index(text, "<!--ENVELOPE_DATA:")
	markerEnd := strings.Index(text, ":ENVELOPE_DATA-->")
	if markerStart < 0 || markerEnd < 0 || markerEnd < markerStart {
		t.Fatalf("malformed ENVELOPE_DATA marker in %q", text)
	}
	envJSON := text[markerStart+len("<!--ENVELOPE_DATA:") : markerEnd]
	var env struct {
		Kind    string `json:"kind"`
		Type    string `json:"type"`
		Version int    `json:"version"`
		Data    struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(envJSON), &env); err != nil {
		t.Fatalf("unmarshal embedded envelope JSON: %v (raw: %s)", err, envJSON)
	}
	if env.Kind != "envelope" || env.Type != "info-card" {
		t.Errorf("envelope kind/type = %q/%q, want envelope/info-card", env.Kind, env.Type)
	}
	if env.Data.Title != "Task update" {
		t.Errorf("envelope data.title = %q, want %q", env.Data.Title, "Task update")
	}
	if env.Data.Body != "build finished" {
		t.Errorf("envelope data.body = %q, want the substituted msg %q", env.Data.Body, "build finished")
	}

	// (b) internal_api_call: a real HTTP call landed on the target server
	// with id/msg correctly substituted into the body.
	if gotMethod != http.MethodPost {
		t.Errorf("internal_api_call method = %q, want POST", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Errorf("internal_api_call Content-Type = %q, want application/json", gotContentType)
	}
	if gotBody["id"] != "t-42" || gotBody["msg"] != "build finished" {
		t.Errorf("internal_api_call body = %+v, want {id: t-42, msg: build finished}", gotBody)
	}

	// (c) exactly two event_log rows at category="selftool_reaction",
	// correctly distinguishing the two kinds.
	events, err := s.ListEvents(context.Background(), reactions.CategorySelftoolReaction, 50)
	if err != nil {
		t.Fatalf("ListEvents(selftool_reaction): %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d selftool_reaction event_log rows, want exactly 2 (events: %+v)", len(events), events)
	}
	seenKinds := map[string]bool{}
	for _, ev := range events {
		if ev.Detail != taskUpdateReportToolName {
			t.Errorf("event detail = %q, want %q", ev.Detail, taskUpdateReportToolName)
		}
		seenKinds[ev.EventType] = true
		if !strings.Contains(ev.Metadata, `"outcome":"success"`) {
			t.Errorf("event %s metadata does not report outcome=success: %s", ev.EventType, ev.Metadata)
		}
	}
	if !seenKinds[reactions.KindRenderCard] || !seenKinds[reactions.KindInternalAPICall] {
		t.Errorf("expected event_log rows for both render_card and internal_api_call, got kinds: %+v", seenKinds)
	}
}

// TestCallTaskUpdateReport_RequiresIDAndMsg pins the validation gate: both
// id and msg are required, per the tool's own definition.
func TestCallTaskUpdateReport_RequiresIDAndMsg(t *testing.T) {
	st := newSelfTools(t)
	ctx := context.Background()

	cases := []map[string]any{
		{},
		{"id": "only-id"},
		{"msg": "only-msg"},
		{"id": "", "msg": "hello"},
		{"id": "abc", "msg": ""},
	}
	for _, args := range cases {
		result, err := st.CallTool(ctx, taskUpdateReportToolName, args)
		if err != nil {
			t.Fatalf("CallTool(task_update_report, %+v): unexpected transport error: %v", args, err)
		}
		if result == nil || !result.IsError {
			t.Errorf("CallTool(task_update_report, %+v) = %+v, want an error result (id/msg both required)", args, result)
		}
	}
}

// TestCallTaskUpdateReport_NoReactionsWired_StillConfirms proves the
// nil-safe fallback: with no Reactions engine wired (e.g. a bare
// SelfToolsTransport), the call still succeeds with a plain confirmation
// and no panic — mirroring every other nil-safe field on
// SelfToolsTransport.
func TestCallTaskUpdateReport_NoReactionsWired_StillConfirms(t *testing.T) {
	st := newSelfTools(t) // st.Reactions is nil — NewSelfToolsTransport doesn't wire it
	ctx := context.Background()

	result, err := st.CallTool(ctx, taskUpdateReportToolName, map[string]any{
		"id":  "no-reactions",
		"msg": "hello",
	})
	if err != nil {
		t.Fatalf("CallTool(task_update_report): %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("CallTool(task_update_report) with no Reactions wired returned an error: %+v", result)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "no-reactions") || !strings.Contains(text, "hello") {
		t.Errorf("confirmation text = %q, want it to mention id and msg", text)
	}
	if strings.Contains(text, "ENVELOPE_DATA") {
		t.Errorf("confirmation text = %q, should carry no envelope marker when Reactions is unwired", text)
	}
}

// TestCallTaskUpdateReport_NoReactionsConfigured_StillConfirmsAndNoEvents
// proves the normal case for every self-tool that ISN'T task_update_report:
// a wired Reactions engine with zero configured rows for the tool fires
// nothing, writes no telemetry, and still returns a clean confirmation.
func TestCallTaskUpdateReport_NoReactionsConfigured_StillConfirmsAndNoEvents(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	st := newTestSelfToolsTransport(s)
	st.Reactions = reactions.NewEngine(s, nil, nil)

	result, err := st.CallTool(ctx, taskUpdateReportToolName, map[string]any{
		"id":  "unconfigured",
		"msg": "hello",
	})
	if err != nil {
		t.Fatalf("CallTool(task_update_report): %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("CallTool(task_update_report) returned an error: %+v", result)
	}
	if strings.Contains(result.Content[0].Text, "ENVELOPE_DATA") {
		t.Errorf("no render_card reaction configured, but result carries a marker: %q", result.Content[0].Text)
	}

	events, err := s.ListEvents(context.Background(), reactions.CategorySelftoolReaction, 50)
	if err != nil {
		t.Fatalf("ListEvents(selftool_reaction): %v", err)
	}
	if len(events) != 0 {
		t.Errorf("got %d selftool_reaction event_log rows for a tool with no configured reactions, want 0", len(events))
	}
}
