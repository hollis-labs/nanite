# Agent Host + ACP — implementation

Implements `docs/engineering/architecture/16-agent-host.md` (adopt `go-agent-wrapper` as
Nanite's shared agent-launch host, replacing bespoke code in `internal/runtime/agent`) and
`docs/engineering/architecture/17-acp.md` (add ACP-as-client support — Nanite driving other
agents' CLIs over the Agent Client Protocol — as a new capability layered on that host).
Treated as one project per operator direction: 17 depends structurally on 16 (every ACP
adapter is a new `go-agent-wrapper` `Adapter`/`RuntimeAdapter` implementation, landing in
`adapters/`, alongside `adapters/claude`/`adapters/codex`/`adapters/opencode`), and both
docs cross-reference each other throughout.

**Not part of `docs/engineering/TASKS.md`'s Phase 0-9 sequence.** A sibling to
`TASKS/reflex-taxonomy/`, `TASKS/harness-reactive-self-tools/`, `TASKS/scheduling/`, and
`TASKS/teams/` — kept in its own top-level `TASKS/` subfolder for the same reason those are:
this work originates from a dedicated architecture-review pass, not the original plan.

## Two repos, one batch

Unlike every prior batch in this folder, **most of Phase 1-2's work lands in a sibling repo**,
not Nanite itself: `/Users/chrispian/dev/hollis-labs/libs/go-agent-wrapper` (the host library
this batch adopts) and, for one narrow item, `/Users/chrispian/dev/hollis-labs/libs/agentkit`
(the library go-agent-wrapper wraps). This is not unprecedented — `TASKS/phase-0/13`/`14`
already made real edits to the sibling `libs/go-envelopes` mirror reached via a local `go.mod`
`replace` directive — but it's the load-bearing shape of this entire batch, not one task's
side effect. Every task below states explicitly which repo it lands in.

`go-agent-wrapper` has **zero adopters today** (confirmed by a full monorepo grep) — Nanite
is the first real consumer. There is no cross-app coordination risk *yet*, but if Tether or
Torque start consuming it concurrently with this batch's execution, the same
shared-mutable-mirror caution `TASKS/phase-0`'s `ESCALATIONS.md` entries documented for
`go-envelopes` applies here too — check `git log` in `libs/go-agent-wrapper` immediately
before merging any task in Phase 1.

## Read before starting any task here

1. `docs/engineering/architecture/16-agent-host.md` — the boundary a host owns vs. doesn't,
   the four-tier portfolio inventory, the audit findings on `go-agent-wrapper`, and the
   concrete gap list ("What's genuinely still open").
2. `docs/engineering/architecture/17-acp.md` — the ACP-as-server vs. ACP-as-client split
   (this batch is the client role only — Tether's server role, `internal/acpadapter`, is
   prior art and stays untouched), the client abstraction shape, the Protocol/Transport
   split, and the decision matrix left for planning.
3. **This batch's own planning-session research corrected several claims in both docs against
   the live code — read the specific task's Context before assuming the docs' prose is
   literally accurate.** In short, so you don't have to rediscover them:
   - Bumping `go-agent-wrapper`'s `agentkit` pin from `v0.1.0` to `v0.3.0` requires **zero
     code changes** — `agentsessions` (the only agentkit subpackage go-agent-wrapper
     imports) is byte-identical across those tags, and the renamed symbol 16-agent-host.md
     cites (`RenderFrontEnd`→`MissingPolicy`) lives in `agentlaunch`, which go-agent-wrapper
     never imports. Not a compat pass — a pure version-bump. See task `01`.
   - **Neither Claude nor Codex's `Stop()` today calls any native wire-level interrupt** —
     both go-agent-wrapper (via `agentkit/agentsessions`) and Nanite's own current bespoke
     code do stdin-close + SIGTERM/SIGKILL only. 17-acp.md's claim that Claude's Agent SDK
     exposes `Query.interrupt()` and Codex's `app-server` exposes `turn/interrupt` may be
     true of those SDKs' own documented surfaces, but neither is wired through today. Only
     OpenCode's `Stop()` calls a real native abort endpoint
     (`/global/dispose`, `/session/{id}/abort`). This is a **carried-forward limitation, not
     a regression** — Nanite's own `internal/service/agent_deps.go:772-776` already has a
     TODO acknowledging the exact same gap. See task `02`.
   - `internal/recovery/broker`'s coupling to `agentkit` is narrow, not deep: it only
     consumes `*agentsessions.ExitError`'s shape and five `Cause*` constants, entirely
     mediated through Nanite's own `agent.Options`/`agent.Session`/`agent.HasBootdirLayout`
     types — it never calls an agentkit method directly. See task `06`'s Context for the
     full call-site list; there is no separate standalone "audit broker" task because the
     audit is already done and its findings are the constraint `06` must preserve.
   - `internal/runtime/agent/context_resolver.go`'s `agent_context_resolvers` mechanism
     (DB-configurable, `Kind`-driven `cmd`/`http` resolver, CRUD already exists per-agent,
     resolved once at launch) is the established precedent task `11` follows for
     per-agent ACP Protocol/Transport selection — not a new mechanism shape.
4. `docs/engineering/GLOSSARY.md` — check before introducing any new name, per this repo's
   standing discipline.
5. `docs/engineering/EXECUTION-PROCESS.md` — the task-file format, worker/reviewer
   discipline, and escalation rules every task file below follows.

## What this batch does NOT do

- **Sequencing Tether/Torque's own adoption of `go-agent-wrapper`.** 16-agent-host.md
  explicitly leaves "whether it becomes the single host for Nanite+Tether+Torque together...
  and in what order" open for "the planner." This is Nanite's own `TASKS/` tracker — it plans
  and executes Nanite's adoption only. Whether/when Tether or Torque adopt the same library is
  a portfolio-level call made in those apps' own planning, not blocked by anything here.
- **Migrating `Mode`/lifecycle-decision logic out of Nanite.** `agent.Mode`
  (`LongLived`/`OneShot`/`Resume`/`Subagent`/`Background`) and every place it drives a
  decision (`agent.go:204-210,361,430-446,485-486,577-589`; `factory.go:72,114,133`) stay
  exactly where they are — this is product-owned lifecycle policy per both docs' own stated
  boundary, not something a host absorbs.
- **Attach/detach to a CLI process launched outside a Hollis app.** Explicit prior direction,
  restated in both docs' own "What's cut" / "Known limitations" sections.
- **Building an actual in-process fs/terminal server for ACP**, even if Phase 5's audit finds
  one is needed. That audit's job is to determine *whether* it's needed and flag the scope
  growth — building it, if required, is new scope for a follow-up planning pass, not
  something this batch silently absorbs.
- **Locking a final ACP bridge library for Claude/Codex/Pi ahead of time.** 17-acp.md is
  explicit that this is a deliberately open question given how fast the ecosystem is moving.
  `04/01` is escalation-gated — logged in `TASKS/ESCALATIONS.md`, not dispatched blind. See
  that task file's own banner.
- **A final, locked default for which Protocol/Transport an agent runs on.** Migration is
  additive per both docs — native adapters (`claude-stream-json`/`codex-app-server`/
  `opencode-native`) stay available in parallel with ACP; the move to ACP-as-default (if it
  ever happens) is a later, per-agent, opportunistic operator call, not this batch's job.

## Task sequence

Flat-numbered `01`-`17` across all five phases (same convention as `TASKS/teams/`), each
file's own header states its Phase.

**Phase 1 — Host foundation (`libs/go-agent-wrapper`, cross-repo).** Small, mechanical,
foundational. No schema/migration.

| Task | Depends on |
|---|---|
| `01-bump-agentkit-pin-and-cut-release.md` | none |
| `02-split-descriptor-protocol-transport-and-interrupt.md` | none directly (parallel-safe with `01` — disjoint files) |

**Phase 2 — Nanite host migration.** The real lift: retire Nanite's bespoke
`internal/runtime/agent` code onto `go-agent-wrapper`'s seams. Strict-ish order.

| Task | Depends on |
|---|---|
| `03-add-go-agent-wrapper-dependency.md` | none |
| `04-migrate-bootdir-layout-to-planter.md` | `02`, `03` |
| `05-migrate-sandbox-profile-to-applier.md` | `03` |
| `06-migrate-session-lifecycle-to-wrapper.md` | `02`, `04`, `05` |
| `07-dogfeed-validate-host-migration.md` | `04`, `05`, `06` |

**Phase 3 — ACP client abstraction & native adapters.** Sequenced after Phase 2's dogfeed —
not a hard technical blocker (the abstraction lives in `go-agent-wrapper` and doesn't need
Nanite's migration to compile), but every ACP adapter is a new `Adapter`/`RuntimeAdapter`
implementation and this batch validates the host they land in end-to-end first, for risk
reasons.

| Task | Depends on |
|---|---|
| `08-build-acp-client-abstraction.md` | `02`; recommended after `07` |
| `09-acp-native-adapter-opencode.md` | `08` |
| `10-acp-native-adapter-copilot-cli.md` | `08` |
| `11-nanite-per-agent-protocol-transport-config.md` | `09`, `10`, `07` |

**Phase 4 — ACP bridge adapters for non-native agents (Claude/Codex/Pi).** `12` is
escalation-gated — see `TASKS/ESCALATIONS.md`. Do not dispatch `13`-`15` until `12`'s decision
is recorded.

| Task | Depends on |
|---|---|
| `12-pin-acp-bridge-library.md` | `08` — **escalation-gated, see banner** |
| `13-acp-bridge-adapter-claude.md` | `12` |
| `14-acp-bridge-adapter-codex.md` | `12` |
| `15-acp-bridge-adapter-pi.md` | `12` |

**Phase 5 — Verification & hardening.**

| Task | Depends on |
|---|---|
| `16-audit-fs-terminal-proxying-requirement.md` | `09`, `10`, `13`, `14`, `15` (Phases 3-4 complete) |
| `17-native-vs-acp-side-by-side-comparison.md` | `07`, `09`, `10`, `13`, `14`, `15` |

## Migration numbering

Highest existing goose migration on disk at this planning session's authoring time
(2026-08-21) is `133_workflow_run_flex_waiting_status.sql`. This batch provisionally claims
`134` onward for anything with a real schema task (currently only `11`, if its
DB-configurable surface needs a new table/column rather than reusing an existing one — check
at dispatch time). Same real cross-batch collision risk every other batch in this folder has
hit — re-list the migrations directory immediately before landing any migration.

See `TASKS/INDEX.md`'s own new section for status tracking as these land.
