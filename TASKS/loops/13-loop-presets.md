# Presets — Go-coded `LoopPreset` registry, `ralph` built end-to-end

**Phase:** 4 — Presets (`TASKS/loops`)
**Status:** implemented
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

**Files touched:** new `internal/loop/presets.go`, new `internal/loop/presets_test.go`,
extended `internal/loop/launcher.go` (`LoopLauncher.Launch`/new `resolvePresetDefaults`/new
`isZeroContinuationPolicy` helper). Also `docs/engineering/GLOSSARY.md` (new **LoopPreset**
entry, matching the density of the existing Goal/Loop/LoopRun entries this same batch added).
No schema/migration touched, per the task's own scope.

**1. `LoopPreset` shape and registration convention.** `LoopPreset{Name, Budget,
ContinuationPolicy, DefinitionTemplate}` — a plain struct, no template-builder function
needed. Confirmed directly against `internal/service/workflow_engine.go`'s
`resolveStepConfig`/`resolveTemplateString`/`resolveTemplateRef`: a `StepDefinition.Config`
string containing `{{input.<key>}}` is resolved, at each iteration's own launch time,
against that iteration's `WorkflowInput.Params` — which is exactly `LoopInput.WorkflowParams`,
forwarded unchanged to every iteration by `engine.go`'s `loopRunPersistentConfig`. That
existing mechanism already does everything a per-launch Go-level template-builder function
would otherwise need to hand-roll, so Ralph's `DefinitionTemplate` is one static
`agentworkflow.WorkflowDefinition` value (registered once, reused by every "ralph" launch)
whose one `llm` step's `prompt`/`provider`/`model`/`agent_id` fields are all `{{input.*}}`
references.

Registration convention: checked `internal/store/seed.go` per the task's own pointer, but
that file's `Seed()`/`SeedProviders()` are DB-row seeders (`INSERT OR IGNORE` compiled Go
literals into real tables) — a different shape than an in-memory-only preset catalog needs
(a `LoopRun.DefinitionName` stores only the preset's *name* as a plain string; nothing about
a preset is ever written to a table). The actual matching precedent turned out to be one
level further down the call chain seed.go itself depends on: `pkg/models`' own catalog
(`pkg/models/registry.go`) — a plain package-level var (`allModels`, `ProviderDefaults`)
built by composite literal, with plain accessor functions (`AllSeeded`, `ByModelID`) over
it, no `func init()`, no explicit `RegisterBuiltinModels()` call. `presets.go` follows that
exact shape: a package-level `presets` map literal plus `GetPreset`/`PresetNames`
accessors — documented in detail in `presets.go`'s own package doc comment ("Init-vs-explicit-
registration convention" section) since this deviates from the task's own literal pointer at
seed.go, even though it satisfies the task's actual underlying ask (match whatever
compiled-in-catalog convention this codebase already uses).

**2. Ralph's deterministic-only mechanism — chosen and documented as BOTH options the task
offered, not just one.** `ralphPreset`'s `Budget.MaxNoProgressIterations = 0` — `Decide`'s own
branch-3 guard (`decide.go`) is `budget.MaxNoProgressIterations > 0 && streak >= ...`, so 0 is
a hard, structural "this branch never fires for this preset," not a "generously large"
threshold that remains reachable in principle. Independently, Ralph's one `llm` step carries
no `Verify` modifier, so `classifyIterationProgress` (engine.go) can only ever classify an
iteration `PROGRESS` (clean completion, no verify data points) or `REGRESSION` (a step error)
— never `NO_PROGRESS` — so `noProgressStreak` never accumulates in the first place regardless
of the budget threshold. Either mechanism alone would suffice; both are kept, documented as
deliberately redundant, in `presets.go`'s own doc comment. Because of this guarantee,
`ralphPreset`'s own `ContinuationPolicy` is left at its zero value (no `Provider`/`Model`)
rather than populated with a placeholder LLM backend that would never actually be called —
`decideByReasoning`'s existing hard-error ("reasoning fallback requires a ContinuationPolicy
with Provider and Model set") is the correct, loud failure mode for the one way this
guarantee could be defeated (a caller overriding `Budget` at launch time to raise
`MaxNoProgressIterations` above 0 without also supplying a real `ContinuationPolicy`).
Deliberately did NOT bake a literal model string into Go source for this — that risks exactly
the staleness `pkg/models`' own registry exists to centralize against (its own doc comments
note a real prior model-string going stale), for a code path this preset's whole design goal
is to make unreachable.

`Budget.MaxIterations = 20` (the design doc's own illustrative `loop_run` example value),
`OnExhausted = "escalate"`.

**3. Collision check performed before wiring preset-name-first precedence (task's own explicit
instruction).** Grepped every `*.yaml`/`*.yml` under the repo for a `name:` field matching any
of the seven preset names, and grepped every `*.go` file for a string literal used as a
workflow-definition name matching one. Findings: (a) the repo's one example
`WorkflowDefinition` (`examples/workflow-definitions/worker-reviewer-gate.yaml`) is named
`worker-reviewer-gate`, not any of the seven; (b) `internal/store/loop_runs.go`'s own doc
comment already uses "ralph" purely as an illustrative example of a preset name in prose, not
a real registered definition; (c) the bare word "durable" appears extensively across
`internal/store/teams.go`/`internal/service/team_run_launcher.go` as a
`TeamSlotDefinition.Resolution` enum VALUE (`"durable"`/`"fresh"`) — a completely different
namespace (a Team Slot's resolution mode, not a `WorkflowDefinition`/`LoopPreset` name) with
no actual technical collision, even though the bare word is shared; worth a maintainer's eye
if "durable" as a preset name and "durable" as a Team Slot resolution value are ever discussed
in the same sentence, but not a real conflict. No compiled-in `WorkflowDefinition` (TeamRun's
own compiled defs always carry the `team-run:` name prefix) or config-driven registry entry
uses any of the seven names. No real collision found — implemented preset-name-first
precedence (checked via `GetPreset` before falling back to the ordinary registry-name path)
as the task's own recommendation, documented in detail in `launcher.go`'s own
`resolvePresetDefaults` doc comment.

**4. Stub-vs-error choice for the six unbuilt presets (`test-fix`, `review-fix`,
`plan-execute`, `queue-drain`, `durable`, `self-improve`) — picked the "clear error" option,
not a placeholder step.** Each stub's `DefinitionTemplate` is the `WorkflowDefinition` zero
value plus only a `Name` (`Steps` left `nil`) — deliberately not a minimal placeholder `llm`
step, because a placeholder step that actually ran (even trivially) would let a caller believe
launching e.g. "test-fix" today does something meaningful toward that preset's own real,
unbuilt job, when it would really just be running Ralph's own generic one-step shape under a
different name — a worse failure mode than a loud, explicit error, since it wouldn't even look
like a stub. `(LoopPreset).implemented()` treats "zero `Steps`" as the signal; `launcher.go`'s
`resolvePresetDefaults` checks this BEFORE ever attempting to register a stub's template into
the shared registry, returning `ErrPresetNotImplemented` (wrapping the preset's own name)
rather than surfacing `agentworkflow.Validate`'s more generic "has no steps" message.
`GetPreset` itself always succeeds for all seven names regardless — only actually launching a
stub via `LoopLauncher.Launch` fails, cleanly, per the task's own explicit split. Every stub's
`Budget`/`ContinuationPolicy` get the same reasonable defaults (`MaxIterations` 20,
`OnExhausted` escalate, `MaxNoProgressIterations` 3 — NOT inherited from Ralph's own `0`,
since a stub's real iteration-definition shape is unknown and there's no basis yet for
asserting its own no-progress detection can be structurally disabled the way Ralph's can; a
future task that builds a stub's real `DefinitionTemplate` should revisit this, not assume it
inherits Ralph's specific value). **This is a real, disclosed follow-up, not a silently
dropped one** — flagging explicitly for whoever picks up the next preset task: none of these
six can actually be launched yet; each needs its own real `DefinitionTemplate` design (and,
per item 5 below, potentially its own no-progress-detection design, since Ralph's specific
"0 + no-verify" trick is Ralph-shaped, not automatically applicable to a preset whose whole
job — e.g. `test-fix`, `review-fix` — is likely to actually need `Verify` and a real
no-progress/REPLAN story).

**5. Real, disclosed gaps left out of scope (per the task's own "Touches" line: presets.go
plus a small extension to launcher.go's resolution logic, nothing in engine.go):**
  - **Optional `tool`+`verify` steps for Ralph — NOT included by default, and v1 has no
    opt-in mechanism to add them to the "ralph" preset itself.** `LoopPreset` is a single
    static `DefinitionTemplate` value per name, not a template-builder function parameterized
    by "which optional steps to include" — the one per-launch parameterization Ralph actually
    needs (prompt/provider/model/agent_id content) is already fully served by `{{input.*}}`
    templating against a FIXED step shape; varying the DAG SHAPE itself per launch is a
    materially different, unbuilt capability. A caller who wants Ralph-with-verify today
    authors and registers their own `WorkflowDefinition` directly against the shared
    `*agentworkflow.Registry` and launches `LoopLaunchRequest.DefinitionName` against THAT
    name instead of `"ralph"` — the existing fallback path (untouched by this task) already
    supports this.
  - **Process-restart survival of a registered preset definition.** `resolvePresetDefaults`
    registers a preset's `DefinitionTemplate` into the shared, in-memory
    `*agentworkflow.Registry` lazily, the first time that preset name is actually launched in
    this process — and, unlike `TeamRun`'s per-launch-unique compiled names, a preset's name
    is fixed and reused across every launch, so this registration is idempotent (checked via
    `registry.Get` before calling `registry.Register`). If the process restarts mid-`LoopRun`
    (a paused `waiting_on_escalation`/`waiting_on_gate` Ralph run, say), the in-memory registry
    starts empty again, and a subsequent `Resume` call (which goes through `LoopEngine.Resume`
    directly, not through `LoopLauncher.Launch`) would fail to find `"ralph"` in the registry
    until some future `Launch` call re-populates it. This is a pre-existing architectural
    property of `*agentworkflow.Registry` being in-process-only (the same property `TeamRun`'s
    own compiled-definition registration already has, per that mechanism's own doc comments)
    — not something this task introduces, but also not something this task fixes, since fixing
    it would mean touching `engine.go`'s `Resume`/`driveIterations`/`resumeBlockedIteration` —
    explicitly out of this task's stated scope ("Touches: new internal/loop/presets.go plus a
    small extension to LoopLaunchRequest's resolution logic in internal/loop/launcher.go").
    Flagging for whoever owns Loop's operational hardening: since presets are deterministic,
    compiled-in config (unlike TeamRun's per-launch-compiled definitions), a defensive
    re-registration in `Resume`/`driveIterations` when `GetPreset(lr.DefinitionName)` succeeds
    but the registry lookup misses would be a safe, low-risk fix — just not this task's job.

**6. Done-means verification.** `presets_test.go` (new) covers: (a) all seven preset names
resolve via `GetPreset`; (b) `ralph.implemented() == true`, all six stubs
`implemented() == false`; (c) `PresetNames()` sorted; (d)
`TestLoopLauncher_Launch_Ralph_EscalatesOnBudgetExhaustion` — a genuine `LoopLauncher.Launch`
call with `DefinitionName: "ralph"`, `BudgetOverrides: {MaxIterations: 1}`, no goal evidence
ever recorded, terminating `waiting_on_escalation`/`DecisionEscalate` after exactly one
iteration; (e) `TestLoopLauncher_Launch_Ralph_CompletesOnGoalMet` — a SEPARATE, genuinely
distinct `LoopLauncher.Launch` call against ralph's own default (un-overridden) `Budget`, whose
`fakeStepExecutor.llmFunc` records qualifying goal evidence during the iteration itself,
terminating `completed`/`DecisionComplete` on iteration 1 — proves the preset's own default
`Budget`/`ContinuationPolicy` apply via `resolvePresetDefaults` (no `BudgetOverrides` supplied)
and that Ralph reaches a real `COMPLETE`, not just an `ESCALATE`, satisfying the task's own
"two-case ... not the same test asserting two different things" requirement; (f)
`TestLoopLauncher_Launch_Ralph_TwiceDoesNotErrorOnRegistration` — two distinct launches of
`"ralph"` against the same `*LoopEngine`/registry succeed without a `Register` duplicate-name
error; (g) `TestLoopLauncher_Launch_StubPreset_ReturnsNotImplemented` — every one of the six
stubs returns `ErrPresetNotImplemented` (via `errors.Is`) on `Launch`; (h)
`TestLoopLauncher_Launch_NonPresetName_StillFallsBackToRegistry` — an ordinary,
non-preset-named `WorkflowDefinition` launch is unaffected by the new preset-resolution path.

**7. Verification (real output read directly, not a piped/masked exit code):**
  - `go build ./cmd/nanite/` — exit 0, no output.
  - `go vet ./...` — exit 1, but the only findings are in `internal/service/container.go`
    (`stopReaper`/`stopRuntimeReaper` possible-context-leak warnings) — confirmed via
    `git diff --stat HEAD -- internal/service/container.go` (empty output: zero diff against
    this worktree's base commit) that this file was untouched by this task and the vet finding
    pre-exists on `HEAD` independent of any change made here. `go vet ./internal/loop/...`
    (this task's own actual surface) passes clean, exit 0, zero findings.
  - `go test ./...` — exit 0, every package `ok`, zero `FAIL`/panic anywhere in the output,
    including `internal/loop` (24.0s, all pre-existing tests plus this task's new ones green).

No `git stash`/`git stash pop` was run at any point (only a read-only `git stash list` to
confirm no stash was ever touched by this session).

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
