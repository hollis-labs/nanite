# Build PluginStore — scoped SQL proxy replacing raw GetService("store")

**Phase:** 2 — Tier 1 hygiene: scoped service proxies (`TASKS/plugin-system`)
**Status:** not-started
**Depends on:** none
**Touches:** new file (e.g. `internal/plugin/pluginstore.go`), `internal/plugin/host.go`
(`GetService`/`RegisterService`, `h.activePlugin`), `cmd/nanite/main.go` (the `"store"` service
registration), `internal/store/store.go` (`migrate()`, the goose provider setup — read closely,
extend only if the chosen mechanism needs a second tracking table). No current builtin plugin
needs migrating off raw store access (see Context) — this is new infrastructure, not a fix to
an existing offender.

## Context

`docs/engineering/architecture/09-plugin-system.md`'s "Target design: capability model"
section — audit findings 01 (`GetService("store")` = full DB access), 03 (`GetService("mcp")`
= full MCP access, this task's sibling `03`), and 07 (`GetService` returns untyped
`interface{}`, no typed accessor at all). **Decision: Tier 1 (builtin) gets scoped service
proxies, not enforcement** — blast-radius hygiene, not a security boundary; a builtin is still
fully trusted and could still reach further via a normal Go import if it tried. The point is
that an ordinary, non-adversarial builtin no longer *casually* gets the raw `*sql.DB` just by
typing `"store"`. **`PluginStore`'s concrete shape: scoped SQL under a host-managed
`plugin_<id>_*` namespace** — not a KV/JSON downgrade, plugins keep real SQL, but the host
creates and migrates their tables on their behalf, folded into the real migration system
instead of ad hoc, unmanaged `InitSchema`-style calls.

This planning session's own research independently re-verified every premise:

- **The specific offender the audit named is already gone — this is genuinely new
  infrastructure, not a migration-off-of-something-live task.** The audit (2026-04-11) cited
  `internal/plugin/builtin/sessionstats/plugin.go:L41-48` creating its own unmanaged SQLite
  schema via the raw `*sql.DB` from `GetService("store")`. That entire package
  (`internal/plugin/builtin/sessionstats/`) was deleted in commit `a19882fc` ("Phase 0 #18a:
  cut dead storage and config", 2026-08-18), along with the `session_stats` table itself. A
  repo-wide grep of every tracked `.go` file for `GetService("store")` or `GetService("mcp")`
  returns **zero source-code hits** today — no builtin plugin currently calls either. (One
  loose end, not code: `plugins/repos.yaml` still lists a `session-stats` external-repo catalog
  entry — stale catalog metadata, not live unmanaged-schema code; not this task's job to clean
  up, flag it if convenient.)
- **`GetService`/`RegisterService` are unchanged from the audit in substance.**
  `internal/plugin/host.go:541-552` (`GetService`) and `:562-570` (`RegisterService`) both
  operate on a plain `map[string]interface{}` (`h.services`, `host.go:112`) — fully untyped,
  no scoping. This matches the plugin SDK's own interface signature
  (`github.com/hollis-labs/plugin-sdk@v0.3.0/plugin.go:71-72`, pinned in `go.mod:77`), so it's
  not a local regression — it's the real, current, unaddressed gap.
- **Current `"store"` registration**: `cmd/nanite/main.go:304` registers the full `*store.Store`
  (constructed `store.New(...)` at `main.go:175`) under the name `"store"`.
- **No per-plugin or dynamic-migration mechanism exists today — this is genuinely new
  design work, not an extension of something partially built.** `internal/store/migrations/`
  is one flat, hand-authored, linearly-numbered goose sequence (134 files as of this planning
  session), run via `goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, ...)`
  (`internal/store/store.go:141`) against goose's own single `goose_db_version` tracking table.
  Nothing in `internal/plugin/host.go` or `internal/plugin/manage.go` creates or migrates
  tables on a plugin's behalf.
- **Per-plugin identity is only valid synchronously during `Load()` — a real design
  constraint, not an oversight to route around.** `Host.activePlugin` (`host.go:123`) is set
  once, at `host.go:1199`, immediately before `p.Load(h)` is called (`host.go:1204`), and read
  by every `Register*`/`Get*` method that needs an owner. The SDK's `Load(host Host) error`
  call gives a plugin no way to identify itself on a *later* `GetService` call made outside
  that synchronous window (e.g. from an event hook). **This means `PluginStore` must be
  captured once, inside `Load()`, and held by the plugin for later use — not re-fetched.**
  Document this requirement explicitly; don't try to make `GetService` support ambient
  identity outside `Load()`, that's a bigger change than this task needs.
- **Plugin IDs are not guaranteed SQL-identifier-safe strings.** For builtins, `ID()` is a
  hardcoded Go string literal per plugin (e.g. `internal/plugin/builtin/adapter-claude/plugin.go:73`,
  `return "adapter-claude"`) — safe by construction, author-controlled. But
  `ParseManifest` (`internal/plugin/config.go:391-401`) does zero validation on manifest
  `id`/`name` fields, and no slug-format check exists anywhere in the load path for plugin IDs
  specifically (`validComponentID`, `host.go:21-22`, exists but is scoped to UI component IDs
  only). Since builtins are trusted, this isn't a security gap — but an unvalidated ID could
  still produce broken SQL (a self-inflicted bug, not an exploit) when interpolated into a
  `plugin_<id>_*` table name.

## What to do

1. Add a plugin-ID slug validator (reuse/extend `validComponentID`'s pattern,
   `internal/plugin/host.go:21-22`) and apply it wherever a plugin ID is used to build a
   `plugin_<id>_*` table-name prefix. Reject (with a clear error at `Load()` time, not a silent
   truncation) any ID that doesn't match.
2. Define a `PluginStore` interface (new file) exposing SQL access scoped to a `plugin_<id>_*`
   table-name prefix for the calling plugin, plus a schema-provisioning method the plugin calls
   once at `Load()` time to declare its own tables. Concrete recommendation, not a locked
   mechanism — validate/adjust if a cleaner approach emerges during implementation: give each
   builtin plugin a way to embed its own migration SQL (Go `embed.FS`, following the existing
   `NNN_description.sql` file-naming convention within its own embedded directory) and register
   it via a new typed `Host` method called from `Load()`; run each plugin's embedded migrations
   through a **second, separate goose provider instance** pointed at a distinct tracking table
   (not the core `goose_db_version` table `(*Store).migrate` already owns —
   `internal/store/store.go:137`, provider built at `:153`; the previously cited `:141` had
   drifted, re-derived at `5ec930c8`) — this
   keeps plugin schema versioning fully independent of the core numbered sequence, so this
   batch's own `04`'s migration claim (`135` — ⚠️ stale and unusable as of `5ec930c8`, see
   `04`'s own "Migration numbering" annotation; whatever `04` actually lands on, the point of
   the separate provider is unchanged) and any future core migration never collide with
   plugin-contributed schema. Whatever mechanism you land on, every table it creates must carry
   the `plugin_<id>_` prefix — enforce this as a real check (parse/lint the plugin's own
   migration SQL for table names, reject any that don't match the prefix), not just a
   convention stated in a comment.
3. Wire `PluginStore` as what `GetService("store")` returns going forward — captured with the
   calling plugin's ID at the moment of the call (valid because `h.activePlugin` is correctly
   scoped during the synchronous `Load()` window this call happens in). Document plainly (in
   the SDK-facing type's doc comment) that a plugin must call this once inside `Load()` and
   hold the returned value — a later out-of-`Load()` call has no host-side notion of which
   plugin is asking. Remove the raw `*store.Store` from the `"store"` service-registry entry
   entirely — don't keep it under a different name as an opt-out; per the doc's own framing, a
   builtin that genuinely needs it can always reach `internal/store` via a normal Go import,
   same as any other trusted core code — no new escape-hatch mechanism is needed or wanted.
4. Add a GLOSSARY.md entry for `PluginStore` and for "Trust tier" if not already covered by a
   sibling task in this batch (check before duplicating — `03`/`04` may add related entries;
   coordinate wording, don't write three inconsistent definitions of the same tier concept).

## Done means

- No builtin plugin can obtain the raw `*store.Store`/`*sql.DB` via `GetService("store")`
  anymore — verified by a test asserting the returned type is `PluginStore`, not
  `*store.Store`.
- A test builtin plugin can declare its own schema at `Load()` time, have it provisioned
  through the new mechanism, and perform real SQL reads/writes against its own
  `plugin_<id>_*`-prefixed table(s) — end-to-end, not mocked.
- Attempting to provision a table without the `plugin_<id>_` prefix is rejected at
  registration/migration time, with a clear error — verified by a negative test.
- A plugin ID that fails the new slug validator is rejected at `Load()` time before any table
  provisioning is attempted.
- The plugin schema's own migration/versioning tracking is fully independent of the core
  `internal/store/migrations/` numbered sequence — verified by confirming the core migration
  count/version is unaffected by loading a plugin that provisions its own schema.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
