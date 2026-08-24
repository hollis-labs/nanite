# Plugin System — target-design implementation

Implements `docs/engineering/architecture/09-plugin-system.md`'s "Target design" sections —
the design produced by a dedicated planning session (2026-08-21) that reconciled the
2026-04-11 internal audit (`docs/audits/2026-04-11-plugin-capability-model/`, findings
01-13 + capability map) against the plugin system's real, current state (several phases of
work — Phase 5's plugin-registration batch, the `agent_profiles[]`/`crud[]` wiring, the
DB-backed installed/enabled model — have landed since that audit and since `TASKS.md`'s
original Phase 5 pass). A sibling to `TASKS/teams/`, `TASKS/reflex-taxonomy/`,
`TASKS/harness-reactive-self-tools/`, `TASKS/scheduling/`, and `TASKS/agent-host-acp/` — kept
in its own top-level `TASKS/` subfolder for the same reason those are: this work originates
from a dedicated architecture-review pass, not `docs/engineering/TASKS.md`'s original plan.

## Read before starting any task here

1. `docs/engineering/architecture/09-plugin-system.md` in full — the "What's real and already
   deep," "Since wired up," and every "Target design" section. This planning session corrected
   two more stale passages in it directly (the CLI-install/hot-reload asymmetry and finding
   04's panic recovery were both already closed by prior work — see that doc's own updated
   text) — read the current on-disk version, not any cached memory of an earlier draft.
2. `docs/audits/2026-04-11-plugin-capability-model/` — `index.md` and `14-capability-map.md`
   first, then the individual findings a task's own Context cites. Written 2026-04-11; several
   of its code citations have since moved, been deleted, or (in two cases — findings 04's
   panic recovery, and the CLI-install asymmetry the architecture doc separately tracked) been
   fully resolved by unrelated work. **Every task file below states explicitly which of the
   audit's citations it independently re-verified against current code and which have drifted
   — don't trust the audit's own file:line numbers without cross-checking the task's own
   Context.**
3. `docs/engineering/GLOSSARY.md` — check before introducing any new name, per this repo's
   standing discipline. `PluginStore`, `PluginMCPClient`, and "Trust tier" are new to this
   batch (confirmed no collision at planning time); each task adds its own GLOSSARY entry.
4. `docs/engineering/EXECUTION-PROCESS.md` — the task-file format, worker/reviewer discipline,
   and escalation rules every task file below follows.

## What this batch does NOT do

Carried forward directly from the architecture doc's own scope fences — not silently narrowed
or widened by this planning pass:

- **A uniform per-plugin capability system for builtins.** Builtins stay at the same trust
  tier as core code — reviewed at PR time, not runtime. `02`/`03` are blast-radius hygiene
  (stop handing out raw `*store.Store`/`*mcp.Manager` by default), not a security boundary; a
  builtin can still reach further via a normal Go import if it tries. Only subprocess plugins
  (`04`-`06`) get real capability *enforcement*.
- **Sandboxing untrusted in-process plugins.** Named in the audit as its own follow-up scope
  (`plugin-sandbox-design`), pursued only if a real need for it shows up. Not this batch.
- **Pre-hook cancellation DoS (audit finding 09), subprocess entrypoint argument injection
  (finding 10), per-plugin OS-level resource limits (finding 12).** Explicitly out of scope in
  the architecture doc — adjacent hardening from the same audit, tracked separately.
- **`registers.panels[]`'s frontend render half.** Backend/manifest registration is already
  fully wired (confirmed in Phase 5); the render half is frontend work, deferred to a separate
  frontend pass per the standing "no frontend work in any backend phase" discipline. No task
  here touches it.
- **A frontend/UI trust boundary.** The architecture doc records this as an accepted
  limitation, not a design to build: a plugin's frontend bundle always runs at full trust in
  the host page regardless of its backend tier, and no current plugin needs isolation. Purely
  a documentation decision — no task file in this batch.
- **Formalizing the two-tier trust model itself as new code.** It's already structural — the
  `plugins.kind` column (`builtin`/`subprocess`, migration `121`, `CHECK` constrained) set by
  *how* a plugin is loaded, not by anything self-declared in its own `plugin.yaml`. Every task
  below builds *on* that existing column; none of them build it.

## Two audit findings this planning session found already closed — not tasks here

Verified directly against current code before drafting any task file, not assumed from the
audit's own age:

- **Finding 04 (no panic recovery in event-hook dispatch).** Landed 2026-04-12, the day after
  the audit, in commit `ce40fbcf7` (the repo-wide `safego` adoption sweep) — `Host.EmitEvent`
  (`internal/plugin/host.go`) and `EmitPreHook` (`internal/plugin/events.go`) both dispatch
  through `safego.Go`/`safego.Call` today, confirmed by a dedicated regression test
  (`internal/plugin/host_panic_test.go`) and a real dogfeed-caught panic recovery
  (`TASKS/INDEX.md`'s Phase 4 validation note, `internal/mcp/dev_tools.go`'s `callGrep`
  divide-by-zero). The architecture doc's own text describing this as "pulled in... worth
  doing alongside this work" was stale prose in this session's in-progress edit — corrected
  in place rather than carried into a phantom task.
- **The CLI-install/hot-reload asymmetry.** Fully closed by `TASKS/phase-5/04`
  (`close-cli-install-hot-reload-asymmetry`, reviewed), made actually load-bearing by
  `TASKS/phase-5/11` (`fix-hot-reload-never-applies-manifest-registrations`, reviewed) and
  `TASKS/phase-5/12` (`fix-unload-plugin-wrong-identifier`, reviewed). CLI install/update/
  enable for subprocess plugins hot-loads live today with zero restart, verified in each of
  those three tasks' own Work Logs against a real running instance. Two narrower, intentional
  exceptions remain (builtin install still restarts — structurally required; uninstall/disable
  still restart for both kinds — by design, since the manifest `handleReload` needs is already
  gone by the time either fires) — neither is the gap the architecture doc used to describe as
  open. Doc corrected in place.

Both corrections are logged in `TASKS/ESCALATIONS.md` under this batch's planning entry.

## Task sequence

Flat-numbered `01`-`07` across four phases (same convention as `TASKS/teams/` and
`TASKS/agent-host-acp/`), each file's own header states its Phase.

**Phase 1 — Registration hardening.** Small, mechanical, foundational — no schema.

| Task | Depends on |
|---|---|
| `01-registration-preflight-validation-and-nanitecompat.md` | none |

**Phase 2 — Tier 1 hygiene: scoped service proxies.** Replaces raw `GetService("store")`/
`GetService("mcp")` with narrow, typed proxies. Blast-radius hygiene per the doc's own framing
— builtins stay fully trusted.

| Task | Depends on |
|---|---|
| `02-build-pluginstore-scoped-sql-proxy.md` | none |
| `03-build-pluginmcpclient-scoped-proxy.md` | `02` (reuses its per-plugin-identity-at-`GetService`-time pattern and slug-validation helper) |

**Phase 3 — Tier 2 capability model: subprocess plugins.** Real enforcement, not hygiene —
this is the only tier that gets it. Strict-ish order: schema before either consumer.

| Task | Depends on |
|---|---|
| `04-plugin-capabilities-manifest-schema-and-storage.md` | none |
| `05-capability-install-time-approval-gate.md` | `04` |
| `06-capability-enforcement-at-rpc-proxy-layer.md` | `04`; parallel-safe with `05` (disjoint files — `05` touches `internal/plugin/install/`, `06` touches the four RPC-proxy call sites) |

**Phase 4 — Middleware plugin-extensibility.** Independent of Phases 2-3; resolves a
previously-open escalation (`TASKS/ESCALATIONS.md`, 2026-08-18, "HTTP middleware
plugin-extensibility") now that the operator has settled the shape.

| Task | Depends on |
|---|---|
| `07-make-http-middleware-plugin-extensible.md` | none directly; coordinate with `04` — both add a new field to `internal/plugin/config.go`'s manifest structs (different fields, low collision risk, sequence merges) |

## Parallelization plan

**Wave 1 — parallel, worktree-isolated.** `01`, `02`, `04`, `07`. Four independent starting
points, confirmed file-disjoint by each task's own Touches list: `01` touches
`internal/plugin/loader.go`/`registrations.go`; `02` touches a new `internal/plugin/` file
plus `host.go`'s service-registration section; `04` touches `internal/plugin/config.go` (new
struct field) plus a new migration; `07` touches `internal/server/server.go` plus
`internal/plugin/config.go` (a different new field than `04`'s — coordinate the merge order,
don't run the two `config.go` edits as truly concurrent commits).

**Wave 2.** `03` (needs `02`), `05` and `06` (both need `04`, mutually parallel-safe with each
other).

## Migration numbering

> **⚠️ STALE CLAIM, AND `135` IS NOT MERELY STALE — IT IS UNUSABLE. Annotated 2026-08-24 at
> `5ec930c8`.** The paragraph below is kept as written so the correction is visible rather than
> silently applied.
>
> `135` is still unoccupied, which is exactly the trap. AD-24's own freeze note recorded that
> "migration `135` remains unclaimed by Plugin System," which reads as *still available*. It is
> not. `135` is a **hole**: `63d79028` renumbered the Loops batch's `135`-`143` up to
> `138`-`146` to clear a collision with Skills, and nothing has filled the gap since.
>
> Nanite builds its goose provider **without** `WithAllowOutofOrder`
> (`internal/store/store.go:153` —
> `goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, goose.WithVerbose(false))`), so
> `allowMissing` is false. Under that default a migration numbered *below* a database's highest
> applied version is a hard error, not a back-fill. Reproduced against goose v3.27.3 with those
> exact options:
>
> ```
> detected 1 missing (out-of-order) migration lower than database version (137): version 135
> ```
>
> `Store.migrate` surfaces that as `goose up: …`, so **the service does not boot.** The live
> database is already past it — its ledger max is `137` with no `135` row
> (`sqlite3 ~/.local/share/nanite/workspaces/default/main.db 'select max(version_id) from goose_db_version'`
> → `137`). Landing task `04` on `135` would break startup for every existing deployment,
> including the operator's.
>
> **Next free is 148**, derived at `5ec930c8`:
>
> ```
> $ ls internal/store/migrations/ | sort -t_ -k1 -n | tail -1
> 147_remove_untouched_official_catalog_source.sql
> ```
>
> **Re-derive at the moment task `04` writes its file — do not carry `148` forward from here.**
> A number is claimed by the file existing on `main`, not by a task file naming it, and several
> frozen batches resume in parallel. See `TASKS/INDEX.md`'s "Migration numbering — the claiming
> rule" banner and `docs/engineering/tracking-integrity.md` check 9.

Highest existing goose migration on disk at this planning session's authoring time
(2026-08-21) is `134_agent_profiles_protocol_transport.sql` (landed by the concurrently-running
`TASKS/agent-host-acp` batch, task `11`). This batch provisionally claims `135` for `04`'s
`plugins` table extension — the only task in this batch that needs a real core-sequence
migration (see that task's own Context for why `02`'s per-plugin schema provisioning
deliberately does *not* consume from this same numbered sequence). Same real cross-batch
collision risk every other batch in this folder has hit — re-list the migrations directory
immediately before landing any migration, and re-check whether `agent-host-acp` or
`filesystem-snapshots` (both concurrently in flight per `TASKS/INDEX.md`) have claimed `135`
first.

## Escalations logged during this planning pass

See `TASKS/ESCALATIONS.md`'s corresponding 2026-08-21 entry for the two stale-doc corrections
above, and for `06`'s and `07`'s genuine design-latitude notes (each flagged in its own task
file, not held as an open escalation — the operator's nine settled decisions already resolve
the shape; what's left is normal "how," not "whether").
