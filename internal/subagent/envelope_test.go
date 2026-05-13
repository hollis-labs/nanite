package subagent

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEnvelopeFromRun_Success pins the success-path shape.
func TestEnvelopeFromRun_Success(t *testing.T) {
	run := &Run{
		ID:     "run-1",
		Role:   "researcher",
		Status: StatusCompleted,
	}
	env := EnvelopeFromRun(run, "here is what I found")

	if !env.Success {
		t.Fatal("expected Success=true for StatusCompleted with non-empty summary")
	}
	if env.Error != nil {
		t.Fatalf("expected nil Error on success, got %+v", env.Error)
	}
	if env.Result == nil {
		t.Fatal("expected non-nil Result on success")
	}
	if env.Result.RunID != "run-1" {
		t.Errorf("RunID = %q, want %q", env.Result.RunID, "run-1")
	}
	if env.Result.Summary != "here is what I found" {
		t.Errorf("Summary = %q, want %q", env.Result.Summary, "here is what I found")
	}
}

// TestEnvelopeFromRun_FailureTimeout pins the timeout failure shape.
// The runner error string carries "context deadline exceeded" → kind=timeout.
func TestEnvelopeFromRun_FailureTimeout(t *testing.T) {
	run := &Run{
		ID:     "run-2",
		Role:   "researcher",
		Status: StatusFailed,
		Error:  "context deadline exceeded",
	}
	env := EnvelopeFromRun(run, "")

	if env.Success {
		t.Fatal("expected Success=false for StatusFailed")
	}
	if env.Result != nil {
		t.Fatalf("expected nil Result on failure, got %+v", env.Result)
	}
	if env.Error == nil {
		t.Fatal("expected non-nil Error on failure")
	}
	if env.Error.Kind != ErrorKindTimeout {
		t.Errorf("Kind = %q, want %q", env.Error.Kind, ErrorKindTimeout)
	}
	if env.Error.Message != "context deadline exceeded" {
		t.Errorf("Message = %q, want passthrough of runner error", env.Error.Message)
	}
	if env.Error.Context["run_id"] != "run-2" {
		t.Errorf("Context[run_id] = %v, want %q", env.Error.Context["run_id"], "run-2")
	}
	if env.Error.Context["status"] != StatusFailed {
		t.Errorf("Context[status] = %v, want %q", env.Error.Context["status"], StatusFailed)
	}
}

// TestEnvelopeFromRun_FailureInternal — non-timeout StatusFailed → kind=internal.
func TestEnvelopeFromRun_FailureInternal(t *testing.T) {
	run := &Run{
		ID:     "run-3",
		Role:   "worker",
		Status: StatusFailed,
		Error:  "child session create failed: disk full",
	}
	env := EnvelopeFromRun(run, "")
	if env.Error.Kind != ErrorKindInternal {
		t.Errorf("Kind = %q, want %q", env.Error.Kind, ErrorKindInternal)
	}
}

// TestEnvelopeFromRun_Cancelled pins the cancel shape.
func TestEnvelopeFromRun_Cancelled(t *testing.T) {
	run := &Run{
		ID:     "run-4",
		Role:   "worker",
		Status: StatusCancelled,
	}
	env := EnvelopeFromRun(run, "")
	if env.Error.Kind != ErrorKindCancelled {
		t.Errorf("Kind = %q, want %q", env.Error.Kind, ErrorKindCancelled)
	}
}

// TestEnvelopeFromRun_Rejected pins the rejection shape including
// rejection_reason propagation into the message.
func TestEnvelopeFromRun_Rejected(t *testing.T) {
	run := &Run{
		ID:              "run-5",
		Role:            "worker",
		Status:          StatusRejected,
		RejectionReason: "approval timed out",
	}
	env := EnvelopeFromRun(run, "")
	if env.Error.Kind != ErrorKindDenied {
		t.Errorf("Kind = %q, want %q", env.Error.Kind, ErrorKindDenied)
	}
	if !strings.Contains(env.Error.Message, "approval timed out") {
		t.Errorf("Message = %q, want substring of rejection reason", env.Error.Message)
	}
}

// TestEnvelopeFromRun_CompletedEmptyReply — completed run with no
// recoverable assistant text is treated as a soft failure
// (empty_reply) so the parent does not narrate a non-existent reply.
// This is the c160 fabrication-class regression target.
func TestEnvelopeFromRun_CompletedEmptyReply(t *testing.T) {
	run := &Run{
		ID:     "run-6",
		Role:   "researcher",
		Status: StatusCompleted,
	}
	env := EnvelopeFromRun(run, "")
	if env.Success {
		t.Fatal("expected Success=false for completed run with empty assistant text")
	}
	if env.Error.Kind != ErrorKindEmptyReply {
		t.Errorf("Kind = %q, want %q", env.Error.Kind, ErrorKindEmptyReply)
	}
}

// TestEnvelopeFromRun_PendingApproval pins the non-terminal mapping:
// StatusRequested / StatusApproved are NOT failures. The run was
// accepted, approval is in flight, and the parent should be guided
// toward subagent_status polling rather than treated as a denial.
//
// Pre-round-1 this case mapped to Success=false / ErrorKindDenied,
// which caused the sync path's envelopeResult(IsError = !Success) to
// stamp IsError=true on every approval-gated spawn — including the
// production default (SubagentApprovalRequired=true). The fix is
// here so the call-site logic does not need to special-case
// non-terminal states.
func TestEnvelopeFromRun_PendingApproval(t *testing.T) {
	for _, status := range []string{StatusRequested, StatusApproved} {
		run := &Run{
			ID:     "run-pending",
			Role:   "researcher",
			Status: status,
		}
		env := EnvelopeFromRun(run, "")
		if !env.Success {
			t.Errorf("status=%q: expected Success=true (non-terminal), got envelope=%+v", status, env)
		}
		if env.Error != nil {
			t.Errorf("status=%q: expected nil Error on non-terminal envelope, got %+v", status, env.Error)
		}
		if env.Result == nil {
			t.Fatalf("status=%q: expected non-nil Result with run_id", status)
		}
		if env.Result.RunID != "run-pending" {
			t.Errorf("status=%q: Result.RunID = %q, want %q", status, env.Result.RunID, "run-pending")
		}
		if !strings.Contains(env.Result.Summary, "awaiting approval") {
			t.Errorf("status=%q: expected Result.Summary to mention awaiting approval, got %q", status, env.Result.Summary)
		}
		if !strings.Contains(env.Result.Summary, "subagent_status") {
			t.Errorf("status=%q: expected Result.Summary to guide toward subagent_status polling, got %q", status, env.Result.Summary)
		}
	}
}

// TestEnvelopeFromRun_NilRun — defensive nil-handling.
func TestEnvelopeFromRun_NilRun(t *testing.T) {
	env := EnvelopeFromRun(nil, "")
	if env.Success {
		t.Fatal("expected Success=false for nil run")
	}
	if env.Error.Kind != ErrorKindInternal {
		t.Errorf("Kind = %q, want %q", env.Error.Kind, ErrorKindInternal)
	}
}

// TestMarshalEnvelope_Shape verifies the JSON shape pinned by the
// ticket spec: success boolean + result/error siblings.
func TestMarshalEnvelope_Shape(t *testing.T) {
	env := NewSuccessEnvelope("run-1", "ok")
	jsonStr := MarshalEnvelope(env)

	// Re-parse to inspect.
	var decoded map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &decoded); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if decoded["success"] != true {
		t.Errorf("success = %v, want true", decoded["success"])
	}
	if _, ok := decoded["result"]; !ok {
		t.Error("expected result field on success envelope")
	}
	if _, ok := decoded["error"]; ok {
		t.Error("expected no error field on success envelope")
	}

	env = NewFailureEnvelope("run-2", ErrorKindTimeout, "ran too long",
		map[string]any{"role": "researcher"})
	jsonStr = MarshalEnvelope(env)
	decoded = nil
	if err := json.Unmarshal([]byte(jsonStr), &decoded); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if decoded["success"] != false {
		t.Errorf("success = %v, want false", decoded["success"])
	}
	if _, ok := decoded["result"]; ok {
		t.Error("expected no result field on failure envelope")
	}
	errBlock, ok := decoded["error"].(map[string]any)
	if !ok {
		t.Fatalf("error field = %T, want map[string]any", decoded["error"])
	}
	if errBlock["kind"] != ErrorKindTimeout {
		t.Errorf("error.kind = %v, want %q", errBlock["kind"], ErrorKindTimeout)
	}
	if errBlock["message"] != "ran too long" {
		t.Errorf("error.message = %v, want %q", errBlock["message"], "ran too long")
	}
	ctx, ok := errBlock["context"].(map[string]any)
	if !ok {
		t.Fatalf("error.context = %T, want map[string]any", errBlock["context"])
	}
	if ctx["run_id"] != "run-2" {
		t.Errorf("context[run_id] = %v, want %q", ctx["run_id"], "run-2")
	}
	if ctx["role"] != "researcher" {
		t.Errorf("context[role] = %v, want %q", ctx["role"], "researcher")
	}
}

// TestNewFailureEnvelope_NoToolUseIDFabrication asserts the envelope
// construction never invents a tool_use_id key in the context map.
// Sharp edge from CW-20260512-0122: failure envelope MUST NOT make up
// tool IDs (same trust class as SP-20260512-0007). This test pins
// that the constructor doesn't synthesize any tool_use_id-shaped
// field on its own — only fields the caller passes in are present.
func TestNewFailureEnvelope_NoToolUseIDFabrication(t *testing.T) {
	env := NewFailureEnvelope("run-x", ErrorKindTimeout, "ran too long", nil)
	if env.Error.Context == nil {
		t.Fatal("expected context map with run_id")
	}
	forbidden := []string{"tool_use_id", "tool_use_ids", "tool_id", "use_id"}
	for _, key := range forbidden {
		if _, present := env.Error.Context[key]; present {
			t.Errorf("envelope context fabricated forbidden key %q (value=%v)",
				key, env.Error.Context[key])
		}
	}
}

// TestNewFailureEnvelope_StripsForbiddenContextKeys pins the
// round-1 #1 defense: NewFailureEnvelope strips known tool_use_id-
// shaped keys from the caller-provided context map (case-
// insensitive) so a buggy or unsuspecting caller cannot violate
// the trust contract by accident. Same trust class as
// SP-20260512-0007 — see envelope.go's forbiddenContextKeys.
func TestNewFailureEnvelope_StripsForbiddenContextKeys(t *testing.T) {
	cases := []map[string]any{
		// Lowercase canonical forms.
		{"tool_use_id": "toolu_evil_lower", "role": "researcher"},
		{"tool_use_ids": []string{"toolu_a", "toolu_b"}, "role": "researcher"},
		{"tool_id": "toolu_evil_id", "role": "researcher"},
		// Mixed-case variants — defense is case-insensitive.
		{"Tool_Use_Id": "toolu_mixed", "role": "researcher"},
		{"TOOL_USE_IDS": []string{"toolu_upper"}, "role": "researcher"},
		// Sanity: a caller passing multiple forbidden keys gets ALL
		// stripped.
		{"tool_use_id": "a", "tool_use_ids": []string{"b"}, "tool_id": "c", "role": "researcher"},
	}
	for _, ctx := range cases {
		env := NewFailureEnvelope("run-defense", ErrorKindInternal, "test", ctx)
		if env.Error.Context == nil {
			t.Fatalf("input=%v: expected context with run_id + role at minimum", ctx)
		}
		for _, forbidden := range []string{"tool_use_id", "tool_use_ids", "tool_id",
			"Tool_Use_Id", "TOOL_USE_IDS"} {
			if v, present := env.Error.Context[forbidden]; present {
				t.Errorf("input=%v: forbidden key %q survived (value=%v) — defense did not strip",
					ctx, forbidden, v)
			}
		}
		// Allowed keys must still pass through.
		if env.Error.Context["role"] != "researcher" {
			t.Errorf("input=%v: role key dropped — defense over-stripped", ctx)
		}
		if env.Error.Context["run_id"] != "run-defense" {
			t.Errorf("input=%v: run_id key dropped — defense over-stripped", ctx)
		}
	}
}

// TestNewFailureEnvelope_RunIDEmptySkipped — when no run row was
// persisted (spawn rejected pre-insert), run_id key is omitted
// rather than emitted as empty string.
func TestNewFailureEnvelope_RunIDEmptySkipped(t *testing.T) {
	env := NewFailureEnvelope("", ErrorKindDenied, "untrusted role", nil)
	if env.Error.Context != nil {
		if _, present := env.Error.Context["run_id"]; present {
			t.Errorf("expected run_id absent when empty; got context=%+v", env.Error.Context)
		}
	}
}

// TestIsTimeoutErrorString — kind discrimination helper.
func TestIsTimeoutErrorString(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"context deadline exceeded", true},
		{"runner: timed out after 300s", true},
		{"TIMEOUT", true},
		{"deadline exceeded while reading", true},
		{"disk full", false},
		{"", false},
		{"panic: nil pointer", false},
	}
	for _, tc := range cases {
		if got := isTimeoutErrorString(tc.in); got != tc.want {
			t.Errorf("isTimeoutErrorString(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
