// Package dispatcher is the single agent-flavored dispatch door. All
// callers that need to run one assistant turn against the unified
// agent-sessions runner (chat, subagent, background-agent) route
// through Dispatcher.Run so the broker invocation order, the slot
// assembly, and the wire payload shape are identical across CallerTypes.
//
// Architectural intent (CW-20260512-0121, SP-20260512-0011):
//
//   - One door, three CallerTypes. Per-site assembly is gone — the
//     Context Broker, Tool Broker, and Skill Broker fire from inside
//     the runner (chat_generate.go → assembleTurnContext → AssembleSlots).
//     Dispatcher is the named boundary callers MUST go through; it
//     stamps CallerType so the request_build slog can prove identical
//     structural slot shape across dispatch types.
//
//   - The runner stays untouched. agent-sessions (chat_generate's
//     generateResponse path) is the single runner; this package
//     consolidates the callers, not the runner. The single-runner
//     constraint is the load-bearing sharp edge from the ticket.
//
//   - CallerType is metadata, not policy. The runner sees the same
//     inputs regardless of CallerType — what changes is the slog
//     attribution and (in future) per-CallerType behavior hooks that
//     remain TBD. Today every CallerType produces the same wire shape;
//     that invariant is the smoke-evidence acceptance criterion.
//
// Pre-launch contract (`feedback_no_compat_shims`): the per-site
// assembly that this package replaces is DELETED at each call-site.
// No alias wrappers, no fallback path. Callers that haven't been
// migrated produce a compile-time break.
package dispatcher

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/chat"
)

// CallerType identifies which dispatch path invoked the runner. This
// is the load-bearing distinction CW-20260512-0020 framed (background-
// shell vs background-agent) and CW-20260512-0121 absorbed: the agent-
// flavored variant of background work becomes a CallerType label here,
// while the shell-flavored variant continues to live in
// internal/background and does NOT route through this package.
type CallerType string

const (
	// CallerChat is the user → chat-agent generateResponse path.
	// HandleMessage / RetryLastMessage / SendAgentMessage all stamp this.
	CallerChat CallerType = "chat"

	// CallerSubagent is the parent-agent → spawned-subagent path
	// (sync / async / fire-and-forget — all variants stamp this).
	// internal/service.ChatRunner stamps this on every invokeChat.
	CallerSubagent CallerType = "subagent"

	// CallerBackground is the agent-flavored background-job path —
	// async, non-session-bound, agent-driven. Reserved by the
	// dispatcher seam (CW-20260512-0020 framing absorbed by
	// CW-20260512-0121); the shell-flavored variant continues to use
	// internal/background's PTY backend and does NOT route through
	// Dispatcher.Run. When the agent-flavored caller lands, it stamps
	// this and goes through the same broker pipeline as Chat /
	// Subagent — that is the architectural answer to "shell vs agent".
	CallerBackground CallerType = "background"
)

// String implements fmt.Stringer for slog.
func (c CallerType) String() string { return string(c) }

// Valid reports whether c is one of the known CallerType values.
// Unknown CallerTypes are rejected at Dispatcher.Run rather than
// silently coerced — the slog attribution depends on this label
// being authoritative.
func (c CallerType) Valid() bool {
	switch c {
	case CallerChat, CallerSubagent, CallerBackground:
		return true
	}
	return false
}

// Request is the dispatcher input. Every CallerType supplies the same
// shape; the runner consumes SessionID + AssistantMsgID + UserContent
// and the brokers downstream derive everything else from the persisted
// session row.
//
// The dispatcher intentionally takes a narrow surface — it is the
// named boundary, not a place to grow per-caller fields. Caller-
// specific upstream concerns (subagent path-grant lineage, background
// budget enforcement) live with the caller, not on Request.
type Request struct {
	// SessionID is the session whose persisted state the runner
	// resolves (agent, mode, workspace, message history). For
	// subagents this is the child session id; for chat it is the
	// user's session id.
	SessionID string

	// AssistantMsgID is the message id the runner stamps onto the
	// stream's stream_start / stream_end events. Caller-allocated so
	// the FE / capture path can correlate before the runner has
	// returned.
	AssistantMsgID string

	// UserContent is the verbatim user-turn text passed to the
	// runner. For subagent dispatch this is the spawn prompt; for
	// chat it is the user's message; for the (future) agent-flavored
	// background path it is the task description.
	UserContent string

	// CallerType is the dispatch-source label. MUST be one of the
	// declared constants; Run rejects unknown values rather than
	// defaulting silently — silent default would defeat the
	// "identical structural shape across CallerTypes" smoke
	// (request_build slog comparison).
	CallerType CallerType
}

// Runner is the narrow surface Dispatcher delegates to. The single
// runner — agent-sessions (chat_generate.go's generateResponse path)
// — implements this structurally; tests inject a stub. The signature
// MUST match chatServiceImpl.generateResponse byte-for-byte so the
// pre-launch refactor is a straight drop-in.
type Runner interface {
	// Invoke executes one assistant turn against sessionID, streaming
	// events on ch and closing ch on completion (success or error).
	// Implementations MUST respect ctx cancellation and MUST close ch
	// exactly once.
	Invoke(ctx context.Context, sessionID, assistantMsgID, userContent string, ch chan chat.StreamEvent)
}

// Dispatcher is the single agent-dispatch door. Construct once at
// service-container wire time; share across all callers.
//
// The struct has one field — a Runner — by design. The dispatcher is
// the named boundary, not a coordinator. Adding per-CallerType fields
// here would smear the "callers consolidated, runner untouched"
// invariant; per-caller wiring lives at the caller.
type Dispatcher struct {
	runner Runner
}

// New constructs a Dispatcher. runner is required — a nil runner
// surfaces at Run time as a typed error rather than a panic so the
// MCP / HTTP / CLI seams stay safe.
func New(runner Runner) *Dispatcher {
	return &Dispatcher{runner: runner}
}

// Run is the single dispatch entry. It validates the request, stamps
// the CallerType into slog, and invokes the runner. Errors are
// returned to the caller — Run does NOT close ch on a validation
// error (the caller owns ch and may want to send a typed error
// event before closing).
//
// On a successful invocation, the runner's Invoke is responsible for
// closing ch via its own deferred close (see
// chat_generate.go:generateResponse defer). Dispatcher.Run does NOT
// double-close.
//
// The slog attribution at dispatch entry is the load-bearing smoke
// evidence: a "dispatch" event with caller=<CallerType> and
// session_id=<sessionID> proves which CallerType drove the request,
// and the downstream request_build slog (chat_generate.go ~line 988)
// stamps the same caller field so the cross-CallerType comparison is
// trivially diffable.
func (d *Dispatcher) Run(ctx context.Context, req Request, ch chan chat.StreamEvent) error {
	if d == nil {
		return fmt.Errorf("dispatcher: nil dispatcher (wiring bug)")
	}
	if d.runner == nil {
		return fmt.Errorf("dispatcher: nil runner (wiring bug)")
	}
	if !req.CallerType.Valid() {
		return fmt.Errorf("dispatcher: invalid caller_type %q (expected chat|subagent|background)", req.CallerType)
	}
	if req.SessionID == "" {
		return fmt.Errorf("dispatcher: session_id is required")
	}
	if req.AssistantMsgID == "" {
		return fmt.Errorf("dispatcher: assistant_msg_id is required")
	}

	slog.Debug("dispatcher: run",
		"caller", req.CallerType.String(),
		"session_id", req.SessionID,
		"assistant_msg_id", req.AssistantMsgID,
	)

	// Stamp CallerType on ctx so the runner's downstream telemetry
	// (request_build slog in chat_generate.go) can report which
	// CallerType drove this dispatch. This is the ONLY ambient piece —
	// scoped to the lifetime of one Run call and owned by Dispatcher,
	// so callers cannot leak a stale CallerType across requests.
	ctx = WithCallerType(ctx, req.CallerType)

	d.runner.Invoke(ctx, req.SessionID, req.AssistantMsgID, req.UserContent, ch)
	return nil
}

// callerTypeContextKey is the unexported type used for the context
// value. Using an unexported struct type prevents accidental
// collision with other packages' context keys.
type callerTypeContextKey struct{}

// WithCallerType returns a new context carrying caller. Dispatcher.Run
// calls this once at entry; the runner extracts via CallerTypeFromContext.
// Exposed so tests and direct-runner integrations (which exist during
// the refactor window) can stamp the same label without going through
// Run.
func WithCallerType(ctx context.Context, caller CallerType) context.Context {
	return context.WithValue(ctx, callerTypeContextKey{}, caller)
}

// CallerTypeFromContext extracts the dispatcher CallerType label from
// ctx. Returns the empty CallerType ("") when ctx carries no value —
// the runner's slog falls back to caller=unknown in that case, which
// is the smoke-detectable signal that some call-site bypassed
// Dispatcher.Run.
func CallerTypeFromContext(ctx context.Context) CallerType {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(callerTypeContextKey{}).(CallerType)
	return v
}
