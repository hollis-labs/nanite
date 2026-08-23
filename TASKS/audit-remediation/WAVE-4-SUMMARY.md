# Wave 4 summary — for the operator

Wave 4 is complete: **six reviewed, zero other Wave 4 statuses**. Five
production islands were retired and one—Team semantic routing—was connected
to the real HTTP launch path. The final Go diff is 5,262 deletions and 336
additions, a net reduction of 4,926 lines.

The repository-wide development freeze remains in force under AD-24. This
summary does not change the operator-approved Wave 3 `08/08` disposition: it
remains `implemented`, not `reviewed`, because its full race gate was deferred.

---

## What shipped

### Memory and context islands

AD-06 retired the complete grounding subsystem, its Store persistence adapter,
and its `SelfToolsTransport` pre-dispatch integration state. The live
`internal/memory.Service` and historical migrations/tables were preserved.

AD-07 retired the Hadron-specific context gate and its latent unbounded
relevance scorer. The four live Memory, Conduit, PCC, and Session context
sources remain registered and unchanged in purpose.

### Tool architecture and selection islands

AD-09 retired the dead root Tool interface, builder, registry, adapter, and
YAML loader with their tests. The live result cache remains byte-unchanged,
with a new accurate package doc. Fresh source inspection found that the live
stash categorizer depended on retired root types; it was detached to private
category values and retained with behavior tests.

AD-10 retired more than the original `ranking.go` estimate. Fresh review found
memory-signal and operator-skill producers, tests, fixtures, templates, config,
and documentation left orphaned by the first pass, so the correction removed
that whole dead support surface and its boot-time I/O. Reflection and
context-window token budgeting remain live.

AD-11 retired the curated matcher. `SelectToolsAsProvider` through
`selectToolsUncapped`/`SelectByIntent` is now the sole live intent-selection
mechanism; `stash.BuiltinCategorizer` remains a separate bucketing mechanism.

### Team semantic routing

AD-08 is the one wire. Current-source re-verification found both the handler
call and service construction missing, so `cmd/nanite/main.go` now constructs
`Container.TeamRouting`, and `handleLaunchTeam` requires
`InstallTeamRunRouting` after launch. HTTP coverage observes four real
run-scoped `dispatch_to_agent` reflex rows.

The operator-approved failure policy is fail closed without pretending
rollback. Returned partial reflex IDs are cleaned best-effort with
`context.WithoutCancel`; a structured HTTP 500 returns the persisted run ID,
status, and routing/cleanup error, explicitly saying the run remains persisted.
Run and member rows remain because no transactional rollback exists. A missing
routing dependency returns 503 before a run is created.

Semantic reflex installation does not make the separate `SendToSlot` /
`ResolveLazySlot` explicit Team-Slot messaging path production-reachable. That
source correction is now recorded in both the task and Teams handoff.

## Final accounting

The count uses `git diff --numstat d9665ea9..3f0e2b54 -- '*.go'`, classifying
`*_test.go` as test and all other Go as production. It excludes docs,
tracking, config, templates, and non-Go fixtures and counts shared-file edits
once in their final form.

| Class | Added | Deleted | Net | Architect estimate |
|---|---:|---:|---:|---:|
| Production Go | 146 | 3,297 | −3,151 | ~3,400 deleted |
| Test Go | 190 | 1,965 | −1,775 | ~2,600 deleted |
| **Total Go** | **336** | **5,262** | **−4,926** | — |

Production deletions landed about 3% below estimate; test deletions landed
about 24% below. The architect estimate was directionally correct but was not
an exact inventory, particularly for AD-09's tests. The final diff is the
source of truth.

## Review and verification

Every task received a fresh review. `09/01`, `09/02`, `09/04`, `09/05`, and
`09/06` each needed at least one correction pass, primarily because current
docs/comments or producer-side support still described or served retired
features. `09/03`'s runtime implementation passed; its only review findings
were tracking wording/status sync. Every correction received a fresh PASS.

The final merged baseline passed:

- `go build ./...`
- `go vet ./...`
- `go test ./... -count=1`

The Wave 4 rows and all six corresponding findings are `reviewed`.
Whole-batch finding status is 46 reviewed, one validated, two implemented, and
64 not-started.

## Decisions and deviations

- AD-06 through AD-11 were applied exactly as five retirements and one wire.
- The pre-Wave-4 planning mismatch in `TASKS/ESCALATIONS.md`—GO-MEM-002 and
  GO-MCPTOOL-003 lacked finding-level architect flags despite being islands—
  was already resolved by routing both through AD-07/AD-11. Both are now
  retired and reviewed.
- AD-08 required more than the architect table's shorthand “+1 call site”:
  the service constructor was also unwired, and observable HTTP-level
  reachability required Container/main composition plus regression coverage.
- AD-10 required more deletion than its original task banner named because
  fresh review found an orphaned producer/support layer. The expanded removal
  stayed within the retired feature and preserved live reflection/token
  budgeting.

## Follow-ups

- Re-derive `10/02` after removal of grounding fields/state; its ToolClient
  inventory also names deleted `ranking.go`.
- Re-derive `11/11` citations in the already-changed
  `self_tools_dispatch.go`; its reflex-sync objective remains live.
- Remove Hadron/root-tool entries from `13/01`'s prospective cleanup scope and
  rebuild that list with a fresh post-Wave-4 `deadcode` run.
- Re-derive `11/12`'s `internal/toolclient/broker.go` citations. Its duplicate
  `DevServerName` still exists, but the file changed substantially in `09/05`.

## Known limitations

- `SendToSlot`/`ResolveLazySlot` still lack a production explicit-addressing
  caller; AD-08 only made semantic/coordinator reflex installation live.
- Team Slots resolved lazily after installation still do not receive
  retroactive asking-side semantic reflex rows.
- `08/08` remains implemented-only under the operator-approved race deferral;
  no Wave 4 result upgrades it or claims a full-repository race PASS.

## Wave 4 status

| Task | Finding | Final status |
|---|---|---|
| `09/01` | GO-MEM-001 | reviewed |
| `09/02` | GO-MEM-002 | reviewed |
| `09/03` | GO-SVCEXEC-003 | reviewed |
| `09/04` | GO-MCPTOOL-001 | reviewed |
| `09/05` | GO-MCPTOOL-002 | reviewed |
| `09/06` | GO-MCPTOOL-003 | reviewed |

**Count: six reviewed; zero implemented, validated, in-progress, blocked, or
not-started within Wave 4.**
