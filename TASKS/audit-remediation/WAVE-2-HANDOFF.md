# Wave 2 handoff — for Wave 3's kickoff author

**Audience: a fresh session with zero memory of this wave.** This records what
landed and survived independent review, not the thirteen task files' original
plans.

Wave 2 is **complete**. All thirteen in-wave tasks are `reviewed` in
`TASKS/INDEX.md`: `04/01`–`04/05`, `05/01`, `06/01`–`06/02`, and
`07/01`–`07/05`. No in-wave task remains implemented-only, validated-only,
in-progress, blocked, or not-started. The out-of-wave store sweep `06/03` and
its correction `06/04` are important baseline context but are not part of this
thirteen-task count; both remain `validated` under the operator's explicit
decision to defer a 381-file line-by-line review.

---

## 1. Baseline drift Wave 2 had to absorb

AD-14's out-of-wave `06/03`/`06/04` work landed before this wave and rewrote
store signatures and callers across the repository. The final sweep state is
371/371 exported `*Store` methods taking `ctx`, zero non-context `Query` /
`Exec` / `QueryRow` calls in non-test store files, and 246 marked
`context.TODO()` call sites. `06/04` detached 29 terminal-outcome writes from
their operation's cancellation after the sweep exposed that a timed-out
workflow could no longer persist the record of its own timeout.

That made several Wave 2 task snippets and line citations stale. Workers and
reviewers re-derived the current signatures before editing: notably
`DeleteAgentByID`, `GetAgent`, `DeleteAgent`, `ListPluginSettings`, and
`SyncDurableAgentInstanceConfig` already took `context.Context`; line numbers
had also moved in `container.go`, `agent_deps.go`, `service.go`, and
`cmd/nanite/main.go`. No Wave 2 task copied the pre-sweep signatures back into
the code.

The top-level audit-remediation README's `06/02` dependency row is already
synced to **none**. AD-14 moved `GO-STORE-005` wholly to `06/03`, leaving
`06/02` file-disjoint from `06/01`; this tracking correction is resolved, not
a pending Wave 3 cleanup.

## 2. What actually shipped

### Container and service lifecycle (`04/01`–`04/05`)

- **`04/01` — constructor partial failure:** `NewContainer` now owns both
  reaper cancellations immediately through constructor-scoped defers and a
  commit flag. Any post-start construction failure stops both reapers; a
  successful return transfers ownership to `Container`. The regression test
  reproduced both leaked loops before the fix and passed 20 reviewed runs.
- **`04/02` — API test lifecycle and race gate:** the current surface was ten
  `NewContainer` constructions across seven test files, not the planned nine
  across six. Every construction now registers `Container.Shutdown` after
  success, with LIFO ordering that shuts the Container down before its Store.
  Cleanup alone was correct but still could not satisfy the exact default
  ten-minute `go test -race ./internal/api` gate: two runs timed out while
  repeatedly applying fresh SQLite migrations. The task therefore added a
  test-only, fully migrated SQLite template copied to a unique database for
  each test and still opened through `store.New`. A fresh, cache-cleared exact
  race run then passed with zero race reports in **219.266s package / 234s
  wall**, without a timeout override. No production code or test assertion was
  changed.
- **`04/03` — service race-timeout investigation:** the isolated 25-minute
  race run timed out while `Store.New` was applying **145 migrations**. The
  dump contained roughly 56 live goroutines, zero `safego.Go`, lifecycle, or
  shutdown stacks, and showed forward progress in SQLite migration work.
  Focused measurements reproduced an approximately 26–27x race multiplier and
  linear per-Store setup cost. The root cause is repeated fresh migrations
  (at least 59 direct Store constructions across 36 files plus 15 shared
  helper calls), not retained `safego` work. This was an investigation, so it
  intentionally shipped no code.
- **`04/04` — delegation safety and lifecycle ownership:** a panicking
  delegated worker now publishes a synthetic indexed error instead of leaving
  the collector hung; cancellation and owner shutdown are also observed while
  collecting, and successful results remain in input order. AD-26's current
  inventory was **34** `safego.Go` sites across seven files, not the task's
  stale estimate. Exactly **23** stateful/asynchronous sites moved to shared
  chat lifecycle ownership; **10** five-second-bounded ActivityEmitter HTTP
  telemetry sends remain intentionally fire-and-forget; the single concurrent
  tool-batch child remains because its caller synchronously joins it. The
  lifecycle admission gate now serializes shutdown's closed transition with
  `WaitGroup.Add`, closing the spawn-after-shutdown race. Container shutdown
  drains chat before plugin unload; failed, panicking, or late chat drain
  permanently skips unload, while an admitted unload is joined. Two review
  correction cycles fixed nil-owner cancellation behavior, plugin-unload
  timeout semantics, and made the admission regression deterministic.
- **`04/05` — service-config coverage:** tests now exercise
  `buildRepairConfig` and `discoverManagedDurableAgentConfigs` at 100% focused
  function coverage. No production code changed. Review rejected an ordering
  fixture that still passed after deleting the production sort; the corrected
  adversarial fixture fails with `zeta alpha` versus `alpha zeta` under that
  mutation. The deferred focused race gate later passed in 213.260s.

### Subagent approval and cancellation (`05/01`)

All runner entry paths now obey the cap of three. Approval installs a per-run
cancellation owner at the durable requested-to-running transition, returns
without waiting for capacity, and acquires the shared slot in its background
dispatch. Operator, caller, and capacity cancellation cannot invoke a queued
runner or strand a `running` row; `Cancel` owns the complete durable
`cancelled` transition, `completed_at`, and exactly one terminal event.

Fresh review drove three correction rounds. They closed a transition-without-
owner race, abandoned `running` rows, incomplete terminal bookkeeping, stale
running-event ordering, and a service-global event FIFO that allowed unrelated
runs to reorder or wedge on a callback panic. The final form is a **bounded
per-run** emission queue/barrier: the immutable `running` event plus at most
one guarded terminal event. Approval waits for its own running event to drain
before launch, cancellation remains reentrant and non-waiting, callback panics
are contained, and unrelated runs share no queue, lock, or backlog.

### Store correctness (`06/01`–`06/02`)

- **`06/01`:** `DeleteAgentByID(ctx, id)` now returns `nil` only for a wrapped
  `sql.ErrNoRows`; driver and lookup failures propagate with operation and
  agent-ID context. A closed real SQLite database reproduces the formerly
  swallowed failure. `GetAgent` and the plugin sweep caller were unchanged.
- **`06/02`:** `ListPluginSettings` now fails closed on malformed `settings`
  or `schema` JSON, matching `GetPluginSettings` and naming the offending
  plugin. The caller trace also proved `SyncDurableAgentInstanceConfig` is
  concurrently reachable for the same slug through HTTP-backed managed-agent
  saves. Its read/branch/write sequence is now one atomic SQLite upsert that
  preserves runtime-owned state and makes archive monotonic; a 16-writer plus
  archive regression reproduced pre-fix resurrection. `GO-STORE-005`,
  `agents.go`, `sessions.go`, and Store abstractions remained out of scope as
  required by AD-14.

### Runtime correctness (`07/01`–`07/05`)

- **`07/01`:** `Create` and `CleanupOrphaned` now share
  `workerBranchName(sessionID)`, so boot cleanup deletes the full branch that
  creation made instead of a silently wrong eight-character truncation. The
  test now checks git branch state, not only worktree-directory state.
- **`07/02`:** per AD-17, `cmdServe` returns an error and `main` exits only
  after its defers run. All seven in-scope `slogx.Fatal` sites in `main.go`
  became contextual returns; 15 unrelated fatal sites were left alone. Review
  rejected the first test because it proved return but not cleanup. A narrow
  initializer seam now proves logging and OTel cleanup each run exactly once
  after a real Store-open failure.
- **`07/03`:** per AD-18, completed background results are retained for 24
  hours with a hard cap of 100; active jobs are never eviction candidates.
  `Status`/`Result` distinguish issued-but-evicted jobs as `expired` rather
  than unknown, and the self-tool returns expiry as a structured terminal
  result. PTY process records are removed on completion. Review found that the
  first issuance-prefix design let callers synthesize expired-looking IDs, so
  identity became a versioned UUID token authenticated with a per-Service
  256-bit HMAC secret. A second review found URL-base64 trailing-bit
  malleability; parsing now requires strict decoding and exact canonical
  re-encoding before constant-time HMAC comparison. Forged, malformed,
  altered, and prior-Service tokens remain unknown; only a valid evicted token
  is expired.
- **`07/04`:** `Container.Shutdown` is wrapped in `sync.Once`; concurrent
  callers wait for the first shutdown, and later calls do not repeat subsystem
  teardown. The load-bearing test passed 100 race-enabled repetitions.
- **`07/05`:** malformed persisted MCP `Args` or `Env` JSON now emits a
  field-specific warning and explicitly clears partially decoded data before
  stdio registration. The regression traverses SQLite through the real loader,
  manager, and subprocess and proved the old partial arg/environment values
  reached the child process.

## 3. Architect calls applied

- **AD-14:** full context propagation moved to isolated `06/03`; `06/02` was
  narrowed to `GO-STORE-004`/`006`, its gate lifted, and its dependency on
  `06/01` removed. No Store-interface rewrite was introduced.
- **AD-17:** `cmdServe` returns `error`; `main` owns process exit. No global
  `slogx` cleanup-hook mechanism was added.
- **AD-18:** completed background results use TTL plus a hard count cap and an
  explicit expired state; count-only LRU and persistence were rejected.
- **AD-26:** lifecycle tracking is selective, not universal: 23 tracked,
  10 bounded telemetry sends and one synchronously joined child retained.
  Lifecycle admission itself was hardened so the selected migrations establish
  a real shutdown-drain boundary.

## 4. Aggregate validation at wave close

The final shared-tree checks were:

- `go build ./...` — PASS.
- `go vet ./...` — PASS.
- First `go test ./... -count=1` — one transient failure in
  `TestDurableAgentStopRuntimeErrorMarksFailed`: its event-order assertion
  failed even though the diagnostic output showed the expected events were
  present.
- The exact failing test isolated with `-count=20` — PASS all 20 runs.
- A fresh full `go test ./... -count=1` rerun — PASS.

Treat the first result as a non-blocking, observed event-order flake rather
than rewriting it as an entirely clean first run. It did not reproduce in the
focused stress or fresh full rerun.

## 5. Independent checks before trusting Wave 2 as a dependency

These are the highest-signal spot checks; do not rely only on status fields.

```bash
# Constructor failure owns both reapers.
go test ./internal/service -run '^TestNewContainer_PostReaperFailureStopsReapers$' -count=1

# Exact API race gate; intentionally no timeout override.
go test -race ./internal/api

# AD-26 inventory: exactly 11 safego sites remain in internal/service.
grep -Rho 'safego\.Go(' internal/service --include='*.go' --exclude='*_test.go' | wc -l

# Lifecycle and plugin shutdown ordering.
go test -race ./internal/lifecycle -run 'TestGoAdmissionGateCoversWaitGroupAdd' -count=20
go test -race ./internal/service -run 'Test(DelegateSubTasks|Container_.*Plugin|ChatService_Shutdown)' -count=1

# Approval cap, cancellation, and per-run event ordering.
go test -race ./internal/subagent -run 'Test(Fanout|Approve).*' -count=3

# Store error and atomicity regressions.
go test ./internal/store -run 'Test(DeleteAgentByID|ListPluginSettings|SyncDurableAgentInstanceConfig)' -count=1

# Runtime fixes.
go test -race ./internal/worktree -run '^TestCleanupOrphaned$' -count=5
go test -race ./internal/background/... -count=1
go test -race ./cmd/nanite -run 'Test(CmdServeStartupFailureReturns|LoadPersistedMCPServersMalformedJSON)' -count=5

# Repository close gate.
go build ./...
go vet ./...
go test ./... -count=1
```

Also inspect `TASKS/audit-remediation/findings.json`: the sixteen findings
owned by the thirteen in-wave tasks are `reviewed`. `GO-STORE-005` is
separately `validated` through out-of-wave `06/03`; do not count it as a
fourteenth Wave 2 task. Whole-batch tracker counts are 26 `reviewed`, one
`validated`, and 86 `not-started`.

## 6. Carry-forward items

1. **Service race-suite performance:** `04/03` names a focused follow-on to
   make a real full-package `internal/service` race verdict practical. Prefer
   a fully migrated SQLite template/snapshot per test or deliberate CI
   sharding; merely raising the timeout toward an hour preserves the repeated
   145-migration cost.
2. **Service-test Store ownership:** independently audit service tests that
   construct a Store without closing it. The 36 idle `database/sql`
   connection-opener goroutines in the timeout dump were not the timeout root
   cause, but remain separate lifecycle debt.
3. **Raw investigation evidence:** `04/03` preserved summarized dump metrics
   but no durable raw-log path/hash and no complete focused-command transcript.
   Fresh review reproduced the conclusion, so this is non-blocking; future
   investigations should retain or link the raw artifact.
4. **Observed event-order flake:** the one aggregate
   `TestDurableAgentStopRuntimeErrorMarksFailed` failure did not reproduce in
   20 isolated runs or the full rerun. It is not a Wave 2 blocker, but the next
   owner of durable-agent event tests should retain the exact-event-versus-
   ordering distinction if it recurs.
5. **Finding-disposition hygiene:** `GO-STORE-006` correctly has
   `task_status: reviewed`, but its independent Wave 0 `disposition` still
   reads `needs-more-evidence` even though `06/02` supplied the caller trace
   and shipped the atomic fix. This does not reopen the reviewed task, but the
   next finding-tracker sync should decide whether that disposition is now
   stale.

The Wave 3 tasks whose dependency reads "Wave 2 complete" are now unblocked.
The repo-wide development freeze remains in force until the operator changes
AD-24; this handoff does not authorize unrelated batches.
