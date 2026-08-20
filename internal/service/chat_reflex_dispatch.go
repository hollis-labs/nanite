// Phase 4 item 02
// (TASKS/phase-4/02-dispatch-to-agent-reflex-action-kind-and-broker-migration.md):
// this file owns the seam that replaces the retired upstream agent-broker
// call site (formerly internal/service/chat_broker_dispatch.go's
// attemptBrokerDispatch/buildBrokerInput, both deleted along with that
// file). Runs at the exact same pre-loop position in generateResponse —
// see chat_generate.go's call to attemptReflexDispatch.
//
// Design decisions (also recorded in the task file's Work Log):
//
//  1. Rule-1-feed question (does the reflex-table match become the
//     broker's Rule-1 feed directly, or is the whole "broker" concept
//     subsumed into dispatch_to_agent's own trigger evaluation?):
//     SUBSUMED. No separate "broker" abstraction survives this task.
//     attemptReflexDispatch below IS the new Rule-1/5/6 evaluation —
//     it lists active agent_reflexes rows filtered to
//     action_kind='dispatch_to_agent', evaluates each trigger, and the
//     first one to fire (priority DESC, matching
//     Store.ListAgentReflexesForAgent's own ordering) wins. "No dispatch"
//     (former Rule 6) is simply the case where no dispatch_to_agent
//     reflex fires — no explicit reflex row represents "do nothing".
//     internal/agent/reflexes is the only steering primitive left; this
//     file is a thin chat-service call site around it, not a second
//     decision layer.
//
//  2. dispatch_to_agent's evaluation deliberately does NOT go through
//     reflexes.Engine.EvaluateState (the same pipeline
//     chat_reflexes.go's evaluateAndInjectReflexes uses for
//     inject_reminder/force_tool_choice/halt_session/etc.).
//     TASKS/reflex-taxonomy/02-recurrence-cascade.md corrected this
//     design note's original framing: this is no longer "skip the
//     pipeline to dodge its 15-minute debounce." The debounce itself is
//     now data, not code (reflex_action_kinds.default_recurrence_seconds
//     / agent_reflexes.recurrence_override_seconds, migration
//     124_reflex_action_taxonomy.sql), and dispatch_to_agent's
//     kind-level default is seeded at 0 ("no cooldown") — the exact
//     "re-evaluate fresh every turn" behavior the retired broker's Rule 5
//     always had. attemptReflexDispatch below applies that same cascade
//     explicitly (reflexes.EffectiveCooldown +
//     reflexEngine.ActionKindDefaultRecurrenceSeconds, same functions
//     EvaluateState's own debounce check uses) rather than inheriting it
//     implicitly from being inside EvaluateState's loop. Under today's
//     data (every dispatch_to_agent row's recurrence_override_seconds is
//     NULL, the kind default is 0) this check is a real comparison that
//     always evaluates to "not suppressed" — but a future non-zero
//     recurrence_override_seconds on a specific dispatch_to_agent row now
//     genuinely takes effect here, not just in theory.
//
//     The real, still-standing reason this file exists as a separate
//     call site from EvaluateState — not a recurrence-semantics
//     difference, and (as of TASKS/reflex-taxonomy/
//     03-shared-decision-engine.md) not a decision-logic difference either
//     — is the import-cycle constraint (internal/mcp cannot import
//     internal/service, so matchDispatchToAgentReflex below in
//     self_tools_dispatch.go cannot call this file's own function) plus
//     the live, in-flight State construction covered in note (3) below:
//     StateCollector (which EvaluateState uses) only reads state already
//     committed to the store, and cannot see this turn's not-yet-persisted
//     classification/user text. attemptReflexDispatch lists active
//     dispatch_to_agent rows itself, then calls the SAME shared
//     reflexes.Resolve() primitive Engine.EvaluateState calls (task 03) —
//     which internally runs reflexes.EvaluateTrigger and
//     reflexEngine.Executor.Apply for whichever candidate the
//     dispatch_to_agent kind's combining algorithm (first_applicable)
//     selects. No hook, no fired_count bump from Apply/Resolve itself —
//     this file bumps fired_count directly (best-effort, for the operator
//     UI's fired_count/last_fired_at telemetry), same as before task 03.
//
//  3. reflexes.State's ScopeTier/ExecutionPattern fields (added by this
//     task) are populated here from ls.Classification() — the CURRENT
//     turn's already-computed pre-loop classification — NOT from
//     StateCollector (which only knows about already-persisted history).
//     UserMessages is also seeded with a single synthetic entry holding
//     the raw, in-flight userContent string (not a DB read) so a future
//     dispatch_to_agent reflex can combine scope_tier/execution_pattern
//     with a window=1 user_regex_window/text_regex_window predicate
//     against the CURRENT turn's text — the substrate
//     02-migrate-promptrouter-to-reflexes.md (Rule 1's promptrouter
//     phrase-match migration) will need. See that task file for the
//     follow-up.
//
//  4. Faithful-parity note for Rule 5 specifically: the retired broker's
//     synthesized task_execute call NEVER threaded decision.AgentProfile
//     into the call's args — task_execute's own internal
//     dispatch.AssignRole(tier, pattern) independently recomputes
//     Worker-vs-Planner from the SAME classify.Classify output, so the
//     broker's decision and AssignRole's decision agreed by construction
//     for TierOpen+PatternSubagent turns, but the broker's own decision
//     value was never actually enforced. This file preserves that exact
//     behavior (synthesizes the same unmodified task_execute call,
//     AssignRole still resolves the real target) rather than adding a
//     new hard-override mechanism — self_tools_dispatch.go's
//     callExecuteTask (the only place that could thread such an
//     override) is the deliberately-independent second consumer this
//     task's brief says to leave alone. The fired reflex's agent_slug is
//     therefore declared intent + event_log telemetry, not an enforced
//     override, for this migration. A future task wiring a real
//     role-override arg into task_execute would need to touch
//     self_tools_dispatch.go, which is out of this task's scope.
package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

// reflexDispatchOutcome is the typed result of attemptReflexDispatch — a
// readable shape for tests and for parity with the retired
// brokerDispatchOutcome. Matched mirrors the old outcome's Consulted+
// Dispatched bits collapsed into one (a dispatch_to_agent reflex firing
// IS the decision, unlike the old broker which "consulted" on every turn
// including chat-direct ones).
type reflexDispatchOutcome struct {
	// Matched is true when a dispatch_to_agent reflex's trigger fired
	// this turn. False on every turn where none did (the former Rule 6
	// "default, no dispatch" case — no row is written, no event_log
	// entry, no task_execute call; the chat-direct LLM loop runs as if
	// this seam did not exist).
	Matched bool

	ReflexID   string
	ReflexName string
	// AgentSlug is the fired reflex's declared target — see design note
	// (4) above for why this is intent/telemetry, not an enforced
	// override, for this migration.
	AgentSlug  string
	Confidence float64
	Reason     string

	// Invoked is true when the synthesized task_execute call was made
	// (Matched=true and s.tools was wired). False when Matched=true but
	// s.tools is nil (degenerate/test wiring).
	Invoked bool

	// EmittedEnvelope is true when the seam pushed a plugin_envelope SSE
	// event onto the channel. False when Invoked=true but task_execute
	// returned an error, an error envelope, or non-JSON output — the
	// chat-direct LLM loop runs as the fallback in every such case.
	EmittedEnvelope bool
}

// attemptReflexDispatch is the dispatch_to_agent call site — the direct
// replacement for the retired attemptBrokerDispatch. Runs once per turn,
// at the same pre-loop position (after ls/classifyAndAttach, before the
// chat-loop entry) the old broker call used.
//
// Pass-through behavior:
//   - reflexEngine, store, or ls nil/unset → zero outcome, no-op.
//   - No dispatch_to_agent reflex fires → zero outcome (Matched=false),
//     the chat-direct LLM loop runs unchanged. No event_log row for a
//     non-firing turn — see design note (1): reflexes are only ever
//     visible when they actually fire, matching every other action
//     kind's behavior (an inject_reminder reflex that doesn't fire
//     doesn't log anything either).
//   - A reflex fires → event_log row written (decision log §14
//     write-site-discipline: real confidence/alternatives-considered/
//     reason, not a bare event name), fired_count bumped, task_execute
//     synthesized, plugin_envelope emitted on success.
func (s *chatServiceImpl) attemptReflexDispatch(
	ctx context.Context,
	sessionID, turnID, userContent, agentID, agentClass string,
	ls *loopState,
	ch chan chat.StreamEvent,
) reflexDispatchOutcome {
	if s.reflexEngine == nil || s.reflexEngine.Store == nil || s.store == nil || ls == nil {
		return reflexDispatchOutcome{}
	}
	tier, pattern := ls.Classification()
	if !tier.IsValid() || !pattern.IsValid() {
		// Degenerate pre-loop classification (e.g. a caller that never
		// ran classifyAndAttach). No signal to evaluate against.
		return reflexDispatchOutcome{}
	}

	class := agentClass
	if class == "" {
		// Same default chat_reflexes.go's evaluateAndInjectReflexes uses
		// for the general reflex pass — kept consistent so a
		// dispatch_to_agent reflex and an inject_reminder reflex seeded
		// for the same class actually see the same agent.
		class = "advisor"
	}

	// s.reflexEngine.Store (a concrete *store.Store, not the narrower
	// service.Store interface) is used directly here because
	// ListAgentReflexesForAgent/BumpAgentReflexFired are not part of the
	// composed service.Store interface s.store is typed as — the reflex
	// engine already holds the full store handle for its own Collect/
	// Evaluate pipeline (engine.go), so reusing it avoids widening the
	// interface just for this call site.
	allCandidates, err := s.reflexEngine.Store.ListAgentReflexesForAgent(ctx, agentID, class)
	if err != nil {
		slog.Warn("chat-service: dispatch-reflex list failed",
			"session_id", sessionID, "agent_id", agentID, "class", class, "err", err)
		return reflexDispatchOutcome{}
	}
	// This call site's own candidate list, per TASKS/reflex-taxonomy/
	// 03-shared-decision-engine.md: Resolve() (below) is the shared
	// combining-algorithm primitive, but it has no opinion on which rows a
	// caller passes it — filtering to dispatch_to_agent only is this
	// caller's own job, same as EvaluateState's dispatch_to_agent
	// exclusion is that (different) caller's own job.
	candidates := make([]store.AgentReflex, 0, len(allCandidates))
	for _, r := range allCandidates {
		if r.ActionKind == store.ReflexActionDispatchToAgent {
			candidates = append(candidates, r)
		}
	}

	state := reflexes.State{
		SessionID:        sessionID,
		AgentID:          agentID,
		AgentClass:       class,
		ScopeTier:        tier.String(),
		ExecutionPattern: pattern.String(),
		// Synthetic single-entry window over the CURRENT turn's raw text
		// — see design note (3). Not a DB read; userContent is the
		// in-flight string, which may not be persisted yet at this point
		// in generateResponse.
		UserMessages: []reflexes.MessageSignal{{Content: userContent}},
	}

	// Facet 4 recurrence cascade (design note 2 above,
	// TASKS/reflex-taxonomy/02-recurrence-cascade.md): looked up once
	// (there's only ever one dispatch_to_agent kind row, not one per
	// candidate) via the Engine's cached ActionKindDefaultRecurrenceSeconds
	// — the same accessor used pre-this-task. Under today's seed data
	// (dispatch_to_agent's kind-level default is 0, no row has a
	// reflex-level override) cooldownFn below always resolves to "not
	// suppressed" — this is exactly what makes design note (2) above
	// correct: the pipeline's own cascade, not a bypass of it.
	now := time.Now()
	kindDefault := s.reflexEngine.ActionKindDefaultRecurrenceSeconds(store.ReflexActionDispatchToAgent)
	cooldownFn := func(r store.AgentReflex) bool {
		cooldown := reflexes.EffectiveCooldown(kindDefault, r.RecurrenceOverrideSeconds)
		suppressed := reflexes.RecentlyFired(r, now, cooldown)
		if suppressed {
			slog.Info("chat-service: dispatch-reflex fired but suppressed by cooldown",
				"session_id", sessionID, "reflex", r.Name, "cooldown", cooldown)
		}
		return suppressed
	}
	// dispatch_to_agent's own reflex_action_kinds row (Facet 2's
	// combining_algorithm, seeded 'first_applicable' by migration
	// 124_reflex_action_taxonomy.sql) — fetched once and reused as the
	// ActionKindLookup Resolve() calls, since candidates above is already
	// filtered to this one kind so no other kind name is ever requested.
	dispatchKind, kindErr := s.reflexEngine.Store.GetReflexActionKind(ctx, store.ReflexActionDispatchToAgent)
	kindLookup := func(_ context.Context, _ string) (*store.ReflexActionKind, error) {
		return dispatchKind, kindErr
	}

	resolved, outcomes, resolveErr := reflexes.Resolve(ctx, candidates, state, s.reflexEngine.Executor, cooldownFn, kindLookup)
	if resolveErr != nil {
		slog.Warn("chat-service: dispatch-reflex resolve failed",
			"session_id", sessionID, "err", resolveErr)
		return reflexDispatchOutcome{}
	}
	for _, oc := range outcomes {
		if oc.TriggerError != "" {
			slog.Warn("chat-service: dispatch-reflex trigger eval failed",
				"session_id", sessionID, "reflex", oc.ReflexName, "err", oc.TriggerError)
		}
		if oc.ApplyError != "" {
			slog.Warn("chat-service: dispatch-reflex apply failed",
				"session_id", sessionID, "reflex", oc.ReflexName, "err", oc.ApplyError)
		}
	}

	if len(resolved.FiredReflexes) == 0 {
		return reflexDispatchOutcome{}
	}
	if len(resolved.FiredReflexes) > 1 {
		// TASKS/reflex-taxonomy/08-fix-resolve-fail-open-visibility.md:
		// canary for the same kind-lookup-failure fail-open Resolve() now
		// Warn-logs directly — dispatch_to_agent's kind is seeded
		// first_applicable (single winner), so more than one candidate
		// selected here means the kind lookup above degraded to
		// all_applicable. Only the first candidate is ever used below;
		// the rest are silently discarded, which is worth an operator-
		// visible note rather than a quiet drop.
		slog.Warn("chat-service: dispatch-reflex resolved multiple candidates, only the first is used",
			"session_id", sessionID,
			"candidate_count", len(resolved.FiredReflexes),
		)
	}
	winner := &resolved.FiredReflexes[0]
	winnerAction := resolved.Actions[0]

	agentSlug, _ := winnerAction.Spec["agent_slug"].(string)
	if agentSlug == "" {
		// validateReflexDefinition (internal/api/reflexes.go) rejects an
		// empty agent_slug at write time, so this should be unreachable
		// for anything created through the CRUD path — defensive only
		// (e.g. a row seeded/edited outside the API).
		slog.Warn("chat-service: dispatch-reflex fired with empty agent_slug, skipping dispatch",
			"session_id", sessionID, "reflex", winner.Name)
		return reflexDispatchOutcome{}
	}
	reason, _ := winnerAction.Spec["reason"].(string)
	if reason == "" {
		reason = "reflex:" + winner.Name
		// Reflected into the emitted trace record's own spec below too —
		// winnerAction.Spec and resolved.Actions[0].Spec are the same
		// underlying map (winnerAction is a value copy of the struct, but
		// Spec is a map, a reference type), so this mutation is visible
		// to EmitFirings without any extra plumbing.
		winnerAction.Spec["reason"] = reason
	}
	confidence, _ := winnerAction.Spec["confidence"].(float64)

	// TASKS/reflex-taxonomy/06-unified-reflex-telemetry.md: the event_log
	// write (decision log §14 write-site-discipline: real structured
	// reasoning, not a bare event name — this call site's own
	// alternatives_considered pattern is now the standard EmitFirings
	// generalizes to every kind), the fired_count/last_fired_at bump, and
	// plugin-hook emission (previously never fired from this call site —
	// gap 3 in the architecture doc's Telemetry section) are now one
	// centralized call, the same EmitFirings Engine.EvaluateState and
	// matchDispatchToAgentReflex go through.
	reflexes.EmitFirings(ctx, s.reflexEngine.Store, s.reflexEngine.Plugins, resolved, outcomes, state, reflexes.FiringContext{
		AgentID:    agentID,
		AgentClass: class,
	}, slog.Default())

	out := reflexDispatchOutcome{
		Matched:    true,
		ReflexID:   winner.ID,
		ReflexName: winner.Name,
		AgentSlug:  agentSlug,
		Confidence: confidence,
		Reason:     reason,
	}

	slog.Info("chat-service: dispatch-reflex fired",
		"session_id", sessionID,
		"reflex", winner.Name,
		"agent_slug", agentSlug,
		"reason", reason,
		"confidence", confidence,
	)

	if s.tools == nil {
		slog.Warn("chat-service: dispatch-reflex fired but ToolService not wired",
			"session_id", sessionID, "reflex", winner.Name)
		return out
	}

	// Synthesize the task_execute call — structurally identical to the
	// retired broker's own synthesized call (design note 4): session_id
	// and message are the only required args; parent_agent_id/turn_id
	// are forwarded when known. No agent_slug/role override is threaded
	// through; dispatch.AssignRole (inside task_execute's own dispatch
	// pipeline) independently re-derives Worker-vs-Planner from the same
	// classify.Classify output that fired this reflex.
	taskInput := map[string]any{
		"session_id":      sessionID,
		"parent_agent_id": agentID,
		"message":         userContent,
	}
	if turnID != "" {
		taskInput["turn_id"] = turnID
	}

	res, terr := s.tools.Execute(ctx, agentID, "task_execute", taskInput)
	if terr != nil {
		slog.Warn("chat-service: dispatch-reflex — task_execute failed, falling back to chat-direct",
			"session_id", sessionID, "reflex", winner.Name, "err", terr)
		return out
	}
	if res == nil || res.IsError {
		summary := ""
		if res != nil {
			summary = res.Output
		}
		slog.Info("chat-service: dispatch-reflex — task_execute returned error envelope, falling back to chat-direct",
			"session_id", sessionID, "reflex", winner.Name,
			"summary", chat.TruncateStr(summary, 200),
		)
		return out
	}
	out.Invoked = true

	if ch != nil && res.Output != "" {
		// Minimal parse — confirm the output is a JSON object before
		// pushing it onto the stream as a plugin_envelope. Mirrors the
		// retired broker's own probe.
		var probe map[string]any
		if err := json.Unmarshal([]byte(res.Output), &probe); err != nil {
			slog.Warn("chat-service: dispatch-reflex — task_execute output is not JSON, skipping envelope emit",
				"session_id", sessionID, "reflex", winner.Name, "err", err)
			return out
		}
		ch <- chat.StreamEvent{
			Type:     "plugin_envelope",
			Envelope: stampEnvelopeDisplayClass(res.Output, EnvelopeDisplayClassContent),
		}
		out.EmittedEnvelope = true
	}

	return out
}
