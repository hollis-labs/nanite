# ADR-003 — Reasoning-augmented tool broker (D3)

- **Status:** Accepted
- **Date:** 2026-04-27
- **Ticket:** CW-20260419-0011 (D3, Phase 5 of architecture-sequence-2026-04-26)
- **Context links:** CW-20260426-0010 (D2 truncation framing), ADR-002 (MCP wrap layer)

## Problem

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

## Decisions

### D3-1. Reflection on cap, then halt

When `total_calls > maxCalls || consecutive_empty >= 2` the broker now emits a
**reflection prompt** the first time, asking the LLM to restate the underlying
goal in one sentence and call `request_tools` ONE more time using that goal as
the intent. Only on the second cap-trip in the same turn does the legacy hard
halt fire. State machine: `reflectionFired bool` lives on `loopState`.

**Why single-shot, not multi-turn.** Multi-turn risks interaction with the
turn cap and `consecutive_failures` machinery. Single-shot keeps the loop
deterministic and bounds reflection cost to one extra round-trip per turn.

### D3-2. Memory recall as parallel ranking signal (not gate)

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

### D3-3. Operator skills as first-class input

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

### D3-4. Ranking-signal blend (precedence)

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

### D3-5. Token budget aware via models.dev

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

### D3-6. "Err toward more not less" bias

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

- **Phase 4 (mining job).** A scheduled job that scans `broker_decisions`,
  derives heuristics ("`clockwork_task_list → clockwork_task_get` happens
  80% of the time after `audit tasks`"), and emits skills automatically.
  Outside this ticket's scope. Captured as a follow-up; the
  `broker_decisions.outcome` index added by migration 031 enables the future
  scan without a schema change.

- **rtk (response-token-kompactor) integration.** No `rtk` references exist
  in the codebase today; this ticket does not introduce one. The broker
  already has the EstimateToolTokens shim (~chars / 4); rtk-grade accuracy
  becomes load-bearing only when budgets get tight. Captured as follow-up.

- **Pre-load tools at event boundaries by context.** A "broker mode" where
  the agent never has to discover anything because the right tools were
  already loaded based on session/agent context. CW-20260426-0010 mentions
  this. Out of scope for D3.

- **Ranking influences tool surface composition.** Today the augmented
  selection runs alongside `SelectToolsAsProvider` and contributes to the
  `broker_decisions` row + slog; the actual provider tool slice still uses
  the legacy keyword-ranked path. Promoting the augmented order into
  provider conversion is straightforward (the universe is identical) but
  requires care around permission filtering + chat-surface enforcement
  invariants. Captured as follow-up.

## Schema impact

Migration **031_broker_decisions_reflection.sql** extends `broker_decisions`:

| Column            | Type    | Default      | Purpose                                          |
| ----------------- | ------- | ------------ | ------------------------------------------------ |
| consecutive_empty | INTEGER | 0            | Counter at decision time                         |
| total_calls       | INTEGER | 0            | Cumulative request_tools calls this turn         |
| outcome           | TEXT    | "selected"   | selected ｜ loaded ｜ empty ｜ halted ｜ reflected |
| loaded_count      | INTEGER | 0            | Newly-loaded tool count for request_tools rows   |
| reflection_query  | TEXT    | NULL         | LLM-restated goal (reflected outcome only)       |

Plus index `idx_broker_decisions_outcome` for the deferred mining job.

`ALTER TABLE ADD COLUMN` is idempotent in `internal/store/store.go`'s migrate
runner — duplicate-column errors are caught and swallowed, mirroring the
pattern from migration 011.

## Test coverage

- `internal/toolclient/skills_test.go` — frontmatter parsing, regex/substring
  patterns, malformed file warnings, slugifyForVanta, opt-in absent-dir.
- `internal/toolclient/ranking_test.go` — skills-beat-keyword, memory-adds-hit,
  unknown-tool drop, tight-budget bound, generous-budget bias.
- `internal/toolclient/memory_signal_test.go` — augmented selection promotes
  memory hits, nil recaller is safe, parseToolNames validation.
- `internal/service/chat_request_tools_reflection_test.go` — first cap-trip
  emits reflection, second cap-trip hard-halts, broker_decisions row written
  with the right outcome.

## Limitations preserved

- **Phase-4 mining job not built** — `broker_decisions.outcome` is now
  populated, indexed, and ready to scan; nothing scans it yet.
- **rtk not integrated** — token estimation is still chars/4.
- **Augmented ranking informs broker_decisions and slog, not provider tool
  ordering** — see "Decisions deliberately deferred" above.
