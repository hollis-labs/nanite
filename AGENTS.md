# AGENTS.md — Nanite

## What is this and why

Nanite is a CLI agent framework and plugin host: an agent runtime that boots
agent sessions into any project with the right context, executes directly
(tools, CLI, PTY, subprocess plugins) the way Claude Code does, and extends via
hot-loadable subprocess MCP plugins. It is an agent runtime, not just a chat
shell — out of the box a fast minimal chat experience, and with plugins a
programmable environment for AI-powered systems and interfaces. Nanite ships as
a Go backend with an embedded React + shadcn frontend, plus the `nanite` and
`nanite-agent` binaries. It is a single-user desktop app; any "enterprise
readiness" material elsewhere in the repo is speculative research only.

Nanite composes as a peer with Torque (durable/queued/scheduled work): Nanite
can submit to Torque for durable tasks, and Torque can spawn Nanite agents as
executors. Neither is subordinate to the other.

## Where to start

- `cmd/nanite/` — main entry point (the `nanite` binary; `nanite serve`)
- `cmd/nanite-agent/` — agent framework CLI (extracts roles/skills/commands via `nanite-agent init`)
- `cmd/nanite-eval/` — eval harness entry point
- `internal/brand/` — app identity constants (single source of truth for rebranding)
- `internal/api/` — HTTP API handlers
- `internal/chat/` — chat engine: orchestration, context, delegation, envelopes
- `internal/dispatch/` — three-role harness (Chat / Worker / Planner) + ScopeTier classifier
- `internal/mcp/`, `internal/toolclient/` — MCP client integration and tool-broker runtime adapter
- `internal/plugin/` — subprocess MCP plugin host (install state machine, catalog, signing)
- `internal/provider/` — LLM provider abstractions (Anthropic, OpenAI, Ollama, PTY bridge)
- `internal/store/` — SQLite persistence layer (also `nanite.db` migrations)
- `internal/context/` — Context Broker + slot system (read `internal/context/INVARIANTS.md` first)
- `ui/src/` — React + shadcn frontend
- `docs/` — architecture, plugin guides, ADRs; start at `docs/architecture/ARCHITECTURE.md`
- `adr/` and `docs/decisions/` — architectural decision records
- `README.md`, `CLAUDE.md` — project overview and build/deploy conventions

## Key domain concepts

- **Agent framework** — roles (`~/.nanite/roles/` domain/stack/meta), skills,
  commands, hooks; embedded in `nanite-agent`, extracted via `nanite-agent init`.
- **Plugin host** — subprocess MCP plugins with manifest v1, hot load/unload,
  install state machine, catalog fetch + Ed25519 signature verification, SSE
  lifecycle stream. Catalog: `plugins.nanite.hollislabs.dev/catalog.yaml`.
- **Plugin SDK** — published `plugin-sdk` module (yaml-authoritative, wire-type
  contracts) for Go plugin authors.
- **Three-role harness** — Chat / Worker / Planner roles, selected by a
  ScopeTier classifier; `nanite_execute_task` self-tool drives delegation.
- **Envelopes** — structured UI cards injected into chat messages; manifest is
  source of truth (`config/envelopes.yaml`), with Go + generated TypeScript
  sides kept in sync. See `CLAUDE.md` "Envelope System" and `docs/envelopes.md`.
- **Context Broker / slot system** — six load-bearing invariants documented in
  `internal/context/INVARIANTS.md`, enforced by `internal/service/slot_invariants_test.go`.
- **Tool broker** — Opencode-style MCP internalization; uniform tool registry;
  `<concept>_<verb>` naming with provider prefix only on real collision;
  `nanite_*` namespace reserved for first-party self-tools.
- **Boot profiles** — operator-registered catalog YAML that surfaces extra rows
  in the chat composer; selecting one spins up a headless CLI agent session.
- **Signing** — Ed25519 catalog + per-plugin signatures; the `devmode` build
  tag gates the dev bypass so production always verifies.

## Common operations

First time in a fresh clone — install the git hooks. `lefthook.yml` is tracked,
but a tracked config installs nothing; without this step none of the pre-commit
or pre-push checks exist.

```bash
lefthook install
find .git/hooks -type f ! -name '*.sample'   # verify: expect pre-commit, pre-push
```

A worktree of an already-installed clone is covered — `core.hooksPath` is an
absolute path into the parent clone's `.git/hooks`, which every worktree shares
(`git config --get core.hooksPath`).

**pre-commit is formatting only**, and every command is scoped to the staged
diff: `go-format` (gofmt + goimports), `migration-purity` (no `VALUES` clause in
`internal/store/migrations/*.sql`), and `frontend-lint` (biome, `skip: true` —
`CW-20260816-0087`). Nothing at commit time can fail for a reason outside the
change in front of you, which is what keeps `--no-verify` — all-or-nothing —
from being the natural escape.

**pre-push** runs `go test ./...` scoped to `main` (`only: - ref: main`), so a
WIP-branch push reports `go-test (skip) by condition`. It is the no-`-race`
suite, not Tier 3.

**Whole-repo analysis lives in `./scripts/check.sh`** — see "Test and lint"
below.

Build (production — no `devmode` tag, signature verification unconditional):

```bash
make build          # generate-envelopes + build-ui + go build -o nanite ./cmd/nanite
make build-dev      # adds devmode tag (signing bypass) — never ship this
make install        # go install ./cmd/nanite to ~/go/bin (used by MCP and Cerberus)
```

Run locally:

```bash
go build ./cmd/nanite/
./nanite serve -port 8090 -db ./nanite.db -dev
make run            # build then ./nanite serve --port 8090
```

Dev loop (two terminals): `air` for the backend, `cd ui && npm run dev` for the
frontend.

Test and lint:

```bash
./scripts/check.sh  # the landing check — run this when a feature lands
make test           # go test -race ./...
make lint           # go vet + golangci-lint + staticcheck + errcheck + govulncheck
make vuln           # govulncheck only
make eval           # interaction-quality eval suite (eval build tag)
```

`./scripts/check.sh` takes no arguments and runs four stages, naming every one
that failed: `format` (gofmt + goimports over every Go file), `vet`
(`go vet ./...`), `lint` (`golangci-lint` scoped to what your work added,
measured from the merge base with `main`), and `test` (`go test ./...` — Tier 1
of `docs/engineering/testing-workflow.md` §3). Run it when a feature lands,
before pushing a branch you care about, and before dispatching the full-repo
quality gate — not on every commit.

The lint stage is scoped on purpose: whole-repo `golangci-lint run` exits
non-zero on a large body of pre-existing findings, and that body is the nightly
gate's business, where it runs with `--issues-exit-code=0` against a ratcheting
baseline (`scripts/quality-ratchet.py`). Override the diff base with
`CHECK_LINT_BASE=<rev>`.

If your change touched goroutines, channels, `context` cancellation, mutexes,
atomics, or shutdown ordering, also run Tier 2 (`-race -count=20` on the package
you touched). Tier 3 (full suite under `-race`) is the nightly gate's; Tier 4
flake hunting is deliberate and manual. Neither belongs on a hook or in the
landing check.

Deploy via Cerberus (the running service uses a separate artifact from a local
`go build` — resource id is `nanite-api-service`, not `nanite-api`):

```bash
cerberus_resource_deploy nanite-api-service   # build + sync artifact
cerberus_resource_reload nanite-api-service   # explicit cutover — restart launchd
cerberus_resource_status nanite-api-service   # verify new launchd_pid
cerberus_resource_logs   nanite-api-service --lines 50 --stream stderr
```

Adding a core envelope type: edit `config/envelopes.yaml`, add the React
component under `ui/src/components/chat/envelopes/`, run `npm run generate:plugins`,
verify the `data` shape on both streaming (SSE) and persisted (reload) paths.

## Where to look for more

- Architecture: `docs/architecture/ARCHITECTURE.md` and the subsystem docs in
  `docs/architecture/` (plugin system, envelope pipeline, tool broker, classifier).
- ADRs: `adr/` (portfolio-level decisions, ADR-001…) and `docs/decisions/`
  (recent local ADRs — models-catalog sync, MCP internalization, broker).
- Plugin authoring: `docs/plugin-authoring-guide.md`, `docs/plugin-yaml-reference.md`,
  `docs/plugin-sdk-reference.md`, `docs/plugin-catalog-guide.md`.
- Roadmap / phase planning: no single `docs/roadmap.md`; see
  `docs/hardening-phase-plan.md`, `docs/post-mvp-plan.md`, and the
  `planning/nanite-release-prep/` tree. Live state truth lives in the Torque
  portfolio summary + sprints (via the `mux` MCP), not in repo files.
- Agent config for this repo: `.nanite/config.yaml` (agents `nanite-backend`,
  `nanite-frontend`, `nanite-plugin-dev`, `nanite-planner`, `nanite-reviewer`);
  boot one with e.g. `Boot nanite-plugin-dev`.
- Knowledge file: `~/dev/agent-os/knowledge/projects/nanite.md`.
