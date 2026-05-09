# Classifier Routing — Design

## Status

DRAFT (CW-20260429-0031, sprint SP-20260429-0001 Phase B). Sibling to
`executor-handoff.md` (B1, CW-20260429-0030 — overall design) and
`internal/executor/envelope_render/` (B3, CW-20260429-0032 — pilot
executor). This doc commits to a single proposal for each design
question; alternatives surface in the implementer report.

## Motivation

B1 (`executor-handoff.md`) lays out the orchestrator-workers split: the
chat agent is a coordinator, multi-step structured flows live behind a
clean interface in dispatched executors. B1's §2 contract assigns the
chat agent four steps — receive user message; **classifier produces
intent**; build `ExecutorRequest` and dispatch; render the response.

This document specifies step 2: how the per-turn classifier decides
whether a turn routes to an executor or stays chat-direct, what
heuristics drive that decision, and how the result reaches the dispatch
seam in `chat_generate.go`.

The key constraint from B1 §"Decision rules pass" Rule 2 (preemptive vs
reactive): **the route is informative, not prohibitive**. The chat agent
can always fall back. The classifier emits a hint; the dispatch seam
attempts a handoff when the hint is non-direct; if the executor returns
a failure (`invalid_intent`, `missing_context`, etc.), the chat-direct
loop runs as before.

## 1. The `Route` field

`internal/classify/route.go` introduces a stable wire-string type with
three v1 values and a forward-compatible vocabulary:

```go
type Route string

const (
    RouteChatDirect              Route = "chat_direct"
    RouteExecutorEnvelopeRender  Route = "executor_envelope_render"
    // Future executor profiles register additional values here.
    // RouteExecutorKnowledgeAnswer Route = "executor_knowledge_answer"
    // RouteExecutorResearchSweep   Route = "executor_research_sweep"
)
```

**Default: `RouteChatDirect`.** Empty input, ambiguous input, anything
the heuristics don't pin down — all return `chat_direct`. The default is
the least-friction baseline; an executor handoff is opt-in for known
multi-step kinds. This mirrors `Classify`'s "ambiguous → conservative"
contract (`internal/classify/doc.go`).

**Consumer contract.** `Route` is informative. Consumers MAY dispatch to
the named executor when a non-direct route is emitted. Consumers MUST
NOT treat a non-direct route as a hard prohibition on chat-direct
handling — the chat-direct path stays the safe fallback when the
executor returns a typed `Failure`.

## 2. Heuristics

Heuristics-first, deterministic, rules-based — same shape as `Classify`
and `ClassifyMode` (per `internal/classify/doc.go` D5: "rules-based MVP;
LLM-judge deferred"). The classifier walks an ordered priority list and
returns at the first hit.

### Priority order (highest first)

1. **Explicit envelope-type cue.** The user names a passive-renderable
   envelope type ("show me a report-card", "render the list-card"). The
   token is checked against `envelope.PassiveRenderableTypes` for
   tool-availability gating: only if the type exists in the v1 allow-list
   does the cue route to executor.
2. **Render-verb keyword.** Phrases like `show me`, `render`,
   `display`, `card for`, `demo report`, `preview a`, `mock up`. These
   are strong signals that the user expects a structured envelope back,
   even when they don't name a specific type. The classifier emits
   `RouteExecutorEnvelopeRender` and leaves `TargetEnvelopeType` empty
   for the executor to pick (B3 today fails-fast with
   `missing_context`; future LLM-driven implementations select by
   `tool_describe`).
3. **Demo / synthesis cue.** Phrases like `let's do some testing`,
   `give me an example`, `demo`, `synthesize`, `placeholder` set
   `SyntheticAllowed=true` AND route through the envelope-render
   executor (the c117 test scenario from B1: "let's do some testing —
   show me X" must reach the executor). Without a render verb, demo
   alone falls through to the next rule.
4. **Complexity signal — multi-step intent.** A user message that
   chains envelope rendering with explicit data-resolution language
   ("look up X then render Y", "fetch the latest sprint metrics and
   show me a report-card"). v1 collapses this into the same
   `RouteExecutorEnvelopeRender` (the executor owns the multi-step
   flow); future executor profiles will pull these out by intent.
5. **Default.** No signal → `RouteChatDirect`.

### Keyword surfaces (initial v1 sets)

Exported (mirrors `ScopeTier*Keywords` in `classify.go`) so the eval
suite can tune the rubric without rewriting the classifier:

```go
var (
    RouteRenderVerbKeywords = []string{
        "show me", "render", "display", "demo report", "demo card",
        "card for", "card showing", "preview a", "mock up", "make a card",
    }
    RouteSyntheticIntentKeywords = []string{
        "let's do some testing", "let me test", "demo", "synthetic",
        "placeholder", "give me an example", "synthesize", "for testing",
    }
    RouteMultiStepRenderKeywords = []string{
        "look up", "fetch the", "pull the", "go grab",
    }
)
```

Matching is case-insensitive substring (same as `ScopeTier*` keyword
matching). Word-boundary anchoring is NOT applied at this layer — these
phrases are long enough that substring false positives are rare; if
they accumulate, follow `mode.go::phraseHit`'s pattern.

### Tool-availability gating

The render-verb and demo-synthesis routes only fire when the
envelope-render executor is reachable (the v1
`PassiveRenderableTypes` allow-list is non-empty in this build). This
is a defensive cue: if a workspace removed all passive-renderable
types via plugin config, the classifier shouldn't route to a dead
executor. v1 implementation: a one-line check against
`envelope.PassiveRenderableTypes` length; the gate is wired at the
classifier seam, not duplicated downstream.

### Anti-rules

The classifier MUST NOT:

- **Bake an envelope-type whitelist into the classifier.** Type
  validity lives at the per-type validator boundary (anti-pattern 3 —
  "redundant enforcement"). The classifier names a target type as a
  HINT; the executor and validator decide whether the type is
  emittable. Keeping that single source of truth at the validator
  boundary prevents drift when types are added/removed.
- **Gate the chat-direct path.** Even when the route is non-direct,
  the chat agent can always fall back. The dispatch seam logs a
  failure and the loop runs normally.
- **Add per-task framing to the system prompt.** Anti-pattern 6 from
  B1's decision-rules pass. The route is a runtime hint, surfaced via
  the dispatch seam — it does NOT mutate the system prompt.

## 3. Wiring

`internal/classify/route.go::ClassifyRoute(intent IntentSignals) Route`
runs at the same pre-loop seam as `Classify` and `ClassifyMode`
(`internal/service/chat_generate.go::generateResponse`). The result is
attached to `loopState` alongside `ScopeTier` / `ExecutionPattern`.

`chat_generate.go` consults the route immediately after attachment:

- **`RouteChatDirect`** — no-op. The chat-direct loop runs.
- **`RouteExecutorEnvelopeRender`** (and future executor routes) —
  build an `ExecutorRequest` from the chat turn (verbatim user message,
  best-guess `TargetEnvelopeType` if pinned by the explicit-type rule,
  `SyntheticAllowed` flag if the demo cue fired). Call
  `dispatch.DispatchExecutor` with the wired executor. Three outcomes:
  - **Envelope returned** — emit it to the chat session as a
    structured response, narrate `ExecutorResponse.Summary`, end the
    turn. The chat-direct LLM loop is short-circuited.
  - **Failure (missing_context, invalid_intent, etc.)** — log the
    failure with telemetry, fall through to the chat-direct loop. The
    user gets normal LLM response; the route was informative.
  - **Wiring nil** — when no executor is injected (early bring-up,
    test paths), the route is purely informative: log it and run the
    chat-direct loop.

This is the **single decision the chat agent makes per
executor-eligible intent**: dispatch or handle inline. Consistent with
B1 §1 "Boundary".

### Executor injection

`chatServiceImpl` carries an optional `envelopeRenderExecutor
dispatch.Executor` field, settable via `ChatServiceConfig`. v1 wires
the in-process pilot from `internal/executor/envelope_render`. Future
revisions replace the single field with a registry keyed by intent (B1
§2 final paragraph). nil = dispatch is skipped; route stays purely
informative.

## 4. Test matrix

`internal/classify/route_test.go` covers every reachable route + the
default fallback:

- **chat_direct (default)** — empty input; ambiguous "help me with
  this"; pure conversation.
- **executor_envelope_render — explicit type** — "show me a
  report-card with the latest sprint metrics".
- **executor_envelope_render — render verb only** — "render a card
  showing the open tickets".
- **executor_envelope_render — demo intent** — "let's do some testing,
  give me an example card" → SyntheticAllowed flag set.
- **executor_envelope_render — multi-step** — "fetch the latest
  metrics then show me a report-card".
- **chat_direct — ambiguous render verb without target** — "render a
  thought" (verb hits but context is conversational, no envelope-type
  cue OR demo cue) → falls back to chat_direct (under the v1 rules
  matrix, render-verb alone is not enough; the c117 test scenario
  pairs with a demo cue or an explicit type).

`internal/service/chat_route_dispatch_test.go` covers the wiring: a
fake executor dispatched on `executor_envelope_render`, returning an
envelope; the same fake returning a `missing_context` failure; nil
executor (informative-only).

## 5. Decision rules pass (`agent-context-architecture.md`)

| Rule | Verdict | Note |
|---|---|---|
| 1 — Right layer | Respects | Classifier + dispatch seam. NOT system prompt. |
| 2 — Preemptive vs reactive | Respects (mitigated) | Route is informative; chat-direct fallback always available. |
| 3 — Redundant enforcement | Respects | Type validity stays at validator boundary; route is a hint. |
| 4 — Tool description vs prompt | N/A | No new tool surface in B2; B5 owns chat agent's `dispatch_executor` description. |
| 5 — Classifier injection | Respects | This IS the classifier injection. |
| 6 — Handcuffs OFF | Respects | Adds capability; chat-direct still works. |
| 7 — c117 test | Respects | Demo + render verb routes to executor; classifier never blocks the request. |
| 8 — Prompt density | N/A | Heuristics live in classifier code, not in prompts. |

7/8 respect (one N/A). Mitigation on Rule 2 noted in §1's consumer
contract.

## 6. Cross-references

- `docs/architecture/executor-handoff.md` — B1 design; this doc
  specifies B1 §2 step 2.
- `internal/dispatch/executor.go` — `ExecutorRequest`, `Executor`
  interface, `DispatchExecutor`. The dispatch seam consumes
  `ClassifyRoute`'s output.
- `internal/executor/envelope_render/` — B3 pilot executor; the v1
  target for `RouteExecutorEnvelopeRender`.
- `internal/classify/classify.go` — `Classify`, `ScopeTierOpenKeywords`
  et al. Sibling rules-based primitive.
- `internal/classify/mode.go` — `ClassifyMode`. Sibling per-turn
  classifier with the same priority-list shape.
- `internal/envelope/validator.go::PassiveRenderableTypes` — v1
  allow-list used for tool-availability gating in §2.
- Sibling tickets:
  - CW-20260429-0030 (B1 — overall design)
  - CW-20260429-0032 (B3 — envelope-rendering executor)
  - CW-20260429-0033 (B4 — lens placement / Phase 2 graduation)
  - CW-20260429-0034 (B5 — chat prompt update; chat agent's
    `dispatch_executor` description)
  - CW-20260429-0035 (B6 — telemetry; Route value lands in B6 metrics)
