package subagent

import (
	"encoding/json"
	"fmt"
)

// ResultEnvelope is the structured response shape returned to the parent
// agent when a subagent_spawn tool call completes (sync mode) or is
// acknowledged (async/api modes). The envelope's JSON encoding is the
// **text content** of the MCP ToolResult — readable directly by the LLM.
//
// The contract is: every subagent_spawn tool result carries an
// envelope with an explicit Success boolean. When Success=true, Result
// holds the subagent's reply (sync) or the run_id (async/api). When
// Success=false, Error holds a structured Kind + Message + Context so
// the parent can acknowledge the failure honestly instead of narrating
// fake success over a fabricated child reply.
//
// This is the structural fix for the c160 turn-18 reproduction
// (CW-20260512-0096): a sync subagent whose run.Status terminated as
// failed/cancelled/rejected previously returned its last assistant text
// to the parent indistinguishable from a successful run. The universal
// slot rule ("if subagent.success=false, acknowledge the failure; do
// not narrate it as success") closes the LLM-side trust contract; this
// envelope is the wire-side contract the rule references.
//
// Pre-launch: this is a clean-break — there is no compat shim for the
// pre-envelope `textResult(summary)` shape (per feedback_no_compat_shims).
// Every consumer parsing subagent_spawn results MUST treat the result
// as a ResultEnvelope JSON document.
type ResultEnvelope struct {
	// Success is the load-bearing flag. true = the subagent completed
	// and the reply is in Result. false = the subagent failed and the
	// structured reason is in Error.
	Success bool `json:"success"`

	// Result carries the subagent's output on success. Mode-dependent:
	//   - sync:  Result.Summary holds the child's last assistant text.
	//            Result.RunID holds the persisted run ID.
	//   - async: Result.RunID holds the run ID for polling via
	//            subagent_status; Result.Summary is empty (parent must
	//            poll for the eventual reply landing in the inbox).
	//   - api:   identical to async — RunID present, Summary empty.
	// Nil when Success=false.
	Result *Reply `json:"result,omitempty"`

	// Error carries the structured failure detail. Nil when Success=true.
	Error *EnvelopeError `json:"error,omitempty"`
}

// Reply is the success-path payload of a ResultEnvelope.
type Reply struct {
	// RunID is the subagent_runs row ID. Always populated.
	RunID string `json:"run_id"`
	// Summary is the subagent's prose reply (sync mode). Empty for
	// async/api modes where the parent receives the reply
	// out-of-band via the inbox / chat channel.
	Summary string `json:"summary,omitempty"`
}

// EnvelopeError is the failure-path payload of a ResultEnvelope.
type EnvelopeError struct {
	// Kind is the machine-readable failure category. Stable enum —
	// see Error* constants below.
	Kind string `json:"kind"`
	// Message is a human-readable explanation. The LLM reads this to
	// describe the failure to the user.
	Message string `json:"message"`
	// Context carries structured detail keyed by string. Populated
	// fields depend on Kind (run_id is always present when known).
	Context map[string]any `json:"context,omitempty"`
}

// Failure kinds. These are the machine-readable values for
// ResultEnvelope.Error.Kind. Stable strings — agents and tests can pin
// on these values.
const (
	// ErrorKindTimeout indicates the subagent runner exceeded the
	// configured timeout (default 300s; per-spawn override via
	// TimeoutSeconds). Run row status = "failed" with an error string
	// referencing context deadline / timeout.
	ErrorKindTimeout = "timeout"

	// ErrorKindDenied indicates the spawn was refused before the
	// runner started. Reasons include trust resolution returning
	// TrustUntrusted, approval rejection, approval timeout, or
	// missing role / profile.
	ErrorKindDenied = "denied"

	// ErrorKindCancelled indicates the run was cancelled
	// mid-flight (Cancel API or parent-side abort). Distinct from
	// timeout — cancellation is operator-driven, timeout is policy.
	ErrorKindCancelled = "cancelled"

	// ErrorKindInternal is the catch-all for runner failures that
	// don't fit a more specific bucket (panic, child-session
	// creation failure, persistence error, generic runner error).
	// The error message carries the specific reason.
	ErrorKindInternal = "internal"

	// ErrorKindEmptyReply indicates the run completed (Status =
	// "completed") but produced no assistant text the parent can
	// surface. This is a soft-failure — the run terminated normally
	// but provided nothing actionable. Treated as success=false so
	// the parent does not narrate a non-existent reply.
	ErrorKindEmptyReply = "empty_reply"
)

// NewSuccessEnvelope builds a Success=true envelope. summary may be
// empty for async/api modes (the parent receives the reply
// out-of-band).
func NewSuccessEnvelope(runID, summary string) ResultEnvelope {
	return ResultEnvelope{
		Success: true,
		Result: &Reply{
			RunID:   runID,
			Summary: summary,
		},
	}
}

// NewFailureEnvelope builds a Success=false envelope. runID may be
// empty when the failure occurred before a run row was persisted
// (e.g. spawn validation rejection). The context map MUST NOT carry
// tool_use_ids — same trust class as SP-20260512-0007 (per
// CW-20260512-0122 sharp edge).
func NewFailureEnvelope(runID, kind, message string, context map[string]any) ResultEnvelope {
	ctx := map[string]any{}
	for k, v := range context {
		ctx[k] = v
	}
	if runID != "" {
		ctx["run_id"] = runID
	}
	if len(ctx) == 0 {
		ctx = nil
	}
	return ResultEnvelope{
		Success: false,
		Error: &EnvelopeError{
			Kind:    kind,
			Message: message,
			Context: ctx,
		},
	}
}

// EnvelopeFromRun builds an envelope from a terminal *Run row. The
// summary is the prose recovered from the child session's last
// assistant text (mode=sync) or empty (mode=async/api). The
// returned envelope's Success flag is derived from run.Status:
//
//   - StatusCompleted + non-empty summary → Success=true
//   - StatusCompleted + empty summary     → Success=false, ErrorKindEmptyReply
//   - StatusFailed                        → Success=false, ErrorKindTimeout
//                                            if error string mentions
//                                            context deadline /
//                                            timeout, otherwise
//                                            ErrorKindInternal
//   - StatusCancelled                     → Success=false, ErrorKindCancelled
//   - StatusRejected                      → Success=false, ErrorKindDenied
//   - StatusRequested / StatusApproved    → Success=false, ErrorKindDenied
//     (non-terminal; should not happen for sync-mode callers but the
//     envelope must be honest about the wedged state)
//   - any other status                    → Success=false, ErrorKindInternal
//
// For async/api modes, callers should use NewSuccessEnvelope directly
// at spawn time with the run ID — the run has not yet terminated so
// this constructor would always report empty_reply or running.
func EnvelopeFromRun(run *Run, summary string) ResultEnvelope {
	if run == nil {
		return NewFailureEnvelope("", ErrorKindInternal, "subagent run not found", nil)
	}
	switch run.Status {
	case StatusCompleted:
		if summary == "" {
			return NewFailureEnvelope(run.ID, ErrorKindEmptyReply,
				"subagent completed but returned no assistant text",
				map[string]any{
					"role":   run.Role,
					"status": run.Status,
				})
		}
		return NewSuccessEnvelope(run.ID, summary)
	case StatusFailed:
		kind := ErrorKindInternal
		if isTimeoutErrorString(run.Error) {
			kind = ErrorKindTimeout
		}
		msg := run.Error
		if msg == "" {
			msg = "subagent runner failed without a captured error reason"
		}
		return NewFailureEnvelope(run.ID, kind, msg, map[string]any{
			"role":   run.Role,
			"status": run.Status,
		})
	case StatusCancelled:
		return NewFailureEnvelope(run.ID, ErrorKindCancelled,
			"subagent run was cancelled before completion",
			map[string]any{
				"role":   run.Role,
				"status": run.Status,
			})
	case StatusRejected:
		msg := "subagent spawn was rejected"
		if run.RejectionReason != "" {
			msg = msg + ": " + run.RejectionReason
		}
		return NewFailureEnvelope(run.ID, ErrorKindDenied, msg,
			map[string]any{
				"role":   run.Role,
				"status": run.Status,
			})
	case StatusRequested, StatusApproved:
		// Gated approval path: Spawn returned the run.ID while the human
		// approval is still pending. Not a failure of the subagent
		// itself — just that no reply exists yet. Surfaces as
		// success=false so the parent does not narrate a non-existent
		// reply; kind=denied because the parent should treat the spawn
		// as "blocked, waiting" rather than retry.
		return NewFailureEnvelope(run.ID, ErrorKindDenied,
			fmt.Sprintf("subagent spawn is awaiting approval (status %q); poll subagent_status for the eventual reply", run.Status),
			map[string]any{
				"role":   run.Role,
				"status": run.Status,
			})
	default:
		return NewFailureEnvelope(run.ID, ErrorKindInternal,
			fmt.Sprintf("subagent run in unknown state %q", run.Status),
			map[string]any{
				"role":   run.Role,
				"status": run.Status,
			})
	}
}

// MarshalEnvelope returns the canonical JSON encoding for transport.
// On marshal error (which should be impossible for the struct shapes
// above) returns a degraded JSON literal that still satisfies the
// success=false contract — better to report a malformed envelope than
// to fabricate a success string.
func MarshalEnvelope(env ResultEnvelope) string {
	b, err := json.Marshal(env)
	if err != nil {
		return `{"success":false,"error":{"kind":"internal","message":"envelope marshal failed"}}`
	}
	return string(b)
}

// isTimeoutErrorString returns true when the captured runner error
// string looks like a context deadline / timeout failure. Used to
// pick between ErrorKindTimeout and ErrorKindInternal for
// StatusFailed runs.
func isTimeoutErrorString(s string) bool {
	if s == "" {
		return false
	}
	for _, needle := range []string{
		"context deadline exceeded",
		"deadline exceeded",
		"timeout",
		"timed out",
	} {
		if containsFold(s, needle) {
			return true
		}
	}
	return false
}

func containsFold(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	if len(s) < len(sub) {
		return false
	}
	// Cheap case-insensitive contains without importing strings (this
	// file is in a tiny package; avoiding the import keeps the diff
	// small for an isolated helper).
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a := s[i+j]
			b := sub[j]
			if 'A' <= a && a <= 'Z' {
				a = a + 32
			}
			if 'A' <= b && b <= 'Z' {
				b = b + 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
