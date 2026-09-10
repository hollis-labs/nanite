# Agent steering

Steering is how Nanite decides what an agent should do moment to moment, and it
runs on three stages: **sense**, **integrate**, **act**. Several mechanisms feed
the first stage and several carry out the third, but they all pass through one
decision primitive in the middle — `reflexes.Resolve()`
(`internal/agent/reflexes/resolve.go`). Reading any single call site hides that,
which is why this document is organized on the stages rather than on the parts.

The purpose is to put knowledge in front of the agent as close as possible to
the moment it matters — the right tool, the right skill, the right next step.
The engine is named for what it produces in the agent, not for what it is
mechanically.

## Sense

Three paths reach the decision layer, and they see different things.

**Predicate evaluation** is the general path. A `StateCollector`
(`internal/agent/reflexes/state.go`, wired with `Window: 5` at `engine.go:56`)
assembles a `State` from committed rows: the last five assistant messages with
their token and tool-call counts, recent user messages, unread mail count, a
50-row slice of `event_log`, and the session tick. Predicates in
`evaluator.go` run against that.

Because it reads committed rows, this path is **lagged by design — it cannot see
the turn it is running inside**. Two `State` fields, `ScopeTier` and
`ExecutionPattern`, cannot be built by the collector at all; a caller that needs
the live classification builds `State` by hand instead
(`internal/service/chat_reflex_dispatch.go:275-284`).

**Self-tool invocation** is the direct path. When an agent calls a
harness-reactive self-tool, that call *is* the trigger — there is no predicate to
evaluate. It enters `reactions.Fire`
(`internal/selftools/reactions/engine.go:137`). One tool uses it today,
`task_update_report`.

**Planted hooks are not a sense path yet.** Nanite can plant a hook into a boot
directory it owns — the script and the settings declaration that makes it fire
(`internal/runtime/agent/bootdir_hooks.go`, `claude` only). But
`DefaultBootDirHooks` is empty by decision (`:177`), and nothing a hook observes
reaches `event_log`; every writer there is host-internal. A planted hook is an
actuator today, not a sense organ. Event-triggered reflexes do work — they fire
on Nanite's own events.

## Integrate

`Resolve()` takes a candidate set and a `State` and decides which reflexes fire.
Each action kind carries a **combining algorithm**, modeled on XACML's
policy-combining algorithms and stored as a DB row rather than a Go constant:

| Algorithm | Behavior | Kinds |
|---|---|---|
| `deny_overrides` | the highest-priority candidate wins outright and short-circuits the whole pass | `halt_session` |
| `first_applicable` | the highest-priority candidate of that kind is selected | `force_tool_choice`, `dispatch_to_agent` |
| `all_applicable` | every eligible candidate of that kind is selected | `inject_reminder`, `add_schedule`, `send_message`, `resume_loop_run` |

Resolution runs in four steps: evaluate every trigger, apply cooldown
eligibility, group by kind and resolve each kind's algorithm once, then apply.
A trigger that errors is recorded and treated as not-fired, never fatal. If a
kind's algorithm cannot be read, it degrades to `all_applicable` and logs a
warning.

Four call sites use `Resolve()`, and **what they share is the algebra, not the
inputs**. Each builds `State` differently and passes a different candidate set:
the general per-turn pass excludes `dispatch_to_agent` and `resume_loop_run`;
the two dispatch sites pass only `dispatch_to_agent`; the loop-resume site
passes only `resume_loop_run`, because a WAIT-parked `LoopRun` has no live chat
turn for a per-turn pass to ride on.

**Scoping is a union, not an override.** `ListAgentReflexesForAgent`
(`internal/store/agent_reflexes.go:378`) returns class-scoped and agent-scoped
rows together in one priority-ordered list. Nothing dedupes by name and nothing
shadows: an agent-level `inject_reminder` does not replace a class-level one —
both fire into the same reminder block. What narrows the set is scoping plus
per-agent **opt-out** (`agent_reflex_opt_outs`), and `priority` only selects
under the single-winner algorithms. Two further scopes exist for workflow runs
and loop runs.

## Act

Two vocabularies, in two engines, for two different questions.

`reflexes.Executor.Apply` (`internal/agent/reflexes/executor.go`) answers *the
harness noticed something — what should happen?* Seven kinds:
`inject_reminder`, `force_tool_choice`, `halt_session`, `add_schedule`,
`send_message`, `dispatch_to_agent`, `resume_loop_run`. The last two are
deliberate no-ops here; the real work happens at the call site that can reach
the subsystem.

`internal/selftools/reactions` answers *the agent declared something — what
should happen?* Four kinds, two executable: `render_card` and
`internal_api_call` work; `external_api_call` and `callback` are declared and
skipped. There is no combining algorithm in this engine, because there is
nothing to arbitrate — the tool call is the trigger, so every enabled row fires
independently.

## Nudges, not gates

Steering prefers the soft form. `force_tool_choice` renders as "prefer calling
the `X` tool next" and must opt in via `enforce: true` or `mode: "hard"` to
become imperative — and even the hard form is prose in a reminder, not a
provider-level constraint (`internal/service/chat_reflexes.go:56-95`).

That is deliberate. If every step needs a hard gate, what you have is not an
open agentic session, it is an agent workflow. The alternative is the
composition this document describes: signposting, hints, just-in-time
information, context in error messages, skills, tools. `halt_session` is the
exception that proves it — the one `deny_overrides` kind, and the only place
steering stops a turn outright.
