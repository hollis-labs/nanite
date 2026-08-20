package reflexes

// TASKS/reflex-taxonomy/06-unified-reflex-telemetry.md — the centralized
// telemetry emission every real Resolve() caller goes through immediately
// after Resolve() returns. Closes the three gaps
// docs/engineering/architecture/10-reflex-action-taxonomy.md's "Telemetry"
// section names, all stemming from the same root cause 03 already fixed
// for *selection* logic: three call sites independently implementing "a
// reflex fired," never unified for telemetry either.
//
//  1. Split sinks — dispatch_to_agent firings from attemptReflexDispatch
//     wrote to event_log; the same kind of event from
//     matchDispatchToAgentReflex instead wrote to playbook_match_log via
//     LogReflexMatch (a different table, different schema). Fixed by
//     routing every real firing through this one function into one sink.
//  2. fired_count/last_fired_at blind spot — matchDispatchToAgentReflex
//     never called Store.BumpAgentReflexFired. Fixed: this function bumps
//     it for every real firing, regardless of caller.
//  3. Plugin hooks blind to two of three paths — EmitReflexFired/
//     EmitReflexActionStaged were invoked exclusively from inside
//     Engine.EvaluateState's loop. Fixed: this function is the one place
//     those hooks fire now, called by all three real callers.
//
// Sink choice (this task's own implementation call, explicitly not an
// architecture one per the design doc's own words): event_log. It was
// already the majority path (Engine.EvaluateState via chat_reflexes.go,
// and attemptReflexDispatch both used it already) and it is a general-
// purpose table with no schema narrowly shaped for one action kind, unlike
// playbook_match_log (see self_tools_dispatch.go's retired
// ReflexMatchLogger for that history). No new dedicated table was added —
// event_log's existing (session_id, event_type, category, detail,
// metadata) shape is sufficient once event_type carries the fired
// action_kind and metadata carries this file's traceRecord.
//
// This is a "thin wrapper every caller of Resolve() goes through
// immediately" (the task's own second option, alongside folding emission
// into Resolve() itself) rather than a change to Resolve()'s own
// signature — Resolve()'s decision logic is independently, heavily unit
// tested (resolve_test.go) and adding caller-specific emission concerns
// (a store handle, plugin hooks, per-caller extra metadata) to its
// parameter list would entangle two things that don't need to be entangled:
// deciding what fired, and recording that it fired. Every real caller
// (Engine.EvaluateState, attemptReflexDispatch,
// matchDispatchToAgentReflex) already calls Resolve() and then does
// something with the result; this function is what all three now do with
// it, in place of each's own previously-divergent implementation.

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// TraceStore is the narrow persistence surface EmitFirings needs: the
// unified event_log write, and the fired_count/last_fired_at bump.
// *store.Store satisfies both methods. Kept as a two-method interface —
// not the full *store.Store — so this package's dependency surface stays
// exactly what telemetry needs, matching the same narrowing pattern
// ActionKindLookup/CooldownFunc already use in resolve.go.
type TraceStore interface {
	LogEvent(sessionID, eventType, category, detail, metadata string)
	BumpAgentReflexFired(ctx context.Context, id string, now time.Time) error
}

// FiringContext carries the caller-specific context EmitFirings needs
// beyond what Resolve() itself already computed — see the architecture
// doc's "One shared decision engine, multiple legitimate invocation
// points" section: how State gets built legitimately differs per call
// site, and so, in one caller's case, does a small amount of caller-local
// audit context. Neither is part of Resolve()'s own decision logic.
type FiringContext struct {
	// AgentID/AgentClass are folded into every emitted trace record and
	// into the plugin-hook payload (reflexEventData), matching what
	// Engine.EvaluateState already threaded through before this task.
	AgentID    string
	AgentClass string

	// ExtraMetadata, when non-nil, is merged as additional top-level keys
	// into every trace record this call emits. Used today only by
	// matchDispatchToAgentReflex (internal/mcp/self_tools_dispatch.go) to
	// fold the CW-20260816-0068 raw-vs-sent audit-trail pair
	// (raw_input_text/sent_input_text, collapsed to absent when
	// identical — same bloat-avoidance rule store.LogReflexMatch used to
	// apply) and a matched_input_excerpt into the unified sink, replacing
	// that call site's retired dedicated playbook_match_log write — see
	// TASKS/reflex-taxonomy/06-unified-reflex-telemetry.md's Work Log.
	// Every other real caller leaves this nil. Caller-supplied keys must
	// not collide with traceRecord's own field names below (they don't,
	// today).
	ExtraMetadata map[string]any
}

// traceRecord is the one consistent shape every reflex firing emits to
// the unified sink — TASKS/reflex-taxonomy/
// 06-unified-reflex-telemetry.md's "one trace record" requirement. Built
// once per fired AppliedAction from data Resolve() already computed (its
// own CandidateOutcome slice) — no re-evaluation of any trigger or
// combining algorithm happens here.
type traceRecord struct {
	ReflexID           string `json:"reflex_id"`
	ReflexName         string `json:"reflex_name"`
	ActionKind         string `json:"action_kind"`
	Category           string `json:"category,omitempty"`
	CombiningAlgorithm string `json:"combining_algorithm,omitempty"`
	// ProvenanceTier (Facet 3, 05-provenance-tier-enforcement.md) comes
	// straight off the fired store.AgentReflex row — every real row
	// carries a genuine tier as of that task, no lookup needed here.
	ProvenanceTier string `json:"provenance_tier,omitempty"`
	Priority       int64  `json:"priority"`

	AgentID    string `json:"agent_id,omitempty"`
	AgentClass string `json:"agent_class,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	// ScopeTier/ExecutionPattern mirror state.ScopeTier/
	// state.ExecutionPattern verbatim when the caller populated them
	// (only the two dispatch_to_agent call sites do, per State's own doc
	// comment) — empty/omitted for the generic per-turn pass.
	ScopeTier        string `json:"scope_tier,omitempty"`
	ExecutionPattern string `json:"execution_pattern,omitempty"`

	// Spec is the fired action's own action_kind-specific payload
	// (agent_slug/confidence/reason for dispatch_to_agent, body for
	// inject_reminder, tool_name/enforce for force_tool_choice, reason
	// for halt_session, etc.) — deliberately nested rather than
	// flattened to top-level keys, since different kinds carry different
	// fields and flattening them would make "one consistent shape"
	// false advertising.
	Spec map[string]interface{} `json:"spec,omitempty"`

	// AlternativesConsidered generalizes attemptReflexDispatch's
	// pre-existing dispatch_to_agent-only pattern (every candidate
	// evaluated that turn, fired or not) to every kind whose combining
	// algorithm is first_applicable or deny_overrides — the two
	// algorithms where "why did this one win" is a meaningful question.
	// all_applicable has no losers (every eligible candidate applies),
	// so it is omitted there. nil/omitted when the winning kind's
	// algorithm is all_applicable or unknown.
	AlternativesConsidered []alternativeOutcome `json:"alternatives_considered,omitempty"`
}

// alternativeOutcome is one non-winning competitor's summary inside a
// traceRecord's AlternativesConsidered — the same
// (reflex_id, reflex_name, fired) shape attemptReflexDispatch's own
// alternatives_considered list already used, plus cooldown_suppressed
// (part of this task's Facet-4 recurrence-outcome ask: a competitor whose
// trigger fired true but lost to cooldown is a distinct, worth-recording
// fact from one that simply never fired).
type alternativeOutcome struct {
	ReflexID           string `json:"reflex_id"`
	ReflexName         string `json:"reflex_name"`
	Fired              bool   `json:"fired"`
	CooldownSuppressed bool   `json:"cooldown_suppressed,omitempty"`
}

// EmitFirings is the single unified telemetry sink every real Resolve()
// caller — Engine.EvaluateState (engine.go), attemptReflexDispatch
// (internal/service/chat_reflex_dispatch.go), and
// matchDispatchToAgentReflex (internal/mcp/self_tools_dispatch.go) — goes
// through immediately after Resolve() returns. For every action in
// applied.Actions (i.e. every real firing this pass, already filtered/
// selected by Resolve() and, for Engine.EvaluateState, already run
// through the FilterReflexAction plugin filter by the caller): writes one
// event_log row (event_type = the fired action_kind, category "reflex",
// detail = the reflex name), bumps that reflex's fired_count/
// last_fired_at, and — when hooks is non-nil — invokes
// EmitReflexFired/EmitReflexActionStaged. A no-op when applied.Actions is
// empty (nothing fired this pass) or ts is nil.
//
// outcomes is Resolve()'s own returned []CandidateOutcome for the SAME
// call — used here only to build AlternativesConsidered and to recover
// each winner's Category/CombiningAlgorithm; never re-evaluated.
func EmitFirings(
	ctx context.Context,
	ts TraceStore,
	hooks PluginHooks,
	applied AppliedActions,
	outcomes []CandidateOutcome,
	state State,
	fc FiringContext,
	logger *slog.Logger,
) {
	if len(applied.Actions) == 0 {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	now := time.Now()

	for i, action := range applied.Actions {
		if i >= len(applied.FiredReflexes) {
			// Defensive only — AppliedActions.Actions/FiredReflexes are
			// always built 1:1 by both applyOne (resolve.go) and every
			// real caller's own construction; this should be
			// unreachable outside a caller-programming error.
			logger.Warn("reflexes.EmitFirings: applied.Actions/FiredReflexes length mismatch, skipping remaining actions",
				"actions", len(applied.Actions), "fired_reflexes", len(applied.FiredReflexes))
			return
		}
		r := applied.FiredReflexes[i]

		var winnerOutcome *CandidateOutcome
		for j := range outcomes {
			if outcomes[j].ReflexID == r.ID && outcomes[j].Selected {
				winnerOutcome = &outcomes[j]
				break
			}
		}

		rec := traceRecord{
			ReflexID:         r.ID,
			ReflexName:       r.Name,
			ActionKind:       action.ActionKind,
			ProvenanceTier:   r.ProvenanceTier,
			Priority:         r.Priority,
			AgentID:          fc.AgentID,
			AgentClass:       fc.AgentClass,
			SessionID:        state.SessionID,
			ScopeTier:        state.ScopeTier,
			ExecutionPattern: state.ExecutionPattern,
			Spec:             action.Spec,
		}

		algo := ""
		if winnerOutcome != nil {
			rec.Category = winnerOutcome.Category
			rec.CombiningAlgorithm = winnerOutcome.CombiningAlgorithm
			algo = winnerOutcome.CombiningAlgorithm
		}

		// TASKS/reflex-taxonomy/06-unified-reflex-telemetry.md item 4:
		// generalize attemptReflexDispatch's alternatives_considered
		// pattern to every kind under first_applicable/deny_overrides.
		// Grouped by ActionKind (not CombiningAlgorithm) so a candidate
		// whose trigger never fired — and therefore never entered
		// Resolve()'s eligibleByKind, so its own CombiningAlgorithm was
		// never set — still shows up as a considered-but-not-fired
		// alternative, matching "every candidate evaluated that turn,
		// fired or not." For deny_overrides specifically this slightly
		// under-generalizes the theoretical cross-kind union Facet 2
		// describes (today's only deny_overrides kind is halt_session,
		// so same-ActionKind and same-algorithm coincide in practice);
		// see the task's Work Log for why that's an accepted, documented
		// simplification rather than a second per-candidate kind lookup.
		if algo == "first_applicable" || algo == "deny_overrides" {
			for _, cand := range outcomes {
				if cand.ReflexID == r.ID || cand.ActionKind != action.ActionKind {
					continue
				}
				rec.AlternativesConsidered = append(rec.AlternativesConsidered, alternativeOutcome{
					ReflexID:           cand.ReflexID,
					ReflexName:         cand.ReflexName,
					Fired:              cand.TriggerFired,
					CooldownSuppressed: cand.CooldownSuppressed,
				})
			}
		}

		metaJSON, err := json.Marshal(rec)
		if err != nil {
			logger.Warn("reflexes.EmitFirings: marshal trace record failed", "reflex", r.Name, "err", err)
			metaJSON = []byte("{}")
		}
		if len(fc.ExtraMetadata) > 0 {
			var m map[string]any
			if uerr := json.Unmarshal(metaJSON, &m); uerr == nil {
				for k, v := range fc.ExtraMetadata {
					m[k] = v
				}
				if merged, merr := json.Marshal(m); merr == nil {
					metaJSON = merged
				}
			}
		}

		if ts != nil {
			ts.LogEvent(state.SessionID, action.ActionKind, "reflex", r.Name, string(metaJSON))
			if err := ts.BumpAgentReflexFired(ctx, r.ID, now); err != nil {
				logger.Warn("reflexes.EmitFirings: bump fired_count failed",
					"reflex", r.Name, "err", err)
			}
		}

		if hooks != nil {
			data := reflexEventData(fc.AgentID, fc.AgentClass, r, action, state)
			hooks.EmitReflexFired(state.SessionID, data)
			hooks.EmitReflexActionStaged(state.SessionID, data)
		}
	}
}
