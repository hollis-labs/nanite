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

	// ErrorKindConfig indicates the spawn was rejected at the Spawn
	// boundary because of a configuration fault — the role has no
	// registered agent profile, or the resolved profile is
	// can_execute=false and not in the text-only role whitelist
	// (CW-20260519-0123). Distinct from ErrorKindDenied (deliberate
	// trust refusal) and ErrorKindInternal (runtime fault): a config
	// error is fixable by registering the missing profile or routing
	// to a known role. The error.context carries `role` and (where
	// applicable) `reason` so the parent can surface or retry with a
	// known role.
	ErrorKindConfig = "config"
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
// (e.g. spawn validation rejection).
//
// Trust contract: the constructor strips known tool_use_id-shaped
// keys from the caller-provided context map defensively (case-
// insensitive). Failure envelopes MUST NOT carry fabricated tool IDs
// — same trust class as SP-20260512-0007 (per CW-20260512-0122
// sharp edge). Pre-round-1 the docstring declared "MUST NOT" but
// copied every key verbatim, so a buggy caller could violate the
// contract silently. Now the constructor enforces it: any key in
// forbiddenContextKeys (tool_use_id, tool_use_ids, tool_id) is
// silently dropped during copy. The defense is belt-and-braces with
// the call-site discipline of only populating from authoritative
// run state (status, role, run_id).
func NewFailureEnvelope(runID, kind, message string, context map[string]any) ResultEnvelope {
	ctx := map[string]any{}
	for k, v := range context {
		if isForbiddenContextKey(k) {
			// Defense in depth: drop silently. A future caller who
			// accidentally threads a tool_use_id through this map
			// (the c160 turn-18 trust class) gets the same protection
			// as the existing call sites without needing to remember
			// the contract.
			continue
		}
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

// forbiddenContextKeys is the set of map keys NewFailureEnvelope
// strips defensively (case-insensitive). Stable list — same trust
// class as SP-20260512-0007 and CW-20260512-0122 sharp edge.
var forbiddenContextKeys = []string{
	"tool_use_id",
	"tool_use_ids",
	"tool_id",
}

// isForbiddenContextKey returns true if the key (case-insensitive)
// matches any entry in forbiddenContextKeys. Used by
// NewFailureEnvelope to strip tool_use_id-shaped keys from caller-
// provided context maps.
func isForbiddenContextKey(k string) bool {
	for _, forbidden := range forbiddenContextKeys {
		if equalFold(k, forbidden) {
			return true
		}
	}
	return false
}

// equalFold is a tiny case-insensitive string equality without
// importing strings — matches the in-file containsFold helper's
// rationale (small isolated package, keep the diff minimal).
func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca = ca + 32
		}
		if 'A' <= cb && cb <= 'Z' {
			cb = cb + 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// EnvelopeFromRun builds an envelope from a *Run row and the
// recovered assistant summary. The returned envelope's Success flag is
// derived from run.Status, enumerated explicitly here so the
// terminal-vs-non-terminal split is not implicit:
//
// Terminal states (run has reached a final outcome):
//
//   - StatusCompleted + non-empty summary → Success=true,
//     Result.Summary = summary (the child's last assistant text).
//   - StatusCompleted + empty summary     → Success=false,
//     ErrorKindEmptyReply. The run finished normally but produced no
//     assistant text the parent can surface — soft failure so the
//     parent does not narrate a non-existent reply (c160 turn-18
//     regression target).
//   - StatusFailed                        → Success=false,
//     ErrorKindTimeout if run.Error matches a timeout/deadline string,
//     otherwise ErrorKindInternal. The runner error string is
//     propagated as the message.
//   - StatusCancelled                     → Success=false,
//     ErrorKindCancelled. Distinct from timeout — operator-driven.
//   - StatusRejected                      → Success=false,
//     ErrorKindDenied. Includes run.RejectionReason in the message
//     when present.
//
// Non-terminal states (spawn accepted, run still in flight):
//
//   - StatusRequested / StatusApproved    → Success=true,
//     Result.RunID = run.ID, Result.Summary = "awaiting approval".
//     This is NOT a failure — the spawn is gated on human approval and
//     the reply will land asynchronously (via subagent_status polling
//     or the inbox). Mapping this to Success=false would cause
//     callSpawnSubagent's sync path to set ToolResult.IsError=true for
//     every approval-gated spawn, even though nothing has failed.
//     Pending-approval IS the design (SubagentApprovalRequired
//     defaults true in production); the envelope must communicate
//     "awaiting" not "failed".
//
// Fallback:
//
//   - any other status                    → Success=false,
//     ErrorKindInternal. Defensive — should not occur with the
//     enumerated status set.
//
// For async/api modes that ack immediately at spawn time, callers
// should use NewSuccessEnvelope directly with the run ID instead of
// routing through EnvelopeFromRun — the run has not yet terminated so
// this constructor would map StatusRunning to ErrorKindInternal.
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
		// approval is still pending. NOT a failure — the spawn was
		// accepted, the runner has not run yet, and the eventual reply
		// will land via subagent_status polling or the inbox. Surfaces
		// as success=true with Result.RunID populated and Result.Summary
		// set to a literal "awaiting approval" so the LLM-visible body
		// communicates the non-terminal state explicitly and the parent
		// is guided toward subagent_status polling.
		//
		// Pre-round-1 this case returned Success=false with
		// ErrorKindDenied. That mapping caused callSpawnSubagent's sync
		// path (envelopeResult IsError = !env.Success) to set
		// ToolResult.IsError=true on every approval-gated spawn —
		// including the production default where SubagentApprovalRequired
		// is true — even though nothing had failed. The fix lives here
		// in the envelope mapping so the sync-path branching does not
		// need to special-case pending approval.
		return NewSuccessEnvelope(run.ID,
			fmt.Sprintf("awaiting approval (status %q); poll subagent_status for the eventual reply", run.Status))
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
