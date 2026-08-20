# Reflex Catalog (live inventory, 2026-08-20)

A point-in-time enumeration of every reflex actually seeded in this codebase — what triggers it, what it's scoped to, and what it does when it fires. Companion to [10-reflex-action-taxonomy.md](10-reflex-action-taxonomy.md), which covers the *design* (categories, combining algorithms, provenance tiers) but doesn't enumerate live instances. This doc will drift out of date as `seeds.go`/`loom_pilot_seeds.go` change — treat it as a snapshot, not a generated artifact.

**Source of truth:** every live reflex is code-seeded (idempotent insert-if-missing on every boot, `internal/service/container.go:933,947`). There is no other reflex-creation call site in the repo — confirmed by grepping all six `action_kind` literals across `internal/`; only `seeds.go`, `loom_pilot_seeds.go`, and the store constants file reference them. An operator- or plugin-authored reflex would live only in the runtime `agent_reflexes` table (via the CRUD API / `pending_reflexes` approval flow) and wouldn't show up here.

**Total: 21 reflexes** — 18 class-scoped base seeds (`internal/agent/reflexes/seeds.go`) + 3 agent-scoped Loom Curator/Weaver pilot seeds (`internal/agent/reflexes/loom_pilot_seeds.go`).

---

## Scope model

A reflex is scoped one of two ways (`internal/store/agent_reflexes.go:275`, `ListAgentReflexesForAgent`):

- **`class_tag`** — matches every agent of that class (`process` / `advisor` / `template`). Opt-out-able per-agent unless `Required: true`.
- **`agent_id`** — targets one specific resolved agent profile by slug (currently only `loom-weaver` and `loom-curator`). **Opt-out is not checked on this branch** — agent-scoped reflexes can't currently be opted out per-agent regardless of their `opt_out_allowed` flag.

There is no session-level or global scope.

## Action-kind reference

From migration `124_reflex_action_taxonomy.sql` (`reflex_action_kinds` table):

| action_kind | category | combining algorithm | default cooldown |
|---|---|---|---|
| `inject_reminder` | system_message | all_applicable | 15 min |
| `force_tool_choice` | system_message | first_applicable | 15 min |
| `send_message` | execute_action | all_applicable | 15 min |
| `add_schedule` | execute_action | all_applicable | 15 min |
| `halt_session` | execute_action | deny_overrides | 15 min |
| `dispatch_to_agent` | execute_action | first_applicable | **0 — every turn** |

Only `inject_reminder`, `halt_session`, and `dispatch_to_agent` are actually used by any live seed today. Nothing seeds `force_tool_choice`, `send_message`, or `add_schedule`.

**Cooldown:** every seed below has `recurrence_override_seconds = NULL`, so all fall through to the kind-level default — 15 minutes for everything except `dispatch_to_agent` (re-evaluated every turn).

**Opt-out:** only the three `halt_session` reflexes (#1, #17, #18) are `Required: true` (opt-out disabled — kill switches). All other class-scoped reflexes are opt-out-able; the three agent-scoped Loom reflexes (#19–21) currently cannot be opted out at all (see Scope model above).

**Evaluation entry points (3):**
1. `Engine.EvaluateState` — the generic per-turn pass; handles every kind *except* `dispatch_to_agent`, which is explicitly filtered out before this runs (`engine.go:189-195`).
2. `attemptReflexDispatch` (`internal/service/chat_reflex_dispatch.go`) — `dispatch_to_agent` only, from the main chat-turn loop.
3. `matchDispatchToAgentReflex` (`internal/mcp/self_tools_dispatch.go`) — `dispatch_to_agent` only, from the `task_execute` self-tool path.

(2) and (3) are near-duplicates that exist separately because `internal/mcp` can't import `internal/service` — see the taxonomy doc for the full writeup.

---

## class = `process` (6 reflexes)

| # | Name | Trigger summary | Action | Priority |
|---|---|---|---|---|
| 1 | `drift_detector_echo` | 3-turn window: 0 tool calls, 0 cache reads, <10 input tokens, identical output | `halt_session` (required) | 100 |
| 2 | `runaway_superlative_detector` | 3-turn window: 0 tool calls, output growing ≥1.5×/turn, escalating language regex | `inject_reminder` (warn) | 90 |
| 3 | `wake_on_mail` (process) | event: unread mail > 0 | `inject_reminder` (info) | 50 |
| 4 | `context_pressure` | prefix tokens ≥ 85% of 200k context window | `inject_reminder` (warn) | 40 |
| 5 | `clean_status_without_tools` | 2-turn window: 0 tool calls + "all clear/healthy/nominal/..." regex | `inject_reminder` (warn) | 75 |
| 6 | `repeated_planning_without_action` | 4-turn window: 0 tool calls + "plan/will continue/going to/..." regex | `inject_reminder` (info) | 65 |

### Details

**1. `drift_detector_echo`** — `seeds.go:98-117`
- Trigger (predicate, AND, window=3): `tool_calls_window`=0 AND `cache_read_window`=0 AND `input_tokens_window`<10 AND `identical_output_window` (last 3 outputs byte-identical on first 1KB). This is the FU-13 cache-miss singleton-race signature.
- Action: `halt_session`, payload `{"reason": "drift_detector_echo: cache-miss singleton race"}`. `deny_overrides` — preempts the rest of the pass once fired.
- Required: **true** (opt-out disabled — kill switch).

**2. `runaway_superlative_detector`** — `seeds.go:126-148`
- Trigger (predicate, AND, window=3): `tool_calls_window`=0 AND `output_growth_window` (each turn ≥1.5× prior) AND `regex_match_window` matching `(?i)(Beyond|Absolute|Ultimate|Maximum)\s+(Emergency|Crisis|Unprecedented)`.
- Action: `inject_reminder` — *"Reorient: escalating-language pattern detected (Beyond/Absolute/Ultimate/Maximum + Emergency/Crisis). Pause and re-ground in concrete state. What is the actual next-action that advances the task?"*

**3. `wake_on_mail` (process)** — `seeds.go:154-167`
- Trigger (event): `mail_received`, fires when `State.MailUnreadCount > 0`.
- Action: `inject_reminder` — *"Mail in the inbox — call mux_message_inbox to triage before continuing."*

**4. `context_pressure`** — `seeds.go:174-189`
- Trigger (predicate): `prefix_pressure` — fires when `PrefixTokens ≥ 0.85 × 200000`.
- Action: `inject_reminder` — *"Context-pressure notice: prefix is over 85% of the window. Consider compacting or wrapping the current line of work before continuing."* Code comment marks this a placeholder pending FU-15's real compaction trigger.

**5. `clean_status_without_tools`** — `seeds.go:197-218`
- Trigger (predicate, AND, window=2): `tool_calls_window`=0 AND `regex_match_window` matching `(?i)\b(all clear|healthy|no issues|nominal|nothing new|looks good)\b`.
- Action: `inject_reminder` — *"Grounding nudge: clean-status language appeared without recent tool calls. Check the relevant source-of-truth/probes before reporting another all-clear."*

**6. `repeated_planning_without_action`** — `seeds.go:225-245`
- Trigger (predicate, AND, window=4): `tool_calls_window`=0 AND `regex_match_window` matching `(?i)\b(plan|next pass|will continue|should proceed|going to)\b`.
- Action: `inject_reminder` — *"Action nudge: recent turns are planning without observable action. Pick one concrete tool/source-of-truth check or pause with an explicit blocker."*

All six: **Provenance: system.**

---

## class = `advisor` (10 reflexes)

| # | Name | Trigger summary | Action | Priority |
|---|---|---|---|---|
| 7 | `wake_on_mail` (advisor) | event: unread mail > 0 | `inject_reminder` (info) | 50 |
| 8 | `idle_drift_advisor` | 5-turn window: 0 tool calls, <5 input tokens | `inject_reminder` (info) | 80 |
| 9 | `evidence_claim_without_tools` | 2-turn window: 0 tool calls + "verified/confirmed/latest/..." regex | `inject_reminder` (info) | 70 |
| 10 | `dispatch_to_agent_open_subagent` | `scope_tier=open` AND `execution_pattern=subagent` | `dispatch_to_agent` → `planner` (conf. 0.75) | 10 |
| 11 | `dispatch_to_agent_background_long_task` | user text matches "in the background/async/overnight/..." AND `execution_pattern=background` | `dispatch_to_agent` → `worker` (conf. 0.25) | 60 |
| 12 | `dispatch_to_agent_planner_mention` | user text matches "let's plan/sprint/plan this/..." AND `scope_tier=open` | `dispatch_to_agent` → `planner` (conf. 0.20) | 50 |
| 13 | `dispatch_to_agent_planner_large_task` | user text matches "big project/multi-step/phases/..." AND `scope_tier` in {large, open} | `dispatch_to_agent` → `planner` (conf. 0.18) | 45 |
| 14 | `dispatch_to_agent_researcher_mention` | user text matches "research/investigate/look into/..." | `dispatch_to_agent` → `researcher` (conf. 0.15) | 35 |
| 15 | `dispatch_to_agent_reviewer_mention` | user text matches "review/assess/second opinion/audit/..." | `dispatch_to_agent` → `reviewer` (conf. 0.15) | 30 |
| 16 | `dispatch_to_agent_worker_execute` | user text matches "build/implement/fix/refactor/..." | `dispatch_to_agent` → `worker` (conf. 0.10) | 20 |

### Details

**7. `wake_on_mail` (advisor)** — `seeds.go:248-261` — same trigger/body as #3, separate row for the `advisor` class.

**8. `idle_drift_advisor`** — `seeds.go:267-284`
- Trigger (predicate, AND, window=5): `tool_calls_window`=0 AND `input_tokens_window`<5.
- Action: `inject_reminder` — *"Idle-drift advisory: 5 consecutive turns with no tool calls and minimal input. State the next concrete action or pause cleanly."*

**9. `evidence_claim_without_tools`** — `seeds.go:290-310`
- Trigger (predicate, AND, window=2): `tool_calls_window`=0 AND `regex_match_window` matching `(?i)\b(verified|confirmed|checked|looked up|current|latest|today|as of)\b`.
- Action: `inject_reminder` — *"Grounding nudge: evidence/currentness language appeared without recent tool use. Cite the source if you have one; otherwise mark it as inference and verify before relying on it."*

**10. `dispatch_to_agent_open_subagent`** — `seeds.go:334-352` — migrated from the retired agent-broker's Rule 5.
- Trigger (predicate, AND): `scope_tier="open"` AND `execution_pattern="subagent"`. These two `State` fields are populated only on the `dispatch_to_agent`-specific call sites, never the generic per-turn pass.
- Scope: `advisor` only — deliberately excludes `process`/`template`, which are already blocked from further sub-dispatch by `task_execute`'s hard recursion-depth cap.
- Action: `dispatch_to_agent`, `{"agent_slug": "planner", "confidence": 0.75, "reason": "scope_tier=open + execution_pattern=subagent (migrated agent-broker Rule 5)"}`.
- Priority deliberately low (10) — the general tier/pattern fallback, meant to lose to any phrase-match reflex (#11–16) below.

**11. `dispatch_to_agent_background_long_task`** — `seeds.go:394-418` — migrated from `internal/promptrouter`.
- Trigger (predicate, AND): current-turn user text (`user_regex_window`, window=1) matches *"in the background", "async", "when you get a chance", "overnight", "index the whole", "crawl the entire"* AND `execution_pattern="background"`.
- Action: `dispatch_to_agent` → `{"agent_slug": "worker", "confidence": 0.25, ...}`.

**12. `dispatch_to_agent_planner_mention`** — `seeds.go:420-443`
- Trigger: user text matches *"let's plan", "let's work on", "sprint", "plan this", "plan out", "create a plan"* AND `scope_tier="open"`.
- Action: `dispatch_to_agent` → `{"agent_slug": "planner", "confidence": 0.20, ...}`.

**13. `dispatch_to_agent_planner_large_task`** — `seeds.go:445-477`
- Trigger: user text matches *"big project", "multi-step", "break this down", "sequence of", "phases", "end to end"* AND `scope_tier` in {large, open}.
- Action: `dispatch_to_agent` → `{"agent_slug": "planner", "confidence": 0.18, ...}`.

**14. `dispatch_to_agent_researcher_mention`** — `seeds.go:480-496`
- Trigger: user text matches *"research", "investigate", "look into", "find out", "dig into", "what does", "find all", "summarize the state"*. No tier/pattern guard.
- Action: `dispatch_to_agent` → `{"agent_slug": "researcher", "confidence": 0.15, ...}`.

**15. `dispatch_to_agent_reviewer_mention`** — `seeds.go:498-515`
- Trigger: user text matches *"review", "assess", "second opinion", "critique", "audit", "check this", "give me feedback on"*.
- Action: `dispatch_to_agent` → `{"agent_slug": "reviewer", "confidence": 0.15, ...}`.

**16. `dispatch_to_agent_worker_execute`** — `seeds.go:517-534`
- Trigger: user text matches *"build", "implement", "fix", "refactor", "write the code", "add the feature", "make it", "run the migration"*.
- Action: `dispatch_to_agent` → `{"agent_slug": "worker", "confidence": 0.10, ...}`. Priority 20 — lowest of the phrase-match set, the most generic verb catch-all.

*Design note (11–16): priorities are all above `dispatch_to_agent_open_subagent`'s 10, so any specific phrase match wins over the general tier/pattern fallback — mirrors the retired promptrouter's Rule-1-before-Rule-5 ordering.*

All ten: **Provenance: system.**

---

## class = `template` (2 reflexes)

| # | Name | Trigger summary | Action | Priority |
|---|---|---|---|---|
| 17 | `task_complete_self_terminate` | event: `task_completed` | `halt_session` (required) | 100 |
| 18 | `task_timeout` | interval: `TickN > 0 AND TickN % 20 == 0` | `halt_session` (required) | 10 |

### Details

**17. `task_complete_self_terminate`** — `seeds.go:547-559`
- Trigger (event): `task_completed` present in `State.Events`.
- Action: `halt_session`, `{"reason": "task_complete_self_terminate"}`. Required — prevents a finished instance-mode agent from running indefinitely.

**18. `task_timeout`** — `seeds.go:563-576`
- Trigger (interval): fires when `TickN > 0` and `TickN % 20 == 0`.
- Action: `halt_session`, `{"reason": "task_timeout: exceeded 20 ticks"}`. Required — hard runaway cap, same protection tier as `drift_detector_echo`.

Both are explicitly marked in code as placeholders — "FU-32 will revisit these with instance-mode lifecycle wiring." **Provenance: system.**

---

## Agent-scoped: Loom Curator / Loom Weaver pilot (3 reflexes)

Change reference: `CW-20260816-0023`. The only `agent_id`-scoped reflexes in the codebase — targeting the resolved `agent_profiles` row for `loom-weaver` and `loom-curator` by slug, not a class. Seeded via `SeedAgentReflexesBySlug`, which skips (warns, non-fatal) if the target slug hasn't been ingested into `agent_profiles` yet on that boot.

| # | Name | Scope (agent) | Trigger summary | Action | Priority |
|---|---|---|---|---|---|
| 19 | `check_before_answer` | `loom-weaver` | 2-turn window: user text matches Nanite-subsystem vocabulary AND no `wiki_*`/`loom_*` tool called yet | `inject_reminder` (info) | 60 |
| 20 | `capture_on_discovery` | `loom-weaver` | 2-turn window: assistant text matches discovery language AND tool_calls > 0 | `inject_reminder` (info) | 55 |
| 21 | `capture_on_discovery` | `loom-curator` | same trigger as #20, reused as-is | `inject_reminder` (info, different body) | 55 |

### Details

**19. `check_before_answer`** — `loom_pilot_seeds.go:127-138`
- Trigger (predicate, AND, window=2): `text_regex_window` (scope=user) matches `envelope|boot-profile|mcp trust|durable-agent|plugin (architecture|system|sdk)|slot system|context broker|tool-result cache|agent_profiles|reflex(es)?|wiki|loom curator|loom weaver` AND `tool_name_window` (mode=none) confirms no tool matching `^(wiki_|loom_)` has been called in that window.
- Scope: `loom-weaver` only — deliberately excludes Curator, since Curator's own system prompt already tells it not to answer user questions directly.
- Action: `inject_reminder` — *"Reflex check_before_answer: the recent question touches Nanite wiki-bundle topics and wiki_*/loom_* tools haven't been called yet this window. Search the nanite bundle via loom_page_search (then loom_page_get / loom_fetch_result / loom_search_result as needed) before answering from memory — per your own answer_with_sources procedure."*
- Opt-out allowed in principle (not safety-critical), but see Scope model note above — agent-scoped rows don't currently honor opt-out.

**20. `capture_on_discovery` (Weaver)** — `loom_pilot_seeds.go:139-150`
- Trigger (predicate, AND, window=2): `regex_match_window` (assistant scope) matches `discovered|found that|turns out|worth documenting|worth capturing|worth keeping|noteworthy|TIL` AND `tool_calls_window` > 0 (backed by real tool activity, not idle speculation).
- Action: `inject_reminder` — *"Reflex capture_on_discovery: recent output reads like a fresh finding backed by real tool activity. If this surfaced a gap, staleness, or contradiction worth keeping in the nanite wiki bundle, drop a concise note into Fragments Engine's inbox now via relay_*/message_* (affected page, source refs, observed gap, confidence) so Loom Curator's next pass can act on it — per your own propose_kb_update procedure."*

**21. `capture_on_discovery` (Curator)** — `loom_pilot_seeds.go:151-162`
- Trigger: identical spec to #20 (same `captureOnDiscoveryTrigger` variable, reused as a separate row).
- Action: `inject_reminder` — *"Reflex capture_on_discovery: recent output reads like a fresh finding backed by real tool activity, outside the normal classify/compile flow. If it's a wiki-worthy observation not already covered by write_or_stage_page's staging step or scheduled_lint_and_export's inbox routing, drop a concise note into Fragments Engine's inbox now via relay_*/message_* so it isn't lost."*

All three: **Provenance: system.**

---

## Predicate vocabulary (reference)

Full set of predicate types a trigger can use (`internal/agent/reflexes/evaluator.go:47-65`):

`AND` / `OR`, `tool_calls_window`, `cache_read_window`, `input_tokens_window`, `output_growth_window`, `regex_match_window` (assistant-only), `user_regex_window` (user-only), `text_regex_window` (scope = user | assistant | all), `entity_mention_window`, `tool_name_window`, `envelope_type_window`, `mail_unread_count`, `identical_output_window`, `prefix_pressure`, `scope_tier`, `execution_pattern`.

Numeric `*_window` predicates use a cold-start guard — they return `false` if fewer than `window` messages exist yet, so they never false-positive during session warmup.

`scope_tier` ∈ {`small`, `large`, `open`}, `execution_pattern` ∈ {`inline`, `subagent`, `background`} — both sourced from `internal/classify` (conservative default `small`/`inline` when ambiguous), and populated **only** on the `dispatch_to_agent`-specific evaluation paths (#10–16), never on the generic per-turn pass.
