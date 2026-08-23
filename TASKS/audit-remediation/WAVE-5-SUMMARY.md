# Wave 5 summary — for the operator

Wave 5 is complete: **five reviewed, zero in progress, zero blocked**. The wave
began with three evidence/disposition tasks, then reopened by express operator
direction on 2026-08-23 to implement AD-12 and AD-13 in two additional
production tasks.

The pre-implementation baseline was `92315edf`; the reopened implementation
and central review integration spans `d61751c2..7304e7be`, followed only by
the final closeout-documentation commit.

---

## What shipped

### Evidence and characterization (`10/01`–`10/03`)

`10/01` locked the real chat production door with characterization through
`Dispatcher.Run` → `chatRunnerAdapter` → `generateResponse`. It covered plain
and tool turns, provider errors, compaction/recovery, plugin cancellation, and
rate-budget behavior. The reviewed map reconciled 52 `chatServiceImpl` fields
and 87 receiver methods and proposed six incremental actions. Fresh review
required one correction round for duplicate field ownership and missing real
`tool.executing` cancellation coverage; both were fixed before PASS.

`10/02` reconciled `SelfToolsTransport` at 31 fields, 82 receiver methods, and
68 names in 66 clauses. It recommended selective delegation based on policy
and dependency cohesion, not metric reduction. It also recounted `ToolClient`
at 20 methods across three files and found no useful split. Fresh review
corrected a stale claim that chat-slot assembly consumed
`RecallToolLearnings`; `callToolDescribe` is its sole production consumer.

`10/03` revalidated `Container`, Store, and plugin `Host` against current
source and shipped no code. The accepted balance is no `Container`
decomposition, no blanket Store split/interface pass, and only pain-driven Host
sub-registry work supported by the existing `GO-PLUGIN-004` boundary.

### AD-12 chat pipeline (`10/04`)

The operator expressly chose a visible action pipeline over a rewrite or six
new service objects. `generateResponse` remains the coordinator; private
actions now own turn preparation, run initialization, provider request,
provider consumption, tool settlement, and final persistence.

The coordinator's cognitive/cyclop/gocyclo complexity fell monotonically from
`458/228/225` to `19/18/18`; maintainability moved from `0` to `27`. The
coordinator still owns lifecycle cleanup, retry decrements, loop and terminal
routing, and final PTY disposition. Provider cancel/span closure is
idempotently owned, and stream/tool/envelope/persistence ordering remains
characterized.

Integrated range: `1d79d7c3..cbf8554f`.

Midpoint review caught an observable literal drift caused by the new
`run.tools` state expression. The correction restored telemetry, JSON detail,
and reason literals to `tools` and added a production-door regression
assertion. Final fresh review passed all six actions.

### AD-13 selective selftools delegation (`10/05`)

The operator expressly chose four cohesive owners while preserving
`SelfToolsTransport` as the one MCP catalog/dispatch adapter:

- `MessagingTools`
- `WorkTrackingTools`
- `AgentProfileTools`
- `PresentationTools`

The transport moved from 31 fields/82 receiver methods to 27/50. All 68 names
and 66 switch clauses remain. The owners hold meaningful behavior and
dependencies, with exactly one project resolver and one presentation trust
gate. Static definitions, response shapes, local-MCP construction, and API
same-turn card delivery remain intact.

Integrated range: `25689cb5..126c2a2a`.

The task corrected one false source premise rather than changing behavior:
todo, reminder, and pin share session-to-project autofill, while plans have no
`project_id` and retain legacy `scope_id=sessionID` behavior for missing
non-workspace scope. Code review passed; the first final acceptance review then
failed only because the task/map still described the pre-extraction state.
The documentation was corrected and focused re-review passed before final
status sync at `7304e7be`.

## Operator decisions preserved

- **AD-12:** action/pipeline extraction was approved because it reduces
  accidental complexity while keeping the state machine, retries, and cleanup
  visible. Wholesale `chatServiceImpl` decomposition remains unauthorized
  without new ownership/testability evidence.
- **AD-13:** selective delegation was approved because the four moved domains
  have real cohesion or exclusive policy. Further selftools delegation is
  pain-driven; hollow wrappers and metric-driven splitting remain rejected.
- **AD-14:** no blanket `Container`, Store, or Host refactor. Narrow interfaces
  and named sub-registries require a concrete consumer/problem.

## Verification

The merged tree passed build, vet, the AD-12/AD-13 focused normal suites, their
focused race suites, and the full repository non-race suite:

```bash
go build ./...
go vet ./...
go test ./... -count=1
```

Two aggregate race commands did not complete and must never be reported as
passes:

- `go test -race ./internal/service/... -timeout 20m -count=1` exited 1 after
  **1200.838s** while
  `TestDurableAgentStartCreatesOrReusesSession` was in SQLite-backed test-store
  setup. No `DATA RACE` report appeared. **TIMEOUT, not PASS.**
- `go test -race ./internal/selftools -timeout 20m -count=1` exited 1 after
  **1200.458s** while `TestSelfToolsTransport_UnknownTool` was applying the
  fresh 0x91 (145) migration fixture through `newTestStore`/`store.New`/goose.
  No race-detector report appeared. **TIMEOUT, not PASS.**

The existing `driveBootSession` send-on-closed-channel flake recurred once in
Wave 5 verification and passed on retry. It predates this wave and remains the
follow-up recorded in the Skills task `10` review entry in
`TASKS/ESCALATIONS.md`.

## Durable record and operator attention

Retain the existing `TASKS/ESCALATIONS.md` entries for:

- `10/01`'s corrected ChatService/StreamManager inventory and the three
  construction-only fields that are observations, not deletion authority;
- `10/02`'s three stale learning-consumer comments requiring explicitly scoped
  cleanup;
- `10/03`'s corrected concentration metrics and the `GO-PLUGIN-004` threshold
  for any Host sub-registry work; and
- Wave 8's six task-local architect decisions that still lack queue entries.

A central decision record needs a future tracking-authorized correction:
`ARCHITECT-DECISIONS.md` contains a decided AD-20 followed by a stale
  duplicate open AD-20. Reconcile it before `11/08` dispatch.

## Wave 6 dependency

Wave 5 complete now satisfies the explicit dependencies for `11/06`, `11/08`,
and `11/09`, and the named `10/01`/`10/02` dependencies used elsewhere in Wave
6. AD-19 remains open and still gates the semantic-divergence work in Wave 6a;
Wave completion is not a substitute for that operator decision. Wave 6b work
that depends on Wave 6a remains sequenced behind it.

## Final status

| Task | Final status |
|---|---|
| `10/01` | reviewed |
| `10/02` | reviewed |
| `10/03` | reviewed |
| `10/04` | reviewed |
| `10/05` | reviewed |

**Count: five reviewed; zero in progress or blocked.**
