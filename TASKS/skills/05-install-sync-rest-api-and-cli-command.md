# Install/sync REST API + CLI command

**Phase:** 3 — Explicit install/sync (`TASKS/skills`)
**Status:** implemented
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

**Container wiring (`internal/service/container.go`).** Added `SkillVendor
*skillvendor.Store` to `service.Container`, constructed at container-build
time from `AppConfig.Skills.VendorStorageDir` (falling back to
`config.DefaultAppConfig().Skills.VendorStorageDir` = `data/skills/vendor`
when `AppConfig` is nil or the field is empty — mirrors `ArtifactsConfig.
StorageDir`'s existing load/default/override pattern). A construction
failure logs a warning and leaves `SkillVendor` nil rather than failing
container boot — install/sync degrades to a 503 at the API layer instead of
taking the whole service down over a skills-only storage-path problem.

Deliberately did **not** add a shared `*skillinstall.Installer` field to the
container. `Installer` carries call-scoped mutable state (`state`, `Emit`)
that task 04's own doc comment says mirrors `internal/plugin/install`'s
"build a fresh Installer per invocation" convention (see `cmd/nanite/
plugin_install_flow.go`'s `buildInstaller`, called fresh on every CLI
command). A single shared `*Installer` reused across concurrent HTTP
requests would race on that state (two concurrent installs would clobber
each other's `State()`/`Emit` observations) — a real correctness bug, not
just a style deviation from the plugin precedent. Both REST handlers and
the CLI construct `&skillinstall.Installer{Vendor: ..., Index: ...}` fresh
per call instead, using the two stateless, concurrency-safe primitives the
container/CLI actually share: the vendored `*skillvendor.Store` (documented
safe for concurrent use) and `*store.Store` (satisfies `skillinstall.
IndexStore` directly — `GetSkillBySlug`/`CreateSkill`/`UpdateSkill` already
existed on it from task 02).

**REST API (`internal/api/skills.go`, `types.go`, `api.go`).**
- `POST /api/skills/install` — body `{"path": "<local dir>"}`. Builds a
  fresh Installer, runs `Install`, returns `{skill, address, reused}` as
  201 on success. Local-path-only, as flagged as acceptable in this task's
  own "What to do" item 1 — **upload support is deferred**, not built in
  this batch (no multipart/archive handling exists anywhere in this
  handler).
- `POST /api/skills/{slug}/sync` — same body shape. Requires the target
  slug to already exist in the index (404 if not) and pre-parses the
  package at `path` via `skill.ParsePackageDir` (a pure read, no
  vendoring/indexing side effect) to confirm its own frontmatter slug
  matches the URL `{slug}` *before* ever calling the real Installer (409
  Conflict if it doesn't) — this prevents a caller from installing/
  mutating an unrelated skill's row through the wrong sync endpoint, a
  failure mode that calling `Install` first and checking the result
  afterward would not have prevented (by the time you could check, a wrong
  package could already have created or updated a different row). On
  match, runs the same fresh-Installer path as `/install` and returns 200.
- Chose **two endpoints** (task's option 2) over folding sync into a single
  endpoint with re-sync-if-exists semantics: the existing `handleCreateSkill`
  /`handleUpdateSkill` pair in this same file already establishes create-vs-
  update as separate verbs/routes in this codebase's house style for this
  exact resource, and the slug-match guard above only makes sense as an
  endpoint whose URL already names a specific existing target.
- Error-status classification (`runSkillInstall`): a Parsing/Validating-step
  failure (malformed package — the caller's problem) maps to 422
  Unprocessable Entity; a Vendoring/Indexing-step failure (vendor-store or
  DB write problem — an infra/operator concern) maps to 500. Determined by
  tracking the last non-error `Event.State` the Installer's own `Emit`
  stream reports before the failure event (the last *entered* step is
  exactly the step that then failed) — not by string-matching the wrapped
  error message, which is documented as being for a human, not for control
  flow.
- `SkillVendor == nil` (container-boot-time vendor-store init failure) → 503
  on both endpoints, not a nil-pointer panic.

**CLI (`cmd/nanite/skill_cmd.go`, `main.go`).** New top-level `nanite skill
<install|sync>` command (registered in `main.go`'s dispatch switch and usage
string). `install <path>` and `sync <slug> <path>` open the shared SQLite DB
directly via `store.New(resolveDBPath())` — the same helper `mcp_cmd.go`'s
import/export subcommands already use — rather than routing through the
REST API. This is a deliberate deviation from `plugin_cmd.go`'s own
install flow, which needs *no* DB access at all (a pure filesystem copy,
only notifying a *running* service over HTTP for the hot-reload tail step);
a skill install/sync genuinely needs to write both a vendored-store copy
and a DB index row, and the store's own WAL + `busy_timeout(5s)` pragmas
(`internal/store/store.go`) are exactly what make that safe to do from a
CLI process running alongside a live `nanite serve` on the same DB file —
confirmed directly in the live dogfeed below, not just asserted. `sync`
carries the identical pre-parse slug-match guard as the REST handler.
Progress is printed via a small `printSkillInstallEvents` mirroring
`plugin_install_flow.go`'s own `printEvents` throttled-by-state-change
convention. Every failure path (`store.New` error, vendor-store init
error, GetSkillBySlug error, unknown slug, parse error, slug mismatch,
Install error) prints a specific `nanite skill <install|sync>: ...` message
to stderr and exits 1 — never a panic or stack trace.

**`.gitignore`.** Added `/data/skills/` alongside the existing
`/data/artifacts/` scratch-output entry. Without this, the default vendor
root (`data/skills/vendor`) was only accidentally excluded by the repo's
pre-existing bare `vendor/` ignore rule (meant for Go dependency vendor
dirs) — a coincidence of the directory being named "vendor", not a
deliberate ignore. Made explicit so it stays ignored even if the default
directory name ever changes. (Noted but left untouched as out of scope: a
pre-existing, already-tracked stray test artifact at `internal/api/data/
artifacts/compact-sess/art-stash-*.txt`, from an unrelated prior task's
test run — not something this task's own dogfeed created or should fix.)

**Live dogfeed** (real `nanite serve` + real CLI invocation, scratch DB —
not a handler-in-isolation unit test), against task 04's
`internal/skillinstall/testdata/fixtures/` (`sample-skill`, `malformed-
frontmatter`, `malformed-missing-script`), all run from an absolute scratch
path outside any tracked directory:
- Built the worktree's own `cmd/nanite` binary, ran `nanite serve` against
  a scratch `NANITE_DB_PATH` from a scratch CWD (so the default
  `data/skills/vendor` root resolved under the scratch dir, never the
  repo).
- `POST /api/skills/install` against `sample-skill` → 201, real vendored
  address (`skl-vendor-1f6340b2077011de`), version 1.
- `POST /api/skills/install` against `malformed-frontmatter` and
  `malformed-missing-script` → both 422 with the exact `ValidationError`
  message (missing `name`; `scripts/does-not-exist.sh` doesn't exist) — no
  panic, no opaque 500.
- `POST /api/skills/sample-skill/sync` against the unchanged fixture → 200,
  `reused: true`, same address/version (no-op re-sync).
- Mutated a scratch copy of `sample-skill`'s body text, synced again → 200,
  new address (`skl-vendor-dc6d77e08a3101a6`), version bumped 1→2 — task
  04's re-sync/re-hash/re-vendor contract confirmed live.
- `POST /api/skills/some-other-slug/sync` (never-installed slug) → 404.
- `POST /api/skills/sample-skill/sync` with a *different* valid package
  (`other-skill`, its own valid slug) as the body → 409, and confirmed via
  `GET /api/skills` that no `other-skill` row was created — the pre-parse
  guard prevented the mutation entirely, not just reported it after the
  fact.
- CLI: `nanite skill install <malformed-frontmatter path>` (same
  `NANITE_DB_PATH`, run from the scratch CWD) → clear stderr error, exit 1.
- CLI: `nanite skill install <other-skill path>` → real install, printed
  step-by-step progress, exit 0; confirmed visible via a concurrent `GET
  /api/skills` against the *still-running* server — proves the CLI and the
  live server genuinely share DB + vendor-store state across processes,
  not just "both happen to work in isolation."
- CLI: `nanite skill sync <unknown-slug> ./other-skill` → clear "not found"
  error, exit 1. `nanite skill sync sample-skill ./other-skill` (mismatched
  slug) → clear "does not match sync target" error, exit 1.
- CLI: `nanite skill sync sample-skill <original unmutated sample-skill
  path>` → succeeded, address reverted to the original hash
  (`skl-vendor-1f6340b2077011de`, already present in the vendor store from
  the very first install, so `reused: true`), version bumped 2→3 because
  the *index row's pointer* changed relative to its immediately-prior state
  even though the underlying bytes were nothing new to the vendor store —
  confirms `Reused` (vendor-store dedup) and `Version` (index-row pointer
  change) are tracking two different, both-correct things, exactly as
  documented in task 04's `upsertIndex`.
- Stopped the server, deleted the entire scratch directory, and confirmed
  `git status --short` in the worktree showed only this task's intended
  source-file changes both before and after the dogfeed.

**Build/test.** `go build ./cmd/nanite/`, `go vet ./...` (two pre-existing,
unrelated `stopReaper`/`stopRuntimeReaper` lostcancel warnings in
`container.go`, confirmed via `git diff` to be outside this task's changed
lines), and `go test ./...` all pass (zero failures across the full suite,
including `internal/api`, `internal/service`, `internal/skillinstall`,
`internal/skillvendor`).

No escalations. No schema/migration changes needed.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
