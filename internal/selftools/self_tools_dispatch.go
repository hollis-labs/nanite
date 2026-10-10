package selftools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/substrate/agent/subagent"
	"github.com/hollis-labs/substrate/harness/broker"
)

// callExecuteTask handles task_execute — the Chat agent's
// dispatch primitive (CW-20260421-0010, B3). Translates the tool args
// into dispatch.ExecuteTaskArgs, invokes the dispatch primitive, and
// returns the resulting envelope JSON as the tool result.
//
// The Chat agent receives the envelope (via the tool result) and
// relays it to the frontend. Raw worker output never enters the Chat
// agent's context window — only the structured envelope does.
//
// Retained mutable dispatch rules are inert. Dispatch still reaches the
// configured lifecycle port, whose real host adapter refuses missing verified
// ownership before spawning; test ports can exercise envelope behavior.
func (st *SelfToolsTransport) callExecuteTask(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	if st.Dispatch == nil {
		return mcp.ErrorResult("dispatch is not configured (subagent service unavailable)"), nil
	}

	// CW-20260516-0066: hard subagent recursion-depth cap. task_execute
	// is a session-spawning dispatch primitive — it creates a new tracked
	// child session. Only a depth-0 progenitor (a root / user-facing
	// session with no parent) may invoke it. If the caller's session is
	// itself a subagent, reject before any dispatch work. The caller's
	// session id is taken from the ctx (stamped by the service layer in
	// executeToolBatch), NOT the LLM-supplied session_id arg, so a
	// subagent cannot evade the cap by passing a different id.
	if blocked, err := st.recursionBlocked(ctx); err != nil {
		return mcp.ErrorResult(err.Error()), nil
	} else if blocked {
		return mcp.ErrorResult(subagentRecursionBlockedMsg), nil
	}
	wrapper := st.DispatchWrapper
	if wrapper == nil {
		// Default to the package-provided wrapper so production wiring
		// gets a working envelope path even if DispatchWrapper was not
		// explicitly set.
		wrapper = dispatch.DefaultEnvelopeWrapper{}
	}

	// Caller identity is authoritative from the ctx (stamped by the service
	// layer), not the LLM-supplied session_id arg — the recursion cap above
	// already trusts ctx, and the dispatch's parent session must be the same
	// identity. The arg is a fallback only for ctx-less paths (tests).
	sessionID := strArg(args, "session_id", "")
	if ctxSID := mcp.SessionIDFromContext(ctx); ctxSID != "" {
		sessionID = ctxSID
	}
	message := strArg(args, "message", "")
	if sessionID == "" {
		return mcp.ErrorResult("session_id is required"), nil
	}
	if message == "" {
		return mcp.ErrorResult("message is required"), nil
	}

	// H1 trust resolution (CW-20260421-0014): populate AgentProfileID from
	// the caller-profile ctx stamped by the service layer in
	// executeToolBatch. Resolved here (rather than at its original
	// pre-dispatch position, below) because the E1 reflex match right
	// after this needs it to resolve the caller's agent class. When the
	// ctx carries no profile (e.g. direct test invocations), the field is
	// empty and the subagent gate falls back to TrustNormal (approval
	// required — existing safe default).
	apID := mcp.CallerProfileFromContext(ctx)

	// Mutable retained dispatch rules cannot supply a target or firing.
	reflexHints := st.matchDispatchToAgentReflex(ctx, sessionID, apID, message)

	// CW-20260502-0005: agent-broker consultation (no-op scaffold).
	// The broker is upstream of dispatch; the no-op impl reads SessionMode
	// and returns the current-behavior agent profile so wiring it produces
	// no semantic change. Decision.Reason is logged to event_log so future
	// sessions (and the v1 deterministic replacement) can audit routing.
	//
	// Phase 0 item 21 ("Cut Modes, in full") deleted store.GetSessionMode —
	// this used to resolve the session's *store.Mode here and project its
	// slug into broker.Input.SessionMode. That lookup is gone; SessionMode
	// is now always "". This is the second real call site of the same
	// "coupled step" TASKS/phase-0/21-cut-modes.md documents for
	// chat_broker_dispatch.go's buildBrokerInput (the task file's own
	// enumeration only named that one) — the production broker instance is
	// agentkit's DeterministicBroker (wired via agentbroker.New() in
	// cmd/nanite/main.go, shared between chatServiceImpl.agentBroker and
	// SelfToolsTransport.Broker here), and per agentkit/broker/broker.go's
	// own doc comment, DeterministicBroker.Decide never actually consults
	// Input.SessionMode (its rules key off the separate per-turn Input.Mode
	// field instead) — SessionMode only ever fed telemetry/audit logging
	// below, which now just always logs an empty string.
	if st.Broker != nil {
		brokerInput := broker.Input{UserText: message}
		if reflexHints != nil {
			brokerInput.ReflexMatchID = reflexHints.ReflexID
		}
		decision, derr := st.Broker.Decide(ctx, brokerInput)
		switch {
		case derr != nil:
			// PR #113 review: surface broker failures via slog + event_log
			// so wiring/config regressions are detectable. Non-fatal — the
			// no-op contract preserves current dispatch behavior on broker
			// failure (we just skip recording a decision).
			slog.Warn("mcp: broker decide failed",
				"session_id", sessionID, "err", derr)
			if st.Writes.Events != nil {
				meta := fmt.Sprintf(
					`{"error":%q,"reflex_match_id":%q}`,
					derr.Error(), brokerInput.ReflexMatchID,
				)
				// Outcome bookkeeping must survive cancellation of the broker decision it records.
				st.Writes.Events.LogEvent(context.WithoutCancel(ctx), sessionID, "broker_decision_error", "error", derr.Error(), meta)
			}
		case st.Writes.Events != nil:
			meta := fmt.Sprintf(
				`{"agent_profile":%q,"reason":%q,"confidence":%g,"reflex_match_id":%q}`,
				decision.AgentProfile, decision.Reason, decision.Confidence, brokerInput.ReflexMatchID,
			)
			// Outcome bookkeeping must survive cancellation of the broker decision it records.
			st.Writes.Events.LogEvent(context.WithoutCancel(ctx), sessionID, "broker_decision", "info", decision.Reason, meta)
		}
	}

	// CW-20260516-0058 / CW-20260815 (emit-react postmortem): ParentAgentID
	// drives the subagent reply-delivery block in subagent.Service.execute
	// (an unresolvable value silently drops the completion reply — see the
	// matching fix in self_tools_transport.go's subagent_spawn handler for
	// the full incident writeup). The LLM cannot reliably know its own
	// agent_profiles.ID and will confidently supply a wrong-but-plausible
	// value (its own slug) rather than an empty one, which is worse than
	// omitting it. Caller identity is authoritative from ctx; the arg is a
	// fallback only for ctx-less paths, never an override.
	parentAgentID := strArg(args, "parent_agent_id", "")
	if apID != "" {
		parentAgentID = apID
	}

	envelope, err := dispatch.ExecuteTask(ctx, st.Dispatch, wrapper, st.WorkflowLauncher, dispatch.ExecuteTaskArgs{
		SessionID:      sessionID,
		ParentAgentID:  parentAgentID,
		Message:        message,
		Provider:       strArg(args, "provider", ""),
		TimeoutSeconds: mcp.IntArg(args, "timeout_seconds", 0),
		ReflexHints:    reflexHints,
		AgentProfileID: apID,
	})
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("dispatch: %v", err)), nil
	}

	// Marshal the envelope back to JSON for the tool result. The Chat
	// agent's harness prompt instructs it to relay the structured
	// payload — this is the channel through which the worker's result
	// reaches the frontend, with no raw text leakage.
	envJSON, err := json.Marshal(envelope)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("dispatch: marshal envelope: %v", err)), nil
	}
	return mcp.TextResult(string(envJSON)), nil
}

// subagentRecursionBlockedMsg is the error surfaced to a parented agent
// that attempts a session-spawning dispatch (subagent_spawn / task_execute).
// Derived from subagent.ErrRecursionBlocked so the MCP layer and the
// subagent service always surface one identical message. CW-20260516-0066.
var subagentRecursionBlockedMsg = subagent.ErrRecursionBlocked.Error()

// recursionBlocked reports whether the caller of a session-spawning tool
// is itself a subagent (has a parent). It is the MCP-layer enforcement
// point of the recursion-depth cap (CW-20260516-0066): a hard cap at
// depth 1 — root agents may spawn workers; workers may not spawn.
//
// The caller's session id is read from the ctx (mcp.SessionIDFromContext),
// which the service layer stamps in executeToolBatch from the
// authoritative session record — NOT from an LLM-supplied tool arg, so a
// subagent cannot dodge the cap by passing a forged session_id.
//
// Returns:
//   - (false, nil) when the caller is a root session, the session id is
//     unknown (bare/test ctx — fail open, the spawn still hits trust +
//     approval gating), or the store is not wired.
//   - (true, nil)  when the caller's session is a subagent — reject.
//   - (false, err) on a real DB error — caller must reject (fail closed).
func (st *SelfToolsTransport) recursionBlocked(ctx context.Context) (bool, error) {
	if st.Writes.Dispatch == nil {
		return false, nil
	}
	callerSessionID := mcp.SessionIDFromContext(ctx)
	if callerSessionID == "" {
		// No authoritative caller identity (direct invocation / test).
		// Fail open here — the subagent.Service guard is the backstop.
		return false, nil
	}
	isChild, err := st.Writes.Dispatch.IsSubagentSession(ctx, callerSessionID)
	if err != nil {
		// Fail closed: an unverifiable parentage means we refuse rather
		// than risk an unbounded recursive spawn chain.
		return false, fmt.Errorf("subagent recursion check failed: %w", err)
	}
	return isChild, nil
}

// matchDispatchToAgentReflex deliberately returns no mutable-history override.
// Native behavior belongs to the pinned definition handler.
func (st *SelfToolsTransport) matchDispatchToAgentReflex(ctx context.Context, sessionID, agentProfileID, message string) *dispatch.ReflexHints {
	// Retained mutable behavior cannot select a dispatch target, supply authority
	// or emit a firing. Native policy is read from the pinned definition by its
	// own handler; absent fabric ownership remains refused at actual Spawn.
	return nil
}
