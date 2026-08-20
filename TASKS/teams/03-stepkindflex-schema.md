# `StepKindFlex` — the one real engine-level schema change the design doc's own ledger names

**Phase:** 1 — Schema & storage foundation (`TASKS/teams`)
**Status:** not-started
**Depends on:** none directly (independent migration/const addition; parallel-safe with `01`/`02`/`04`/`05` — coordinate migration numbering only, see `01`'s numbering note)
**Touches:** `internal/agentworkflow/types.go` (new `StepKindFlex` constant), `internal/store/migrations/` (new migration — `workflow_run_steps.kind` CHECK widening, table-rebuild), `internal/service/workflow_engine.go` (confirm/adjust the step-kind dispatch switch does not error on an unrecognized-but-now-valid `flex` kind before task `06` gives it real behavior).

## Context

`docs/engineering/architecture/15-teams.md`'s "New mechanism vs. reuse" ledger names this explicitly: *"Fluid coordination stretches | **New** step kind (`StepKindFlex`) — real engine-level work."* But the design doc's own text never names the specific schema obstacle this creates — found directly by this planning session's research, not assumed:

**`internal/agentworkflow/types.go:9-18`** defines `StepKind` as a `string` type with exactly three constants today: `StepKindLLM = "llm"`, `StepKindTool = "tool"`, `StepKindGate = "gate"`. Adding a fourth Go constant is trivial — but **`internal/store/migrations/051_agent_workflows.sql`** hardcodes a matching DB-level constraint: `workflow_run_steps.kind` has `CHECK (kind IN ('llm','tool','gate'))`. SQLite cannot `ALTER` a `CHECK` constraint in place — this needs the same table-rebuild pattern already used elsewhere in this codebase (`internal/store/migrations/119_agent_reflex_dispatch_to_agent.sql` is the cited precedent for exactly this kind of CHECK-widening migration in the scheduling batch, `TASKS/scheduling/01-schema-schedule-kind-collapse-and-retry-columns.md`). Without this migration, any code inserting a `workflow_run_steps` row with `kind='flex'` (task `06`'s job) fails at the DB layer regardless of how correct the Go-level engine logic is.

**`StepDefinition`** (`types.go:221-241`) is `{ID string; Kind StepKind; DependsOn []string; Config map[string]any; Verify *VerifySpec}` — `Config` is already untyped `map[string]any`, so a flex step's `{active_slots, exit_trigger}` config (per the design doc's illustrative compiled-`WorkflowDefinition` example) needs **no struct change** here, only the new constant and the schema fix above.

**Secondary, judgment-call item:** `durable_agent_instances.launch_source_type` (migrations `083`/`084`) has `CHECK (... IN ('api_chat','cli_harness','boot_profile','durable_advisor','process_tick','task_template_run'))` — no `team_run` value. Whether a TeamRun-launched durable slot member needs a distinct `launch_source_type` tag, or can correctly use an existing generic value (`process_tick`/`task_template_run` are both plausible fits), is genuinely task `08`'s call once it builds the real launch path — **this task should confirm with `08` (or make and document its own default) rather than add an unused CHECK value speculatively.**

## What to do

1. Add `StepKindFlex StepKind = "flex"` to `internal/agentworkflow/types.go`, alongside the existing three constants, with a doc comment: a flex step is a phase boundary where N active Team Slot members self-organize, not a single prescribed executor action — cite `15-teams.md`'s own framing verbatim ("A flex step is a phase boundary, not a single execution action").

2. New migration (confirm the actual next-available number at dispatch time — see `01`'s numbering note): table-rebuild `workflow_run_steps` (same rename-recreate-copy pattern as `119_agent_reflex_dispatch_to_agent.sql` — `PRAGMA foreign_keys = OFF` / `BEGIN` / `CREATE ... _new` / `INSERT ... SELECT` / `DROP` / `RENAME` / recreate indexes / `END` / `PRAGMA foreign_keys = ON`), widening the CHECK to `IN ('llm','tool','gate','flex')`. Preserve every existing column and the `UNIQUE (workflow_run_id, step_id)` index exactly.

3. Confirm no other CHECK constraint or Go-side exhaustive `switch`/`case` over step-kind string literals needs a matching update — grep `"llm"`, `"tool"`, `"gate"` as string literals in `internal/agentworkflow/` and `internal/service/workflow_engine.go`. The engine's real per-kind execution switch (whichever function that turns out to be — confirm the exact name) will need a `case agentworkflow.StepKindFlex` eventually, but **this task's job is only to make that switch not silently fall through or panic on an unrecognized-but-now-schema-valid kind** — leave the actual flex-step execution body to task `06`. Document exactly what placeholder behavior you leave in place (e.g. a clear "not yet implemented" error is fine and expected here; a silent no-op is not) so `03` landing alone doesn't quietly break anything for existing `llm`/`tool`/`gate` steps or leave a `flex` step hanging with no diagnostic.

4. Resolve the `launch_source_type` question above: either add `'team_run'` to that CHECK in the same migration (if you judge it's clearly needed and low-risk to add now) or explicitly punt to task `08` with a one-line note here documenting which you chose and why.

## Done means

- Migration applies cleanly against a real backup copy of the database, not just an empty fixture.
- Regression test: inserting a `workflow_run_steps` row with `kind='flex'` succeeds where it previously violated the CHECK; existing `llm`/`tool`/`gate` inserts are unaffected.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass — including a test proving a `flex`-kind step reaching the engine's dispatch switch produces a clear, non-crashing "not yet implemented" result rather than an unhandled-case failure, pending task `06`.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
