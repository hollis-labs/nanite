package service

// CW-20260501-0006: agent self-honesty regression — when a tool returns an
// error, the actual error string must travel through to the next-turn
// tool_result content block verbatim, NEVER replaced by a truncation hint or
// dropped to a cache pointer.
//
// c121 evidence: agent called nanite_memory_recall, tool returned
// errorResult("memory service not configured"), agent narrated "I don't have
// access to a memory recall tool" — a fabricated non-existence claim.
//
// Code analysis on this branch shows the error text DOES reach the agent's
// prompt for short errors (the canonical case). The c121 hallucination is
// disposition-shaped (H2) and is gated to the orchestrator. These tests
// pin the mechanical contract so any future regression fails CI:
//
//  1. Short error (~the c121 case): tool returns errorResult("test error"),
//     verify the next-turn tool_result block content is exactly "test error"
//     and IsError is true.
//  2. Long error (preventative — H3 hardening): tool returns a long error
//     payload (>4 KB). truncate.Output's 4K cap WOULD have replaced the body
//     with a "delegate to a research agent" hint. Verify the new isError
//     gate keeps the original text intact.

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/chat"
)

// drainEvents pulls everything currently buffered on ch and returns it.
func drainEvents(ch chan chat.StreamEvent) []chat.StreamEvent {
	var out []chat.StreamEvent
	for {
		select {
		case ev := <-ch:
			out = append(out, ev)
		default:
			return out
		}
	}
}

// makeErrorHonestyService builds a minimal chatServiceImpl wired with the
// stubs postProcessToolResults reaches for: store.LogEvent and ToolService.
func makeErrorHonestyService() *chatServiceImpl {
	return &chatServiceImpl{
		streams: NewStreamManager(),
		tools:   &loopTestToolStub{output: "ok"},
		store:   &loopTestStore{},
		// resultCache nil — exercises non-cache path
		// orchestrator nil — canDelegate=false (irrelevant for errors)
		// appConfig nil — maybeCreateAutoArtifact short-circuits safely
	}
}

// runPostProcess drives a single error-result through postProcessToolResults
// and returns the resulting tool_result content blocks.
func runPostProcessForError(
	t *testing.T,
	toolName string,
	errText string,
) []provider.ContentBlock {
	t.Helper()

	svc := makeErrorHonestyService()
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ch := make(chan chat.StreamEvent, 32)

	tu := provider.ToolUseBlock{
		ID:    "tu-error-honesty-1",
		Name:  toolName,
		Input: map[string]any{"query": "anything"},
	}

	plans := []toolPlan{{
		tu:            tu,
		status:        toolPlanReady,
		originalIndex: 0,
	}}
	results := []toolExecResult{{
		originalIndex: 0,
		resultBlock: provider.ContentBlock{
			Type: "tool_result", ToolUseID: tu.ID,
			Content: errText, IsError: true,
		},
		ref:       chat.ToolCallRef{ID: tu.ID, Name: tu.Name},
		isError:   true,
		rawOutput: errText,
	}}

	blocks, _ := svc.postProcessToolResults(
		context.Background(),
		plans, results, ls, ch,
		"sess-error-honesty-1", "agent-1", "msg-1",
		"", // modelID — empty exercises the static-floor branch (CW-20260430-0008).
	)
	_ = drainEvents(ch)
	return blocks
}

// TestPostProcess_ShortError_PreservedVerbatim is the c121 reproducer: the
// tool returned errorResult("test error"), the agent's next-turn tool_result
// block must contain "test error" verbatim with IsError=true.
func TestPostProcess_ShortError_PreservedVerbatim(t *testing.T) {
	const errText = "test error"

	blocks := runPostProcessForError(t, "nanite_memory_recall", errText)

	if len(blocks) != 1 {
		t.Fatalf("expected 1 result block, got %d", len(blocks))
	}
	got := blocks[0]
	if got.Type != "tool_result" {
		t.Errorf("Type = %q, want tool_result", got.Type)
	}
	if got.Content != errText {
		t.Errorf("Content mismatch.\n got: %q\nwant: %q", got.Content, errText)
	}
	if !got.IsError {
		t.Error("IsError must be true on an error result")
	}
}

// TestPostProcess_RealisticErrorString_PreservedVerbatim covers the actual
// shape the c121 path produces: the recover layer prepends "Error: " and
// wraps the transport error. The full string still must reach the agent's
// tool_result content block intact.
func TestPostProcess_RealisticErrorString_PreservedVerbatim(t *testing.T) {
	const errText = "Error: call tool memory_recall on builtin: tool error: memory service not configured"

	blocks := runPostProcessForError(t, "nanite_memory_recall", errText)

	if len(blocks) != 1 {
		t.Fatalf("expected 1 result block, got %d", len(blocks))
	}
	if !strings.Contains(blocks[0].Content, "memory service not configured") {
		t.Errorf("error content lost the actual reason; got: %q", blocks[0].Content)
	}
	if !blocks[0].IsError {
		t.Error("IsError must be true on an error result")
	}
}

// TestPostProcess_LongError_NotTruncated is the preventative H3 guard.
// Pre-fix, an error >MaxChars (4000) would be passed to truncate.Output
// and emerge with a "delegate to a research agent" hint replacing the
// actual reason. Verify the isError gate keeps the original verbatim.
func TestPostProcess_LongError_NotTruncated(t *testing.T) {
	// Build a long error string (>4K chars) with a load-bearing prefix and
	// suffix so we can detect partial truncation either way.
	const head = "Error: tool error: schema validation failed because the following fields are missing or invalid: "
	const tail = " (END_OF_ERROR_MARKER)"
	body := strings.Repeat("field_name_with_padding, ", 250) // ~6 KB
	errText := head + body + tail

	blocks := runPostProcessForError(t, "some_long_error_tool", errText)

	if len(blocks) != 1 {
		t.Fatalf("expected 1 result block, got %d", len(blocks))
	}
	got := blocks[0].Content
	if got != errText {
		t.Errorf("long error was modified by truncation/cache layer.\n"+
			"want len=%d, got len=%d\nwant prefix: %q\nwant suffix: %q",
			len(errText), len(got), head, tail)
	}
	if !strings.HasSuffix(got, tail) {
		t.Errorf("long error missing END_OF_ERROR_MARKER — body was clipped before reaching the agent: %q",
			got[max(0, len(got)-200):])
	}
	if strings.Contains(got, "delegating to a research agent") ||
		strings.Contains(got, "more lines") && strings.Contains(got, "truncated") {
		t.Errorf("long error replaced with truncation hint — agent recovery clue lost: %q",
			got[max(0, len(got)-200):])
	}
	if !blocks[0].IsError {
		t.Error("IsError must remain true after the no-truncate path")
	}
}

// max is provided for Go 1.20 compatibility within tests; stdlib helper is
// available on 1.21+ but we keep this local to avoid import churn.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// runPostProcessForErrorReturningRefs is a sibling of runPostProcessForError
// that returns the ToolCallRef slice instead of (or in addition to) the
// content blocks. Used to assert that ErrorReason gets populated end-to-end
// through postProcessToolResults (CW-20260501-0013).
func runPostProcessForErrorReturningRefs(
	t *testing.T,
	toolName string,
	errText string,
) []chat.ToolCallRef {
	t.Helper()

	svc := makeErrorHonestyService()
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ch := make(chan chat.StreamEvent, 32)

	tu := provider.ToolUseBlock{
		ID:    "tu-error-honesty-2",
		Name:  toolName,
		Input: map[string]any{"query": "anything"},
	}
	plans := []toolPlan{{
		tu:            tu,
		status:        toolPlanReady,
		originalIndex: 0,
	}}
	results := []toolExecResult{{
		originalIndex: 0,
		resultBlock: provider.ContentBlock{
			Type: "tool_result", ToolUseID: tu.ID,
			Content: errText, IsError: true,
		},
		ref:       chat.ToolCallRef{ID: tu.ID, Name: tu.Name},
		isError:   true,
		rawOutput: errText,
	}}
	_, refs := svc.postProcessToolResults(
		context.Background(),
		plans, results, ls, ch,
		"sess-error-honesty-2", "agent-1", "msg-1",
		"",
	)
	_ = drainEvents(ch)
	return refs
}

// CW-20260501-0013: end-to-end propagation. After postProcessToolResults
// runs over an error result, the resulting ToolCallRef must carry the
// verbatim error string in ErrorReason so the failure-footer can inline it.
func TestPostProcess_ErrorReason_PopulatedOnRef(t *testing.T) {
	const errText = "memory service not configured"

	refs := runPostProcessForErrorReturningRefs(t, "nanite_memory_recall", errText)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].Status != "error" {
		t.Fatalf("Status = %q, want error", refs[0].Status)
	}
	if refs[0].ErrorReason != errText {
		t.Errorf("ErrorReason mismatch.\n got: %q\nwant: %q", refs[0].ErrorReason, errText)
	}
}

// CW-20260501-0013: full pipeline — postProcessToolResults populates
// ErrorReason → maybeAppendFailureFooter inlines it. The c121-shaped
// reproducer asserts the entire chain.
func TestPostProcess_C121Pipeline_ReasonInlinedInFooter(t *testing.T) {
	const errText = "memory service not configured"

	refs := runPostProcessForErrorReturningRefs(t, "nanite_memory_recall", errText)
	got := maybeAppendFailureFooter("Sure, let me look into that.", refs)
	if !strings.Contains(got, `"memory service not configured"`) {
		t.Fatalf("c121 pipeline: verbatim reason missing from footer.\noutput:\n%s", got)
	}
	if !strings.Contains(got, "`nanite_memory_recall`") {
		t.Fatalf("c121 pipeline: tool name missing from footer.\noutput:\n%s", got)
	}
}
