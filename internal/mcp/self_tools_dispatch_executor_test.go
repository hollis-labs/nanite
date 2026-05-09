package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/dispatch"
	envelope_render "github.com/hollis-labs/nanite/internal/executor/envelope_render"
)

// stubExecutor is the test seam for the dispatch_executor self-tool.
// Records the request it was called with and returns a configurable
// response/error so the suite can drive every branch (happy, harness
// error, agent-visible failure) without booting envelope_render.
type stubExecutor struct {
	intents []string
	resp    *dispatch.ExecutorResponse
	err     error
	saw     dispatch.ExecutorRequest
	calls   int
}

func (s *stubExecutor) Intents() []string { return s.intents }
func (s *stubExecutor) Execute(_ context.Context, req dispatch.ExecutorRequest) (*dispatch.ExecutorResponse, error) {
	s.saw = req
	s.calls++
	return s.resp, s.err
}

// TestDispatchExecutorTool_Definition_OnSelfSurface confirms the tool is
// registered in selfToolDefinitions(). The B5 prompt cue + B4 chat-surface
// filter are both load-bearing on this tool name being live.
func TestDispatchExecutorTool_Definition_OnSelfSurface(t *testing.T) {
	defs := selfToolDefinitions()
	for _, d := range defs {
		if d.Name == "dispatch_executor" {
			return
		}
	}
	t.Fatalf("dispatch_executor not registered in selfToolDefinitions()")
}

// TestDispatchExecutorTool_Definition_RequiredFields locks the schema's
// required-list to the v1 contract. user_request is the load-bearing field —
// the executor reads it verbatim for grounding (B1 §2). intent gates routing.
func TestDispatchExecutorTool_Definition_RequiredFields(t *testing.T) {
	def := dispatchExecutorToolDefinition()
	required, _ := def.InputSchema["required"].([]string)
	got := map[string]bool{}
	for _, r := range required {
		got[r] = true
	}
	for _, want := range []string{"intent", "user_request"} {
		if !got[want] {
			t.Errorf("required field %q missing from schema; got %v", want, required)
		}
	}
}

// TestCallDispatchExecutor_NilExecutor_ErrorResult asserts the wiring
// guard: a nil Executor surfaces a recoverable errorResult so a wiring
// miss is visible at the call site rather than silently swallowed.
func TestCallDispatchExecutor_NilExecutor_ErrorResult(t *testing.T) {
	st := &SelfToolsTransport{} // Executor unset
	res, err := st.callDispatchExecutor(context.Background(), map[string]any{
		"intent":       "render_envelope",
		"user_request": "show me a card",
	})
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("nil executor should return IsError=true; got %+v", res)
	}
	if !strings.Contains(textBody(res), "executor is not configured") {
		t.Errorf("error message did not mention wiring miss: %q", textBody(res))
	}
}

// TestCallDispatchExecutor_MissingIntent_ErrorResult covers the args-side
// validation (different from the executor-side ExecutorFailureInvalidIntent
// path — that fires when the executor doesn't recognize a non-empty intent).
func TestCallDispatchExecutor_MissingIntent_ErrorResult(t *testing.T) {
	st := &SelfToolsTransport{Executor: &stubExecutor{intents: []string{"render_envelope"}}}
	res, _ := st.callDispatchExecutor(context.Background(), map[string]any{
		"user_request": "show me a card",
	})
	if !res.IsError {
		t.Fatalf("missing intent should return IsError=true; got %+v", res)
	}
	if !strings.Contains(textBody(res), "intent is required") {
		t.Errorf("error message did not mention intent: %q", textBody(res))
	}
}

// TestCallDispatchExecutor_MissingUserRequest_ErrorResult — the executor
// reads user_request verbatim, so an empty value would corrupt grounding.
// Reject up-front rather than send "" to the executor.
func TestCallDispatchExecutor_MissingUserRequest_ErrorResult(t *testing.T) {
	st := &SelfToolsTransport{Executor: &stubExecutor{intents: []string{"render_envelope"}}}
	res, _ := st.callDispatchExecutor(context.Background(), map[string]any{
		"intent": "render_envelope",
	})
	if !res.IsError {
		t.Fatalf("missing user_request should return IsError=true; got %+v", res)
	}
	if !strings.Contains(textBody(res), "user_request is required") {
		t.Errorf("error message did not mention user_request: %q", textBody(res))
	}
}

// TestCallDispatchExecutor_HappyPath_PropagatesResponse asserts the
// pass-through contract: the handler hands the typed ExecutorResponse to
// the chat agent as JSON in a non-error ToolResult.
func TestCallDispatchExecutor_HappyPath_PropagatesResponse(t *testing.T) {
	want := &dispatch.ExecutorResponse{
		Summary: "Rendered info-card.",
		Envelope: &dispatch.Envelope{
			Kind: "envelope", Version: 1, Type: "info-card",
			Title: "Hello",
			Data:  map[string]any{"title": "Hello", "body": "World"},
		},
	}
	stub := &stubExecutor{intents: []string{"render_envelope"}, resp: want}
	st := &SelfToolsTransport{Executor: stub}

	res, err := st.callDispatchExecutor(context.Background(), map[string]any{
		"intent":               "render_envelope",
		"target_envelope_type": "info-card",
		"user_request":         "show me an info card about hello world",
		"data": map[string]any{
			"title": "Hello",
			"body":  "World",
		},
	})
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("happy-path response should not be an error result; got %+v", res)
	}
	if stub.calls != 1 {
		t.Fatalf("expected one Executor.Execute call; got %d", stub.calls)
	}
	if stub.saw.Intent != "render_envelope" || stub.saw.TargetEnvelopeType != "info-card" {
		t.Errorf("request not passed through as expected: %+v", stub.saw)
	}
	if stub.saw.UserRequest != "show me an info card about hello world" {
		t.Errorf("user_request not preserved verbatim: %q", stub.saw.UserRequest)
	}
	if got, ok := stub.saw.Data["title"].(string); !ok || got != "Hello" {
		t.Errorf("data not passed through: %+v", stub.saw.Data)
	}

	var got dispatch.ExecutorResponse
	if err := json.Unmarshal([]byte(textBody(res)), &got); err != nil {
		t.Fatalf("response is not JSON: %v\nbody: %s", err, textBody(res))
	}
	if got.Summary != want.Summary {
		t.Errorf("summary not propagated: got %q want %q", got.Summary, want.Summary)
	}
	if got.Envelope == nil || got.Envelope.Type != "info-card" {
		t.Errorf("envelope not propagated: %+v", got.Envelope)
	}
}

// TestCallDispatchExecutor_UnknownIntent_FailureInResponse confirms that
// agent-visible executor failures (here: invalid_intent from the dispatch
// router when no registered executor recognizes the intent) ride in the
// response JSON's failure field, NOT as IsError. Per B1 §5 the chat agent
// surfaces these failures verbatim and does not auto-retry.
func TestCallDispatchExecutor_UnknownIntent_FailureInResponse(t *testing.T) {
	stub := &stubExecutor{intents: []string{"render_envelope"}}
	st := &SelfToolsTransport{Executor: stub}

	res, err := st.callDispatchExecutor(context.Background(), map[string]any{
		"intent":       "knowledge_grounded_answer",
		"user_request": "answer with grounding",
	})
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("invalid_intent is agent-visible, not a recoverable error result; got %+v", res)
	}
	if stub.calls != 0 {
		t.Errorf("dispatch.DispatchExecutor should short-circuit unknown intents before Execute; calls=%d", stub.calls)
	}
	var got dispatch.ExecutorResponse
	if err := json.Unmarshal([]byte(textBody(res)), &got); err != nil {
		t.Fatalf("response is not JSON: %v\nbody: %s", err, textBody(res))
	}
	if got.Failure == nil || got.Failure.Code != dispatch.ExecutorFailureInvalidIntent {
		t.Fatalf("expected invalid_intent failure in response; got %+v", got.Failure)
	}
}

// TestCallDispatchExecutor_ExecutorReturnsError_SurfacesAsErrorResult
// asserts the harness-error path: an Executor.Execute that returns a Go
// error (per its own contract: reserved for wiring problems, not
// agent-visible failures) is surfaced as IsError so the chat agent sees a
// recoverable error and can retry rather than silently dropping the
// dispatch.
func TestCallDispatchExecutor_ExecutorReturnsError_SurfacesAsErrorResult(t *testing.T) {
	stub := &stubExecutor{
		intents: []string{"render_envelope"},
		err:     errors.New("registry unreachable"),
	}
	st := &SelfToolsTransport{Executor: stub}

	res, _ := st.callDispatchExecutor(context.Background(), map[string]any{
		"intent":               "render_envelope",
		"target_envelope_type": "info-card",
		"user_request":         "show me an info card",
	})
	if !res.IsError {
		t.Fatalf("executor go-error should surface as IsError; got %+v", res)
	}
	if !strings.Contains(textBody(res), "registry unreachable") {
		t.Errorf("error message did not include underlying error: %q", textBody(res))
	}
}

// TestCallDispatchExecutor_PassesAllOptionalFields wires sources +
// context_handles + synthetic_allowed through the schema-typed args-map and
// verifies they reach the executor unchanged. Forward-compat surface for
// future LLM-driven executors.
func TestCallDispatchExecutor_PassesAllOptionalFields(t *testing.T) {
	stub := &stubExecutor{
		intents: []string{"render_envelope"},
		resp:    &dispatch.ExecutorResponse{Summary: "ok"},
	}
	st := &SelfToolsTransport{Executor: stub}

	_, err := st.callDispatchExecutor(context.Background(), map[string]any{
		"intent":               "render_envelope",
		"target_envelope_type": "report-card",
		"user_request":         "summarize the sprint",
		"data":                 map[string]any{"title": "Sprint"},
		"sources": []any{
			map[string]any{"tool_use_id": "toolu_01abc", "tool_name": "memory_recall", "note": "sprint state"},
		},
		"context_handles": []any{
			map[string]any{"source": "scratchpad", "key": "sprint:notes"},
		},
		"synthetic_allowed": true,
		"session_id":        "sess-42",
	})
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if !stub.saw.SyntheticAllowed {
		t.Error("synthetic_allowed not propagated")
	}
	if stub.saw.SessionID != "sess-42" {
		t.Errorf("session_id not propagated: %q", stub.saw.SessionID)
	}
	if len(stub.saw.Sources) != 1 || stub.saw.Sources[0].ToolUseID != "toolu_01abc" {
		t.Errorf("sources not propagated: %+v", stub.saw.Sources)
	}
	if len(stub.saw.ContextHandles) != 1 || stub.saw.ContextHandles[0].Source != "scratchpad" {
		t.Errorf("context_handles not propagated: %+v", stub.saw.ContextHandles)
	}
}

// TestCallDispatchExecutor_DroppedMalformedHandles_DoesNotFail asserts the
// handler tolerates malformed context_handles entries (missing source or
// key) by dropping them silently. Per the schema, both fields are required;
// a malformed entry is a model bug and the schema-level validation catches
// it pre-call. The runtime drop ensures the executor sees only well-formed
// handles even if validation is bypassed.
func TestCallDispatchExecutor_DroppedMalformedHandles_DoesNotFail(t *testing.T) {
	stub := &stubExecutor{
		intents: []string{"render_envelope"},
		resp:    &dispatch.ExecutorResponse{Summary: "ok"},
	}
	st := &SelfToolsTransport{Executor: stub}

	_, _ = st.callDispatchExecutor(context.Background(), map[string]any{
		"intent":               "render_envelope",
		"target_envelope_type": "info-card",
		"user_request":         "show me an info card",
		"context_handles": []any{
			map[string]any{"source": "scratchpad", "key": ""}, // dropped
			map[string]any{"source": "", "key": "k"},          // dropped
			map[string]any{"source": "memory", "key": "good"}, // kept
		},
	})
	if got := len(stub.saw.ContextHandles); got != 1 {
		t.Errorf("expected 1 well-formed handle, got %d: %+v", got, stub.saw.ContextHandles)
	}
}

// TestCallDispatchExecutor_EndToEnd_EnvelopeRender boots the real B3
// envelope_render executor and dispatches a happy-path info-card render.
// Closes the loop the implementer-prompt §4 calls for: agent-shape inputs
// → real executor → returned envelope JSON in the tool result. Guards
// against a regression where the schema and the executor's request shape
// drift apart.
func TestCallDispatchExecutor_EndToEnd_EnvelopeRender(t *testing.T) {
	st := &SelfToolsTransport{Executor: envelope_render.New()}

	res, err := st.callDispatchExecutor(context.Background(), map[string]any{
		"intent":               "render_envelope",
		"target_envelope_type": "info-card",
		"user_request":         "show me an info card about the rollout freeze",
		"data": map[string]any{
			"title": "Rollout freeze",
			"body":  "Non-critical merges paused through 2026-03-09.",
		},
	})
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("end-to-end render should not be an error result; got %+v", res)
	}
	var got dispatch.ExecutorResponse
	if uerr := json.Unmarshal([]byte(textBody(res)), &got); uerr != nil {
		t.Fatalf("response is not JSON: %v\nbody: %s", uerr, textBody(res))
	}
	if got.Failure != nil {
		t.Fatalf("end-to-end render should not carry failure; got %+v", got.Failure)
	}
	if got.Envelope == nil || got.Envelope.Type != "info-card" {
		t.Fatalf("envelope not produced: %+v", got.Envelope)
	}
	if got.Envelope.Title != "Rollout freeze" {
		t.Errorf("envelope title not stamped: %q", got.Envelope.Title)
	}
}

// textBody returns the Text content of a *ToolResult or empty string.
// Safe across both happy and error results — both shapes use a single
// text content block.
func textBody(r *ToolResult) string {
	if r == nil || len(r.Content) == 0 {
		return ""
	}
	return r.Content[0].Text
}
