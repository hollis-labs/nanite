# Transport-layer validation duplication (schedules/settings) and a memories-pagination correctness bug

**Phase:** Wave 3 — Remaining security hardening (guide §4; sequenced 2026-08-21 — see the sequencing block below)
**Status:** reviewed
**Depends on:** none
**Touches:** `internal/api/schedules.go`, `internal/api/settings.go` (GO-API-004); `internal/api/memories.go`, `internal/memory` package (`RecallOpts`/`Recall`) (GO-API-005)
**Requires architect decision:** **mixed** — GO-API-004: false (task-authoring call, see divergence note); GO-API-005: false (matches `findings.json`)

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 3 — remaining security hardening · **Dispatch unit:** `W3`
> - **Depends on:** Wave 2 complete
> - **Blocks:** `11/15`
> - **Parallel-safe with:** `08/01`–`08/06`
> - **Gated on:** none
> - **requires_security_review:** false · **requires_regression_test:** true

## Findings addressed

- **GO-API-004** (medium severity, high confidence, package-cohesion + duplication) — report §8.5.
- **GO-API-005** (medium severity, high confidence, correctness) — report §8.5.

> **Divergence from `findings.json` on GO-API-004:** the catalog flags this finding's `requires_architect_decision` as `true`. This task sets it to `false` because the in-repo comment at `schedules.go:117-137` already names the proposed fix path — consolidate once all three producers exist — so this task treats it as executing an already-decided plan, not opening a new architectural question. If whoever picks this up finds that precondition (three stable producers) isn't actually met, that's a real scope correction to log, not an invitation to freelance a different design.

## Context

These two findings are bundled in one file because they're both `internal/api` findings that didn't fit elsewhere in this folder's grouping — **say so explicitly, they do not share a root cause, and this task should not force one.** GO-API-004 is a package-cohesion/duplication finding about *where* validation logic lives; GO-API-005 is a straightforward correctness bug in query/pagination ordering. Treat them as two independent sub-tasks within this one file.

**GO-API-004:** schedule and settings field validation live in the transport layer (`internal/api`), not a shared domain layer. The schedules case is **self-documented in-repo**: `schedules.go:117-137`'s own comment already names this as a known, real duplication across **3 independent producers** — the HTTP handler, a reflex hook, and a self-tool handler — all validating "the same conceptual fields... at different call sites," and the comment explicitly frames this as "a real follow-up candidate, not done in this task" at the time it was written. This is not a new discovery; it's already-tracked debt with its own proposed fix path written into the code.

**GO-API-005:** `handleListMemories` (`memories.go`) caps its underlying fetch to `limit+offset` **before** applying `status`/`q` filters — `memories.go:93-98` sets `opts.Limit=limit+offset` before the filters are applied at `:116-137` — then reports `total: len(out)` (`memories.go:149-151`), the post-filter page length, not a real match count. If matching records exist beyond the initial capped `limit+offset` window, the endpoint silently under-returns below the requested `limit` even though more matches exist, and `total` is unconditionally misleading for any UI computing page counts from it. The report is explicit this is "a straightforward code-order bug, not a hypothesis — confirmed by direct reading," not a speculative finding.

## What to do

**GO-API-004:** execute the in-repo comment's own already-proposed fix path — consolidate schedule (and, per the same underlying pattern, settings) field validation out of the transport-layer handler and into one shared validator all producers call, **once the three named producers (HTTP handler, reflex hook, self-tool handler) are confirmed to all exist and be stable.** First step: verify all three producers referenced in `schedules.go:117-137`'s comment are actually present in current code — this batch's README flags that the Wave-0 HEAD-vs-audit-commit revalidation hasn't been done yet, so confirm this specific claim before assuming it's still accurate. Then extract the shared validation logic into a single location both the HTTP handler and the other producers call, removing the duplicated copies. Apply the same treatment to `settings.go`'s enum validation *if* it shares the same "duplicated across producers" shape — the report doesn't explicitly confirm `settings.go` has multiple producers the way `schedules.go` does; verify before assuming. If `settings.go` only has one producer today, its transport-layer validation may not need to move — log that as a scope correction rather than silently consolidating it anyway.

> **Wave 0 revalidation (2026-08-22) — premise partially evaporated, in this task's favor.** The precondition `schedules.go:117-137`'s own comment names ("three independent producers... neither [reflex hook nor self-tool] has landed") **is now met.** All three producers are confirmed present in current source: the HTTP handler (`internal/api/schedules.go`, unchanged), the reflex hook (`store.ReflexActionAddSchedule` case, `internal/agent/reflexes/executor.go:90`), and the self-tool (`SelfToolsTransport.callScheduleCreate`, `internal/selftools/self_tools_schedule_create.go:141`) — both landed by the intervening `TASKS/loops/`/reflex-taxonomy work. No consolidation has happened yet (none of the three call into `schedules.go`'s validators), so the finding itself is still fully open — but the "verify before assuming" caveat above is now resolved *affirmatively*: this task is shovel-ready as originally scoped, not blocked on producers that don't exist yet.

**GO-API-005:** push `status`/`q` filtering (and a true total count) into `memory.RecallOpts`/`Recall` itself, **before** the limit/offset window is applied — i.e., the underlying store-layer query should filter first, then paginate, then report a real total distinct from the page length. This is a code-order fix at the query layer, not just the handler layer: `memories.go:93-98`'s premature `opts.Limit=limit+offset` assignment needs to move to after (or be restructured around) the `status`/`q` filter application, and the query itself (wherever `memory.Recall`'s SQL/store logic lives) needs to compute a true total independent of the page window.

**All production callers:** for GO-API-005, confirm `handleListMemories` is the only production caller of the affected `RecallOpts`/`Recall` path, or that any other caller is unaffected by moving filter logic into the store layer — a store-layer change is more likely to have multiple callers than a handler-layer change; verify this explicitly, don't assume single-caller just because the audit only names one.

## Non-goals

GO-API-004 is not a mandate to build a general cross-cutting validation framework — scope to the schedules/settings fields the audit and the in-repo comment specifically name. GO-API-005 is not a mandate to redesign `memory.RecallOpts`/`Recall`'s broader API — scope to fixing the filter-then-paginate ordering and the total count, not restructuring the type.

## Tests required

- **GO-API-004:** a test asserting the same validation rule now fires identically regardless of which producer (HTTP handler, reflex hook, self-tool handler) triggers it — a parity test proving the consolidation actually removed the divergence risk, not just the duplicated lines.
- **GO-API-005 (mandatory, named explicitly in this batch's authoring instruction):** a test with more matching records than fit in one `limit`+`offset` window after a `status`/`q` filter is applied, asserting the endpoint does **not** silently truncate below the requested `limit` when more matches exist — i.e., seed enough records that naive limit+offset-then-filter would previously have under-returned, and assert the fixed version returns the correct page and a correct total.

## Prevention

GO-API-004's fix directly closes one instance of the guide's own "Semantic Duplication" standard (§4 Wave 7: "duplicating a semantic rule is a correctness concern") — cross-reference folder 11 (`semantic-duplication-migration-drift`) since this is exactly the pattern that folder is built around, even though this specific instance is scoped here because it's an `internal/api`-specific, already-self-documented case. GO-API-005's fix is a straightforward query-ordering correctness fix; the new pagination-under-filter test is itself the regression-prevention mechanism — no broader structural change needed.

## Verification

`go build ./...`; `go test ./internal/api/...` (new/updated schedules/settings and memories tests); manual verification that memories pagination now returns a correct `total` when filters reduce the match count.

## Risk / rollback

GO-API-004's consolidation carries real regression risk **if the three producers' current validation rules have quietly diverged** from each other since the in-repo comment was written (i.e., "consolidate" assumes they're still meant to be identical — verify this assumption before merging, don't just assume the comment is still accurate). GO-API-005's fix changes `handleListMemories`'s returned `total` and potentially the returned page contents for any caller currently relying on the buggy under-return behavior (unlikely, since it's a bug, but worth a quick check that no UI code silently depends on the current wrong total). Rollback for either is a straightforward revert.

## Done means

- [x] GO-API-004: three producers' current validation rules confirmed still equivalent (or divergence resolved) before consolidation; consolidation landed; parity test passing
- [x] GO-API-005: filter-then-paginate ordering fixed in `memory.RecallOpts`/`Recall`; true total count implemented; the many-matches-beyond-one-window regression test passing

## Work log

- Implemented 2026-08-23. GO-API-004 and GO-API-005 remain independent
  fixes bundled only because both findings were filed against `internal/api`.
- GO-API-004 producer trace confirmed the three named production paths:
  HTTP create/patch in `internal/api/schedules.go`; reflex execution through
  `internal/agent/reflexes.Executor.Apply` and
  `service.NewReflexScheduleHook`; and the `schedule_create` self-tool. Their
  rules had drifted: one-shot spec handling, whitespace handling, expiry and
  retry checks, and accepted job types differed. `store.ValidateAgentSchedule`
  now owns the common row invariants, all three producers call it, and
  `InsertAgentSchedule` enforces it as the final boundary for the additional
  managed-config and loop-tick producers. The self-tool retains only its
  narrower tool-schema rule that `cron_expr` is omitted for one-shot requests.
  The shared job taxonomy includes `loop_run_tick`.
- Settings scope correction: `handleUpdateSettings` is the only producer of
  the named enum fields. `internal/api/tools.go` also persists
  `UserSettings`, but changes only `ToolLoadPreferences`; other direct store
  callers do not independently parse those enums. With no sibling producer
  or duplicated rule to consolidate, `settings.go` was intentionally left
  unchanged.
- GO-API-005 caller trace found production `Recall` callers in the memories
  API, context broker, grounding, learnings, and tool-client memory signal.
  The new `Statuses`, `Search`, and `Offset` options are zero-value compatible,
  so only the API opts into list filtering/pagination. Status and text filters
  now run before the page slice, and `RecallPage` returns a separately computed
  filtered total. The handler no longer performs post-window filtering or
  reports page length as total.
- Regression coverage pins the identical malformed-cron rule at the HTTP,
  reflex, and self-tool paths plus the shared producer-row validator. The
  memories regression seeds five higher-ranked nonmatches before four matching
  records; `status=reviewed&q=needle&limit=2&offset=1` returns two records and
  total four, where the old `limit+offset` fetch returned none.
- Verification passed: focused schedule/memory tests; full relevant-package
  tests; `go test ./internal/api/... -count=1`; focused `go vet`; and the
  non-race baseline `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...`.
- Correction pass 2026-08-23: removed the finite 500-candidate prefilter from
  list paging. `RecallPage` now queries all metadata-matching current revisions,
  applies one Go Unicode-aware summary/body predicate, reproduces the pinned
  Tesseract activation/chronological score order, derives total from that exact
  filtered set, and only then applies offset/limit. The 500 ceiling remains only
  as the per-page maximum. Regressions cover 505 higher-ranked text nonmatches,
  a status-only offset of 500, Unicode case matching with identical page/total,
  and preserved activation order.
- The same correction tightened the shared schedule domain rule so whitespace-
  only `Name` and `Body` are invalid for every row producer. HTTP, reflex,
  self-tool, and producer-shaped validator tests now cover this second parity
  rule in addition to malformed cron.
- Correction verification passed focused normal and race tests across API,
  memory, store, service, and self-tools; focused vet; and the full non-race
  `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` baseline.
- Second correction pass 2026-08-23: the API fixture had isolated Nanite's DB
  but not the independently resolved embedded Tesseract DB. The 510-row
  regression could therefore open and write the operator path during tests,
  and concurrent runs surfaced `SQLITE_BUSY`. API tests now pin all XDG roots
  and `TESSERACT_DB_PATH` before any container construction; `newTestAPI`
  asserts both the pre-open resolved path and SQLite's actual `main` file are
  under its `t.TempDir`, while package `TestMain` protects direct container
  fixtures. Memory tests similarly inspect their explicit temp Conduit DB.
  No cleanup or mutation of the operator DB was attempted; that remains
  outside this worker's scope.
- The uncapped list path now parses timestamps exactly like pinned Tesseract:
  RFC3339Nano first, then legacy `time.DateTime`. Tests write legacy
  `created_at` and `last_accessed_at` values and pin chronological and
  activation ordering. A current-revision test also compares the list order
  with Tesseract Recall's own activation order.
- List reads again preserve Tesseract's best-effort access reinforcement
  semantics (`activation + 0.1*(2-activation)`, access count, RFC3339Nano last
  access), applied only to records actually returned after filtering and
  offset. Tests prove filtered, offset-skipped, and after-page records remain
  unchanged while total and returned order stay correct.
- Second-correction verification passed an isolated-path proof before the
  affected regressions, focused API/memory tests, focused API/memory race
  tests, focused vet, and the full non-race `go build ./cmd/nanite/`,
  `go vet ./...`, `go test ./...` baseline.
- Final bounded isolation correction 2026-08-23: audited every
  `NewContainer`/`ContainerConfig` occurrence in `internal/service` tests. The
  pre-existing post-reaper-failure fixture was the sole call; the new
  isolation regression is now the only other one. Package `TestMain` creates
  one unique disposable root before any test runs, pins `HOME`, all four XDG
  roots, and `TESSERACT_DB_PATH` beneath it, and selects a test-only Tesseract
  workspace. Those values remain immutable for the test process, so parallel
  service tests are race-safe; each package test binary has its own environment
  and temp root, so the isolation cannot leak or collide across packages.
- The regression first asserts every resolved Tesseract/XDG layout path is
  under the package temp root, then constructs a real service Container and
  inspects its opened Conduit SQLite connection with `PRAGMA database_list`.
  SQLite's actual `main` file must remain under the disposable root and equal
  the resolved Tesseract DB after symlink canonicalization. Focused normal and
  race tests for both service Container fixtures passed, followed by the full
  `internal/service` package and non-race `go build ./cmd/nanite/`,
  `go vet ./...`, and `go test ./...` baselines. The operator's database was
  neither opened nor modified.

## Review notes

- 2026-08-23: Final fresh review through `eb373144fff495fbe566e937254d10cf19253d4e` passed after two correction rounds. The review verified shared schedule validation across every producer and final store boundary, the settings scope correction, uncapped filter-before-pagination, large offsets, unified Unicode page/total matching, legacy timestamp/order parity with Tesseract, and returned-page-only access reinforcement. It also verified every service/API/memory test database resolves beneath disposable roots using the actually opened SQLite path. Full service tests, focused race suites, build, vet, and series-wide diff checks passed; the operator database was not opened during final review.
