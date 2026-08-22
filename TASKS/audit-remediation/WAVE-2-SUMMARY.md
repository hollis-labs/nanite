# Wave 2 summary — for the operator

Wave 2 of audit remediation is complete: **13 reviewed, 0 other**. It closed
the container/reaper lifecycle, subagent ordering, Store correctness, and
runtime-lifecycle dispatch units. The thirteen tasks account for sixteen audit
findings: one high, ten medium, four low, and one informational.

The separate `06/03` context sweep and `06/04` cancellation-safety fix shaped
the baseline but were not Wave 2 dispatch work. They remain `validated`, not
deep-reviewed, by explicit operator choice.

---

## What shipped

### Lifecycle ownership and race-test reliability

`NewContainer` now stops both reapers if construction fails after they start,
and `Container.Shutdown` is idempotent under concurrent or repeated calls.
API tests now shut down all ten test-created Containers before their Stores.

That cleanup was correct but did not, by itself, meet the task's exact race
gate: two `go test -race ./internal/api` runs still hit the default ten-minute
timeout while repeatedly applying fresh SQLite migrations. A test-only
migrated-database template removed that repeated setup cost while preserving a
unique database and a real `store.New` open for every test. The fresh exact
gate passed with zero race reports in **219.266s package / 234s wall**.

The companion `internal/service` investigation established the same underlying
performance shape there. Its 25-minute race timeout was forward progress
through **145 migrations** repeated across many fresh Stores, not accumulation
of `safego` or shutdown work. The task was correctly closed as an evidenced
investigation rather than forcing an unrelated code fix.

AD-26's lifecycle audit found **34** current `safego.Go` sites, well above the
task file's stale estimate. The implemented policy is selective: **23**
stateful/asynchronous jobs are now lifecycle-owned and drained; **10** bounded
ActivityEmitter telemetry calls and **one** synchronously joined tool child
remain intentionally untracked. The lifecycle admission gate itself was also
hardened, and plugin unload now respects chat drain ordering. Review required
two correction cycles for nil-owner cancellation, deterministic gate testing,
and plugin-unload timeout behavior before acceptance.

The two formerly weakly-tested service configuration functions now have 100%
focused function coverage. Review caught that the first ordering test was
vacuous under mutation; the corrected adversarial fixture now fails when the
production sort is removed.

### Subagent execution ordering

Approved runs now obey the same cap-of-three semaphore as direct spawns, and a
run cancelled while queued never invokes its runner. Cancellation now owns the
complete durable terminal transition, including `completed_at` and exactly one
terminal event.

This task changed substantially under review. Three correction rounds closed
transition/owner races, abandoned `running` rows, incomplete operator-cancel
bookkeeping, stale event ordering, and a service-wide status FIFO that could
reorder unrelated runs or wedge after callback panic. The accepted design uses
a bounded per-run emission queue/barrier: approval drains its own `running`
event before launch, cancellation is reentrant, observer panics are contained,
and unrelated runs share no queue or backlog.

### Store correctness

`DeleteAgentByID` now treats only genuine `sql.ErrNoRows` as a no-op; real
SQLite lookup errors propagate to the plugin-agent cleanup caller instead of
being silently reported as success.

`ListPluginSettings` now rejects malformed settings/schema JSON consistently
with the single-row getter. `SyncDurableAgentInstanceConfig` was confirmed
concurrently reachable for the same slug through HTTP-backed saves, so its
read/branch/write race became one atomic upsert. Runtime-owned state is
preserved and archive is monotonic; the regression reproduced a stale writer
resurrecting an archived instance before the fix.

AD-14's re-scope was honored: `06/02` did no context sweep, did not touch
`agents.go`/`sessions.go`, and introduced no Store abstraction. The top-level
README dependency row now correctly says `06/02` depends on **none**; that
tracking item is resolved.

### Runtime correctness

- Orphan worktree cleanup now deletes the same full branch name creation used;
  the regression checks real git branch state.
- Per AD-17, `cmdServe` returns errors through all seven in-scope fatal paths,
  allowing logging, OTel, SQLite, and coordination-store defers to run before
  `main` exits. Review required an observable cleanup test, not only a return
  test.
- Per AD-18, completed background results have a 24-hour TTL and a hard cap of
  100, active jobs are protected, and issued-but-evicted IDs report a distinct
  structured `expired` result. Review replaced a forgeable disclosed-prefix
  identity with per-Service HMAC-authenticated tokens, then required strict
  canonical URL-base64 decoding after finding trailing-bit malleability.
- Persisted MCP Args/Env decode failures now warn and clear partial values. The
  regression proves malformed partial data previously crossed the full
  SQLite-to-subprocess path.

## Architect calls applied

- **AD-14:** full context propagation belonged to isolated `06/03`; `06/02`
  narrowed to `GO-STORE-004`/`006` and became parallel-safe with `06/01`.
- **AD-17:** return errors from `cmdServe`; keep process exit in `main`.
- **AD-18:** TTL plus hard count cap, with expired distinct from unknown.
- **AD-26:** selectively track 23 of 34 sites, retain 10 bounded telemetry
  sends and one joined child, and harden lifecycle admission so shutdown cannot
  return before a late-admitted spawn starts.

## Escalations and review outcomes

The full durable records remain in task Work Logs/Review notes,
`TASKS/ESCALATIONS.md`, and `ARCHITECT-DECISIONS.md`.

- **Missing AD-26 queue entry:** Wave 2 pre-flight found that
  `GO-SVCCORE-002` required an architect call but had no decision record, the
  same planning gap class as Wave 1's AD-25. The operator chose the selective
  policy above before Part B was implemented; the gate is fully resolved.
- **`06/03` cancellation regression:** planner verification contradicted the
  sweep's initial claim that `go test ./...` passed. The sweep had exposed a
  latent rule: terminal-outcome writes must survive the cancellation they
  record. It was separated into `06/04`, which audited 50 sites, detached 29,
  left 21 deliberately unchanged, repaired the marker count to 246, and landed
  with `06/03`. This is out-of-wave baseline context, not one of the thirteen
  reviewed tasks.
- **`04/02` exact-gate failure:** cleanup received a code-review PASS but the
  task remained unclosed after two full default-timeout race runs. The migrated
  test DB fixture resolved the gate; the final PASS was earned, not waived.
- **`04/04`:** two review failures drove lifecycle, nil-owner, plugin unload,
  and deterministic race-test corrections before final PASS.
- **`04/05`:** review mutation-testing found the ordering fixture did not
  exercise sorting; the fixture was corrected before PASS.
- **`05/01`:** three review correction rounds produced the final bounded
  per-run event-ordering design rather than accepting the initial shared queue.
- **`07/02`:** review rejected a test that could not observe actual cleanup;
  the replacement proves both logging and OTel defers.
- **`07/03`:** two identity/canonicalization findings were fixed and freshly
  re-reviewed before PASS.

## Verification at close

- `go build ./...` — PASS.
- `go vet ./...` — PASS.
- First `go test ./... -count=1` — transient
  `TestDurableAgentStopRuntimeErrorMarksFailed` event-order assertion failure,
  although the printed expected events were present.
- Exact isolated test, 20 repetitions — PASS.
- Fresh full `go test ./... -count=1` rerun — PASS.

The first full run is recorded honestly as a non-reproducing event-order flake,
not rewritten as a clean first attempt and not treated as a Wave 2 blocker.

## Items carried forward

- Create a focused service-test performance task: use a migrated SQLite
  template/snapshot or deliberate CI sharding so `internal/service` can obtain
  a practical full race verdict without repeating 145 migrations for every
  Store fixture.
- Separately audit service tests that create Stores without closing them. The
  36 idle connection-opener goroutines were not the timeout cause, but are real
  lifecycle debt.
- The `04/03` raw timeout dump was summarized but not retained with a durable
  path/hash and exact focused command transcript. Fresh review independently
  reproduced the conclusion, making this a non-blocking provenance limitation.
- Watch the durable-agent event-order test if the aggregate flake recurs; the
  observed event set was present, so future diagnosis should distinguish
  missing events from order-only assertions.
- `GO-STORE-006` has the correct `task_status: reviewed`, but its independent
  Wave 0 `disposition` still says `needs-more-evidence` after `06/02` supplied
  that evidence and shipped the atomic fix. This does not reopen the task; it
  is a finding-tracker hygiene item for the next sync.

## `TASKS/INDEX.md` state for Wave 2

| Task | Findings closed | Status |
|---|---|---|
| `04/01` | GO-LIFE-001 | reviewed |
| `04/02` | GO-TEST-001 | reviewed |
| `04/03` | GO-SVCCORE-006 | reviewed |
| `04/04` | GO-SVCCORE-001, GO-SVCCORE-002 | reviewed |
| `04/05` | GO-SVCEXEC-005 | reviewed |
| `05/01` | GO-EXEC-001, GO-EXEC-002 | reviewed |
| `06/01` | GO-STORE-003 | reviewed |
| `06/02` | GO-STORE-004, GO-STORE-006 | reviewed |
| `07/01` | GO-RUNTIME-003 | reviewed |
| `07/02` | GO-RUNTIME-001 | reviewed |
| `07/03` | GO-RUNTIME-004 | reviewed |
| `07/04` | GO-RUNTIME-005 | reviewed |
| `07/05` | GO-RUNTIME-007 | reviewed |

**Count: 13 reviewed; 0 implemented, validated, in-progress, blocked, or
not-started within Wave 2.** The sixteen corresponding `findings.json` entries
are also `reviewed`. `GO-STORE-005` is separately `validated` through
out-of-wave `06/03`. Whole-batch tracker counts are **26 reviewed, 1 validated,
86 not-started**.

Wave 3's "Wave 2 complete" dependencies are now satisfied. The repo-wide
development freeze remains in effect under AD-24 until the operator lifts it.
