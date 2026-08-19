package reflexes

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
)

// anyPhraseRegex builds a case-insensitive "any of these phrases is a
// substring of the message" regex pattern from a literal phrase list —
// the trigger shape TASKS/phase-4/03-migrate-promptrouter-to-reflexes.md
// uses to migrate internal/promptrouter's UserPhraseAnyOf catalog onto
// the reflex engine's user_regex_window predicate (a documented superset
// of promptrouter's flat phrase-list matching, per architecture doc
// 03-steering.md: "the reflex predicate engine already supports
// regex-match triggers, a superset of promptrouter's flat phrase-list
// matching"). Each phrase is regexp.QuoteMeta-escaped before joining —
// none of the migrated phrases below actually contain a regex
// metacharacter, but this keeps future phrase edits safe by
// construction rather than by re-auditing every string for
// specialness.
func anyPhraseRegex(phrases []string) string {
	quoted := make([]string, len(phrases))
	for i, p := range phrases {
		quoted[i] = regexp.QuoteMeta(p)
	}
	return "(?i)(" + strings.Join(quoted, "|") + ")"
}

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

// BaseSeeds returns the canonical per-class base reflex seeds — 18 as
// of this writing (process: 6, advisor: 10, template: 2; TASKS/phase-1/
// 07-add-reflex-opt-out-field.md's task file cites 12 against an
// 11-seed baseline, an off-by-one in that task file itself, not in this
// count — corrected here per EXECUTION-PROCESS.md worker step 7. Phase 4
// item 02 added the 12th seed, advisor's dispatch_to_agent_open_subagent.
// Phase 4 item 03 (TASKS/phase-4/03-migrate-promptrouter-to-reflexes.md)
// added 6 more advisor-class dispatch_to_agent seeds, migrating the 6
// non-phantom entries of the retired internal/promptrouter package's
// BuiltinReflexes() phrase catalog — see the "migrated from
// promptrouter" seeds below for the per-entry mapping and the task
// file's Work Log for the phantom-entry (documentor-mention,
// strategist-mention) drop decision).
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

		// dispatch_to_agent_open_subagent (Phase 4 item 02,
		// TASKS/phase-4/02-dispatch-to-agent-reflex-action-kind-and-broker-migration.md):
		// migrated from the retired agent-broker's Rule 5
		// (agentkit/broker/deterministic.go's ScopeTier==TierOpen &&
		// ExecutionPattern==PatternSubagent -> planner rule, fixed
		// confidence 0.75, mode-independent — see that task file's
		// Context for the full rule enumeration). Class-bound to
		// "advisor" only, NOT "process"/"template" (the old broker had
		// no class gating at all — this narrows it): process/template
		// class agents are exclusively subagent/instance contexts
		// already blocked by task_execute's own hard recursion-depth
		// cap (internal/mcp/self_tools_dispatch.go's recursionBlocked),
		// so seeding this reflex for those classes would only ever
		// produce a swallowed "recursion blocked" dispatch failure —
		// never a different observable outcome than not seeding them.
		// Priority 10 — deliberately low among dispatch_to_agent
		// reflexes so a future, more-specific promptrouter-migrated
		// phrase-match reflex (the old broker's Rule 1, higher priority
		// than Rule 5 in the retired priority-ordered rule list) can be
		// seeded above it and win first. See
		// internal/service/chat_reflex_dispatch.go for the evaluation
		// call site (priority DESC, first fire wins).
		{
			ClassTag:    "advisor",
			Name:        "dispatch_to_agent_open_subagent",
			TriggerKind: "predicate",
			Priority:    10,
			TriggerSpec: map[string]interface{}{
				"kind": "AND",
				"clauses": []interface{}{
					map[string]interface{}{"kind": "scope_tier", "op": "=", "value": "open"},
					map[string]interface{}{"kind": "execution_pattern", "op": "=", "value": "subagent"},
				},
			},
			ActionKind: "dispatch_to_agent",
			ActionSpec: map[string]interface{}{
				"agent_slug": "planner",
				"confidence": 0.75,
				"reason":     "scope_tier=open + execution_pattern=subagent (migrated agent-broker Rule 5)",
			},
		},

		// ── class=advisor, migrated from internal/promptrouter ─────
		//
		// TASKS/phase-4/03-migrate-promptrouter-to-reflexes.md: the 6
		// non-phantom entries of the retired internal/promptrouter
		// package's BuiltinReflexes() (catalog.go), migrated onto
		// dispatch_to_agent reflex rows. Two entries (documentor-mention,
		// strategist-mention) are deliberately DROPPED, not migrated —
		// both left `resolves_to.profile` empty in the old catalog
		// because no matching agent profile exists (no `documentor.md`;
		// `.nanite/agents/content-strategist.md` is a real but unrelated,
		// narrower Glyph-editorial role) — see the task file's Work Log
		// for the full phantom-entry accounting.
		//
		// Priority: every entry here is > 10 (dispatch_to_agent_open_
		// subagent's priority, above) so a specific phrase match always
		// wins over the general open-tier/subagent-pattern fallback —
		// mirroring the old promptrouter-fed broker's Rule 1 (phrase
		// match) being checked before Rule 5 (tier/pattern) in the
		// retired priority-ordered rule list (see task 02's Work Log,
		// design decision 2). Relative ordering among these 6 preserves
		// the original promptrouter catalog's Priority field ordering
		// (25 > 20 > 18 > 15 = 15 > 10), rescaled upward with distinct
		// values so DB created_at tie-break timing never matters.
		// `confidence` is the original promptrouter Priority/100.0 (the
		// same derivation task 02's retired broker Rule 1 used for
		// in.ReflexConfidence) — an audit/telemetry value only, not
		// gated on anywhere, same as every other dispatch_to_agent
		// reflex's confidence field.
		//
		// Predicate shape: user_regex_window (scope=user implied,
		// window=1) matches the CURRENT turn's raw text — the synthetic
		// single-entry State.UserMessages window
		// internal/service/chat_reflex_dispatch.go populates (Phase 4
		// item 02's design note 3). Entries that originally carried a
		// ScopeTierHint/ExecutionPatternHint guard AND that phrase
		// window with a scope_tier/execution_pattern predicate;
		// tierAtLeast's old ">=" semantics (a ScopeTierHint of X means
		// "X or broader") are reproduced via an OR over every tier at or
		// above X, since scope_tier's predicate is plain string equality,
		// not a comparison.
		{
			ClassTag:    "advisor",
			Name:        "dispatch_to_agent_background_long_task",
			TriggerKind: "predicate",
			Priority:    60,
			TriggerSpec: map[string]interface{}{
				"kind": "AND",
				"clauses": []interface{}{
					map[string]interface{}{
						"kind": "user_regex_window", "window": 1,
						"pattern": anyPhraseRegex([]string{
							"in the background", "async", "when you get a chance",
							"overnight", "index the whole", "crawl the entire",
						}),
					},
					map[string]interface{}{"kind": "execution_pattern", "op": "=", "value": "background"},
				},
			},
			ActionKind: "dispatch_to_agent",
			ActionSpec: map[string]interface{}{
				"agent_slug": "worker",
				"confidence": 0.25,
				"reason":     "phrase match: background-long-task (migrated from internal/promptrouter)",
			},
		},
		{
			ClassTag:    "advisor",
			Name:        "dispatch_to_agent_planner_mention",
			TriggerKind: "predicate",
			Priority:    50,
			TriggerSpec: map[string]interface{}{
				"kind": "AND",
				"clauses": []interface{}{
					map[string]interface{}{
						"kind": "user_regex_window", "window": 1,
						"pattern": anyPhraseRegex([]string{
							"let's plan", "let's work on", "sprint",
							"plan this", "plan out", "create a plan",
						}),
					},
					map[string]interface{}{"kind": "scope_tier", "op": "=", "value": "open"},
				},
			},
			ActionKind: "dispatch_to_agent",
			ActionSpec: map[string]interface{}{
				"agent_slug": "planner",
				"confidence": 0.20,
				"reason":     "phrase match: planner-mention (migrated from internal/promptrouter)",
			},
		},
		{
			ClassTag:    "advisor",
			Name:        "dispatch_to_agent_planner_large_task",
			TriggerKind: "predicate",
			Priority:    45,
			TriggerSpec: map[string]interface{}{
				"kind": "AND",
				"clauses": []interface{}{
					map[string]interface{}{
						"kind": "user_regex_window", "window": 1,
						"pattern": anyPhraseRegex([]string{
							"big project", "multi-step", "break this down",
							"sequence of", "phases", "end to end",
						}),
					},
					// scope_tier_hint: large in the old catalog meant
					// "large or broader" (tierAtLeast) — large's only
					// broader tier is open, so OR both.
					map[string]interface{}{
						"kind": "OR",
						"clauses": []interface{}{
							map[string]interface{}{"kind": "scope_tier", "op": "=", "value": "large"},
							map[string]interface{}{"kind": "scope_tier", "op": "=", "value": "open"},
						},
					},
				},
			},
			ActionKind: "dispatch_to_agent",
			ActionSpec: map[string]interface{}{
				"agent_slug": "planner",
				"confidence": 0.18,
				"reason":     "phrase match: planner-large-task (migrated from internal/promptrouter)",
			},
		},
		{
			ClassTag:    "advisor",
			Name:        "dispatch_to_agent_researcher_mention",
			TriggerKind: "predicate",
			Priority:    35,
			TriggerSpec: map[string]interface{}{
				"kind": "user_regex_window", "window": 1,
				"pattern": anyPhraseRegex([]string{
					"research", "investigate", "look into", "find out",
					"dig into", "what does", "find all", "summarize the state",
				}),
			},
			ActionKind: "dispatch_to_agent",
			ActionSpec: map[string]interface{}{
				"agent_slug": "researcher",
				"confidence": 0.15,
				"reason":     "phrase match: researcher-mention (migrated from internal/promptrouter)",
			},
		},
		{
			ClassTag:    "advisor",
			Name:        "dispatch_to_agent_reviewer_mention",
			TriggerKind: "predicate",
			Priority:    30,
			TriggerSpec: map[string]interface{}{
				"kind": "user_regex_window", "window": 1,
				"pattern": anyPhraseRegex([]string{
					"review", "assess", "second opinion", "critique",
					"audit", "check this", "give me feedback on",
				}),
			},
			ActionKind: "dispatch_to_agent",
			ActionSpec: map[string]interface{}{
				"agent_slug": "reviewer",
				"confidence": 0.15,
				"reason":     "phrase match: reviewer-mention (migrated from internal/promptrouter)",
			},
		},
		{
			ClassTag:    "advisor",
			Name:        "dispatch_to_agent_worker_execute",
			TriggerKind: "predicate",
			Priority:    20,
			TriggerSpec: map[string]interface{}{
				"kind": "user_regex_window", "window": 1,
				"pattern": anyPhraseRegex([]string{
					"build", "implement", "fix", "refactor", "write the code",
					"add the feature", "make it", "run the migration",
				}),
			},
			ActionKind: "dispatch_to_agent",
			ActionSpec: map[string]interface{}{
				"agent_slug": "worker",
				"confidence": 0.10,
				"reason":     "phrase match: worker-execute (migrated from internal/promptrouter)",
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
