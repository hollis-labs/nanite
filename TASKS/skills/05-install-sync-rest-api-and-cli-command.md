# Install/sync REST API + CLI command

**Phase:** 3 — Explicit install/sync (`TASKS/skills`)
**Status:** not-started
**Depends on:** `04`
**Touches:** `internal/api/skills.go` (new file, or extend an existing skill-admin API file if
one exists — grep `internal/api/` for existing skill routes before creating a duplicate),
`internal/api/api.go` (route registration), `cmd/nanite/` (new CLI subcommand, mirroring
whatever pattern `cmd/nanite/plugin_cmd.go` uses for plugin install/enable/disable, per
`docs/engineering/architecture/09-plugin-system.md`'s own CLI-install precedent).

## Context

`docs/engineering/architecture/20-skills.md`'s "API surface" section: *"Install/sync a skill
package (by path or upload) into the vendored store + index; re-sync re-hashes and re-vendors on
change."* This is the operator-facing trigger for task `04`'s pipeline — this task does not
implement any parsing/vendoring/indexing logic itself, only the REST endpoint and CLI command
that call task `04`'s `Installer`.

The plugin system has a directly analogous, already-live CLI-install path
(`cmd/nanite/plugin_cmd.go`, referenced in `TASKS/plugin-system`'s planning as already fully
hot-loading with zero restart for subprocess plugins) — worth reading for the shape of a
"local-path install" CLI command in this codebase before inventing a new pattern from scratch.

## What to do

1. Add `POST /api/skills/install` (body: local package path, or — matching `20-skills.md`'s "by
   path or upload" — an uploaded archive/directory, though a local-path-only first cut is
   acceptable for this batch given the ecosystem-format-adaptation scope fence in
   `TASKS/skills/README.md`; note explicitly in your Work Log if upload support is deferred) that
   invokes task `04`'s `Installer` for a single named target and returns the resulting index row
   (or a structured validation-failure response).
2. Add `POST /api/skills/{slug}/sync` (or fold into the same endpoint with re-sync-if-exists
   semantics — pick whichever reads more consistently with this project's existing REST
   conventions, check a few sibling `internal/api/*.go` files for the house style before
   deciding) that re-runs install against the same source path for an already-indexed skill.
3. Add a CLI subcommand (e.g. `nanite skill install <path>`, `nanite skill sync <slug>`) wired
   through `cmd/nanite/`, following whatever existing subcommand-registration pattern
   `plugin_cmd.go` (or the closest actual precedent you find) uses.
4. Error surfacing: a validation failure (task `04`'s `Validator` step) must return a clear,
   specific error through both the REST and CLI paths — not a generic 500/panic.

## Done means

- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- A real install via both the REST endpoint and the CLI command against the same test fixture
  package (from task `04`'s Done-means) succeeds end-to-end, confirmed via a live dogfeed
  (scratch DB, real `nanite serve` + real CLI invocation) — not just a unit test against the
  handler in isolation.
- A malformed package produces a clear error response/exit code through both paths, not a panic
  or an opaque 500.
- Re-sync via either path updates the existing index row and produces a new vendored address per
  task `04`'s re-sync behavior.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
