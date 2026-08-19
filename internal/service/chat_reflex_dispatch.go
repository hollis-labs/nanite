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
//     inject_reminder/force_tool_choice/halt_session/etc.) because that
//     pipeline suppresses a reflex from re-firing for 15 minutes
//     (Engine.recentlyFired). That debounce is correct for a nudge
//     (inject_reminder) but wrong for a routing decision — the retired
//     broker re-evaluated Rule 5 fresh on every single turn, with no
//     cooldown. attemptReflexDispatch below re-implements the
//     list-active-reflexes + evaluate-trigger steps directly (via
//     reflexes.EvaluateTrigger, the same exported primitive
//     Engine.EvaluateState calls internally) and calls
//     reflexEngine.Executor.Apply once per candidate purely to get the
//     parsed action_spec back as an AppliedAction — no hook, no
//     recently-fired gate, no fired_count bump from Apply itself. This
//     file bumps fired_count directly (best-effort, for the operator UI's
//     fired_count/last_fired_at telemetry) without the debounce gate.
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
	candidates, err := s.reflexEngine.Store.ListAgentReflexesForAgent(ctx, agentID, class)
	if err != nil {
		slog.Warn("chat-service: dispatch-reflex list failed",
			"session_id", sessionID, "agent_id", agentID, "class", class, "err", err)
		return reflexDispatchOutcome{}
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

	type evalResult struct {
		id, name string
		fired    bool
	}
	results := make([]evalResult, 0, len(candidates))

	var winner *store.AgentReflex
	var winnerAction reflexes.AppliedAction

	for i := range candidates {
		r := candidates[i]
		if r.ActionKind != store.ReflexActionDispatchToAgent {
			continue
		}
		fired, evalErr := reflexes.EvaluateTrigger(r.TriggerKind, r.TriggerSpec, state)
		if evalErr != nil {
			slog.Warn("chat-service: dispatch-reflex trigger eval failed",
				"session_id", sessionID, "reflex", r.Name, "err", evalErr)
			results = append(results, evalResult{id: r.ID, name: r.Name, fired: false})
			continue
		}
		results = append(results, evalResult{id: r.ID, name: r.Name, fired: fired})
		if !fired || winner != nil {
			continue
		}
		action, applyErr := s.reflexEngine.Executor.Apply(ctx, r, state)
		if applyErr != nil {
			slog.Warn("chat-service: dispatch-reflex apply failed",
				"session_id", sessionID, "reflex", r.Name, "err", applyErr)
			continue
		}
		rCopy := r
		winner = &rCopy
		winnerAction = action
	}

	if winner == nil {
		return reflexDispatchOutcome{}
	}

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
	}
	confidence, _ := winnerAction.Spec["confidence"].(float64)

	// Best-effort fired_count/last_fired_at bump — mirrors the operator
	// UI telemetry every other action kind gets via Engine.EvaluateState,
	// without that pipeline's 15-minute recently-fired debounce (design
	// note 2). Failure does not block the dispatch.
	if err := s.reflexEngine.Store.BumpAgentReflexFired(ctx, winner.ID, time.Now()); err != nil {
		slog.Warn("chat-service: dispatch-reflex bump fired_count failed",
			"session_id", sessionID, "reflex", winner.Name, "err", err)
	}

	// decision log §14 write-site-discipline: real structured reasoning,
	// not a bare event name. alternatives_considered lists every
	// dispatch_to_agent candidate evaluated this turn (not just the
	// winner) so an operator/auditor can see why this reflex won over
	// the others — including candidates whose trigger did NOT fire.
	alternatives := make([]map[string]any, 0, len(results))
	for _, r := range results {
		if r.id == winner.ID {
			continue
		}
		alternatives = append(alternatives, map[string]any{
			"reflex_id":   r.id,
			"reflex_name": r.name,
			"fired":       r.fired,
		})
	}
	meta := map[string]any{
		"reflex_id":               winner.ID,
		"reflex_name":             winner.Name,
		"agent_slug":              agentSlug,
		"confidence":              confidence,
		"reason":                  reason,
		"scope_tier":              tier.String(),
		"execution_pattern":       pattern.String(),
		"alternatives_considered": alternatives,
	}
	if metaJSON, mErr := json.Marshal(meta); mErr != nil {
		slog.Warn("chat-service: dispatch-reflex marshal event_log metadata failed",
			"session_id", sessionID, "reflex", winner.Name, "err", mErr)
	} else {
		s.store.LogEvent(sessionID, "dispatch_to_agent", "reflex", reason, string(metaJSON))
	}

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
