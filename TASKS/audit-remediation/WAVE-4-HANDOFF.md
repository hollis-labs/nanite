# Wave 4 handoff — for the Wave 5 kickoff author

**Audience: a fresh session with zero memory of this wave.** This records the
code and tracking state that survived independent review, including the
current-source corrections that changed the authored scope. It is not a
restatement of the six original option analyses.

Wave 4 is complete: all six tasks `09/01`–`09/06` are `reviewed` in
`TASKS/INDEX.md`, and their six findings are `reviewed` in `findings.json`.
AD-06 through AD-11 resolved as **five retirements and one production wire**.
The repository-wide development freeze under AD-24 remains in force; this
handoff only establishes that Wave 5's `Wave 4 complete` dependency is met.

---

## 1. Final deletion accounting

The final source accounting is derived from the pre-wave implementation-
approval baseline `d9665ea9` through the reviewed source/tracking close
`3f0e2b54`:

```bash
git diff --numstat d9665ea9..3f0e2b54 -- '*.go'
```

Files ending in `_test.go` are counted as test Go; every other `.go` file is
counted as production Go. Documentation, tracking, configuration, templates,
and non-Go test fixtures are excluded. This is final-tree accounting rather
than a sum of task-commit stats, so overlapping edits to shared files are
counted once.

| Class | Additions | Deletions | Net |
|---|---:|---:|---:|
| Production Go | 146 | 3,297 | −3,151 |
| Test Go | 190 | 1,965 | −1,775 |
| **Total Go** | **336** | **5,262** | **−4,926** |

The architect estimate was approximately 3,400 production and 2,600 test
lines deleted. Final production deletions were 103 lower (about 3%); final
test deletions were 635 lower (about 24%). The estimate was directionally
right but overstated the test surface, especially AD-09's pre-implementation
`~1.7k` test estimate. AD-10's fresh review then expanded its retirement beyond
`ranking.go` to orphaned memory-signal/skill support. The table above is the
canonical comparable count; it does not include the additional deleted config
templates and fixtures.

## 2. What actually shipped

| Task | Outcome and preserved surface | Implementation / correction commits | Review |
|---|---|---|---|
| `09/01` | Retired all of `internal/grounding`, the 173-line Store adapter, transport fields, and full E2 pre-dispatch grounding state. Preserved the broader `internal/memory.Service` and historical migrations/tables. | `cc019cff`, `1620756d` | Fresh review found stale current tracking; corrected and re-reviewed PASS. |
| `09/02` | Retired the Hadron-specific gate and its latent unclamped relevance scorer. Preserved the four live Memory, Conduit, PCC, and Session context sources. | `95f64bf5`, `2773c128` | Fresh review found tracking/accounting gaps; corrected and re-reviewed PASS. |
| `09/03` | Constructed `Container.TeamRouting` in the production root and made the HTTP Team launch path require `InstallTeamRunRouting`; the HTTP regression observes four real run-scoped `dispatch_to_agent` reflex rows. | `990734f2`, `db226ce3` | Runtime passed first review; two tracking-only corrections were made and freshly re-reviewed PASS. |
| `09/04` | Retired the root Tool interface/builder/YAML architecture and its tests. Preserved byte-unchanged `cache.go`/`cache_test.go`, added an accurate result-cache package doc, and preserved the live stash categorizer by detaching it from retired types. | `435d24e7`, `30b9c17a` | Fresh review found three stale comments; corrected and re-reviewed PASS. |
| `09/05` | Retired ranking, augmented selection, memory-signal and operator-skill support, tests/fixtures/templates, dead config fields, and boot-time memory/skills I/O. Preserved `SelectToolsAsProvider`/`selectToolsUncapped`/`SelectByIntent`, reflection, and context-window token budgeting. | `4698fe29`, `a16e8141` | Fresh review found the first deletion stopped at the consumer; the orphaned producer/support surface was removed and freshly re-reviewed PASS. |
| `09/06` | Retired the curated tool-knowledge matcher and test. Preserved the live `intent.go` keyword scorer. | `e7ee54c4`, `621eb408`, `d44d4c3f` | Two documentation-only correction rounds removed stale current-architecture claims; final fresh re-review PASS. |

The production-islands README now records all six implemented dispositions;
its former AD-09/AD-10 placeholder rows no longer describe stale current
state.

## 3. Decisions applied and source corrections

### Required routing-install failure policy (`09/03`)

The operator approved routing installation as mandatory for the HTTP launch
operation. `handleLaunchTeam` preflights both launcher and routing dependencies
before creating a run. Once launch has committed a workflow run and members,
an `InstallTeamRunRouting` failure:

- attempts best-effort deletion of every returned partial reflex ID using
  `context.WithoutCancel`, so request cancellation does not suppress cleanup;
- returns a structured HTTP 500 containing the persisted
  `workflow_run_id`, current status, and explicit routing/cleanup error; and
- states that the run remains persisted. There is no transactional rollback or
  `DeleteWorkflowRun` path, so the response must not claim one.

The regression uses a valid-first/invalid-second routing definition: two
partial rows are inserted, then removed; the workflow run and member rows are
independently verified to persist. Missing `TeamRouting` wiring returns 503
before the workflow-run count changes.

### Two authored premises were false at current source

1. `NewTeamRoutingService` also had zero production callers. AD-08 therefore
   needed construction in `cmd/nanite/main.go` and a `Container.TeamRouting`
   field in addition to the handler call. The completed four-step proof is:
   HTTP entry point → production construction/Container wiring →
   `InstallTeamRunRouting` invocation → observable run-scoped reflex rows.
2. Installed semantic/coordinator `dispatch_to_agent` reflexes execute through
   `chat_reflex_dispatch`/`task_execute`; they do not invoke `SendToSlot`.
   `SendToSlot` and `ResolveLazySlot` remain the separate explicit Team-Slot
   messaging path and still have no production self-tool/UI caller. Wave 4 did
   not invent that caller.

## 4. Verification at close

The final merged source baseline passed:

- `go build ./...`
- `go vet ./...`
- `go test ./... -count=1`

High-signal independent checks before treating Wave 4 as a dependency:

```bash
# Five retired islands are absent; the preserved live files remain.
test ! -d internal/grounding
test ! -e internal/contextbroker/gate_hadron_blueprints.go
test -e internal/tool/cache.go && test -e internal/tool/doc.go
test ! -e internal/tool/tool.go && test ! -e internal/tool/yaml_loader.go
test -e internal/toolclient/intent.go
test ! -e internal/toolclient/ranking.go
test ! -e internal/toolclient/memory_signal.go
test ! -e internal/toolclient/skills.go
test ! -e internal/toolclient/tool_knowledge.go

# The real HTTP launch path proves routing installation and its failure policy.
go test ./internal/api -run '^TestTeamRunLaunchAPI_(EndToEnd_ReachesRealWorkflowRunAndTeamRunMembers|ServiceUnavailableWhenRoutingNotWired|RoutingInstallFailureReturnsRunAndCleansPartialRows)$' -count=1

# Preserved context, cache, stash, intent, service, and Team routing behavior.
go test ./internal/contextbroker/... ./internal/tool/... \
  ./internal/toolclient/... ./internal/selftools/... ./internal/service/... -count=1

# Repository close gate.
go build ./...
go vet ./...
go test ./... -count=1
```

Also check that `TASKS/INDEX.md` shows all six Wave 4 rows `reviewed` and that
the corresponding findings (`GO-MEM-001`, `GO-MEM-002`, `GO-SVCEXEC-003`,
`GO-MCPTOOL-001`, `GO-MCPTOOL-002`, `GO-MCPTOOL-003`) are `reviewed`.

## 5. Follow-ups for later kickoff authors

These are scope corrections, not authorization to execute the later tasks in
this frozen session:

1. **`10/02` — `SelfToolsTransport` decomposition.** Rebuild its field/domain
   inventory from current source. `09/01` removed both grounding fields and
   the E2 pre-dispatch grounding state, so the authored 31-field/81-method
   surface and capability map inputs are stale. Its read-only ToolClient
   inventory also names the now-deleted `ranking.go`; do not recreate it.
2. **`11/11` — dispatch-reflex sync test.** It still owns comparison of the
   two live `dispatch_to_agent` evaluators, but `09/01` already changed
   `self_tools_dispatch.go` by deleting grounding-derived message/state
   plumbing. Re-derive function citations and preserve the remaining reflex
   interpretation behavior rather than restoring removed E2 state.
3. **`13/01` — confirmed dead-code removal.** AD-07's Hadron gate and AD-09's
   five root-tool files are already deleted and reviewed. They are no longer
   Wave 8 cleanup candidates. Re-run `deadcode` and rebuild the task's list
   from the post-Wave-4 tree; preserve the four live context sources and
   `internal/tool/cache.go`.
4. **`11/12` — `DevServerName` deduplication.** The duplicate declaration
   still exists, but `09/05` substantially changed
   `internal/toolclient/broker.go`. Re-derive the declaration/use citations;
   do not trust the pre-Wave-4 line numbers or reintroduce augmented-selection
   support while editing the constant.

## 6. Known limitations

- AD-08 makes semantic/coordinator routing installation production-reachable,
  not explicit `@Team Slot` messaging. `SendToSlot`/`ResolveLazySlot` remain
  unwired as described above.
- A Team Slot resolved lazily after routing installation does not
  retroactively receive asking-side semantic-routing rows. This pre-existing
  Teams limitation remains accepted and out of Wave 4 scope.
- Wave 3 task `08/08` remains `implemented`, not `reviewed`, under the
  operator-approved full-race deferral. Wave 4 did not change that status or
  claim a passing full-repository race verdict.

Whole-batch finding status at this close is 46 `reviewed`, one `validated`,
two `implemented` (GO-SEC-001/GO-SEC-002 from `08/08`), and 64
`not-started`.
