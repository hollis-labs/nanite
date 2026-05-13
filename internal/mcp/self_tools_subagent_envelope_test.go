package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// newSubagentTestTransport wires a SelfToolsTransport with a *subagent.Service
// backed by an injected Runner. The store is a fresh file-backed SQLite DB
// with all nanite migrations applied — matches internal/subagent's test
// fixture so the run lifecycle persists end-to-end through the same paths
// production uses.
func newSubagentTestTransport(t *testing.T, runner subagent.Runner) *SelfToolsTransport {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(); _ = os.Remove(dbPath) })
	svc := subagent.NewService(s.DB, runner, nil, nil, nil)
	return &SelfToolsTransport{Store: s, Subagent: svc}
}

// failingRunner returns the configured error from Run — used to drive
// the StatusFailed path.
type failingRunner struct {
	err error
}

func (r failingRunner) Run(_ context.Context, _ *subagent.Run) (*subagent.Result, error) {
	return nil, r.err
}

// emptyReplyRunner completes (Status=completed) but writes no assistant
// text to the child session — used to drive ErrorKindEmptyReply.
type emptyReplyRunner struct{}

func (emptyReplyRunner) Run(_ context.Context, _ *subagent.Run) (*subagent.Result, error) {
	return &subagent.Result{Summary: "completed", ResultJSON: "{}"}, nil
}

// TestCallSpawnSubagent_NotConfigured_EmitsFailureEnvelope pins the
// wiring-miss path: when no subagent.Service is wired, the tool result
// is a failure envelope (not a textResult), so the LLM-side rule fires
// instead of the parent fabricating a success.
func TestCallSpawnSubagent_NotConfigured_EmitsFailureEnvelope(t *testing.T) {
	st := &SelfToolsTransport{} // No Subagent service.

	res, err := st.callSpawnSubagent(context.Background(), map[string]any{
		"parent_session_id": "sess-1",
		"parent_agent_id":   "primary",
		"role":              "researcher",
		"prompt":            "look at things",
	})
	if err != nil {
		t.Fatalf("callSpawnSubagent: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true when subagent service unwired")
	}

	env := parseEnvelopeFromResult(t, res)
	if env.Success {
		t.Errorf("expected success=false, got envelope=%+v", env)
	}
	if env.Error == nil {
		t.Fatal("expected error block on success=false envelope")
	}
	if env.Error.Kind != subagent.ErrorKindInternal {
		t.Errorf("error.kind = %q, want %q", env.Error.Kind, subagent.ErrorKindInternal)
	}
	if !strings.Contains(env.Error.Message, "subagent service not configured") {
		t.Errorf("error.message = %q, want substring of wiring-miss reason", env.Error.Message)
	}
}

// TestCallSpawnSubagent_RunnerFails_EmitsFailureEnvelope is the
// c160 turn-18 regression target: when the subagent runner fails
// mid-flight, the tool result is a failure envelope with the
// structured error.kind reflecting the failure cause. The parent
// reads success=false and the universal slot rule tells the LLM to
// acknowledge the failure, NOT narrate fake success over the
// (absent) child reply text.
//
// This is the structural fix for CW-20260512-0096 (parent fabricates
// success when subagents fail) per the harness-restoration design
// session.
func TestCallSpawnSubagent_RunnerFails_EmitsFailureEnvelope(t *testing.T) {
	st := newSubagentTestTransport(t, failingRunner{err: errors.New("path-grant denied: /tachyon-app")})

	res, err := st.callSpawnSubagent(context.Background(), map[string]any{
		"parent_session_id": "sess-1",
		"parent_agent_id":   "primary",
		"role":              "researcher",
		"prompt":            "set up tachyon-app",
		"mode":              subagent.ModeSync,
	})
	if err != nil {
		t.Fatalf("callSpawnSubagent: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true on failed subagent run")
	}

	env := parseEnvelopeFromResult(t, res)
	if env.Success {
		t.Errorf("expected success=false for failed runner, got envelope=%+v (text=%q)", env, readToolText(t, res))
	}
	if env.Error == nil {
		t.Fatal("expected error block on success=false envelope")
	}
	if env.Error.Kind != subagent.ErrorKindInternal {
		t.Errorf("error.kind = %q, want %q", env.Error.Kind, subagent.ErrorKindInternal)
	}
	if !strings.Contains(env.Error.Message, "path-grant denied") {
		t.Errorf("error.message = %q, want runner-error passthrough", env.Error.Message)
	}
	// The error.context must carry run_id but MUST NOT carry any
	// fabricated tool_use_id (CW-20260512-0122 sharp edge — same trust
	// class as SP-20260512-0007).
	if env.Error.Context == nil {
		t.Fatal("expected non-nil error.context with run_id")
	}
	if _, ok := env.Error.Context["run_id"]; !ok {
		t.Error("expected run_id in error.context")
	}
	for _, forbidden := range []string{"tool_use_id", "tool_use_ids", "tool_id"} {
		if v, ok := env.Error.Context[forbidden]; ok {
			t.Errorf("envelope fabricated forbidden context key %q=%v (sharp edge: don't make up tool_use_ids)",
				forbidden, v)
		}
	}
}

// TestCallSpawnSubagent_EmptyReply_EmitsFailureEnvelope — a completed
// run with no assistant text is treated as a soft failure so the
// parent does not narrate a non-existent reply. The c160 fabrication
// chain shows this is the dangerous shape: child completes, has no
// text to surface, parent fills the gap with synthesized prose.
func TestCallSpawnSubagent_EmptyReply_EmitsFailureEnvelope(t *testing.T) {
	st := newSubagentTestTransport(t, emptyReplyRunner{})

	res, err := st.callSpawnSubagent(context.Background(), map[string]any{
		"parent_session_id": "sess-1",
		"parent_agent_id":   "primary",
		"role":              "researcher",
		"prompt":            "do the thing",
		"mode":              subagent.ModeSync,
	})
	if err != nil {
		t.Fatalf("callSpawnSubagent: %v", err)
	}

	env := parseEnvelopeFromResult(t, res)
	if env.Success {
		t.Errorf("expected success=false for completed-but-empty reply, got envelope=%+v", env)
	}
	if env.Error == nil || env.Error.Kind != subagent.ErrorKindEmptyReply {
		t.Errorf("error.kind = %v, want %q",
			func() any {
				if env.Error == nil {
					return "<nil>"
				}
				return env.Error.Kind
			}(),
			subagent.ErrorKindEmptyReply)
	}
}

// TestCallSpawnSubagent_AsyncMode_AcksWithSuccessEnvelope pins the
// async/api branch: the spawn ack is success=true with the run_id, so
// the parent can poll subagent_status. Async-run failures surface
// through subagent_status, not the spawn return.
func TestCallSpawnSubagent_AsyncMode_AcksWithSuccessEnvelope(t *testing.T) {
	st := newSubagentTestTransport(t, subagent.EchoRunner{})

	res, err := st.callSpawnSubagent(context.Background(), map[string]any{
		"parent_session_id": "sess-1",
		"parent_agent_id":   "primary",
		"role":              "researcher",
		"prompt":            "fire and forget",
		"mode":              subagent.ModeAsync,
	})
	if err != nil {
		t.Fatalf("callSpawnSubagent: %v", err)
	}
	if res.IsError {
		t.Error("expected IsError=false on async spawn ack")
	}

	env := parseEnvelopeFromResult(t, res)
	if !env.Success {
		t.Fatalf("expected success=true on async spawn ack, got %+v", env)
	}
	if env.Result == nil || env.Result.RunID == "" {
		t.Errorf("expected non-empty result.run_id on async spawn ack, got %+v", env.Result)
	}
	// async ack carries no summary — the parent must poll for the reply.
	if env.Result != nil && env.Result.Summary != "" {
		t.Errorf("expected empty result.summary on async ack, got %q", env.Result.Summary)
	}

	// Async mode runs the runner in a goroutine; wait for it to land in
	// a terminal state so t.Cleanup's DB-close doesn't race the
	// finalize write (the test's previous run surfaced a benign
	// "sql: database is closed" WARN from that race).
	waitForRunTerminal(t, st, env.Result.RunID)
}

// waitForRunTerminal polls subagent_status until the run reaches a
// terminal state, so async-mode tests can let the runner finish
// before t.Cleanup tears down the DB.
func waitForRunTerminal(t *testing.T, st *SelfToolsTransport, runID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		run, err := st.Subagent.Status(context.Background(), runID)
		if err == nil && run != nil {
			switch run.Status {
			case subagent.StatusCompleted, subagent.StatusFailed, subagent.StatusCancelled, subagent.StatusRejected:
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Logf("waitForRunTerminal: run %s did not reach terminal state within deadline (likely a benign cleanup race)", runID)
}

// TestCallSpawnSubagent_ValidationError_EmitsFailureEnvelope pins the
// spawn-stage rejection path: when Spawn returns an error (missing
// fields, untrusted role, etc.), the envelope carries success=false
// with kind=internal (or kind=denied for trust-untrusted).
func TestCallSpawnSubagent_ValidationError_EmitsFailureEnvelope(t *testing.T) {
	st := newSubagentTestTransport(t, subagent.EchoRunner{})

	// Missing role triggers spawn-side validation error.
	res, err := st.callSpawnSubagent(context.Background(), map[string]any{
		"parent_session_id": "sess-1",
		"parent_agent_id":   "primary",
		// "role" intentionally omitted.
		"prompt": "do something",
	})
	if err != nil {
		t.Fatalf("callSpawnSubagent: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true on validation rejection")
	}

	env := parseEnvelopeFromResult(t, res)
	if env.Success {
		t.Errorf("expected success=false on validation rejection, got %+v", env)
	}
	if env.Error == nil {
		t.Fatal("expected error block")
	}
	if !strings.Contains(env.Error.Message, "role") {
		t.Errorf("error.message = %q, want passthrough of validation reason", env.Error.Message)
	}
}

// TestCallSpawnSubagent_C160TurnEighteenReproduction is the explicit
// regression smoke for the c160 turn-18 "set up Tachyon" fabrication
// chain. Pre-CW-20260512-0122, a failing subagent returned its last
// assistant text (or nothing) indistinguishable from a successful
// run; the parent narrated "I've successfully set up the Tachyon
// project". Post-CW-20260512-0122, the tool result carries
// success=false with a structured error.kind, and the universal slot
// rule "Acknowledge subagent failure" requires the parent LLM to
// acknowledge the failure rather than narrate success.
//
// This test pins the wire-side contract (the envelope shape the LLM
// reads). The LLM-side rule is asserted in internal/chat's
// universal_rules_test.go.
func TestCallSpawnSubagent_C160TurnEighteenReproduction(t *testing.T) {
	// Simulate the c160 turn-18 conditions: parent dispatches a
	// "set up tachyon-app" subagent, the runner fails because the
	// path-access substrate denies the write.
	st := newSubagentTestTransport(t, failingRunner{
		err: errors.New("path access denied: /tachyon-app (no path_grant for child session)"),
	})

	res, err := st.callSpawnSubagent(context.Background(), map[string]any{
		"parent_session_id": "sess-1",
		"parent_agent_id":   "chat-primary",
		"role":              "worker",
		"prompt":            "set up the Tachyon project at /tachyon-app",
		"mode":              subagent.ModeSync,
	})
	if err != nil {
		t.Fatalf("callSpawnSubagent: %v", err)
	}

	// 1) Wire-side: ToolResult.IsError must be true so the chat-tool
	// executor's existing failure path lights up.
	if !res.IsError {
		t.Error("c160 reproduction: tool result IsError=false — fail to mark failure on the wire")
	}

	// 2) Structural: envelope success=false.
	env := parseEnvelopeFromResult(t, res)
	if env.Success {
		t.Errorf("c160 reproduction: envelope.success=true on failed subagent — parent will narrate fake success. Envelope: %+v", env)
	}

	// 3) Honest: error.message contains the actual failure reason.
	if env.Error == nil {
		t.Fatal("c160 reproduction: missing error block — parent has nothing structured to acknowledge")
	}
	if !strings.Contains(env.Error.Message, "path access denied") {
		t.Errorf("c160 reproduction: error.message = %q, want passthrough of denial reason", env.Error.Message)
	}

	// 4) No-fabrication: no synthesized success body alongside the failure.
	if env.Result != nil {
		t.Errorf("c160 reproduction: envelope carries a Result block alongside the error — this is the exact shape that lets the parent narrate fake success. Envelope: %+v", env)
	}

	// 5) Tool-id integrity: failure envelope must NOT make up tool_use_ids
	// (CW-20260512-0122 sharp edge; same trust class as SP-20260512-0007).
	if env.Error.Context != nil {
		for _, forbidden := range []string{"tool_use_id", "tool_use_ids", "tool_id"} {
			if v, ok := env.Error.Context[forbidden]; ok {
				t.Errorf("c160 reproduction: envelope fabricated forbidden context key %q=%v", forbidden, v)
			}
		}
	}
}

// parseEnvelopeFromResult marshals the ToolResult's text body as a
// ResultEnvelope. Helper for envelope-shape assertions across the
// subagent tests.
func parseEnvelopeFromResult(t *testing.T, res *ToolResult) subagent.ResultEnvelope {
	t.Helper()
	if res == nil {
		t.Fatal("nil ToolResult")
	}
	if len(res.Content) == 0 {
		t.Fatal("ToolResult has no content")
	}
	text := res.Content[0].Text
	var env subagent.ResultEnvelope
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("ToolResult text is not a ResultEnvelope JSON document: %v (text=%q)", err, text)
	}
	return env
}
