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
