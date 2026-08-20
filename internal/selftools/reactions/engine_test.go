package reactions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestFire_RenderCardAndInternalAPICall_BothFireIndependently is the
// regression test TASKS/harness-reactive-self-tools/
// 03-reaction-engine-core.md's "Done means" requires: a tool with one
// enabled render_card reaction and one enabled internal_api_call reaction
// firing together, each independently resolving/executing — the "both"
// case docs/engineering/architecture/11-harness-reactive-self-tools.md's
// own framing promises, not just one kind at a time.
func TestFire_RenderCardAndInternalAPICall_BothFireIndependently(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	insertReaction(t, s, store.SelftoolReaction{
		ToolName:       "task_update_report",
		ReactionKindID: KindRenderCard,
		Config:         `{"envelope_type":"info-card","template":{"title":"Task update","body":"{{msg}}"}}`,
		Enabled:        true,
	})
	insertReaction(t, s, store.SelftoolReaction{
		ToolName:       "task_update_report",
		ReactionKindID: KindInternalAPICall,
		Config: `{"endpoint":"` + srv.URL + `/api/example/task-updates","method":"POST",` +
			`"body_template":{"id":"{{id}}","msg":"{{msg}}"}}`,
		Enabled: true,
	})

	engine := NewEngine(s, srv.Client(), nil)
	result, err := engine.Fire(ctx, "task_update_report", map[string]any{
		"id":  "task-42",
		"msg": "done",
	})
	if err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if result.ToolName != "task_update_report" {
		t.Errorf("Result.ToolName = %q, want %q", result.ToolName, "task_update_report")
	}
	if len(result.Reactions) != 2 {
		t.Fatalf("len(Result.Reactions) = %d, want 2: %+v", len(result.Reactions), result.Reactions)
	}

	byKind := make(map[string]FiredReaction)
	for _, fr := range result.Reactions {
		byKind[fr.Kind] = fr
	}

	// internal_api_call: executed synchronously, request actually hit srv.
	iac, ok := byKind[KindInternalAPICall]
	if !ok {
		t.Fatalf("no internal_api_call entry in Result.Reactions: %+v", result.Reactions)
	}
	if iac.Outcome != OutcomeSuccess {
		t.Errorf("internal_api_call Outcome = %q, want %q (err=%q)", iac.Outcome, OutcomeSuccess, iac.Error)
	}
	if iac.Category != "execute" {
		t.Errorf("internal_api_call Category = %q, want %q", iac.Category, "execute")
	}
	if gotMethod != http.MethodPost {
		t.Errorf("server saw method %q, want POST", gotMethod)
	}
	if gotPath != "/api/example/task-updates" {
		t.Errorf("server saw path %q, want /api/example/task-updates", gotPath)
	}
	if gotBody["id"] != "task-42" || gotBody["msg"] != "done" {
		t.Errorf("server saw body %+v, want {id: task-42, msg: done} (payload substitution failed)", gotBody)
	}

	// render_card: resolved (not delivered), payload carries substituted data.
	rc, ok := byKind[KindRenderCard]
	if !ok {
		t.Fatalf("no render_card entry in Result.Reactions: %+v", result.Reactions)
	}
	if rc.Outcome != OutcomeSuccess {
		t.Errorf("render_card Outcome = %q, want %q (err=%q)", rc.Outcome, OutcomeSuccess, rc.Error)
	}
	if rc.Category != "render" {
		t.Errorf("render_card Category = %q, want %q", rc.Category, "render")
	}
	if len(rc.Payload) == 0 {
		t.Fatal("render_card Payload is empty, want the resolved envelope JSON")
	}
	var env map[string]any
	if err := json.Unmarshal(rc.Payload, &env); err != nil {
		t.Fatalf("render_card Payload does not unmarshal as JSON: %v (%s)", err, rc.Payload)
	}
	if env["type"] != "info-card" {
		t.Errorf("render_card Payload type = %v, want info-card", env["type"])
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("render_card Payload data is not an object: %+v", env)
	}
	if data["body"] != "done" {
		t.Errorf("render_card Payload data.body = %v, want %q (payload substitution failed)", data["body"], "done")
	}
	if data["title"] != "Task update" {
		t.Errorf("render_card Payload data.title = %v, want %q (non-placeholder field mangled)", data["title"], "Task update")
	}

	// The RenderCardPayload accessor 04 is expected to use should surface
	// the same bytes.
	if payload, ok := result.RenderCardPayload(); !ok || string(payload) != string(rc.Payload) {
		t.Errorf("Result.RenderCardPayload() = (%s, %v), want (%s, true)", payload, ok, rc.Payload)
	}
}

// TestFire_ImplementedFalseReaction_SkippedNotErrored is the regression
// test TASKS/harness-reactive-self-tools/03-reaction-engine-core.md's
// "Done means" requires: an implemented=false reaction (seeded
// synthetically as enabled=true, since nothing in this batch enables one
// by default) is skipped, not executed, and does not error the whole
// Fire() call — a sibling implemented=true reaction on the same tool
// still fires normally alongside it.
func TestFire_ImplementedFalseReaction_SkippedNotErrored(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// external_api_call and callback are both seeded implemented=false by
	// migration 126 — use external_api_call here (callback is exercised
	// implicitly by the same code path; both hit the same defensive
	// switch case in Fire).
	insertReaction(t, s, store.SelftoolReaction{
		ToolName:       "task_update_report",
		ReactionKindID: KindExternalAPICall,
		Config:         `{"endpoint":"https://example.invalid/webhook"}`,
		Enabled:        true,
	})

	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	insertReaction(t, s, store.SelftoolReaction{
		ToolName:       "task_update_report",
		ReactionKindID: KindInternalAPICall,
		Config:         `{"endpoint":"` + srv.URL + `","method":"POST","body_template":{}}`,
		Enabled:        true,
	})

	engine := NewEngine(s, srv.Client(), nil)
	result, err := engine.Fire(ctx, "task_update_report", map[string]any{})
	if err != nil {
		t.Fatalf("Fire: %v, want nil (an implemented=false reaction must not error the whole call)", err)
	}
	if len(result.Reactions) != 2 {
		t.Fatalf("len(Result.Reactions) = %d, want 2: %+v", len(result.Reactions), result.Reactions)
	}

	byKind := make(map[string]FiredReaction)
	for _, fr := range result.Reactions {
		byKind[fr.Kind] = fr
	}

	eac, ok := byKind[KindExternalAPICall]
	if !ok {
		t.Fatalf("no external_api_call entry in Result.Reactions: %+v", result.Reactions)
	}
	if eac.Outcome != OutcomeSkippedNotImplemented {
		t.Errorf("external_api_call Outcome = %q, want %q", eac.Outcome, OutcomeSkippedNotImplemented)
	}
	if eac.Error != "" {
		t.Errorf("external_api_call Error = %q, want empty (skip is not a failure)", eac.Error)
	}

	iac, ok := byKind[KindInternalAPICall]
	if !ok {
		t.Fatalf("no internal_api_call entry in Result.Reactions: %+v", result.Reactions)
	}
	if iac.Outcome != OutcomeSuccess {
		t.Errorf("sibling internal_api_call Outcome = %q, want %q (err=%q) — an implemented=false"+
			" reaction must not block a sibling from firing", iac.Outcome, OutcomeSuccess, iac.Error)
	}
	if !called {
		t.Error("sibling internal_api_call never actually reached the server")
	}
}

// TestFire_InternalAPICall_NonTwoXX_RecordedAsFailure_SiblingStillFires is
// the regression test TASKS/harness-reactive-self-tools/
// 03-reaction-engine-core.md's "Done means" requires: internal_api_call's
// non-2xx response is recorded as a per-reaction failure without stopping
// a sibling reaction from firing.
func TestFire_InternalAPICall_NonTwoXX_RecordedAsFailure_SiblingStillFires(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer failing.Close()

	insertReaction(t, s, store.SelftoolReaction{
		ToolName:       "task_update_report",
		ReactionKindID: KindInternalAPICall,
		Config:         `{"endpoint":"` + failing.URL + `","method":"POST","body_template":{}}`,
		Enabled:        true,
	})
	insertReaction(t, s, store.SelftoolReaction{
		ToolName:       "task_update_report",
		ReactionKindID: KindRenderCard,
		Config:         `{"envelope_type":"info-card","template":{"body":"{{msg}}"}}`,
		Enabled:        true,
	})

	engine := NewEngine(s, failing.Client(), nil)
	result, err := engine.Fire(ctx, "task_update_report", map[string]any{"msg": "still rendered"})
	if err != nil {
		t.Fatalf("Fire: %v, want nil (a per-reaction failure must not error the whole call)", err)
	}
	if len(result.Reactions) != 2 {
		t.Fatalf("len(Result.Reactions) = %d, want 2: %+v", len(result.Reactions), result.Reactions)
	}

	byKind := make(map[string]FiredReaction)
	for _, fr := range result.Reactions {
		byKind[fr.Kind] = fr
	}

	iac, ok := byKind[KindInternalAPICall]
	if !ok {
		t.Fatalf("no internal_api_call entry in Result.Reactions: %+v", result.Reactions)
	}
	if iac.Outcome != OutcomeError {
		t.Errorf("internal_api_call Outcome = %q, want %q", iac.Outcome, OutcomeError)
	}
	if iac.Error == "" {
		t.Error("internal_api_call Error is empty, want a non-2xx failure message")
	}

	rc, ok := byKind[KindRenderCard]
	if !ok {
		t.Fatalf("no render_card entry in Result.Reactions: %+v", result.Reactions)
	}
	if rc.Outcome != OutcomeSuccess {
		t.Errorf("sibling render_card Outcome = %q, want %q (err=%q) — a failed internal_api_call"+
			" reaction must not block a sibling from firing", rc.Outcome, OutcomeSuccess, rc.Error)
	}
	if len(rc.Payload) == 0 {
		t.Error("sibling render_card Payload is empty, want the resolved envelope JSON")
	}
}

// TestFire_NoEnabledReactions_ReturnsEmptyResult confirms the normal case
// (a self-tool with no configured reactions at all — the overwhelming
// majority of the ~70 existing self-tools) returns a clean, empty, no-op
// Result rather than an error.
func TestFire_NoEnabledReactions_ReturnsEmptyResult(t *testing.T) {
	s := newTestStore(t)
	engine := NewEngine(s, nil, nil)

	result, err := engine.Fire(context.Background(), "some_unrelated_tool", map[string]any{})
	if err != nil {
		t.Fatalf("Fire: %v, want nil", err)
	}
	if result.ToolName != "some_unrelated_tool" {
		t.Errorf("Result.ToolName = %q, want %q", result.ToolName, "some_unrelated_tool")
	}
	if len(result.Reactions) != 0 {
		t.Errorf("len(Result.Reactions) = %d, want 0", len(result.Reactions))
	}
	if _, ok := result.RenderCardPayload(); ok {
		t.Error("RenderCardPayload() ok = true on an empty Result, want false")
	}
}
