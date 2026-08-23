# ADR-003 — Reasoning-augmented tool broker (D3; partially retired)

- **Status:** Partially superseded — D3-1 and D3-5 remain accepted; D3-2,
  D3-3, D3-4, and D3-6 were retired by AD-10 on 2026-08-22
- **Date:** 2026-04-27
- **Disposition update:** 2026-08-23
- **Ticket:** CW-20260419-0011 (D3, Phase 5 of architecture-sequence-2026-04-26)
- **Context links:** CW-20260426-0010 (D2 truncation framing), ADR-002 (MCP wrap layer)
- **Superseded in part by:** `TASKS/audit-remediation/ARCHITECT-DECISIONS.md`
  AD-10 and `TASKS/audit-remediation/09-production-islands/05-reasoning-augmented-tool-selection.md`

## Current disposition

AD-10 retired the production-unreachable reasoning-augmented selection chain
and all machinery dedicated to it: memory-derived tool-pattern signals,
operator-authored tool-preference files, blended ranking, and the
err-toward-more padding rule. Their implementation, tests, boot wiring, and
checked-in operator templates have been removed.

Two decisions from this ADR remain live and independent of that ranking chain:

- **D3-1:** one reflection prompt on the first `request_tools` cap trip, then a
  hard halt on the second.
- **D3-5:** context-window-aware token budgeting and final pruning of the
  selected tool surface.

The sections below retain the original rationale as an architectural record,
but each heading now states whether the decision is retained or retired.

## Historical problem statement

The pre-Phase-5 tool broker was a reactive keyword matcher. When the LLM asked
for more tools and the per-turn cap tripped, it died with a flat
"No more request_tools calls will be processed." The harness gave the LLM
neither the introspection prompt it needed to reroute nor the contextual
signals (memory, operator preferences) the broker had access to.

Three concrete shortcomings:

1. **Hard halt at cap.** The LLM was punished for asking three times instead of
   redirected toward an answerable question.
2. **Single ranking signal.** Selection was keyword overlap only. Prior
   successful sessions and operator-encoded heuristics were not consumed.
3. **Token budget under-loaded.** The pruning step erred toward removing tools
   to fit a budget, even when the budget had headroom. Per
   CW-20260426-0010 #3 the bias should be the other direction: load one extra
   description rather than force a request_tools round-trip.

## Original decisions and current status

### D3-1. Reflection on cap, then halt — retained

When `total_calls > maxCalls || consecutive_empty >= 2` the broker now emits a
**reflection prompt** the first time, asking the LLM to restate the underlying
goal in one sentence and call `request_tools` ONE more time using that goal as
the intent. Only on the second cap-trip in the same turn does the legacy hard
halt fire. State machine: `reflectionFired bool` lives on `loopState`.

**Why single-shot, not multi-turn.** Multi-turn risks interaction with the
turn cap and `consecutive_failures` machinery. Single-shot keeps the loop
deterministic and bounds reflection cost to one extra round-trip per turn.

### D3-2. Memory recall as parallel ranking signal (not gate) — retired

> Retired by AD-10. `MemoryRecaller`, its `internal/memory.Service` adapter,
> tool-pattern namespace/recording helpers, and their tests no longer exist.

Vanta memory is queried via the existing `internal/memory.Service` (which
wraps the embedded Conduit memory store). Hits are added to the rank score
**alongside** keyword and skill signals — never gated. Memory unreachability,
empty hits, or service errors absorb at the toolclient layer; the broker
falls through to keyword + skill ranking unchanged.

**Namespace convention:** `user/<user>/project/nanite/memory/tool_use_patterns/`.
Memory keys are normalized via `slugifyForVanta` (lowercase + non-alphanumeric →
underscore) per Vanta's segment rule (`a-z 0-9 _`).

**Recording trigger:** chosen point is **the post-compaction hook**
(`memory.Trigger = post_compact`). The chat service already fires that hook
through the embedded extractor; tool-use-pattern records ride on the same
boundary. End-of-session was rejected because Nanite sessions are long-lived;
per-tool-success was rejected because most tool calls don't carry enough
context to know whether the sequence was useful (that's a Phase 4 mining
job, deferred).

**API:** `MemoryRecaller` interface with two methods. Production
implementation is `memoryRecaller` wrapping `*memory.Service`; tests inject
`stubMemoryRecaller` so they don't require live Conduit.

### D3-3. Operator skills as first-class input — retired

> Retired by AD-10. The tool-preference loader, `Config.SkillsDir`, dedicated
> tests/testdata, and checked-in `config/broker-skills/` templates no longer
> exist. This retired format is unrelated to Nanite's current Skill catalog
> and vendor-store system.

Authoring format: **YAML frontmatter inside Markdown** at
`~/.nanite/skills/*.tools.preferences.md` (custom suffix coexists with the
existing skill files in that directory). Frontmatter schema:

```yaml
---
pattern: "search code"           # substring (lowercased) or "re:<regexp>"
prefer: [dev_glob, dev_grep]     # ordered tool names to bias toward
weight: 7                        # default 5; ranking contribution per match
rationale: "Why this skill exists, surfaced in slog."
---
```

**Why YAML frontmatter, not pure YAML.** Operators already author skills as
Markdown for the harness; matching that format keeps a single mental model
for "where do my skills live and how do I author one."

**Loading:** `LoadSkillsFromDir(dir)` walks the directory + first-level
subdirs, parses each `*.tools.preferences.md`, and returns the slice.
Container wiring sets the loaded slice on the toolclient via `SetSkills`.

**Opt-in by default.** Missing directory → no skills loaded → broker behaves
exactly as before. Operators that don't author skills don't pay any cost.

### D3-4. Ranking-signal blend (precedence) — retired

> Retired by AD-10 with `RankTools`, `SelectWithSignals`, and
> `SelectToolsAugmented`. `SelectByIntent` remains as the live, separate
> intent-matching mechanism.

```
score(tool) = skill_weight + memory_weight × confidence + keyword_weight
              ┌──────────┐  ┌────────────────────────┐  ┌────────────────┐
              │  0..N×7  │  │       0..3 typical     │  │      0..2      │
              └──────────┘  └────────────────────────┘  └────────────────┘
```

- **Skills** (default weight 5, raised to 7 on the example skill) sit on top
  because they encode operator intent — the human said "this is the right
  shape for this category of task" and we trust that signal more than
  pattern recognition.
- **Memory** (default weight 3, scaled by confidence) sits in the middle —
  it's high-signal but probabilistic (the recalled session might not be
  perfectly analogous).
- **Keyword** (1-2) is the floor — high recall, low precision.

A tool that picks up one skill hit (5) outranks the same tool with two
keyword hits (≤4), so skill assertions reliably beat lexical accidents.
A skill + a memory hit (5+3=8) beats a different tool with three keyword
hits (≤6).

**Tie-break:** stable secondary sort by the broker's own rule-priority order,
so deterministic-with-ties is preserved.

### D3-5. Token budget aware via models.dev — retained

The broker already received `windowSize` from the chat service, which routes
through `pkg/models/registry.go` (Nanite's models.dev-style metadata table —
`pkg/models.Model.ContextWindow`). This ADR formalizes the contract: every
selection respects `ToolTokenBudgetPct × ContextWindow`, with fallbacks
(`DefaultContextWindowTokens = 200000`, `DefaultToolTokenBudgetPct = 0.20`)
on unknown models.

`pkg/models/registry.go` covers Anthropic Claude, OpenAI GPT, Google Gemini,
and Ollama Llama families. No new metadata adapter is needed; the existing
registry is the source of truth. (`models.dev` is the upstream catalog the
team mirrors into `pkg/models/registry.go`.)

### D3-6. "Err toward more not less" bias — retired

> Retired by AD-10. `Config.ErrTowardMorePad` and the zero-score padding pass
> were removed with the augmented ranker. The retained D3-5 token-budget prune
> still caps the final, already-filtered tool surface.

After the strict-relevance tier is composed (skills + memory + keyword), the
broker pads the final selection with up to `Config.ErrTowardMorePad`
zero-score candidates **iff the token budget admits them**. Default: 3.

**Rationale (per CW-20260426-0010 #3):** the cost of NOT loading a needed
tool is much higher than the cost of an extra ~14-token description. An
unrequited tool forces a `request_tools` round-trip; the LLM pays a full
provider call for the round-trip. An over-loaded but unused tool description
costs ~14 tokens once. Asymmetry favors the "load slightly more" choice.

**Disabled by default in tests** that assert strict-relevance bounds; kept
on in production via the Config default.

## Decisions deliberately deferred

- **Phase 4 (mining job) — closed by retirement.** The proposed job depended
  on the retired ranking/skill mechanism and the later-removed
  `broker_decisions` table. It is no longer an active follow-up from this ADR.

- **rtk (response-token-kompactor) integration.** No `rtk` references exist
  in the codebase today; this ticket does not introduce one. The broker
  already has the EstimateToolTokens shim (~chars / 4); rtk-grade accuracy
  becomes load-bearing only when budgets get tight. Captured as follow-up.

- **Pre-load tools at event boundaries by context.** A "broker mode" where
  the agent never has to discover anything because the right tools were
  already loaded based on session/agent context. CW-20260426-0010 mentions
  this. Out of scope for D3.

- **Ranking influences tool surface composition — closed by retirement.**
  AD-10 chose retirement instead of promoting the augmented order into the
  provider path. `SelectToolsAsProvider` and `selectToolsUncapped` remain
  unchanged.

## Historical schema impact

Migration **031_broker_decisions_reflection.sql** originally extended
`broker_decisions` as described below. The table was later exported and
dropped by `TASKS/phase-0/23-export-and-drop-decision-tables.md`; this section
is historical and does not describe current schema.

The migration added:

| Column            | Type    | Default      | Purpose                                          |
| ----------------- | ------- | ------------ | ------------------------------------------------ |
| consecutive_empty | INTEGER | 0            | Counter at decision time                         |
| total_calls       | INTEGER | 0            | Cumulative request_tools calls this turn         |
| outcome           | TEXT    | "selected"   | selected ｜ loaded ｜ empty ｜ halted ｜ reflected |
| loaded_count      | INTEGER | 0            | Newly-loaded tool count for request_tools rows   |
| reflection_query  | TEXT    | NULL         | LLM-restated goal (reflected outcome only)       |

Plus index `idx_broker_decisions_outcome` for the then-deferred mining job.

`ALTER TABLE ADD COLUMN` is idempotent in `internal/store/store.go`'s migrate
runner — duplicate-column errors are caught and swallowed, mirroring the
pattern from migration 011.

## Current test coverage

- `internal/service/chat_request_tools_reflection_test.go` — first cap-trip
  emits reflection and the second cap-trip hard-halts.
- `internal/toolclient/broker_test.go` — context-window fallback, token-budget
  scaling, and pruning behavior for the retained D3-5 path.

The former `skills_test.go`, `ranking_test.go`, and `memory_signal_test.go`
coverage was deleted with the retired implementations.

## Limitations preserved for retained decisions

- **rtk not integrated** — token estimation is still chars/4.
