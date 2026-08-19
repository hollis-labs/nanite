package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/hollis-labs/agentkit/broker"
	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/classify"
	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/grounding"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
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
// E2 integration (CW-20260419-0028): when GroundingRecaller is set and
// NANITE_GROUNDING_ENABLED=true, a memory recall step fires FIRST, before
// the E1 reflex matcher. Memories above the similarity threshold are
// prepended to the message as a "## Relevant memories" block (≤200 tokens).
// Consultation rows are logged; outcome rows are written after the
// follow-up (caller responsibility via grounding.RecordOutcome).
//
// E1 integration (CW-20260419-0027; migrated off internal/promptrouter by
// TASKS/phase-4/03-migrate-promptrouter-to-reflexes.md): before calling
// dispatch.ExecuteTask this function runs matchDispatchToAgentReflex
// (below) against the live DB-backed dispatch_to_agent agent_reflexes
// rows for the caller's agent class. A matched reflex injects
// ReflexHints.AgentSlug into ExecuteTaskArgs so the dispatch layer uses
// the reflex's target agent slug instead of AssignRole's tier/pattern
// default. On a miss the dispatch path is unchanged.
//
// This is a second, DELIBERATELY INDEPENDENT evaluation of the same
// dispatch_to_agent reflex rows internal/service/chat_reflex_dispatch.go's
// attemptReflexDispatch evaluates upstream (before task_execute is ever
// invoked) — this file's evaluation runs downstream, INSIDE the
// task_execute call itself, once the LLM has already decided to
// dispatch. Both layers run; neither is collapsed into the other (the
// retired internal/service/chat_broker_dispatch.go's own header comment
// stated this design instruction for the pre-migration broker/promptrouter
// pair, and it still applies conceptually to this pair post-migration).
func (st *SelfToolsTransport) callExecuteTask(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if st.Dispatch == nil {
		return errorResult("dispatch is not configured (subagent service unavailable)"), nil
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
		return errorResult(err.Error()), nil
	} else if blocked {
		return errorResult(subagentRecursionBlockedMsg), nil
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
	if ctxSID := SessionIDFromContext(ctx); ctxSID != "" {
		sessionID = ctxSID
	}
	message := strArg(args, "message", "")
	if sessionID == "" {
		return errorResult("session_id is required"), nil
	}
	if message == "" {
		return errorResult("message is required"), nil
	}

	// E2: Pre-strategy memory-grounding recall (CW-20260419-0028).
	// Fires before E1 reflex matching so that memory context can influence
	// the message seen by the dispatch layer. The grounding block is
	// prepended to the dispatch message, not the original user-facing
	// message — the worker/planner sees the enriched prompt.
	//
	// When the gate is off, groundingResult.Enabled == false and no rows
	// are written. When enabled but no hits exceed the threshold, the
	// message is unchanged.
	turnID := strArg(args, "turn_id", "")
	userID := strArg(args, "user_id", "")
	dispatchMessage := message // may be prepended with memories block below
	var groundingConsultationIDs []int64
	if st.GroundingRecaller != nil {
		groundingResult := st.GroundingRecaller.Recall(ctx, grounding.RecallInput{
			UserInput: message,
			SessionID: sessionID,
			UserID:    userID,
			TurnID:    turnID,
		})
		if groundingResult.Enabled {
			// Log all hits (consumed/discarded) before dispatch. Errors are swallowed.
			groundingConsultationIDs = grounding.LogConsultations(st.GroundingLogger, groundingResult, turnID)

			// Prepend the surfaced memories block to the dispatch message.
			if block := grounding.SystemPromptBlock(groundingResult); block != "" {
				dispatchMessage = block + "\n" + message
			}
		}
	}
	// groundingConsultationIDs is available for post-generation outcome
	// write-back via grounding.RecordOutcome; the MCP dispatch layer does
	// not observe the follow-up turn directly, so write-back is the
	// responsibility of the chat generation layer when it records outcomes.
	_ = groundingConsultationIDs

	// H1 trust resolution (CW-20260421-0014): populate AgentProfileID from
	// the caller-profile ctx stamped by the service layer in
	// executeToolBatch. Resolved here (rather than at its original
	// pre-dispatch position, below) because the E1 reflex match right
	// after this needs it to resolve the caller's agent class. When the
	// ctx carries no profile (e.g. direct test invocations), the field is
	// empty and the subagent gate falls back to TrustNormal (approval
	// required — existing safe default).
	apID := CallerProfileFromContext(ctx)

	// E1: DB-backed dispatch_to_agent reflex match — see
	// matchDispatchToAgentReflex's doc comment for the full design
	// (migrated off internal/promptrouter by TASKS/phase-4/
	// 03-migrate-promptrouter-to-reflexes.md).
	reflexHints := st.matchDispatchToAgentReflex(ctx, sessionID, strArg(args, "turn_id", ""), apID, message, dispatchMessage)

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
		brokerInput := broker.Input{
			// PR #113 review: broker must see the same effective text
			// dispatch will see (post-grounding-injection), otherwise the
			// audit trail and any future non-noop broker logic won't
			// correspond to the actual dispatched prompt.
			UserText: dispatchMessage,
		}
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
			if st.Store != nil {
				meta := fmt.Sprintf(
					`{"error":%q,"reflex_match_id":%q}`,
					derr.Error(), brokerInput.ReflexMatchID,
				)
				st.Store.LogEvent(sessionID, "broker_decision_error", "error", derr.Error(), meta)
			}
		case st.Store != nil:
			meta := fmt.Sprintf(
				`{"agent_profile":%q,"reason":%q,"confidence":%g,"reflex_match_id":%q}`,
				decision.AgentProfile, decision.Reason, decision.Confidence, brokerInput.ReflexMatchID,
			)
			st.Store.LogEvent(sessionID, "broker_decision", "info", decision.Reason, meta)
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
		Message:        dispatchMessage,
		Provider:       strArg(args, "provider", ""),
		TimeoutSeconds: intArg(args, "timeout_seconds", 0),
		ReflexHints:    reflexHints,
		AgentProfileID: apID,
	})
	if err != nil {
		return errorResult(fmt.Sprintf("dispatch: %v", err)), nil
	}

	// Marshal the envelope back to JSON for the tool result. The Chat
	// agent's harness prompt instructs it to relay the structured
	// payload — this is the conduit through which the worker's result
	// reaches the frontend, with no raw text leakage.
	envJSON, err := json.Marshal(envelope)
	if err != nil {
		return errorResult(fmt.Sprintf("dispatch: marshal envelope: %v", err)), nil
	}
	return textResult(string(envJSON)), nil
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
	if st.Store == nil {
		return false, nil
	}
	callerSessionID := SessionIDFromContext(ctx)
	if callerSessionID == "" {
		// No authoritative caller identity (direct invocation / test).
		// Fail open here — the subagent.Service guard is the backstop.
		return false, nil
	}
	isChild, err := st.Store.IsSubagentSession(callerSessionID)
	if err != nil {
		// Fail closed: an unverifiable parentage means we refuse rather
		// than risk an unbounded recursive spawn chain.
		return false, fmt.Errorf("subagent recursion check failed: %v", err)
	}
	return isChild, nil
}

// ReflexMatchLogger is the narrow store interface
// matchDispatchToAgentReflex uses to persist a dispatch_to_agent match
// event to playbook_match_log. *store.Store satisfies it.
//
// Previously (pre-migration) this package referenced
// promptrouter.MatchLogger directly; TASKS/phase-4/
// 03-migrate-promptrouter-to-reflexes.md retired internal/promptrouter
// in full, so this narrow interface now lives here, against
// store.ReflexMatchLogEntry instead of the retired
// promptrouter.ReflexMatchEntry.
type ReflexMatchLogger interface {
	LogReflexMatch(entry store.ReflexMatchLogEntry) error
}

// matchDispatchToAgentReflex is callExecuteTask's own, deliberately
// independent evaluation of the DB-backed dispatch_to_agent
// agent_reflexes rows (see callExecuteTask's header comment for why this
// is a second layer, not a call into
// internal/service/chat_reflex_dispatch.go's upstream
// attemptReflexDispatch).
//
// It replicates the shape of attemptReflexDispatch's own evaluation loop
// (list active dispatch_to_agent rows for the caller's class via
// Store.ListAgentReflexesForAgent — already ordered priority DESC,
// created_at ASC — then reflexes.EvaluateTrigger each one, first fire
// wins) rather than calling into internal/service, since internal/mcp
// cannot import internal/service (service already imports mcp — that
// would be a cycle) and the evaluation itself is cheap, read-only, and
// has no side effects beyond the optional match-log write below.
//
// Returns nil on any of: no store wired, no candidate rows, no firing
// trigger, or a fired trigger whose action_spec has an empty agent_slug
// (defensive — internal/api/reflexes.go's validateReflexDefinition
// rejects that at write time for anything created through the CRUD
// path). nil means "no override" — dispatch.ExecuteTask falls through to
// AssignRole's own tier/pattern default, exactly as a promptrouter miss
// used to.
//
// Design note: unlike attemptReflexDispatch, this function does NOT
// carry HintTier/HintPattern/Mode/WorkflowName into the returned
// ReflexHints — the dispatch_to_agent action_spec shape task 02 settled
// on ({"agent_slug","confidence","reason"}) has no fields for them. This
// is a real, deliberate behavior narrowing from the old promptrouter-fed
// hints, documented in the migration task's Work Log: AgentSlug is the
// only field that ever had an observable effect at THIS call site
// anyway (dispatch.ExecuteTask forces mode to sync regardless of Mode;
// Role — derived from tier/pattern, not from AgentSlug — only feeds a
// cosmetic title fallback string when the spawned agent's own envelope
// output is absent). WorkflowName-via-implicit-phrase-match is retired
// outright — the workflow_run self-tool remains the direct, supported
// way to invoke a named workflow.
func (st *SelfToolsTransport) matchDispatchToAgentReflex(ctx context.Context, sessionID, turnID, agentProfileID, message, dispatchMessage string) *dispatch.ReflexHints {
	if st.Store == nil {
		return nil
	}

	class := ""
	if agentProfileID != "" {
		if ap, err := st.Store.GetAgent(agentProfileID); err == nil && ap != nil {
			class = ap.Class
		}
	}
	if class == "" {
		// Same default internal/service/chat_reflex_dispatch.go's
		// attemptReflexDispatch and chat_reflexes.go's
		// evaluateAndInjectReflexes use — advisor is the class of every
		// real top-level chat-facing agent_profiles row (task 02's Work
		// Log verified this against a real backup DB).
		class = "advisor"
	}

	m1Tier, m1Pattern := classify.Classify(classify.IntentSignals{
		Message:         message,
		MessageTokenEst: len(message) / 4,
	})

	candidates, err := st.Store.ListAgentReflexesForAgent(ctx, agentProfileID, class)
	if err != nil {
		slog.Warn("mcp: dispatch-reflex list failed",
			"agent_id", agentProfileID, "class", class, "err", err)
		return nil
	}

	state := reflexes.State{
		AgentID:          agentProfileID,
		AgentClass:       class,
		ScopeTier:        m1Tier.String(),
		ExecutionPattern: m1Pattern.String(),
		// Synthetic single-entry window over the CURRENT turn's raw text
		// — same substrate internal/service/chat_reflex_dispatch.go
		// builds for its own (upstream) evaluation. Not a DB read.
		UserMessages: []reflexes.MessageSignal{{Content: message}},
	}

	for i := range candidates {
		r := candidates[i]
		if r.ActionKind != store.ReflexActionDispatchToAgent {
			continue
		}
		fired, evalErr := reflexes.EvaluateTrigger(r.TriggerKind, r.TriggerSpec, state)
		if evalErr != nil {
			slog.Warn("mcp: dispatch-reflex trigger eval failed",
				"reflex", r.Name, "err", evalErr)
			continue
		}
		if !fired {
			continue
		}
		var spec map[string]any
		if err := json.Unmarshal([]byte(r.ActionSpec), &spec); err != nil {
			slog.Warn("mcp: dispatch-reflex parse action_spec failed",
				"reflex", r.Name, "err", err)
			continue
		}
		agentSlug, _ := spec["agent_slug"].(string)
		if agentSlug == "" {
			continue
		}

		if st.ReflexLogger != nil {
			excerpt := message
			if len(excerpt) > 200 {
				excerpt = excerpt[:200]
			}
			_ = st.ReflexLogger.LogReflexMatch(store.ReflexMatchLogEntry{
				SessionID:           sessionID,
				TurnID:              turnID,
				ReflexID:            r.ID,
				Priority:            int(r.Priority),
				Source:              "reflex",
				MatchedInputExcerpt: excerpt,
				HintTier:            m1Tier.String(),
				HintPattern:         m1Pattern.String(),
				ProfileSlug:         agentSlug,
				// CW-20260816-0068: raw-vs-sent audit trail. message is
				// the raw user input the reflex matcher ran against;
				// dispatchMessage is what actually reaches the spawned
				// agent (may be prepended with the E2 grounding block
				// above, or any future rewrite-for-clarity step). The
				// writer collapses identical pairs to avoid bloat.
				RawInputText:  message,
				SentInputText: dispatchMessage,
			})
		}

		return &dispatch.ReflexHints{
			AgentSlug: agentSlug,
			ReflexID:  r.ID,
		}
	}
	return nil
}
