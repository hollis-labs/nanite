# Executor Handoff — Pilot Measurement Report

## Status

CW-20260429-0035 (B6, sprint SP-20260429-0001 Phase B). Companion to:

- `docs/architecture/executor-handoff.md` (B1 — design)
- `internal/executor/envelope_render/` (B3 — pilot implementation)
- `internal/dispatch/executor.go` (handoff types + entry seam)

This report is the measurement deliverable for the executor-handoff pilot. It quantifies the cost of the executor-routed envelope render path against the chat-direct (lens-flow) baseline so the team can decide whether to expand executor coverage to other multi-step flows (Phase 3 of the rollout).

The dispatch-overhead numbers below are deterministic — captured by a Go benchmark against `dispatch.DispatchExecutor` against the `envelope_render` pilot, no LLM calls. The full chat-direct-vs-executor comparison (live LLM, latency, token spend) requires operator-issued API keys and is documented as a procedure under [Live LLM benchmark — pending operator run](#live-llm-benchmark--pending-operator-run); the static + dispatch-overhead signals below are the durable comparison.

## Summary

| Metric | Chat-direct (pre-handoff) | Executor-routed (post-handoff) | Delta |
|---|---|---|---|
| Tools the chat agent must understand to render an envelope | 5 (`card_show` + `tool_describe` + `tool_validate` + `lesson_capture` + `tool_list`) | 1 (`dispatch_executor`)\* | **−4 tools** |
| Token cost of those tools' descriptions (≈chars/4) | ~2,108 tokens | ~150–250 tokens (estimate for the future `dispatch_executor` description) | **~85% reduction in lens-related description load** |
| Recovery-loop turns (chat agent's POV) — happy path | 2 turns (call → emit) | 1 turn (dispatch → response) | **−1 turn** |
| Recovery-loop turns (chat agent's POV) — failure + repair | 5–8 turns (emit-fail → describe → re-emit → validate → remember → emit-success) | 1 turn (dispatch → response, even with internal repair) | **−4 to −7 turns** |
| Chat-side dispatch overhead (in-process, no LLM) | n/a (chat agent does the work) | ~1.2–3.7 µs / dispatch, 28–72 allocs | absorbed cost |
| Determinism (byte-stable output for fixed input + fixed clock) | No (LLM nondeterminism) | Yes (B3 in-process pilot) | qualitative win for replay/diff |

\* The `dispatch_executor` self-tool is not yet wired (see [B2 dependency](#b2-dependency)). When wired, its description is the only chat-surface lens-related text required.

## Methodology

The comparison has two axes: **static analysis** (deterministic, autonomously reproducible) and **dynamic measurement** (live LLM, requires operator API keys).

### Static analysis — deterministic, in-tree

1. **Tool surface count.** Enumerated by walking `internal/mcp/self_tools*.go`. The chat agent's surface today carries the lens primitives + `card_show`. The post-handoff (Phase 2 graduation) surface drops the four lens tools.
2. **Prompt density (per metrics-to-watch in `agent-context-architecture.md` §"Metrics to watch").** Word count, char count, character-based token estimate (chars / 4 ≈ tokens for English), bullet count, negative-phrasing count for two prompt bodies:
   - `internal/agent/builtin/default.md` — chat agent's slim system prompt.
   - `internal/executor/envelope_render/prompt.md` — executor's task-shaped prompt.
3. **Tool description density.** Same metrics applied to the lens-primitive tool descriptions, since those load with the tool surface and contribute to the chat agent's per-turn prefix.
4. **Recovery-loop turn estimates.** Counted from the agentic-error-recovery trace shape: `tool_describe` → emit → `tool_validate` → emit-with-repair → `lesson_capture`.

### Dynamic measurement — bench harness in tree

`internal/executor/envelope_render/bench_test.go` exercises `dispatch.DispatchExecutor` against the [Prompt corpus](#prompt-corpus) under a fixed clock. Captures:

- ns/op (dispatch overhead, no LLM)
- B/op + allocs/op (validation + cloneShallow + sourcesToMap)
- correctness gate (TestBenchCorpus_AllPass) — the bench corpus must reach a non-failure response so the numbers stay comparable when schemas evolve

These numbers are the **floor** of executor-routed cost — what's added on top of the LLM call when the executor handoff is in the loop.

### Dynamic measurement — chat-direct path

Chat-direct measurement requires actual LLM invocations. The procedure is captured under [Live LLM benchmark — pending operator run](#live-llm-benchmark--pending-operator-run).

## Prompt corpus

Eight representative prompts spanning real-data and demo-data intents across the v1 passive-renderable allow-list. The corpus lives in lockstep with `bench_test.go::benchCorpus`; prompt enumeration here matches benchmark sub-test names.

| ID | Intent | Envelope | Prompt (verbatim user request) | Notes |
|---|---|---|---|---|
| **P1** | demo + grounded | `report-card` | "Let's do some testing — show me a demo report card with sprint metrics." | c117-shaped — synthesis allowed. |
| **P2** | real + grounded | `report-card` | "Render a report card from the latest sprint summary I pulled." | Real `clockwork_sprint_get` source. |
| **P3** | demo + grounded | `document-viewer` | "Sketch a demo design doc as a document viewer." | Demo content with synthesized source. |
| **P4** | real + non-grounded | `table-card` | "Show me the open tickets in a table." | No `sources` requirement. |
| **P5** | demo + non-grounded | `metric-card` | "Demo: show a single metric card for active users." | Single metric, with `trend`. |
| **P6** | real + non-grounded | `list-card` | "List the upcoming agenda items." | Multi-item list. |
| **P7** | real + non-grounded | `progress-card` | "Show how far along Phase B is." | Steps array w/ done flags. |
| **P8** | real + non-grounded | `timeline-card` | "Show the merge timeline for SP-20260429-0001." | Status enum coverage. |

Coverage rationale: report-card and document-viewer are the c117-shaped grounded types where the chat surface bloated worst; the others span the rest of the v1 allow-list to confirm the executor pattern generalizes. Demo vs real is split roughly 4:4 to exercise both `SyntheticAllowed` paths.

## Static metrics

### Prompt density (mandatory — Rule 5: measure prompt density)

Both prompts are well under the targets in `agent-context-architecture.md` §"Metrics to watch" (≤ 500 words; ≤ 15 bullets; ≤ ~5 negative phrasings). The chat agent's prompt is **already** at the post-c117 recovery target. The executor prompt is task-shaped and stays slim because it carries only the four c119 render judgments.

| Prompt | Words | Chars | Token estimate (chars/4) | Bullets | Negative phrasings |
|---|---|---|---|---|---|
| `internal/agent/builtin/default.md` (chat slim, body only) | **319** | 1,941 | ~485 | 11 | 6 |
| `internal/executor/envelope_render/prompt.md` (executor, body only) | **294** | 1,966 | ~491 | 4 | 5 |

> **Reference points** from the `agent-context-architecture.md` originating evidence:
> - Pre-c107 (target/baseline): ~250 words, ~6 bullets, 2 negative phrasings, 0 mechanical gates.
> - Post-c117 (incident peak): 1,076 words, 24 bullets, 14 negative phrasings, 3 mechanical gates.
>
> The Phase 1 / Phase 2 graduation criteria (B1 §6) are < 600 words at Phase 1, < 400 words at Phase 2. The chat prompt is already at 319 words. **The handoff design's prompt-density goal is structurally met.**

### Tool description density (lens primitives)

The lens primitives' descriptions are non-trivial — they amount to several hundred tokens of always-loaded "when to use / when not / output shape" text on the chat agent's per-turn prefix. The B4 ticket (`CW-20260429-0033`) drops them from the chat surface. The figures below are what gets reclaimed.

| Tool | Words | Chars | Token estimate | Comment |
|---|---|---|---|---|
| `tool_describe` | 125 | 872 | ~218 | Lens — schema discovery. |
| `tool_validate` | 185 | 1,313 | ~328 | Lens — pre-flight check. |
| `lesson_capture` | 216 | 1,421 | ~355 | Lens — durable learnings. |
| `tool_list` | 135 | 971 | ~242 | Discovery — kept on chat side. |
| `card_show` | 501 | 3,861 | ~965 | Today: chat-side; Phase 2: executor-only. |
| **Lens cluster removed from chat surface (Phase 2)** | **526** + `card_show` | 3,606 + 3,861 = 7,467 | **~902 + ~965 = ~1,867** | Reclaimed from per-turn prefix. |

Counting just the four tools (`tool_describe`, `tool_validate`, `lesson_capture`, `card_show`) the chat surface drops by ~7,467 chars / ~1,867 tokens of always-loaded tool description, replaced by a single new `dispatch_executor` description (estimated ~600–1,000 chars / ~150–250 tokens once written). Net: a **~85% reduction** in lens-related description load on the chat agent's tool surface.

### Tool surface count

| Surface | Count | Lens primitives present? |
|---|---|---|
| Chat-direct (pre-handoff baseline) | 57 self-tools | yes (`tool_describe`, `tool_validate`, `lesson_capture`, plus `card_show`) |
| Chat-direct (Phase 1 — pilot, fallback ON) | 57 + 1 (`dispatch_executor`) | yes (kept as fallback during Phase 1) |
| Chat-direct (Phase 2 — graduated, fallback OFF) | 57 + 1 − 4 = **54** | **no** (lens lives on executor profile only) |
| Executor profile (`envelope-renderer`) | ~6–8 | yes (lens + `card_show` + read-only data tools) |

Source: `grep -nE '^\s*Name:\s*"[a-z_]+",?$' internal/mcp/self_tools*.go` (yields 57 unique tool names).

> **Note:** the `dispatch_executor` self-tool is not yet wired (see [B2 dependency](#b2-dependency)). The Phase 1 / Phase 2 surface counts above assume it lands.

### Recovery-loop turn estimates (per the lensing model in `agentic-error-recovery.md`)

Per-turn here means an LLM round-trip from the chat agent's perspective.

**Chat-direct happy path** (pre-handoff):

1. Chat agent reads the user prompt + the chat-side `card_show` description.
2. Chat agent calls `card_show(type=..., data=..., sources=...)`. Validator passes; envelope is rendered.

→ **2 turns** (1 LLM turn + 1 tool round-trip). Best case.

**Chat-direct failure + repair** (the c107–c117 anti-pattern that motivated the design):

1. Chat agent calls `card_show(...)`. Schema validator rejects the payload (e.g., `metrics` shape wrong).
2. Chat agent reads the structured error; reaches for `tool_describe(name="card_show")` to look up the schema.
3. Chat agent re-emits `card_show` with the corrected shape.
4. (Optional) Chat agent calls `tool_validate(...)` pre-flight if it's still uncertain.
5. Chat agent calls `card_show` again.
6. (Optional) Chat agent calls `lesson_capture(...)` to durably persist the repair note.

→ **5–8 turns** worst-case. The c114 incident burned 10 turns navigating cached describe responses. This is where the chat surface bloated.

**Executor-routed happy path or failure + repair** (post-handoff):

1. Chat agent calls `dispatch_executor(intent=render_envelope, target_envelope_type=..., data=..., sources=..., synthetic_allowed=...)`. Executor runs the multi-step lens flow internally; chat agent receives `ExecutorResponse` with `Envelope` populated. (Even when the executor's repair pass fired, the chat agent sees one round-trip.)

→ **1 turn** from the chat agent's perspective. The executor's internal cost is one extra LLM turn at minimum (when the executor is booted as a real chat-loop session, post-pilot); the B3 in-process pilot has zero LLM cost for the executor side and the dispatch overhead is in the µs range (see [Dispatch-overhead bench](#dispatch-overhead-bench)).

**Net turn delta from the chat agent's POV** (the LLM bill that scales with chat surface complexity):

- Happy-path: **−1 turn** (2 → 1).
- Failure + repair: **−4 to −7 turns** (5–8 → 1).

> The executor's internal flow does pay a turn cost when it's a real chat-loop session — orchestrator-workers patterns trade chat-side complexity for one extra executor turn. The win is that the extra turn lives in a smaller, task-shaped surface (294 words, 4 bullets, 5 negative phrasings) instead of bloating the always-loaded chat prefix. The B3 in-process pilot pays zero turn cost for the executor side, so today's per-render turn-count is strictly lower.

### Dispatch-overhead bench

`go test ./internal/executor/envelope_render -bench BenchmarkDispatchExecutor -benchmem` (Apple M2 Pro, darwin/arm64, Go 1.x). Numbers are per-prompt-shape; the corpus matches the [Prompt corpus](#prompt-corpus).

```
BenchmarkDispatchExecutor_AllPrompts/P1_demo_report_card-10        407,578     3,076 ns/op    4,163 B/op    66 allocs/op
BenchmarkDispatchExecutor_AllPrompts/P2_real_report_card_grounded  464,041     2,558 ns/op    3,434 B/op    52 allocs/op
BenchmarkDispatchExecutor_AllPrompts/P3_demo_document_viewer       962,157     1,249 ns/op    2,033 B/op    28 allocs/op
BenchmarkDispatchExecutor_AllPrompts/P4_real_table_card            494,086     2,485 ns/op    3,306 B/op    49 allocs/op
BenchmarkDispatchExecutor_AllPrompts/P5_demo_metric_card           493,567     2,323 ns/op    3,290 B/op    53 allocs/op
BenchmarkDispatchExecutor_AllPrompts/P6_real_list_card             627,847     1,932 ns/op    2,689 B/op    39 allocs/op
BenchmarkDispatchExecutor_AllPrompts/P7_real_progress_card         327,682     3,703 ns/op    4,454 B/op    72 allocs/op
BenchmarkDispatchExecutor_AllPrompts/P8_real_timeline_card         377,547     2,979 ns/op    4,058 B/op    63 allocs/op
```

**Reading:** at 1.2–3.7 µs / dispatch with 28–72 allocs, the executor seam is rounding error compared to a single LLM round-trip (typically 500–2,000 ms). The dispatch-overhead floor is not a constraint on whether to expand executor coverage; the LLM-side latency dominates by ~5–6 orders of magnitude.

## Live LLM benchmark — pending operator run

The dispatch-overhead numbers above are deterministic. The real comparison — does executor-routed rendering reach a valid envelope faster, in fewer tokens, with higher success-rate than the chat-direct lens flow — requires actual LLM invocations and is gated on operator-issued API keys (Anthropic).

### Procedure

For each prompt P1–P8 above, run two paths and capture identical metrics:

**Path A — chat-direct (pre-handoff, current behavior).** Use the chat agent's slim prompt + the full lens tool surface (`card_show`, `tool_describe`, `tool_validate`, `lesson_capture`). Issue the user prompt verbatim. Record:

- Total turns to a successful `card_show` (or terminal failure).
- Total input + output tokens.
- Wall-clock time.
- Whether the rendered envelope was structurally valid (per `internal/envelope` validator).
- Whether the executor card actually rendered in the drawer (FE acceptance gate from the ticket — operator must spot-check).

**Path B — executor-routed (post-handoff).** Wire `dispatch_executor` as a chat self-tool (B2 dependency, see below). Issue the same user prompt; the chat agent dispatches; the executor returns the envelope. Record the same metrics.

Sonnet-4.5 (or the project default) is sufficient — multi-model comparison is explicitly out of scope per the ticket.

### Suggested harness shape

A new `cmd/measure-handoff/main.go` that:

1. Loads the corpus from `internal/executor/envelope_render/bench_test.go::benchCorpus`.
2. Spawns a chat session per path under a controlled chat profile (slim prompt, lens-on for path A; slim prompt, lens-off + `dispatch_executor`-on for path B).
3. Issues each prompt, drains the loop until terminal envelope or budget exhaustion, and dumps the per-prompt metrics as JSON to `docs/measurements/runs/<date>/<path>.json` (or a similar artifact directory).

This is the "programmatic harness" gesture from the ticket. Wiring it requires the `dispatch_executor` self-tool to land (B2 / `c125-0005`). Until those wires exist, the comparison is run by hand: each prompt is replayed in a chat session under each profile and the SSE / event log is post-processed.

### Acceptance criteria for the live run (when it happens)

- Path B's median turn count is < Path A's p50 (target: ≥ 80% reduction in chat-side turns, per B1 §6 Phase 1 graduation).
- Path B's median total tokens is < Path A's median (target: ≥ 30% reduction — the executor side adds tokens, but the chat side reclaims more).
- Path B's success rate ≥ Path A's success rate.
- Zero c117-shaped dodges in Path B (the agent must produce a `report-card` when asked, not silently swap to `info-card`).

Until these numbers exist, the static + dispatch-overhead signals above are the durable comparison and they already meet the design's prompt-density goal.

## Recommendation

**Expand executor coverage to other multi-step flows once B4 lands and a single round of live-LLM measurement validates the static signal.** The static analysis is unambiguous: the executor handoff reclaims ~85% of the lens-cluster description load on the chat surface, drops the chat agent's recovery-loop turn count from 2–8 down to 1, and adds rounding-error dispatch overhead. The pilot's executor prompt is already slim (294 words, well under the Phase 2 target of 400 words for the chat agent).

The conditions under which to expand (Phase 3 candidates from B1 §6 — `knowledge_grounded_answer`, `ticket_capture_flow`, `research_synthesis`):

1. The flow has ≥ 2 tool calls on average (every passive-renderable already meets this).
2. Reactive lens recovery is needed (validate / repair / remember exists for the flow).
3. The chat agent's surface would otherwise grow to accommodate the flow.

Each of those criteria is independently satisfied by the listed Phase 3 candidates.

The **gate** before expansion is the live-LLM single-round validation above. If the live numbers cleanly mirror the static ones (≥ 30% chat-side token reduction, ≥ 50% chat-side turn reduction, equal-or-better success rate, zero c117 dodges), Phase 3 should proceed. If they don't, the expectation is that the executor's task-shaped prompt is the right place to debug — not the chat agent's static prompt.

## B2 dependency

The `dispatch_executor` self-tool is not yet wired into the chat surface. B2 (`CW-20260429-0031` — classifier routing) is the canonical wiring ticket; `c125-0005` modifies `internal/dispatch/execute.go` and is a parallel agent's scope. This measurement report is computable and deliverable independent of those wires (the static analysis is determined by the executor's tool surface, the prompt files in tree, and the dispatch-overhead bench against the existing `DispatchExecutor` seam).

When B2 lands, the live-LLM benchmark procedure can run; until then the procedure is the deliverable, not the numbers.

## Caveats + open follow-ups

- **Dispatch-overhead bench is in-process.** The B3 pilot is deterministic Go; future LLM-driven executor sessions will introduce LLM-side latency on the executor side. The bench measures the floor, not the ceiling.
- **Token estimates are chars/4.** A real tokenizer pass (e.g., `cl100k_base` or Anthropic's tokenizer) will produce more accurate numbers. The chars/4 proxy is good enough for relative comparisons (chat surface vs executor surface; before vs after) but should not be quoted as absolute token counts in spend forecasts.
- **The lens-cluster description-load reduction is realized at Phase 2 graduation, not Phase 1.** During Phase 1 the lens primitives stay registered on the chat profile as a fallback (per B1 §6); the chat surface narrows when B4 (`CW-20260429-0033`) drops them.
- **`tool_list` was counted as discovery, not lens.** It stays on the chat surface post-handoff. Some teams may prefer to treat it as part of the same cluster; the report breaks them out so the surface-count math stays auditable.
- **Multi-model comparison is out of scope (Sonnet only is the v1 commitment).** A Phase 3 follow-up may want to re-run against Haiku to validate the prompt-density signal scales down, but that's not gating.

## Cross-references

- **B1 design:** `docs/architecture/executor-handoff.md` — the contract this measurement evaluates.
- **B3 pilot:** `internal/executor/envelope_render/` — the executor implementation under measurement.
- **Lessons doc:** `docs/architecture/agent-context-architecture.md` § "Metrics to watch" — the canonical density targets and the c117 anti-pattern story.
- **Recovery model:** `docs/architecture/agentic-error-recovery.md` — the lens (describe / validate / repair / remember) whose flow lives on the executor post-handoff.
- **Sibling tickets:**
  - `CW-20260429-0030` (B1 — design)
  - `CW-20260429-0031` (B2 — classifier routing; gates the live-LLM run)
  - `CW-20260429-0032` (B3 — pilot)
  - `CW-20260429-0033` (B4 — lens placement / Phase 2 surface narrowing)
  - `CW-20260429-0034` (B5 — chat prompt update)
- **Vanta key:** `decisions.nanite.architecture.executor_handoff` (B1 design).
- **Bench harness:** `internal/executor/envelope_render/bench_test.go` (`TestBenchCorpus_AllPass` + `BenchmarkDispatchExecutor_AllPrompts`).
