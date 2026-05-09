package dispatch

import (
	"context"
	"errors"

	"github.com/hollis-labs/nanite/internal/learnings"
)

// ExecutorRequest is the payload the Chat agent sends to dispatch an
// executor (B1 design — docs/architecture/executor-handoff.md §2). Carries
// intent + minimum-viable context, NOT the chat transcript or the Chat
// agent's full message history.
//
// The classifier (B2 — CW-20260429-0031) populates Intent from the user
// message; the Chat agent does not free-text it.
//
// Initial vocabulary (B3 pilot):
//
//	"render_envelope" — produce one of the v1 passive-renderable envelope types.
//
// Future intents land as additional executor profiles register (Phase 3 of
// the rollout in B1 §6).
type ExecutorRequest struct {
	// Intent is the executor-recognized verb. Registry-controlled —
	// unregistered intents fail fast with ExecutorFailureInvalidIntent.
	Intent string `json:"intent"`

	// TargetEnvelopeType is the envelope type slug when Intent is
	// "render_envelope" (e.g. "report-card", "list-card"). Empty for
	// intents that do not produce an envelope. The classifier may leave
	// this empty; the executor's first step is then to pick the type.
	TargetEnvelopeType string `json:"target_envelope_type,omitempty"`

	// UserRequest is the verbatim user message that motivated the
	// dispatch. The executor reads this, NOT the Chat agent's
	// paraphrase. Preserves the user's exact framing for grounding.
	UserRequest string `json:"user_request"`

	// Data is the optional pre-resolved envelope payload the dispatching
	// caller has already fetched/synthesized. When present and the
	// intent is "render_envelope", the executor validates this against
	// the per-type schema and emits without a re-fetch.
	//
	// B3 pilot note: the in-process renderer accepts Data inline. Future
	// LLM-driven executor sessions will instead read from ContextHandles
	// and/or call data tools themselves; Data is the deterministic
	// pilot's fast path.
	Data map[string]any `json:"data,omitempty"`

	// Sources is the optional grounding citation list that the
	// dispatching caller already has. When the executor receives a
	// "report-card" or "document-viewer" type with Sources set, it
	// stamps them onto data["sources"] post-validation (see B3 c119
	// judgment: source citations must be semantically aligned).
	Sources []ExecutorSource `json:"sources,omitempty"`

	// ContextHandles names the slices of session state the executor is
	// allowed to read. Each handle is opaque to the Chat agent — it
	// points at scratchpad keys, message-window slices, or memory-recall
	// result IDs the executor can fetch through its own tool surface.
	// Empty = executor starts from scratch.
	ContextHandles []ContextHandle `json:"context_handles,omitempty"`

	// SessionID is the dispatching session. The executor inherits this
	// session's path-grants via the lineage walk (B1 §4).
	SessionID string `json:"session_id"`

	// SyntheticAllowed is the disclosure-aware demo-intent flag. The
	// classifier sets this true when the user's request is recognizably
	// synthetic ("demo", "test", "sketch", "example") and the executor
	// may synthesize realistic placeholder content with a
	// "this is synthesized" disclosure in the response Summary.
	//
	// When false, the executor must ground in real tool output or fail
	// with ExecutorFailureMissingContext. (B3 c119 judgment: empty data
	// tool result is a signal to pivot, not to render with empty
	// grounding.)
	SyntheticAllowed bool `json:"synthetic_allowed,omitempty"`
}

// ExecutorSource is a single grounding citation. tool_use_id and
// tool_name must match a tool call in the dispatching turn whose result
// actually provided the cited content. The executor passes these through
// to the envelope's data["sources"] field; the per-type schemas validate
// the rest.
type ExecutorSource struct {
	ToolUseID string `json:"tool_use_id"`
	ToolName  string `json:"tool_name"`
	Note      string `json:"note,omitempty"`
}

// ContextHandle is a typed pointer to a slice of session state. The
// Source determines which read API resolves it.
type ContextHandle struct {
	// Source is one of "scratchpad", "memory", "message_window".
	// Other values are accepted but the in-process pilot does not
	// resolve them — they pass through unchanged for future executor
	// implementations.
	Source string `json:"source"`
	// Key is source-specific (scratchpad key, memory ID, etc.).
	Key string `json:"key"`
}

// ExecutorResponse is the single envelope returned to the Chat agent.
// One response per dispatch — no streaming, no multi-message reply.
type ExecutorResponse struct {
	// Result is the executor's primary output as free-form text. Empty
	// when Envelope is set and the Chat agent should rely on the
	// envelope's render path.
	Result string `json:"result,omitempty"`

	// Envelope is the structured card the executor produced. Optional;
	// populated for "render_envelope" intent. The envelope is fully
	// validated and (if applicable) repair-stamped by the executor
	// before this response is sent — the Chat agent does NOT
	// re-validate.
	//
	// Uses dispatch.Envelope (the projection of chat.Envelope) to keep
	// this package importable from internal/mcp without an import
	// cycle. The service layer maps to chat.Envelope at the seam.
	Envelope *Envelope `json:"envelope,omitempty"`

	// Lessons are repair-hints the executor learned during the flow.
	// Informational / telemetry only — the executor persists them via
	// lesson_capture itself before responding (B1 §3 lens placement).
	// The Chat agent does NOT call lesson_capture (it no longer has
	// the tool). Field is kept for narration ("I learned X") and
	// observability, not for persistence.
	Lessons []learnings.Hint `json:"lessons,omitempty"`

	// Summary is a one-paragraph account of what the executor did, what
	// got coerced/repaired, and what (if anything) was deferred. The
	// Chat agent narrates this to the user verbatim or in condensed
	// form. Mirrors the "repair always informs the caller" contract
	// from agentic-error-recovery.md.
	Summary string `json:"summary"`

	// Failure is set when the executor could not produce a valid result
	// within its budget. Carries a typed code + message; the Chat
	// agent's response is to surface the failure to the user (NOT to
	// retry the dispatch — see B1 §5).
	Failure *ExecutorFailure `json:"failure,omitempty"`
}

// ExecutorFailure carries a typed failure code and message back to the
// Chat agent.
type ExecutorFailure struct {
	Code    ExecutorFailureCode `json:"code"`
	Message string              `json:"message"`
	// PartialEnvelope is set when the executor produced an envelope
	// that failed final validation. The Chat agent can still render it
	// as a degraded card if appropriate, with a banner indicating the
	// failure.
	PartialEnvelope *Envelope `json:"partial_envelope,omitempty"`
}

// ExecutorFailureCode is the typed failure taxonomy from B1 §5.
type ExecutorFailureCode string

const (
	// ExecutorFailureBudgetExhausted — executor ran out of turns/time.
	ExecutorFailureBudgetExhausted ExecutorFailureCode = "budget_exhausted"
	// ExecutorFailureUnrecoverable — validator returned a hard error
	// outside the recoverable taxonomy.
	ExecutorFailureUnrecoverable ExecutorFailureCode = "unrecoverable"
	// ExecutorFailureInvalidIntent — classifier emitted an intent the
	// executor doesn't recognize. Chat agent falls back to chat-direct
	// handling.
	ExecutorFailureInvalidIntent ExecutorFailureCode = "invalid_intent"
	// ExecutorFailureMissingContext — executor needed a context handle
	// the Chat agent didn't provide. Chat agent narrates "I need more
	// context — could you share X?" and re-dispatches once the user
	// replies.
	ExecutorFailureMissingContext ExecutorFailureCode = "missing_context"
	// ExecutorFailureTrustDenied — executor profile is untrusted for
	// this workspace. Chat agent narrates the denial.
	ExecutorFailureTrustDenied ExecutorFailureCode = "trust_denied"
)

// Executor is the narrow surface DispatchExecutor uses to invoke a
// registered executor implementation. Each executor profile registers
// one Executor that recognizes its intent vocabulary.
//
// The B3 pilot ships one implementation —
// internal/executor/envelope_render — which recognizes the
// "render_envelope" intent and produces v1 passive-renderable
// envelopes.
type Executor interface {
	// Intents returns the intent vocabulary this executor recognizes.
	// DispatchExecutor uses this list to route requests.
	Intents() []string

	// Execute runs the executor flow for the given request and returns
	// the response. Implementations should return a non-nil
	// *ExecutorResponse even on failure (with Failure set) so the Chat
	// agent always sees a typed result rather than a Go error. Returning
	// an error from Execute is reserved for harness/wiring failures
	// (e.g. nil dependencies) — agent-visible failures go in the
	// response's Failure field.
	Execute(ctx context.Context, req ExecutorRequest) (*ExecutorResponse, error)
}

// ErrExecutorNotRegistered is returned by DispatchExecutor when no
// registered executor recognizes the request's Intent. Indicates a
// classifier coverage gap (B1 §5: surfaces as ExecutorFailureInvalidIntent
// to the Chat agent, but the wiring layer typically promotes Go errors
// to typed failures itself).
var ErrExecutorNotRegistered = errors.New("dispatch: no executor registered for intent")

// DispatchExecutor is the in-process executor entry seam. It looks up
// the Executor that recognizes req.Intent and invokes Execute. Returns
// the executor's response unchanged.
//
// The B3 pilot accepts a single Executor; future revisions will replace
// this signature with a registry. Keeping the seam narrow lets B2
// (CW-20260429-0031) wire chat_generate.go without committing to a
// specific registry shape.
//
// Wiring (B2 future): chat_generate.go calls
// dispatch.DispatchExecutor(ctx, executor, req) when the classifier
// emits a "render_envelope" intent. The executor argument is provided
// by the service-layer composition root.
//
// Returns:
//   - non-nil *ExecutorResponse with Failure set when the executor
//     reports an agent-visible failure (the typical case);
//   - nil *ExecutorResponse with a Go error only on harness-level
//     wiring problems (nil executor, intent mismatch).
func DispatchExecutor(ctx context.Context, executor Executor, req ExecutorRequest) (*ExecutorResponse, error) {
	if executor == nil {
		return nil, errors.New("dispatch: executor is nil")
	}
	if req.Intent == "" {
		return &ExecutorResponse{
			Failure: &ExecutorFailure{
				Code:    ExecutorFailureInvalidIntent,
				Message: "intent is required",
			},
			Summary: "Dispatch failed: empty intent.",
		}, nil
	}
	known := false
	for _, intent := range executor.Intents() {
		if intent == req.Intent {
			known = true
			break
		}
	}
	if !known {
		return &ExecutorResponse{
			Failure: &ExecutorFailure{
				Code:    ExecutorFailureInvalidIntent,
				Message: "no executor recognizes intent " + req.Intent,
			},
			Summary: "Dispatch failed: unknown intent.",
		}, nil
	}
	return executor.Execute(ctx, req)
}
