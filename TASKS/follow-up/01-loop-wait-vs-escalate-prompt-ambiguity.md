# Loop continuation policy — `WAIT` vs `ESCALATE` has no differentiation criteria

**Status:** blocked (deferred — see "When to pick this up," below)
**Blocks:** building the `durable` preset (`internal/loop/presets.go`'s `PresetDurable` stub), and any other
future preset or custom launch path that enables a real reasoning-fallback branch (`Budget.MaxNoProgressIterations
> 0` with a real `ContinuationPolicy`) for an unattended/long-running loop. **Do not build `durable` (or any
similarly unattended-running preset) until this is resolved.**
**Depends on:** nothing — this is a standalone fix to already-shipped, already-reviewed code
(`internal/loop/decide.go`, `internal/loop/engine.go`, `internal/loop/tick_schedule.go`,
`internal/scheduler/runner_adapter.go`, `internal/loop/tick_resume.go`), all from the `TASKS/loops/` batch.
**Not urgent standalone** — see "Current exposure" below for why this is safe to leave open today.

## Context

Raised during the `TASKS/loops/` batch's own close-out (2026-08-22), while explaining the finding to the
operator for a resolve-now-or-later decision. Originally logged in `TASKS/ESCALATIONS.md`'s
"Phase 3's two parallel trigger tasks (`11`, `12`) built compatible but disconnected mechanisms" entry
(2026-08-21/22) as a named follow-up; broken out into its own tracked file here per the operator's explicit
instruction, since it's a real, standalone piece of work with its own blocking condition, not just a
paragraph inside a batch-closeout escalation log.

### The mechanism

`internal/loop/decide.go`'s `Decide()` function is deterministic-first per
`docs/engineering/architecture/21-loops.md`: `goal_met` → `COMPLETE`; budget exhausted → `ESCALATE`/`FAIL`
per `Budget.OnExhausted`; otherwise `CONTINUE`. The **one** case needing real judgment is a sustained
no-progress streak (`budget.MaxNoProgressIterations` consecutive non-progressing iterations) — that falls
through to `decideByReasoning` (`internal/loop/decide.go`), a bare `StepExecutor.ExecuteLLMStep` call whose
system prompt is `reasoningSystemPrompt` (`internal/loop/decide.go:373`):

> *"You are the continuation-policy reasoning fallback for an autonomous iteration loop that has made no
> measurable progress toward its goal for several iterations in a row. You are given the goal, the most
> recent iteration's evaluation, and the iteration history. Decide what should happen next... `{"decision":
> "RETRY|REPLAN|REARCHITECT|WAIT|ESCALATE|FAIL", "reason": "<short reason>", ...}`. 'decision' must be
> exactly one of RETRY, REPLAN, REARCHITECT, WAIT, ESCALATE, or FAIL — no other value is accepted..."*

That is the **entire** specification handed to the model. Six bare enum words, zero operational criteria for
any of them — nothing distinguishing when `RETRY` is right vs. `WAIT` vs. `ESCALATE`, and specifically
nothing telling the model that `WAIT` and `ESCALATE` have **different real-world consequences** once the
decision is acted on (see below). The model is guessing from ordinary English connotation alone.

### Why this used to be harmless, and isn't anymore

Both `DecisionWait` and `DecisionEscalate` persist the identical `loop_runs.status = 'waiting_on_escalation'`
(`internal/loop/engine.go:664`, `case DecisionWait, DecisionEscalate:` — the schema only has two pause
buckets, `waiting_on_gate`/`waiting_on_escalation`, a documented v1 simplification from task `08`'s own
review). Before task `12` (the scheduled `loop_run_tick` trigger), that status persist was the *only* effect
of either decision — a human had to act either way, via the resolve API or a manually-authored
`resume_loop_run` reflex. The model's word choice had **zero** downstream behavioral difference. Purely
cosmetic.

Task `12` changed that. `internal/loop/engine.go:683`: `if decision.Kind == DecisionWait { ...
e.scheduleLoopRunTick(ctx, lr.ID, cfg.AgentProfileID) ... }` (line 695) — **only** `DecisionWait` gets an
automatic `loop_run_tick` scheduled (`internal/loop/tick_schedule.go`, ~5 minutes later by default).
`DecisionEscalate` gets no such schedule; it just sits.

When that scheduled tick fires, `internal/scheduler/runner_adapter.go:443`'s `enqueueLoopRunTick` checks the
LoopRun's live status (`isResumableLoopRunStatus`, line 477) and, if still resumable, calls
`RunnerAdapter.Loops.Resume` — which, after the `11`/`12` integration fix, is
`internal/loop/tick_resume.go`'s `TickResumeBridge.Resume` (`NewTickResumeBridge`, line 65): it checks for an
attached `resume_loop_run` reflex first, and **only when none is attached at all** (the default/common case
— nothing auto-attaches one) falls back to calling `LoopEngine.Resume` directly. That direct `Resume` call
re-enters `Decide` and **launches a brand-new `WorkflowRun` iteration with zero human review.**

### The concrete risk

- If the model says `WAIT` when the situation actually needed `ESCALATE` (a human should look before
  anything continues), the loop **auto-continues unsupervised** roughly 5 minutes later — exactly the
  failure mode `ESCALATE` exists to prevent.
- If the model says `ESCALATE` when `WAIT` would have been fine, the loop just stalls longer than
  necessary — an efficiency loss, not a safety problem.
- These two mistakes are **not symmetric in severity**, and nothing today gives the model a reliable basis
  for choosing correctly between them.

### Current exposure — why this is safe to defer, not an active incident

`ralph` (`internal/loop/presets.go`'s `ralphPreset()`), the only preset built and launchable today, is
**structurally immune**: its `Budget.MaxNoProgressIterations` is hardcoded to `0`, which makes
`decide.go`'s branch-3 guard (`budget.MaxNoProgressIterations > 0 && streak >= ...`) permanently false by
construction — the reasoning-fallback branch is unreachable for `ralph`, full stop (independently verified
during `TASKS/loops/13`'s review). The other six named presets (`test-fix`, `review-fix`, `plan-execute`,
`queue-drain`, `durable`, `self-improve`) are all stubs — `GetPreset` resolves them, but launching returns
`ErrPresetNotImplemented`; none of them can run at all yet.

**There is currently no way to reach this failure mode in the shipped system** — it would require someone to
hand-construct a custom `LoopLaunchRequest`/`LoopStepLaunchRequest` with `MaxNoProgressIterations > 0` and a
real `ContinuationPolicy`, bypassing every preset entirely. The risk is latent, not live.

## What to do when this is picked up

Three options were presented to the operator; no direction has been given yet on which to take (that's part
of what "picking this up" means — this file doesn't presuppose the answer):

1. **Tighten the prompt** — add explicit criteria to `reasoningSystemPrompt` distinguishing `WAIT` ("choose
   this only when a human need not review before the next attempt — it will resume automatically without
   oversight") from `ESCALATE` ("choose this whenever a human should look before anything continues").
   Contained to one already-reviewed file (`internal/loop/decide.go`); low risk; depends on LLM instruction-
   following reliability.
2. **Fix the system instead of the prompt** — flip `internal/loop/tick_resume.go`'s default so "no
   `resume_loop_run` reflex attached" means "leave it paused like `ESCALATE`," not "auto-resume." More
   robust (doesn't depend on the model getting the word choice right at all), but a real behavior change to
   already-reviewed logic (`TickResumeBridge.Resume`'s case-3 fallback, `internal/loop/tick_resume.go`) —
   would narrow what the "plain durable-preset, just retry on a timer" default case (per
   `internal/loop/tick_schedule.go`'s own doc comment) actually covers.
3. **Both**, belt-and-suspenders — matches the pattern this batch already used for `ralph`'s own
   deterministic-only guarantee (two independent, redundant mechanisms rather than relying on either alone).

Whichever is chosen, update:
- `internal/loop/decide.go`'s `reasoningSystemPrompt` and/or `internal/loop/tick_resume.go`'s
  `TickResumeBridge.Resume` (per the option chosen).
- `internal/loop/decide_test.go` / `internal/loop/tick_resume_test.go` with real coverage for the new
  behavior.
- `docs/engineering/architecture/21-loops.md` if the fix changes any documented decision semantics.
- This file's own Status, once resolved.

## Pointers

- `internal/loop/decide.go:373` — `reasoningSystemPrompt` (the prompt text itself).
- `internal/loop/decide.go` — `decideByReasoning`, `parseReasoningVerdict`, `reasoningEligibleKinds`.
- `internal/loop/engine.go:664` — `evaluateDecideAndAct`'s `case DecisionWait, DecisionEscalate:` (the shared
  status persist) and line 683's `if decision.Kind == DecisionWait` (the asymmetric tick-scheduling branch).
- `internal/loop/tick_schedule.go` — `scheduleLoopRunTick`, and its own doc comment's "Deliberately NOT
  gated on a durable preset marker" section (the reasoning for why every `DecisionWait` currently qualifies).
- `internal/scheduler/runner_adapter.go:443` — `enqueueLoopRunTick`; line 477 — `isResumableLoopRunStatus`.
- `internal/loop/tick_resume.go` — `TickResumeBridge.Resume` (the three-case fired/hadCandidates/neither
  branch this issue's fix would touch under option 2).
- `internal/loop/presets.go` — `ralphPreset()` (the `MaxNoProgressIterations: 0` immunity), `stubPreset`
  (why `durable` can't be launched yet).
- `TASKS/loops/12-loop-run-tick-scheduled-trigger.md` — the task that made `WAIT` behaviorally consequential.
- `TASKS/ESCALATIONS.md` — the original finding, logged under the 2026-08-21/22 `11`/`12` integration entry.
- `TASKS/loops/HANDOFF.md` / `TASKS/loops/SUMMARY.md` — where this was first surfaced to the operator.

## When to pick this up

Explicitly deferred per operator instruction (2026-08-22): **after the current audit-remediation work
(AD-24 freeze) is resolved.** Not urgent standalone, per "Current exposure" above — but treat it as a hard
prerequisite the moment anyone starts building `durable` or any other preset intended for unattended,
long-running operation.
