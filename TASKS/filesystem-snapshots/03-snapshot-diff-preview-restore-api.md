# Snapshot diff/preview/restore API + conflict hash-check

**Phase:** 3 — Consumer surface (`TASKS/filesystem-snapshots`)
**Status:** not-started
**Depends on:** `01` (host mechanism), `02` (real capture data must exist to diff/restore
against)
**Touches:** new `internal/api/snapshots.go` (or similar — follow the closest existing REST
CRUD precedent, e.g. `internal/api/teams.go`), route registration in `internal/api/api.go`.
Repo: Nanite. **No frontend work** — this is a backend API surface only, per this task's own
Done means and this project's standing no-frontend-in-any-phase discipline.

## Context

`docs/engineering/architecture/18-filesystem-snapshots.md` names the GUI surface for
selective restore ("a per-changed-file checklist... restore selected") as "a real, valuable
UX but a frontend concern — deferred." This task builds the backend API that surface would
eventually call, following this project's existing pattern of building backend-complete,
frontend-deferred REST surfaces (e.g. `TASKS/teams/10-team-crud-api.md`'s own explicit
framing).

**Settled design call this task must implement directly**: "Restore is selective/path-scoped
by default, never whole-tree-unconditional... A lightweight conflict check (compare the
current file's hash against the snapshot's hash before restoring that path; warn rather than
silently overwrite) is cheap given git's content addressing and belongs in v1 — this is a
hash comparison, not 'sophisticated merge behavior,' so it doesn't conflict with deferring
real conflict resolution." Real conflict/concurrency handling beyond this hash-check is
explicitly deferred (see task `02`'s Context) — do not attempt to build merge logic here.

**Correlation, not coupling, with the session/conversation timeline**: the architecture doc
is explicit that "the UI can still *correlate* the two axes without the underlying data model
coupling them — 'restore files touched after this point in the conversation' is a perfectly
good feature... Correlation at the UI layer is fine; coupling at the data layer is not." This
task's API can accept a session/turn identifier as a convenience parameter for *looking up*
which `SnapshotSet`s correspond to a given conversation point, but must never make a session
restore imply a filesystem restore or vice versa at the data-model level — the two remain
genuinely independent records.

**Security note carried forward from task `01`'s Context**: the shadow store holds full file
contents indefinitely until cleanup and needs equivalent access-control treatment to other
host-owned artifacts (boot dir, sandbox). This API surface is a new way to read/reconstruct
that content — apply the same auth/permission model this codebase's other per-agent/
per-project resource endpoints already use (check the closest precedent, e.g. how
`/api/agents/{id}/...` or `/api/projects/{id}/...` gate access) rather than leaving this
endpoint open by omission.

## What to do

1. `GET /api/snapshots?project_id=...` (or similar — list `SnapshotSet`s for a project/agent
   scope), `GET /api/snapshots/{id}/diff?to={id}` (task `01`'s `Diff`), `GET /api/snapshots/
   {id}/preview?paths=...` (task `01`'s `Preview`), `POST /api/snapshots/{id}/restore` (task
   `01`'s `Restore`, body: `{paths: []string}` — selective only, no whole-tree-restore
   parameter should exist in the request shape at all, matching task `01`'s own "no
   whole-tree-restore code path" constraint).
2. Before calling `Restore` for any given path, run the lightweight conflict check: compare
   the current on-disk file's hash against the snapshot's stored hash for that path. If they
   differ (meaning the file has been modified since the snapshot's `to` point in a way that
   isn't just "restore to an earlier state" — i.e., something else touched it after), return
   a warning in the response rather than silently overwriting; require an explicit
   confirm-anyway flag on the request to proceed past that warning.
3. Accept an optional session/turn correlation parameter for listing (per the Context's
   correlation-not-coupling note) — implement this as a lookup convenience only, never as a
   foreign key or implicit trigger between session state and `SnapshotSet` state.
4. Apply the same auth/permission gating this codebase's comparable per-project/per-agent
   resource endpoints already use — cite the specific precedent you followed in your Work Log,
   same discipline `TASKS/teams/10-team-crud-api.md`'s own Work Log used for its own auth-
   model call.
5. Standard REST error handling — 404 for a nonexistent `SnapshotSet`/project, 400 for a
   malformed request body, matching this codebase's existing API conventions.

## Done means

- All four endpoints implemented, backed directly by task `01`'s
  `FilesystemSnapshotProvider` interface.
- The conflict hash-check is real and tested: a path whose on-disk content diverges from the
  snapshot's stored hash is flagged, not silently overwritten, and requires an explicit
  confirm to proceed — covered by an explicit test for both the warned and confirmed-anyway
  paths.
- No request shape permits a whole-tree restore.
- Session/turn correlation is lookup-only — no schema coupling between session state and
  `SnapshotSet` state (verified: no foreign key from a snapshot table to a session/turn
  table beyond an optional, non-enforced reference used purely for the listing filter, if
  even that).
- Auth/permission gating matches this codebase's comparable existing endpoints, documented
  with a cited precedent in the Work Log.
- Standard REST test coverage (list, diff, preview, restore-with-conflict, restore-clean,
  404, 400).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` clean.
- No frontend code — this task's scope ends at the API surface.
