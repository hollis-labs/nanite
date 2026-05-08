# 08 · Classification & Intent

> **Scope:** the layer between user input and the harness loop that classifies what the user is trying to do, then turns that into hints for mode, role, scope tier, execution pattern, and turn budget.
>
> **Related:** [04 Harness](04-chat-harness-and-loop-orchestration.md) (consumes strategy hints), [09 Session/Slot](09-session-and-slot-management.md) (session intent column), [03 SSE](03-sse-envelope-and-interaction-protocol.md) (`mode_suggestion`), [05 External Agent Execution](05-external-agent-execution.md) (`AssignRole` mapping).

## Purpose

Cheap, deterministic upfront classification that nudges the loop toward an appropriate shape without binding the user to it. Most outputs are **non-binding suggestions** — the harness uses them as soft hints, the user can accept/reject mode suggestions explicitly.

## Key files

- `internal/classify/classify.go` — `Classify(IntentSignals)` → `(ScopeTier, ExecutionPattern)`
- `internal/classify/scope.go` — scope tier enum
- `internal/classify/mode.go` — `ClassifyMode(userContent)` → `ModeClassification{Suggested, Confidence, Signals}`
- `internal/dispatch/execute.go:182-201` — `AssignRole` mapping
- `internal/service/chat_strategy.go` — `applyStrategyToLimits` (turns strategy into soft `MaxTurns`)
- `internal/reflex/` — reflex matcher (rules-based hints upstream of `AssignRole`)

## Layers

There are four classification layers, evaluated in order:

### 1. Mode classification (binding-soft, user-confirmable)

`classify.ClassifyMode(userContent)` returns `ModeClassification{Suggested, Confidence, Signals}`.

- Runs at `chat_generate.go:384`.
- If `Confidence >= 0.7` AND suggested mode disagrees with current session mode → emit `mode_suggestion` SSE event ([03](03-sse-envelope-and-interaction-protocol.md)).
- Frontend stages it in `useChatStore.pendingModeSuggestion`; renders a confirm card. User accepts → mode flips for the session.

### 2. Intent classification (planner)

`classify.Classify(IntentSignals{Message, MessageTokenEst})` returns `(ScopeTier, ExecutionPattern)`.

Used in [05](05-external-agent-execution.md)'s dispatch. Scope tiers: open-ended vs targeted vs narrow. Execution patterns: subagent, background, inline, etc.

### 3. Strategy planner

`applyStrategyToLimits` produces a `strategy.Strategy` with a soft `MaxTurns` hint. Feeds [04](04-chat-harness-and-loop-orchestration.md)'s `loopState`.

### 4. Reflex matcher

`internal/reflex` runs rule-based pattern matchers on the user input upstream of `AssignRole`. Produces hints (ex: "this looks like a one-liner code question — bias to inline + tight budget"). Cheap, deterministic.

## `AssignRole` mapping

`internal/dispatch/execute.go:182-201`:

| Pattern × Tier | Role | Mode |
|---|---|---|
| `PatternBackground` | Worker | async |
| `TierOpen × PatternSubagent` | Planner | sync |
| anything else | Worker | sync |

Worker = single-shot execution. Planner = subagent that itself dispatches further subagents (open-ended scope).

## Logic gates

- **Mode suggestion is non-binding.** The card is staged (B2 plumbing); no automatic mode flip. B3 wires the confirm-card UX.
- **Confidence ≥ 0.7 threshold** for emitting suggestions — empirical; lower confidence is silent.
- **Reflex hints are upstream of role assignment.** A reflex match can short-circuit role assignment with a fixed `Worker/sync` recommendation.
- **Strategy `MaxTurns` is a soft hint.** It only triggers `chat-loop-budget-soft-warning`; doesn't terminate the loop. ([04](04-chat-harness-and-loop-orchestration.md))
- **Session intent column** `migrations/052_session_intent` adds `sessions.intent` (`long-running` / `per-turn` / `ephemeral`). Classification fires at session start in Glass-4; gates the handoff path ([09](09-session-and-slot-management.md)).

## Current gaps

- **G-MODE-CONFIRM-UX** — Mode suggestion plumbing (B2) is wired; the FE confirm-card UX (B3) is staged but not wired in production. The `pendingModeSuggestion` is set but no auto-action.

## Test surface

- `classify_test.go`, `mode_test.go`, `scope_test.go` — table-tested classifiers; extend with adversarial fixtures (intentionally ambiguous prompts).
- Reflex rules: a fixture user message that should match a known reflex rule, assert the role assignment short-circuits.
- Mode suggestion threshold: build a fixture at confidence 0.69 and 0.71, assert SSE emission only at the second.
- Strategy MaxTurns hint: assert the soft warning fires at the strategy-set turn count, not the agent default.
