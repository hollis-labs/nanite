package selftools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/hollis-labs/agentkit/broker"
	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/classify"
	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/mcp"
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

	// E1: DB-backed dispatch_to_agent reflex match — see
	// matchDispatchToAgentReflex's doc comment for the full design
	// (migrated off internal/promptrouter by TASKS/phase-4/
	// 03-migrate-promptrouter-to-reflexes.md).
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
			if st.Store != nil {
				meta := fmt.Sprintf(
					`{"error":%q,"reflex_match_id":%q}`,
					derr.Error(), brokerInput.ReflexMatchID,
				)
				// Outcome bookkeeping must survive cancellation of the broker decision it records.
				st.Store.LogEvent(context.WithoutCancel(ctx), sessionID, "broker_decision_error", "error", derr.Error(), meta)
			}
		case st.Store != nil:
			meta := fmt.Sprintf(
				`{"agent_profile":%q,"reason":%q,"confidence":%g,"reflex_match_id":%q}`,
				decision.AgentProfile, decision.Reason, decision.Confidence, brokerInput.ReflexMatchID,
			)
			// Outcome bookkeeping must survive cancellation of the broker decision it records.
			st.Store.LogEvent(context.WithoutCancel(ctx), sessionID, "broker_decision", "info", decision.Reason, meta)
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
	// payload — this is the conduit through which the worker's result
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
	if st.Store == nil {
		return false, nil
	}
	callerSessionID := mcp.SessionIDFromContext(ctx)
	if callerSessionID == "" {
		// No authoritative caller identity (direct invocation / test).
		// Fail open here — the subagent.Service guard is the backstop.
		return false, nil
	}
	isChild, err := st.Store.IsSubagentSession(ctx, callerSessionID)
	if err != nil {
		// Fail closed: an unverifiable parentage means we refuse rather
		// than risk an unbounded recursive spawn chain.
		return false, fmt.Errorf("subagent recursion check failed: %w", err)
	}
	return isChild, nil
}

// matchDispatchToAgentReflex is callExecuteTask's own, deliberately
// independent evaluation of the DB-backed dispatch_to_agent
// agent_reflexes rows (see callExecuteTask's header comment for why this
// is a second layer, not a call into
// internal/service/chat_reflex_dispatch.go's upstream
// attemptReflexDispatch).
//
// It lists active dispatch_to_agent rows for the caller's class via
// Store.ListAgentReflexesForAgent (already ordered priority DESC,
// created_at ASC), then — as of TASKS/reflex-taxonomy/
// 03-shared-decision-engine.md — decides which one (if any) actually wins
// via the SAME shared reflexes.Resolve() primitive Engine.EvaluateState
// and attemptReflexDispatch call, rather than a hand-rolled loop of its
// own. This function still can't call into internal/service directly
// (internal/mcp cannot import internal/service — service already imports
// mcp, that would be a cycle), so it builds its own State/candidate list
// and Resolve() call rather than calling attemptReflexDispatch itself —
// but the actual combining-algorithm decision logic is no longer
// duplicated, only the State-building and store access are (a legitimate,
// permanent difference per the architecture doc's "One shared decision
// engine, multiple legitimate invocation points" section, not the
// duplicated-decision-logic problem task 03 fixes).
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
func (st *SelfToolsTransport) matchDispatchToAgentReflex(ctx context.Context, sessionID, agentProfileID, message string) *dispatch.ReflexHints {
	if st.Store == nil {
		return nil
	}

	class := ""
	if agentProfileID != "" {
		if ap, err := st.Store.GetAgent(ctx, agentProfileID); err == nil && ap != nil {
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

	allCandidates, err := st.Store.ListAgentReflexesForAgent(ctx, agentProfileID, class)
	if err != nil {
		slog.Warn("mcp: dispatch-reflex list failed",
			"agent_id", agentProfileID, "class", class, "err", err)
		return nil
	}

	// TASKS/teams/05-agent-reflexes-run-scoping.md: allCandidates above
	// (ListAgentReflexesForAgent) is NOT filtered by workflow_run_id at
	// the SQL level — it returns every row matching (agentProfileID,
	// class) regardless of scope, exactly as it always has (narrowing
	// that shared query would change behavior for every other caller of
	// it too, including Engine.EvaluateState's generic per-turn pass,
	// which has no run context of its own). The WorkflowRunID=="" check
	// below is this call site's own responsibility, same as the
	// candidates variable's existing ActionKind filter (below,
	// dispatchCandidates) — it is what keeps a run-scoped row (once one
	// exists) from leaking into a session outside its own run via this
	// global list.
	candidates := make([]store.AgentReflex, 0, len(allCandidates))
	for _, r := range allCandidates {
		if r.WorkflowRunID == "" {
			candidates = append(candidates, r)
		}
	}

	// Widen with this session's TeamRun-scoped dispatch_to_agent rows, if
	// any — the same widening internal/service/chat_reflex_dispatch.go's
	// attemptReflexDispatch applies upstream (see that call site's own
	// comment for the full rationale, including the documented
	// global-vs-run-scoped priority-ordering call: candidates are merged
	// into one flat list and dispatch_to_agent's existing
	// first_applicable combining algorithm — unchanged — is the sole
	// arbiter of which one wins). Global rules (candidates above) still
	// apply; run-scoped rules layer on top, they do not replace the
	// global set. A resolve failure (including "team_run_members doesn't
	// exist on this database yet") degrades to "no run scoping" rather
	// than aborting this call site's own evaluation — additive widening
	// must never regress the base (non-Team) dispatch_to_agent behavior
	// that existed before this task.
	if runID, found, rerr := st.Store.ResolveWorkflowRunIDForSession(ctx, sessionID); rerr != nil {
		slog.Warn("mcp: dispatch-reflex workflow-run resolve failed",
			"session_id", sessionID, "err", rerr)
	} else if found {
		runScoped, rlErr := st.Store.ListAgentReflexesForWorkflowRun(ctx, runID, agentProfileID, class)
		if rlErr != nil {
			slog.Warn("mcp: dispatch-reflex run-scoped list failed",
				"session_id", sessionID, "workflow_run_id", runID, "err", rlErr)
		} else {
			candidates = append(candidates, runScoped...)
		}
	}

	state := reflexes.State{
		// SessionID (TASKS/reflex-taxonomy/06-unified-reflex-telemetry.md):
		// no trigger predicate reads this (evaluator.go never touches
		// State.SessionID), but reflexes.EmitFirings' unified event_log
		// write below needs it to attribute the emitted trace record to
		// the right session — the same field
		// internal/service/chat_reflex_dispatch.go's attemptReflexDispatch
		// already populates for its own (upstream) evaluation.
		SessionID:        sessionID,
		AgentID:          agentProfileID,
		AgentClass:       class,
		ScopeTier:        m1Tier.String(),
		ExecutionPattern: m1Pattern.String(),
		// Synthetic single-entry window over the CURRENT turn's raw text
		// — same substrate internal/service/chat_reflex_dispatch.go
		// builds for its own (upstream) evaluation. Not a DB read.
		UserMessages: []reflexes.MessageSignal{{Content: message}},
	}

	// Facet 4 recurrence cascade (TASKS/reflex-taxonomy/
	// 02-recurrence-cascade.md, docs/engineering/architecture/
	// 10-reflex-action-taxonomy.md): resolved once per call (there's only
	// ever one dispatch_to_agent kind row to look up, not one per
	// candidate) via the same store.GetReflexActionKind primitive
	// internal/agent/reflexes.Engine's own cache is built from. A lookup
	// failure (e.g. an unmigrated test DB) degrades to nil — the same
	// "inherit the system default" behavior EffectiveCooldown gives an
	// absent kind-level override. The same fetched row's
	// CombiningAlgorithm (Facet 2, seeded 'first_applicable') is reused
	// below as the ActionKindLookup Resolve() calls — this call site's own
	// candidates list (below) is filtered to dispatch_to_agent only, so no
	// other kind name is ever requested.
	dispatchKind, kindErr := st.Store.GetReflexActionKind(ctx, store.ReflexActionDispatchToAgent)
	var dispatchKindDefaultSeconds *int64
	if kindErr == nil {
		dispatchKindDefaultSeconds = dispatchKind.DefaultRecurrenceSeconds
	}
	now := time.Now()
	cooldownFn := func(r store.AgentReflex) bool {
		// A fired candidate whose own cooldown hasn't elapsed yet is not
		// eligible to win — same shared check
		// internal/service/chat_reflex_dispatch.go's attemptReflexDispatch
		// applies. Under today's seed data (kind default 0, no
		// reflex-level overrides) this never suppresses; a future non-zero
		// recurrence_override_seconds on a dispatch_to_agent row now takes
		// effect here too, not just at the upstream call site.
		cooldown := reflexes.EffectiveCooldown(dispatchKindDefaultSeconds, r.RecurrenceOverrideSeconds)
		suppressed := reflexes.RecentlyFired(r, now, cooldown)
		if suppressed {
			slog.Info("mcp: dispatch-reflex fired but suppressed by cooldown",
				"reflex", r.Name, "cooldown", cooldown)
		}
		return suppressed
	}
	kindLookup := func(_ context.Context, _ string) (*store.ReflexActionKind, error) {
		return dispatchKind, kindErr
	}

	// This call site's own candidate list — see engine.go's/
	// attemptReflexDispatch's identical comment: Resolve() (below) has no
	// opinion on which rows a caller passes it; filtering to
	// dispatch_to_agent only is this caller's own job.
	dispatchCandidates := make([]store.AgentReflex, 0, len(candidates))
	for _, r := range candidates {
		if r.ActionKind == store.ReflexActionDispatchToAgent {
			dispatchCandidates = append(dispatchCandidates, r)
		}
	}

	// This call site has no *reflexes.Engine of its own to reuse an
	// Executor from (unlike attemptReflexDispatch, which reuses
	// s.reflexEngine.Executor) — a bare Executor with only Logger set is
	// equivalent for dispatch_to_agent's own Apply case (executor.go's
	// dispatch_to_agent branch touches no hook, it only parses
	// action_spec into the returned AppliedAction.Spec), matching exactly
	// what this function's own hand-rolled json.Unmarshal(r.ActionSpec)
	// used to do before this task.
	dispatchExecutor := &reflexes.Executor{Logger: slog.Default()}
	resolved, outcomes, resolveErr := reflexes.Resolve(ctx, dispatchCandidates, state, dispatchExecutor, cooldownFn, kindLookup)
	if resolveErr != nil {
		slog.Warn("mcp: dispatch-reflex resolve failed", "err", resolveErr)
		return nil
	}
	for _, oc := range outcomes {
		if oc.TriggerError != "" {
			slog.Warn("mcp: dispatch-reflex trigger eval failed",
				"reflex", oc.ReflexName, "err", oc.TriggerError)
		}
		if oc.ApplyError != "" {
			slog.Warn("mcp: dispatch-reflex apply failed",
				"reflex", oc.ReflexName, "err", oc.ApplyError)
		}
	}
	if len(resolved.FiredReflexes) == 0 {
		return nil
	}
	if len(resolved.FiredReflexes) > 1 {
		// TASKS/reflex-taxonomy/08-fix-resolve-fail-open-visibility.md:
		// canary for the same kind-lookup-failure fail-open Resolve() now
		// Warn-logs directly — see the matching note in
		// chat_reflex_dispatch.go's attemptReflexDispatch. Only the first
		// candidate is ever used below.
		slog.Warn("mcp: dispatch-reflex resolved multiple candidates, only the first is used",
			"session_id", sessionID,
			"candidate_count", len(resolved.FiredReflexes),
		)
	}
	r := resolved.FiredReflexes[0]
	winnerAction := resolved.Actions[0]

	agentSlug, _ := winnerAction.Spec["agent_slug"].(string)
	if agentSlug == "" {
		return nil
	}

	// TASKS/reflex-taxonomy/06-unified-reflex-telemetry.md: this call
	// site's former dedicated playbook_match_log write (via
	// st.ReflexLogger/store.LogReflexMatch) is retired in favor of the
	// same unified event_log sink Engine.EvaluateState and
	// attemptReflexDispatch now go through — see the task's Work Log for
	// the "no real reader" grep confirming playbook_match_log had no
	// consumer left to starve. The matched-input excerpt is preserved via
	// ExtraMetadata rather than dropped.
	excerpt := message
	if len(excerpt) > 200 {
		excerpt = excerpt[:200]
	}
	extra := map[string]any{"matched_input_excerpt": excerpt}
	reflexes.EmitFirings(ctx, st.Store, st.Plugins, resolved, outcomes, state, reflexes.FiringContext{
		AgentID:       agentProfileID,
		AgentClass:    class,
		ExtraMetadata: extra,
	}, slog.Default())

	return &dispatch.ReflexHints{
		AgentSlug: agentSlug,
		ReflexID:  r.ID,
	}
}
