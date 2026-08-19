package reflexes

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/store"
)

// BaseReflexSeed declares a class-bound base reflex (agent_id NULL,
// class_tag set). The seeder inserts these idempotently — on every
// boot, missing rows are added but existing rows are left alone so
// operators can mutate (pause / delete / re-priority) without seed
// undoing it.
//
// Required (Phase 1 item 07, TASKS/phase-1/07-add-reflex-opt-out-field.md)
// marks a seed as "cannot opt out": SeedBaseReflexes sets the inserted
// row's opt_out_allowed to false for these, so no per-agent opt-out
// entry in agent_reflex_opt_outs can suppress it. Zero-value default
// (false, i.e. not required) preserves every existing seed's current
// opt-out-able behavior without having to touch each literal below —
// only the genuinely safety-critical ones set Required: true.
type BaseReflexSeed struct {
	ClassTag    string
	Name        string
	TriggerKind string
	TriggerSpec map[string]interface{}
	ActionKind  string
	ActionSpec  map[string]interface{}
	Priority    int64
	Required    bool
}

// BaseSeeds returns the canonical per-class base reflex seeds — 11 as
// of this writing (process: 6, advisor: 3, template: 2; TASKS/phase-1/
// 07-add-reflex-opt-out-field.md's task file cites 12, an off-by-one in
// the task file itself, not in this count — corrected here per
// EXECUTION-PROCESS.md worker step 7).
//
// All predicates are conjunctions — see types.go's package doc for
// rationale. The conjunction principle is the FU-30 design contract;
// the corresponding tests in evaluator_test.go encode it.
//
// Phase 1 item 07 safety audit (which of these 11 get Required: true,
// i.e. opt_out_allowed=false): exactly the three whose action_kind is
// halt_session — drift_detector_echo, task_complete_self_terminate,
// task_timeout. All three are hard kill switches (runaway-detection or
// lifecycle-cap), where letting an agent suppress its own halt defeats
// the mechanism. Every inject_reminder seed (the other 8) is a nudge an
// agent can legitimately outgrow or find too noisy, so all 8 stay at
// the permissive default (opt_out_allowed=true).
func BaseSeeds() []BaseReflexSeed {
	return []BaseReflexSeed{
		// ── class=process ────────────────────────────────────────
		//
		// drift_detector_echo: the FU-13 cache-miss singleton-race
		// signature. Trigger requires ALL of: zero tool calls across
		// 3 turns, zero cache_read, input_tokens < 10, and identical
		// output across the window. The conjunction blocks
		// false-positives on healthy idle compression (which has
		// cache_read in the millions). Required: true (Phase 1 item
		// 07 safety audit) — this is a kill switch for a real runaway
		// attractor; an agent opting out of its own drift-halt would
		// defeat the mechanism entirely.
		{
			ClassTag:    "process",
			Name:        "drift_detector_echo",
			TriggerKind: "predicate",
			Priority:    100,
			Required:    true,
			TriggerSpec: map[string]interface{}{
				"kind": "AND",
				"clauses": []interface{}{
					map[string]interface{}{"kind": "tool_calls_window", "window": 3, "op": "=", "value": 0},
					map[string]interface{}{"kind": "cache_read_window", "window": 3, "op": "=", "value": 0},
					map[string]interface{}{"kind": "input_tokens_window", "window": 3, "op": "<", "value": 10},
					map[string]interface{}{"kind": "identical_output_window", "window": 3},
				},
			},
			ActionKind: "halt_session",
			ActionSpec: map[string]interface{}{
				"reason": "drift_detector_echo: cache-miss singleton race",
			},
		},

		// runaway_superlative_detector: the escalating-language
		// attractor (Beyond/Absolute/Ultimate/Maximum + Emergency/Crisis/...).
		// Conjunction: 3 consecutive turns with zero tool calls AND
		// each turn's output is at least 1.5x the prior AND the
		// escalation regex matches somewhere in the window. Action is
		// inject_reminder (Reorient knock) — NOT halt. The agent gets
		// a chance to course-correct before we kill the session.
		{
			ClassTag:    "process",
			Name:        "runaway_superlative_detector",
			TriggerKind: "predicate",
			Priority:    90,
			TriggerSpec: map[string]interface{}{
				"kind": "AND",
				"clauses": []interface{}{
					map[string]interface{}{"kind": "tool_calls_window", "window": 3, "op": "=", "value": 0},
					map[string]interface{}{"kind": "output_growth_window", "window": 3, "factor": 1.5},
					map[string]interface{}{
						"kind":    "regex_match_window",
						"window":  3,
						"pattern": `(?i)(Beyond|Absolute|Ultimate|Maximum)\s+(Emergency|Crisis|Unprecedented)`,
					},
				},
			},
			ActionKind: "inject_reminder",
			ActionSpec: map[string]interface{}{
				"body":    "Reorient: escalating-language pattern detected (Beyond/Absolute/Ultimate/Maximum + Emergency/Crisis). Pause and re-ground in concrete state. What is the actual next-action that advances the task?",
				"urgency": "warn",
			},
		},

		// wake_on_mail: when the mailbox has unread messages, surface
		// a reminder so the agent picks them up next turn. Maps to
		// inject_reminder for now; the live wake-from-sleep wiring
		// lands when session status='sleeping' is plumbed through.
		{
			ClassTag:    "process",
			Name:        "wake_on_mail",
			TriggerKind: "event",
			Priority:    50,
			TriggerSpec: map[string]interface{}{
				"name": "mail_received",
			},
			ActionKind: "inject_reminder",
			ActionSpec: map[string]interface{}{
				"body":    "Mail in the inbox — call mux_message_inbox to triage before continuing.",
				"urgency": "info",
			},
		},

		// context_pressure: prefix tokens crossing 0.85× of the
		// context window. Spike-only inject_reminder; the actual
		// compaction lives in FU-15. Interval-1 so it runs every tick.
		// TODO(FU-15): swap to a real compaction trigger once the
		// engine lands.
		{
			ClassTag:    "process",
			Name:        "context_pressure",
			TriggerKind: "predicate",
			Priority:    40,
			TriggerSpec: map[string]interface{}{
				"kind":           "prefix_pressure",
				"ratio":          0.85,
				"context_window": 200000,
			},
			ActionKind: "inject_reminder",
			ActionSpec: map[string]interface{}{
				"body":    "Context-pressure notice: prefix is over 85% of the window. Consider compacting or wrapping the current line of work before continuing.",
				"urgency": "warn",
			},
		},

		// clean_status_without_tools: a monitor/process agent reporting
		// "all clear" / "healthy" for consecutive turns without tool
		// calls is probably narrating stale confidence rather than
		// observing current state. This is a nudge only: the agent should
		// re-ground in probes/mailbox/source-of-truth before making the
		// next clean-status claim.
		{
			ClassTag:    "process",
			Name:        "clean_status_without_tools",
			TriggerKind: "predicate",
			Priority:    75,
			TriggerSpec: map[string]interface{}{
				"kind": "AND",
				"clauses": []interface{}{
					map[string]interface{}{"kind": "tool_calls_window", "window": 2, "op": "=", "value": 0},
					map[string]interface{}{
						"kind":    "regex_match_window",
						"window":  2,
						"pattern": `(?i)\b(all clear|healthy|no issues|nominal|nothing new|looks good)\b`,
					},
				},
			},
			ActionKind: "inject_reminder",
			ActionSpec: map[string]interface{}{
				"body":    "Grounding nudge: clean-status language appeared without recent tool calls. Check the relevant source-of-truth/probes before reporting another all-clear.",
				"urgency": "warn",
			},
		},

		// repeated_planning_without_action: repeated planning language
		// across a process agent's recent window with zero tool calls is
		// the "talking about the loop" failure mode. The nudge asks for
		// one concrete action, not a mode switch or hidden dispatch.
		{
			ClassTag:    "process",
			Name:        "repeated_planning_without_action",
			TriggerKind: "predicate",
			Priority:    65,
			TriggerSpec: map[string]interface{}{
				"kind": "AND",
				"clauses": []interface{}{
					map[string]interface{}{"kind": "tool_calls_window", "window": 4, "op": "=", "value": 0},
					map[string]interface{}{
						"kind":    "regex_match_window",
						"window":  4,
						"pattern": `(?i)\b(plan|next pass|will continue|should proceed|going to)\b`,
					},
				},
			},
			ActionKind: "inject_reminder",
			ActionSpec: map[string]interface{}{
				"body":    "Action nudge: recent turns are planning without observable action. Pick one concrete tool/source-of-truth check or pause with an explicit blocker.",
				"urgency": "info",
			},
		},

		// ── class=advisor ────────────────────────────────────────
		{
			ClassTag:    "advisor",
			Name:        "wake_on_mail",
			TriggerKind: "event",
			Priority:    50,
			TriggerSpec: map[string]interface{}{
				"name": "mail_received",
			},
			ActionKind: "inject_reminder",
			ActionSpec: map[string]interface{}{
				"body":    "Mail in the inbox — call mux_message_inbox to triage.",
				"urgency": "info",
			},
		},
		// idle_drift_advisor: advisors call tools far less frequently
		// than process agents, so the drift window is widened to 5
		// and uses a softer signature (no growth/regex requirements).
		// Still conjunctive: tool_calls=0 AND input_tokens<5 over the
		// window, both of which together indicate the model is stuck.
		{
			ClassTag:    "advisor",
			Name:        "idle_drift_advisor",
			TriggerKind: "predicate",
			Priority:    80,
			TriggerSpec: map[string]interface{}{
				"kind": "AND",
				"clauses": []interface{}{
					map[string]interface{}{"kind": "tool_calls_window", "window": 5, "op": "=", "value": 0},
					map[string]interface{}{"kind": "input_tokens_window", "window": 5, "op": "<", "value": 5},
				},
			},
			ActionKind: "inject_reminder",
			ActionSpec: map[string]interface{}{
				"body":    "Idle-drift advisory: 5 consecutive turns with no tool calls and minimal input. State the next concrete action or pause cleanly.",
				"urgency": "info",
			},
		},
		// evidence_claim_without_tools: advisors may answer from memory,
		// but claims like "verified", "current", or "latest" without
		// recent tool grounding should be labeled as inference or backed
		// by a source on the next turn.
		{
			ClassTag:    "advisor",
			Name:        "evidence_claim_without_tools",
			TriggerKind: "predicate",
			Priority:    70,
			TriggerSpec: map[string]interface{}{
				"kind": "AND",
				"clauses": []interface{}{
					map[string]interface{}{"kind": "tool_calls_window", "window": 2, "op": "=", "value": 0},
					map[string]interface{}{
						"kind":    "regex_match_window",
						"window":  2,
						"pattern": `(?i)\b(verified|confirmed|checked|looked up|current|latest|today|as of)\b`,
					},
				},
			},
			ActionKind: "inject_reminder",
			ActionSpec: map[string]interface{}{
				"body":    "Grounding nudge: evidence/currentness language appeared without recent tool use. Cite the source if you have one; otherwise mark it as inference and verify before relying on it.",
				"urgency": "info",
			},
		},

		// ── class=template ───────────────────────────────────────
		//
		// FU-32 will revisit these with instance-mode lifecycle wiring.
		// For now they are placeholders so the class has at least one
		// seeded reflex.
		// task_complete_self_terminate: Required: true (Phase 1 item 07
		// safety audit) — a template-instance agent that could opt out
		// of its own post-completion halt would keep running (and
		// consuming resources) indefinitely past the point its
		// instance-mode lifecycle says it should stop.
		{
			ClassTag:    "template",
			Name:        "task_complete_self_terminate",
			TriggerKind: "event",
			Priority:    100,
			Required:    true,
			TriggerSpec: map[string]interface{}{
				"name": "task_completed",
			},
			ActionKind: "halt_session",
			ActionSpec: map[string]interface{}{
				"reason": "task_complete_self_terminate",
			},
		},
		// task_timeout: Required: true (Phase 1 item 07 safety audit) —
		// the hard runaway cap for template-instance agents, same class
		// of protection as drift_detector_echo, just interval-triggered.
		{
			ClassTag:    "template",
			Name:        "task_timeout",
			TriggerKind: "interval",
			Priority:    10,
			Required:    true,
			TriggerSpec: map[string]interface{}{
				"every_n_ticks": 20,
			},
			ActionKind: "halt_session",
			ActionSpec: map[string]interface{}{
				"reason": "task_timeout: exceeded 20 ticks",
			},
		},
	}
}

// SeedBaseReflexes inserts the canonical base reflex seeds into the
// agent_reflexes table. Idempotent: rows that already exist (matching
// class_tag + name with agent_id NULL) are left alone so operator
// mutations survive re-boot.
//
// Returns the number of newly inserted seeds.
func SeedBaseReflexes(ctx context.Context, st *store.Store, logger *slog.Logger) (int, error) {
	if logger == nil {
		logger = slog.Default()
	}
	seeds := BaseSeeds()
	inserted := 0
	for _, s := range seeds {
		n, err := st.CountClassBaseReflexByName(ctx, s.ClassTag, s.Name)
		if err != nil {
			logger.Warn("seed: count failed", "class", s.ClassTag, "name", s.Name, "err", err)
			continue
		}
		if n > 0 {
			continue
		}
		triggerJSON, err := json.Marshal(s.TriggerSpec)
		if err != nil {
			return inserted, fmt.Errorf("marshal trigger %s: %w", s.Name, err)
		}
		actionJSON, err := json.Marshal(s.ActionSpec)
		if err != nil {
			return inserted, fmt.Errorf("marshal action %s: %w", s.Name, err)
		}
		if _, err := st.InsertAgentReflex(ctx, store.AgentReflex{
			ClassTag:      s.ClassTag,
			Name:          s.Name,
			TriggerKind:   s.TriggerKind,
			TriggerSpec:   string(triggerJSON),
			ActionKind:    s.ActionKind,
			ActionSpec:    string(actionJSON),
			Priority:      s.Priority,
			CreatedBy:     "system",
			OptOutAllowed: !s.Required,
		}); err != nil {
			return inserted, fmt.Errorf("seed %s: %w", s.Name, err)
		}
		inserted++
	}
	return inserted, nil
}
