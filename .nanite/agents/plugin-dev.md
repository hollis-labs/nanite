# Agent Context: Nanite Plugin Developer

> Updated 2026-04-10 (second pass, post-finding-#7). Prior versions of this file were written before the session discovered the parallel extraction state, so they pointed at wrong paths and assumed the wrong architecture. This is the authoritative plugin-dev context.
>
> **Primary reference:** `docs/architecture/plugin-execution-plan-2026-04-10.md` — the ten-track execution plan. Always read this before starting any plugin work.

## Purpose

Develop, audit, and maintain the Nanite plugin contract, the plugin SDK, the built-in plugins, the installable plugins, and the plugin scaffold. Nanite is being overhauled to a true subprocess plugin architecture; this agent drives that work.

## ⚠ Do this first, every time

**Read `docs/architecture/plugin-execution-plan-2026-04-10.md` §0 and §13 before touching any code.** §0 explains the drift state that bites every session; §13 is the consolidated sharp-edges list. Skipping this costs hours.

**If the execution plan's Track A has not been completed yet, execute Track A first and nothing else.** Track A is ~30–60 minutes of mechanical cleanup that unblocks every other track. Prior sessions failed because they tried to start in the middle while stale copies of plugin code were still around. Do not start Tracks B/C/D/E/F/G/H/I/J before Track A's gate passes. Don't batch Track A with other work — it deserves its own clean session.

After Track A: Tracks B, C, and F can run in parallel. See §17 of the execution plan for suggested session splits.

## Target architecture (post-execution)

```
┌──────────────────────────────────────────────────────────────────────┐
│                          Nanite Binary                                │
│                                                                      │
│  ┌─────────────────┐      ┌─────────────────┐                        │
│  │ Core plugins    │      │ Subprocess       │                        │
│  │ (compiled in)   │      │ plugin host      │                        │
│  │                 │◀────▶│ + JSON-RPC 2.0   │                        │
│  │ yaml loader     │      │ manager          │                        │
│  └────────┬────────┘      └────────┬─────────┘                        │
│           │                        │                                  │
│           │ internal/*             │ stdio pipes                       │
│           │ imports OK             │                                  │
└───────────┼────────────────────────┼──────────────────────────────────┘
            │                        │
            ▼                        ▼
   github.com/hollis-labs/   ~/.nanite/plugins/<id>/bin/<id>
   nanite/pkg/plugin         (spawned subprocess, talks JSON-RPC via stdio)
   (Nanite-specific types)          │
            ▲                        │ imports only
            │                        ▼
            └──────── github.com/hollis-labs/plugin-sdk
                      (universal Plugin SDK, external module)
```

**Two tiers:**

- **Core plugins** — compiled into the nanite binary. Can import `internal/*`. Live in their own GitHub repos (`nanite-plugin-<name>`) and are linked into nanite at build time via `go.work` or `replace` directives. Same yaml-authoritative loader as subprocess plugins. Cannot be uninstalled.
- **Subprocess plugins** — distributed as prebuilt per-platform archives. Spawned as separate processes. Talk to host over JSON-RPC 2.0 on stdin/stdout. ZERO `internal/*` imports — the Go type system enforces it. Installable via the Plugin Manager UI. Hot-loadable (no nanite rebuild required for users).

**Both tiers use the same SDK, the same plugin.yaml schema, the same registration path.** The only difference is how they're physically built and shipped.

## Key Paths

### Authoritative docs (read these)

| Path | Purpose |
|---|---|
| `docs/architecture/plugin-execution-plan-2026-04-10.md` | **THE execution plan.** Ten tracks with file:line pointers. Start here. |
| `docs/architecture/plugin-envelope-emission-findings-2026-04-10.md` | Raw envelope-emission gap findings from the audit. |
| `docs/architecture/plugin-audit-2026-04-10.md` | Superseded fix plan, kept for §1–§3 context only (banner at top points at the execution plan). |
| `docs/architecture/plugin-system.md` | Original architecture doc. Partially stale; cross-check. |
| `docs/architecture/plugin-evolution-plan.md` | Phase 1–8 plan, several status claims are stale. |
| `docs/plugin-extraction-plan.md` | Phase 1/3/6/7 done; rest replaced by execution plan. |

### Target module layout (after execution)

| Path | Module | Role |
|---|---|---|
| `github.com/hollis-labs/plugin-sdk` | NEW external repo | Universal SDK: `Plugin`, `Host`, `Error`, `EnvelopeOut`, subprocess server library, testing harness. Zero Nanite dependencies. |
| `github.com/hollis-labs/nanite/pkg/plugin` | NEW public package inside nanite | Nanite-specific SDK types: slot constants, `SlashCommandDef`, `KeybindingDef`, envelope/MCP/HTTP/agent registration types. Importable by plugins. |
| `github.com/hollis-labs/nanite/internal/plugin` | existing (reworked) | Host implementation, loader, concrete `*Host`, registration bridge. Imports `plugin-sdk/subprocess/protocol` for wire types. |
| `github.com/hollis-labs/nanite/internal/plugin/subprocess` | existing (reworked) | Host-side subprocess manager, wraps the plugin-sdk wire protocol. |
| `github.com/hollis-labs/nanite-plugin-<name>` | NEW repos, one per plugin | Individual plugin repos. Own `go.mod`, own release cycle, own signing key. |
| `github.com/hollis-labs/nanite-plugins-catalog` | NEW repo | Catalog build pipeline source + signing keys. Pushes to Cloudflare Pages. |

### Modules to DELETE (after execution)

| Path | Reason |
|---|---|
| `github.com/hollis-labs/plugin` (at `/Users/chrispian/Projects-apps/plugin/`) | Absorbed into plugin-sdk. Deleted in Track I. |

### Hosting infrastructure

| Path | Purpose |
|---|---|
| `plugins.nanite.hollis-labs.dev` (Cloudflare Pages) | Catalog hosting: `catalog.yaml` + `catalog.yaml.sig` |
| `archives.nanite.hollis-labs.dev` (Cloudflare R2) | Release archive hosting: `plugins/<id>/<version>/<id>-<version>-<platform>.tar.gz` |

### User filesystem layout

```
~/.nanite/
├── plugins/<id>/                  read-only after install
│   ├── plugin.yaml
│   ├── bin/<id>                   prebuilt subprocess binary
│   ├── ui/dist/                   prebuilt ESM bundle + CSS
│   ├── envelopes/*.schema.json
│   └── .meta/install.json         install metadata
├── plugin-data/<id>/              writable, survives updates, DataDir
├── plugin-cache/<id>/              writable, wipeable, CacheDir
├── plugin-staging/                 temp dir for downloads + atomic swap
└── plugin-catalog/
    ├── catalog.yaml
    └── catalog.yaml.sig
```

## Plugin Contract Summary

A plugin implements the minimal `plugin.Plugin` interface plus whichever optional capability interfaces it needs. The plugin-side SDK exposes:

```go
// Required
type Plugin interface {
    Init(ctx context.Context, params InitParams) (InitResult, error)
    Load(ctx context.Context) (LoadResult, error)
    Unload(ctx context.Context) error
}

// Optional — implement the ones you use
type CommandHandler  interface { Command(ctx, req) (CommandResult, error) }
type MCPHandler      interface { MCPCall(ctx, req) (MCPCallResult, error) }
type EventHandler    interface { EventHandle(ctx, req) (EventHandleResult, error) }
type HTTPHandler     interface { HTTPHandle(ctx, req) (HTTPResponse, error) }
type CRUDHandler     interface { Create/Read/Update/Delete/List }
type HealthChecker   interface { Health(ctx) error }
type Migrator        interface { Migrate(ctx, from, to string) error }
```

Plugin `main.go` calls `subprocess.Serve(&myPlugin{})` and that's it. The SDK handles stdin/stdout, JSON-RPC dispatch, concurrency, panic recovery, shutdown. Plugin author writes business logic only.

See execution plan §finding #5 (referenced in conversation history) and the giphy example in finding #3 for the full pattern.

## plugin.yaml is authoritative

After the Track B refactor, `plugin.yaml` is the single source of truth for all plugin registrations. The plugin's Go code does NOT call `host.RegisterCommand` or `chat.RegisterEnvelopeType` or `mgr.AddServer` — those registrations are declared in yaml and the loader makes them on the plugin's behalf.

Current state (pre-execution): the loader still parses only a subset of plugin.yaml (`name`, `version`, `config`, `dependencies`, `runtime`, `entrypoint`, etc.) and ignores `registers.envelopes`, `registers.commands`, `registers.slots`, `registers.keybindings`, `registers.mcp_servers`, `registers.http_routes`, `registers.agent_profiles`, `registers.events`, `registers.crud`. Track B adds parsing + registration for all of these.

The JSON Schema for v1 is at `internal/plugin/schemas/plugin.schema.v1.json` (created in Track B.2). Validate every plugin.yaml against it during Track B and beyond.

## The hard rules

1. **NEVER add `internal/*` imports to a subprocess plugin.** Plugin code is in its own Go module and cannot reach into nanite's internals. If a plugin needs something the SDK doesn't expose, flag it and extend the SDK. Core plugins (compiled-in) can still import `internal/*` but should minimize it.

2. **NEVER call `chat.RegisterEnvelopeType` directly from plugin code.** Declare in `plugin.yaml registers.envelopes` instead. The loader registers on the plugin's behalf.

3. **NEVER bypass the envelope validation.** Envelopes emitted via `CommandResult.Envelopes`, `EventHandleResult.Envelopes`, or `MCPCallResult.Envelopes` are validated against the plugin's declared envelope types AND the JSON Schema at emission time. No advisory passes, no "just log and continue." Production mode is strict; developer mode downgrades to warnings only.

4. **NEVER skip Track A.** Prior sessions have lost hours to drift issues that Track A eliminates in 30 minutes of cleanup. Track A is the single most important item in the plan and it is non-negotiable.

5. **NEVER treat fragments-engine as anything other than a deletion target.** The user has decided to remove it entirely; migration is explicitly out of scope. The deletion sweep is in Track A.3 and touches core code as well (emission sites in `internal/mcp/self_tools_transport.go`).

6. **NEVER ship a subprocess plugin with runtime behavior that depends on state the plugin.yaml didn't declare.** If the plugin needs to do something yaml can't express, either extend yaml or re-think the design. No hidden runtime registrations.

7. **ALWAYS cross-check plugin-sdk imports against plugin-sdk's public surface before writing plugin code.** The SDK is the contract; don't reach past it via reflection or type assertions that would break with a new SDK version.

## Currently shipping plugins (pre-execution state)

| Plugin | Canonical repo | Status | Notes |
|---|---|---|---|
| `giphy` | `hollis-labs/nanite-plugin-giphy` | Stale — needs rewrite | Track E. Currently the event-hook emission path is dead; `!giphy` works via core `internal/mcp/self_tools_transport.go`. Rewrite as a proper subprocess plugin with slash command + MCP tool. |
| `oembed` | `hollis-labs/nanite-plugin-oembed` | Non-functional — needs rewrite | Track H.1. No working emission path today. Rewrite as event-driven subprocess plugin that reacts to `message.sent` with URL detection. |
| `support-ticket` | `hollis-labs/support-ticket` (to rename) | Active, needs migration | Track H.2. The most complex plugin — CRUD + MCP KB server + HTTP download route + 4 envelope types + agent profile + 5 React components. Real commit history on the canonical repo. |
| `fragments-engine` | — | **DELETE** | Track A.3. No migration; full removal. |
| `agentrc-sync` (core) | `hollis-labs/nanite-plugin-agentrc` | Core, migrate to yaml loader | Track H.3. Stays compiled-in. |
| `agent-widgets` (core) | `hollis-labs/nanite-plugin-agent-widgets` | Core | Track H.3. |
| `context-widgets` (core) | `hollis-labs/nanite-plugin-context-widgets` | Core | Track H.3. |
| `debug-widgets` (core) | `hollis-labs/nanite-plugin-debug-widgets` | Core | Track H.3. |
| `observability-widgets` (core) | `hollis-labs/nanite-plugin-observability-widgets` | Core | Track H.3. |
| `bookmarks-widget` (core) | `hollis-labs/nanite-plugin-bookmarks` | Core | Track H.3. |
| `session-stats` | `hollis-labs/nanite-plugin-session-stats` | Needs decision (core or subprocess) | Track H.3. |

## Sharp edges (summary — full list in execution plan §13)

1. **Drift between three parallel plugin trees.** Delete stale copies in Track A.
2. **`Host.Shutdown()` deadlock** at `host.go:1170` — holds mutex across `Unload()` loop. Fix in Track A.2.
3. **`http.ServeMux` can't unregister routes** — use the mutable mux adapter pattern from §B.6.
4. **`EventHook` interface breaking change** — adding `PluginID() string` method. Every implementer must be updated.
5. **`os.Rename` atomicity** — staging dir must be on the same filesystem as plugins dir.
6. **Windows `os.Rename`** requires destination-not-exist; update flow needs prepare-delete-rename.
7. **SDK wire-type move** (Track C.3) is a coordinated flip; follow the exact order or compilation breaks.
8. **Envelope validation** becomes strict, not advisory. Plugin-sourced envelopes with unregistered types are dropped in production.
9. **React version coupling** — if nanite bumps React, plugins need to be rebuilt. Enforce via `ui.react_version` at install time.
10. **Fragments-engine removal** is not just a `rm -rf` — core code emits envelope types that fragments-engine claims to own. Sweep the emission sites in `internal/mcp/self_tools_transport.go`.

## Development workflow

### Starting a new subprocess plugin

After Track J completes:

```bash
nanite plugin new my-plugin --subprocess
cd my-plugin
make release          # cross-compiles + builds UI + signs
nanite plugin install ./dist/my-plugin-0.1.0-darwin-arm64.tar.gz
```

Before Track J: scaffold template is broken (Track A.2 fixes imports as a stopgap; Track J.1 does the full rewrite). Hand-author plugin.yaml and main.go until the scaffold is current.

### Testing a plugin without spawning a subprocess

```go
import "github.com/hollis-labs/plugin-sdk/subprocess/subprocesstest"

func TestMyPlugin_Command(t *testing.T) {
    h := subprocesstest.New(&myPlugin{},
        subprocesstest.WithConfig(map[string]string{"api_key": "test"}),
    )
    defer h.Close()

    ctx := context.Background()
    h.Init(ctx)
    h.Load(ctx)

    result, err := h.Command(ctx, "my-cmd", "args", "session-id")
    require.NoError(t, err)
    require.Equal(t, "message", result.Action)
    require.Len(t, result.Envelopes, 1)
}
```

The harness calls plugin methods directly with mocked InitParams, no JSON-RPC. For wire-format validation, set `NANITE_PLUGIN_SDK_JSON_ROUNDTRIP=1` (or use `WithJSONRoundtrip(true)`) which runs every request/response through JSON marshal+unmarshal to catch wire issues.

### Deploying host changes

Use Cerberus for all backend deploys. See `.nanite/agents/backend.md` §"Two binaries, only one is live" — never rely on raw `go build` for the live service.

```bash
cerberus_rebuild nanite-api --reason "plugin loader yaml-authoritative refactor"
cerberus_logs nanite-api
```

## Conventions

- **Every plugin ships** its own `plugin.yaml`, `README.md`, `LICENSE`, `CHANGELOG.md`, and tests. The scaffold enforces this.
- **Plugin config resolution** order (handled by the SDK, not the plugin): `plugin_settings` DB → environment variable → `plugin.yaml default` → Go zero value.
- **Agent seeding** happens at the loader level from yaml (`registers.agent_profiles`), not by plugin code calling `store.CreateAgent`.
- **File-based agents** — per the architecture rule, agent profiles are file-based with yaml frontmatter. DB is runtime state only.
- **Envelope schemas** are required (not advisory) and must be shipped in the plugin's `envelopes/` directory.
- **Plugin-scoped CSS** uses CSS Modules and references nanite design tokens via `var(--nanite-*)` custom properties.
- **React components** are bundled against a React that's shared via importmap — plugins declare `react` as external in their Vite config.
- **Plugin Go code** imports only `github.com/hollis-labs/plugin-sdk` (and `github.com/hollis-labs/nanite/pkg/plugin` for Nanite-specific types, if needed).

## When starting a plugin-related task

1. **Read the execution plan section relevant to your task.** Every track has specific work items and gates.
2. **Check if Track A has been completed.** If not, stop and execute Track A first. Nothing else can start.
3. **Identify which track your work falls into.** If it's cross-track, check the dependency graph (§2 of the execution plan).
4. **Run the relevant track's gate checks after you finish.** Don't declare a track complete without running them.
5. **Flag any new findings immediately.** If you discover something the execution plan didn't anticipate, surface it before continuing. Capture as a new finding in the plan or a follow-up document.
6. **Do not add to the execution plan without surfacing the change.** The plan is stable; amendments are explicit.
