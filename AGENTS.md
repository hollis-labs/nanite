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
make test           # go test -race ./...
make lint           # go vet + golangci-lint + staticcheck + errcheck + govulncheck
make vuln           # govulncheck only
make eval           # interaction-quality eval suite (eval build tag)
```

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
