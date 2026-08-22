# Transport-layer validation duplication (schedules/settings) and a memories-pagination correctness bug

**Phase:** Wave 3 — Remaining security hardening (guide §4; sequenced 2026-08-21 — see the sequencing block below)
**Status:** not-started
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

- [ ] GO-API-004: three producers' current validation rules confirmed still equivalent (or divergence resolved) before consolidation; consolidation landed; parity test passing
- [ ] GO-API-005: filter-then-paginate ordering fixed in `memory.RecallOpts`/`Recall`; true total count implemented; the many-matches-beyond-one-window regression test passing

## Work log

<!-- Worker fills this in. -->

## Review notes

<!-- Reviewer fills this in. -->
