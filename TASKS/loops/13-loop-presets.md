# Presets — Go-coded `LoopPreset` registry, `ralph` built end-to-end

**Phase:** 4 — Presets (`TASKS/loops`)
**Status:** not-started
**Depends on:** `07-loop-continuation-policy.md`, `08-loop-engine-core.md`,
`10-loop-launcher-and-api.md`
**Touches:** new `internal/loop/presets.go`.

## Context

Implements `docs/engineering/architecture/21-loops.md`'s closing claim: *"Ralph falls out of
this for free: it's `max_iterations` runs of a trivial one-`llm`-step (optionally + `tool`
+ `verify`) `WorkflowDefinition`, no REPLAN, no REARCHITECT. That's the proposal's own
closing line, realized directly: 'Ralph support can therefore be a preset over a more
general loop abstraction rather than a special subsystem.'"* And from the ledger:
*"Presets (ralph, test-fix, review-fix, …) — New, but pure configuration — named `(budget,
continuation_policy, iteration-definition template)` bundles, no per-preset engine."*

**This planning session's decision** (README, "What this session decided"): Go-coded
constants for v1, not a DB table — a `map[string]LoopPreset` registry, matching this repo's
own "start narrow" bias (the one Decision 2 explicitly diverged from only for Goal, not
extended here — presets are pure config with no independent lifecycle a DB row would earn
its keep for). `ralph` is the one preset this batch builds and tests fully end-to-end; the
rest of the design doc's own named list (`test-fix`, `review-fix`, `plan-execute`,
`queue-drain`, `durable`, `self-improve`) are registered as named stubs with a documented
shape but not fully authored.

**Ralph's exact shape, per the design doc's own closing line**: `budget = {MaxIterations:
N, OnExhausted: "escalate", MaxNoProgressIterations: <some default>}`; `continuation_policy`
= deterministic-only in practice (no REPLAN/REARCHITECT ever selected — either because the
iteration `WorkflowDefinition` never produces a `no_progress` streak signal that would
trigger the reasoning fallback, or because Ralph's own continuation-policy config
short-circuits straight to `RETRY`/`COMPLETE`/`FAIL` — pick one and document it, the design
doc doesn't specify which); `iteration-definition template` = a single `llm` step, optionally
followed by a `tool` step and a `verify` step, per the design doc's own parenthetical.

## What to do

1. **`internal/loop/presets.go`** — `LoopPreset{Name string, Budget Budget,
   ContinuationPolicy ContinuationPolicyConfig, DefinitionTemplate agentworkflow.WorkflowDefinition}`
   (or a template-builder function if the `WorkflowDefinition` needs per-launch
   parameterization — e.g. Ralph's own prompt/task content varies per launch even though its
   shape doesn't). `Presets map[string]LoopPreset` (or a `GetPreset(name string)
   (LoopPreset, bool)` accessor) registered at package init or via an explicit
   `RegisterBuiltinPresets()` call — match whatever init-vs-explicit-registration convention
   this codebase already uses for a similar built-in-catalog case (check
   `internal/store/seed.go`'s builtin-seed pattern, since presets are conceptually similar:
   compiled-in defaults, not DB rows).

2. **`ralph` preset, fully built and tested**: `Budget{MaxIterations: <a real default, e.g.
   20, matching the design doc's own illustrative `loop_run` example>, OnExhausted:
   "escalate", MaxNoProgressIterations: <your call, document it>}`; a `WorkflowDefinition`
   template with one `llm` step (the task/prompt content parameterized per launch via
   `LoopLaunchRequest`'s params, not hardcoded in the preset), and document whether/how the
   optional `tool`+`verify` steps are included by default or opt-in.

3. **Stub the rest** — `test-fix`, `review-fix`, `plan-execute`, `queue-drain`, `durable`,
   `self-improve`: register each by name with a `LoopPreset` whose `Budget`/
   `ContinuationPolicy` are reasonable defaults but whose `DefinitionTemplate` is either
   deliberately minimal/placeholder or returns a clear "not yet implemented" error if
   actually launched — your call which, document it, and make sure `GetPreset` still
   succeeds (the name is real and registered) even if launching against it fails cleanly
   rather than silently doing the wrong thing. This is a real, disclosed follow-up, not
   silently dropped — note it plainly in this task's own Work Log for whoever picks up the
   next preset.

4. **Wire into `LoopLaunchRequest`** — task `10`'s `DefinitionName` field should accept
   either a real `WorkflowDefinition` name (as today) or a preset name, resolved via
   `GetPreset` first before falling back to the registry lookup — document which one takes
   precedence if a name collides (recommend: preset names are reserved/checked first, since
   they're a small, fixed, compiled-in set unlikely to collide with a real workflow
   definition's own name, but confirm no existing `WorkflowDefinition` is actually named
   `ralph` etc. before assuming this is safe).

## Done means

- `ralph` launches and runs to completion end-to-end (via `LoopLauncher.Launch` with
  `DefinitionName: "ralph"`) in a regression test with a stubbed `StepExecutor`, terminating
  correctly on `MaxIterations` exhaustion (`ESCALATE`, per `OnExhausted`) in one test case
  and on a real `COMPLETE` signal in another.
- Every named preset in the design doc's list is registered and resolvable via `GetPreset`,
  even the stubbed ones.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
