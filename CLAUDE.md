# NANITE — Chat Harness

Agent-agnostic multi-agent chat harness for Fragments Engine.

## First-time setup in a fresh clone

```bash
lefthook install     # REQUIRED — installs the git hooks in .git/hooks
```

`lefthook.yml` is tracked, but a tracked config **installs nothing**. Until
`lefthook install` has run in your clone, `find .git/hooks -type f ! -name '*.sample'`
returns zero files and every pre-commit/pre-push check silently does not exist.
Verify with that command; expect `pre-commit` and `pre-push` afterwards.

If it errors with `core.hooksPath is set locally`, either
`lefthook install --reset-hooks-path` (unsets it) or `lefthook install --force`
(installs into the configured dir and leaves the setting alone).

## Build & Test

```bash
# Backend — compile check only (does NOT deploy)
go build ./cmd/nanite/
go test ./...

# Frontend
cd ui && npm install && npm run build
```

**What the hooks gate.** Commit time is formatting only, and every pre-commit
command is scoped to the staged diff — nothing there can fail for a reason
outside the change in front of you. pre-commit: `go-format` (gofmt + goimports,
staged files), `migration-purity` (no `VALUES` clause in
`internal/store/migrations/*.sql` — seed rows belong in `seed.go`; `UPDATE`/
`DELETE` backfills and `INSERT ... SELECT` table rebuilds are allowed),
`frontend-lint` (biome, `skip: true` — `CW-20260816-0087`). Measured at
`9591c1a6` across three real one-`.go`-file commits: **0.17s / 0.06s / 0.06s**
total, read off lefthook's own summary.

**pre-push:** `go test ./...` on **every push to `main`**. The branch is the
only thing that scopes it (`only: - ref: main`, and deliberately no `glob`), so
the suite runs whatever the push contains — a `go.mod`-only, `go.sum`-only or
migration-`.sql`-only push included. A push from a WIP branch reports
`go-test (skip) by condition`, and that is the only skip there is. A file filter
here is a silent fail-open: `*.go` matches none of `go.mod`, `go.sum`, or the
non-Go inputs the binary embeds, so it drops a migration-only push while printing
`(skip) no matching push files`, which reads as a benign, correct skip. Migration
number collisions are this repo's one unrecoverable failure class, so that is the
worst thing to skip quietly. Measured at `9591c1a6`:
**41.34s** with the test cache cleared (`go clean -testcache && /usr/bin/time -p
go test ./...`), **5.06s / 4.59s** on two back-to-back cached runs (99/99
cached). This is the **no-`-race`** suite — not Tier 3.

**The landing check: `./scripts/check.sh`.** Whole-repo analysis lives here, not
on a hook: gofmt/goimports over every Go file, `go vet ./...`, `golangci-lint`
scoped to what your work added since the merge base with `origin/main`, and
`go test ./...` (Tier 1 of
`docs/engineering/testing-workflow.md` §3). No arguments; it names every stage
that failed. Measured at `9591c1a6`: **67.36s** with the golangci-lint and test
caches both cleared, **8.75s** fully warm.

Run it **when a feature lands**, before pushing a branch you care about, and
before dispatching the full-repo quality gate — *not* on every commit. If your
change touched goroutines, channels, `context` cancellation, mutexes, atomics,
or shutdown ordering, it is not enough on its own: run Tier 2 (`-race -count=20`
on the package you touched) as well. Tier 3 (full suite under `-race`, ~9 min)
belongs to the nightly quality gate, and Tier 4 flake hunting is deliberate and
manual; neither runs from a hook or from this script.

**Deploying changes:** Always use Cerberus. Direct `go build` outputs to `./nanite` in the project root, but the running service uses the artifact at `~/.cerberus/apps/nanite/nanite-api-service/bin/nanite-api-service`. These are **separate binaries** — editing one does not affect the other.

The Cerberus resource id is `nanite-api-service` (NOT `nanite-api`).

```bash
# Cutover recipe for a code change: build + sync the artifact, THEN
# restart launchd so the new bytes actually run. Deploy alone may
# return "launchd unchanged" and leave the prior pid running on the
# old artifact — reload is the explicit cutover step.
cerberus_resource_deploy nanite-api-service
cerberus_resource_reload nanite-api-service

# Restart without rebuilding (config change, MCP catalog refresh, etc.):
cerberus_resource_reload nanite-api-service

# Verify deployment — check status, then logs:
cerberus_resource_status nanite-api-service
cerberus_resource_logs nanite-api-service --lines 50 --stream stderr
```

After `reload`, `cerberus_resource_status` should show a new `launchd_pid` and `last exit code = 0` for the prior process. If the pid hasn't changed, the cutover didn't happen — re-run `reload`.

## Verification discipline (read before executing a dispatched task)

`docs/engineering/agent-verification-discipline.md` holds the execution rules every dispatched agent follows in full — derive numbers at the moment of use and ship the command beside each one, re-derive cited line numbers before editing, do the dispatched task and nothing else. Reference it from dispatch prompts rather than copying it inline; append newly found environment hazards to its §3.

Companions: `docs/engineering/failure-modes.md` (why measurements and documents mislead) and `docs/engineering/testing-workflow.md` (test tiers, and `-race` vs. high `-count`).

## Architecture

- `cmd/nanite/` — Entry point
- `internal/brand/` — App identity constants (single source of truth for rebranding)
- `internal/api/` — HTTP API handlers
- `internal/chat/` — Chat engine (orchestration, context, delegation)
- `internal/mcp/` — MCP client integration
- `internal/provider/` — LLM provider abstractions (Anthropic, OpenAI, Ollama, PTY bridge)
- `internal/store/` — SQLite persistence layer
- `internal/config/` — Config loader (user + project merge)
- `ui/src/` — React frontend
- `config/agents/` — Agent profile definitions
- `docs/` — Architecture docs

## Slot system invariants (read before touching the Context Broker)

The slot system's six load-bearing invariants — stable sent shape, universal slot at position 0, cache marker priority, mode-aware content swap, pointer/stash determinism, permission visibility — are documented in `internal/context/INVARIANTS.md` and enforced by `internal/service/slot_invariants_test.go`. Update both in lock-step if a future ticket needs to change one.

## Agent launching (Phase 2 — boot-profile catalog retired)

The boot-profile catalog (a shared YAML directory that surfaced extra provider/model dropdown rows and spun up headless CLI sessions against a compiled boot prompt) is retired in full — `TASKS/phase-2/04-retire-boot-profile-catalog.md`. It never competed with agent construction; it only ever overrode prompt content, with a real `agents` row still resolved underneath. Two pieces carry forward as first-class, DB-configurable mechanisms available to every agent, not gated behind a separate catalog:

- **Dynamic context resolvers** (`agent_context_resolvers` table, `internal/runtime/agent/context_resolver.go`) — configure a `cmd`- or `http`-kind resolver per agent to fetch live data at launch time and fold it into the assembled boot context. CRUD lives at `GET/POST/PATCH/DELETE /api/agents/{id}/context-resolvers`. See `docs/engineering/architecture/02-agent-launching.md`.
- **Mandatory post-compaction re-read** — every CLI-based agent's planted boot content unconditionally instructs it to re-read the project's real `CLAUDE.md`/`AGENTS.md` after a Claude Code compaction event (`internal/runtime/agent/prompt.go`'s `mandatoryPostCompactionRereadInstruction`). Not a per-agent opt-in.

Nothing else carries forward — "lineage" (`LineageAlias`/`LineageID`) is dropped entirely, and cross-app portability (Tether/Torque interop via a shared boot-profile file format) is deliberately opt-in via MCP, not a structural default.

## Envelope System (critical — read before touching)

Envelopes are structured UI cards injected into chat messages. The system has two sides kept in sync by a shared manifest:

- **Manifest** (source of truth): the external `github.com/hollis-labs/go-envelopes` module's embedded `manifest/envelopes.yaml` — defines core envelope types. It is no longer vendored as `config/envelopes.yaml`.
- **Schema**: `manifest/envelopes.schema.json` inside the `go-envelopes` module — validates manifest structure
- **Backend** (Go): `internal/chat/envelope.go` — types loaded from the `go-envelopes` module at startup via `envelopes.LoadCore` → `chat.InitCoreTypes()`
- **Frontend** (TypeScript): `ui/src/generated/plugin-envelopes.ts` — fully generated by `scripts/generate-plugin-imports.mjs` from the `go-envelopes` manifest, with host-side `CORE_OVERRIDES` for frontend-only mappings
- **Plugin types**: declared in `plugins/*/plugin.yaml` under `registers.envelopes`

**When adding a new core envelope type:**
1. Add an entry to the `go-envelopes` module's `manifest/envelopes.yaml` (type + component + export), or — for a host-only frontend mapping — add a `CORE_OVERRIDES` entry in `scripts/generate-plugin-imports.mjs`
2. Create the React component in `ui/src/components/chat/envelopes/`
3. Run `npm run generate:plugins` (or it runs automatically on build/dev)
4. Verify the `data` shape the backend sends matches what the component expects
5. Test both the streaming path (SSE deltas) and the persisted path (page reload)

**Staleness check — manual, not enforced.** `node scripts/generate-plugin-imports.mjs --check` validates that the generated file is up to date (`scripts/generate-plugin-imports.mjs:25`). **Nothing runs it automatically.** `grep -rn "generate-plugin-imports" .github/` returns no matches — no workflow invokes it, and the pre-commit hooks do not either. The only automatic path is regeneration, not validation: `ui/package.json:17-18` runs the script in write mode from `prebuild`/`predev`, so `npm run build` and `npm run dev` silently rewrite the file rather than failing on drift. Run the `--check` form yourself if you need the assertion. (verified-at 2a4cdf0f)

**Known envelope types** (as of 2026-04-05):
- Core primitives: `session-task`, `document-viewer`, `report-card`, `error-report`, `approval-card`, `proposal-card`
- Core primitives (Phase 7): `info-card`, `list-card`, `metric-card`, `progress-card`, `confirmation-card`, `table-card`, `timeline-card`, `diff-card`

(The giphy self-tool/plugin and its `giphy-modal` type were cut in full — `TASKS/phase-0/15a-cut-giphy.md`. The oembed plugin was cut in full — `TASKS/phase-0/15b-cut-oembed.md`. The support-ticket plugin and its `kb-result`/`ticket-form`/`ticket-confirmation`/`resolution-capture` types were cut in full — `TASKS/phase-0/15c-cut-support-ticket.md`.)

<!-- nanite:start -->
<!-- DO NOT EDIT — managed by the nanite framework. This section will be regenerated by `nanite-agent init --project .`. -->

## Nanite Agents

Agent configuration for this project lives in `.nanite/config.yaml`. Roles load from `~/.nanite/roles/`, skills from `~/.nanite/skills/`, and per-agent project context from `.nanite/agents/`.

**Available agents:**

- `nanite-backend` — Go service development for the Nanite chat harness
- `nanite-frontend` — React/TypeScript UI development for the chat interface
- `nanite-plugin-dev` — Develop, audit, and maintain Nanite plugins and the plugin scaffold
- `nanite-planner` — Strategic planning, scoping, and architecture decisions
- `nanite-reviewer` — Code review for Nanite PRs

**Boot a specific agent:** say `Boot nanite-plugin-dev` (or any agent above). The boot process reads `.nanite/config.yaml`, loads the listed roles and skills, and reads the per-agent context file.

**Session state** (if present) lives in `.nanite/boot-prompt.md` — read it first when starting a new session.

## Tool result cache (Phase 3 S4a)

When a tool result exceeds 64 KiB, the full body is cached and the LLM sees a truncated view with a `tool_result://<id>` pointer. Two meta-tools are always available:

- `fetch_tool_result({id, offset?, length?})` — retrieve a byte slice.
- `search_tool_result({id, pattern, max_matches?})` — regex search with context.

These meta-tools are exempt from the per-tool call cap.

## MCP trust tiers (Phase 3 S4b)

Every MCP server is classified into a trust tier (`builtin`, `plugin_stdio`, `plugin_http`, `third_party_http`) and each tier carries its own per-result size ceiling — 2 MiB, 512 KiB, 256 KiB, 128 KiB respectively. Results over the ceiling are rejected at the boundary; they never reach the LLM. The S4a cache-and-pointer pattern still applies: it caches whatever made it through the tier ceiling. Expect third-party results to truncate earlier than built-in results. Text blocks are ANSI-stripped unconditionally, so terminal escape sequences won't appear in tool output. See `docs/mcp-trust-model.md` for the full tier table and validator architecture.

## Tool naming convention (Phase 5 D-naming)

Tool names use `<concept>_<verb>` with a provider prefix only when a real collision exists.
See `docs/tool-naming-convention.md` for the full rule set and `docs/tool-naming-audit.md`
for the per-tool verdict table. The `nanite_*` namespace is reserved for first-party
self-tools — do not use it for MCP-origin tools.

<!-- nanite:end -->
