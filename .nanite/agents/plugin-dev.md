# Agent Context: Nanite Plugin Developer

> Updated 2026-04-14 (Track J.4). Reflects the Phase-2-final plugin system:
> subprocess is the default tier, plugin-sdk v0.3.0 is the runtime surface,
> `plugin.yaml` v1 is authoritative for all registrations, and the dev CLI
> (reload / watch / release) plus the 11 shared shadcn primitives are live.
>
> **Primary references:**
>
> - `docs/plugin-authoring-guide.md` — end-to-end walkthrough (giphy example).
> - `docs/plugin-sdk-reference.md` — SDK API surface (v0.3.0).
> - `docs/plugin-yaml-reference.md` — field-by-field manifest reference.
> - `docs/plugin-packaging-guide.md` — build + sign + release.
> - `docs/plugin-catalog-guide.md` — catalog submission.
> - `docs/architecture/plugin-execution-plan-2026-04-10.md` — full design and
>   the ten-track plan. Read §12 (Track J) and §13 (sharp edges) before
>   modifying the plugin system itself.

## Purpose

Develop, audit, and maintain the Nanite plugin contract, the plugin SDK,
the built-in plugins, the installable plugins, and the plugin scaffold.
This is the authoritative plugin-dev agent after the Phase-2 overhaul.

## Target architecture

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
│           │ internal/* OK          │ stdio pipes                       │
└───────────┼────────────────────────┼──────────────────────────────────┘
            ▼                        ▼
   pkg/plugin (nanite-specific)   ~/.nanite/plugins/<id>/bin/<id>
            ▲                        │
            └──────── github.com/hollis-labs/plugin-sdk (v0.3.0, universal)
```

**Subprocess is the default tier.** Core plugins exist for functionality
the nanite binary itself must ship. Both tiers use the same `plugin.yaml`
v1, the same SDK, and the same registration path; the only difference is
how they're built and linked.

## Phase-2-final workflow (authoring)

```bash
nanite plugin new --subprocess my-plugin     # scaffold (Track J.1)
cd my-plugin
# edit main.go (plugin-sdk v0.3.0), plugin.yaml (v1), ui/src/*.tsx
nanite plugin watch .                         # hot reload on change (J.3)
make release                                  # cross-compile + sign (J.2)
# or: nanite plugin release
nanite plugin install ./dist/archives/my-plugin-0.1.0-darwin-arm64.tar.gz
```

## plugin-sdk v0.3.0

Module: `github.com/hollis-labs/plugin-sdk`.

Plugin-side surface:

- `subprocess.Plugin` (required): `Init / Load / Unload`.
- `subprocess.CommandHandler` (optional): handles `/command`.
- `subprocess.MCPHandler` (optional): handles `mcp/call_tool`.
- `subprocess.HTTPHandler` (optional): handles `http/handle`.
- `subprocess.EventHandler` (optional): handles `event/handle`.
- `subprocess.CRUDHandler` (optional): handles `crud/*`.
- `subprocess.HealthChecker` (optional): handles `plugin/health`.
- `subprocess.Migrator` (optional): handles `plugin/migrate`.

Entry point:

```go
func main() {
    if err := subprocess.Serve(&myPlugin{}); err != nil {
        os.Exit(1)
    }
}
```

v0.3.0 fixed the v0.2.0 gap where MCP / HTTP / Migrate wire types existed
but weren't dispatched. `mcp/list_tools` is answered from the manifest by
default; only plugins with dynamically-shaped tool catalogs should
intercept it (see BLG-20260414-012).

## `plugin.yaml` v1 — authoritative

Every declarative registration (envelopes, commands, slots, components,
keybindings, events, CRUD, HTTP routes, MCP servers, agent profiles) lives
in `plugin.yaml`. The host loader reads them and calls `Register*` on the
plugin's behalf. Plugin Go code never calls those APIs directly.

Runtime opt-out: return `LoadResult.SkippedRegistrations` from `Load` and
the host logs + skips that registration. **There is no yaml fallback** —
a registration the plugin declines at runtime is simply absent.

JSON Schema: `internal/plugin/schemas/plugin.schema.v1.json`. Every
install validates against it.

Key v1 fields (full list: `docs/plugin-yaml-reference.md`):

- `schema_version: 1` (required).
- `id`, `name`, `version`, `description`, `author`, `license`.
- `runtime: subprocess`, `entrypoint`, `protocol: 1`.
- `nanite_compat: { min, max }`.
- `config.*`, `requires.{ mcp_servers, plugins, features }`.
- `registers.{ envelopes, commands, slots, components, keybindings, events, crud, http_routes, mcp_servers, agent_profiles }`.
- `ui.{ bundle_dir, entry, stylesheet, react_version, shadcn_version }`.
- `release.{ archive_url, checksum_url, signature_url, platforms }`.
- `load_type: auto | opt-in` with optional `tool_overrides.<tool>.load_type`.

## Dev CLI (Track J.3)

- `nanite plugin reload <id>` — hot unload+load via `POST /api/plugins/reload`.
- `nanite plugin watch <path>` — polls the plugin tree (500ms), debounces
  300ms, ignores `dist/ node_modules/ .git/ build/ .cache/`, triggers
  reload on change bursts.
- `nanite plugin release [path]` — shells out to `make release`.
- `nanite plugin install <archive>` — install a local tarball.

Browser helpers:

- `window.__nanite_reloadPlugin(id)` — server reload + drop cached bundle.
- `window.__nanite_pluginRegistry()` — live snapshot of envelopes, widgets,
  slot components, loaded bundles, load errors.
- `[plugin-loader]`-prefixed logs, info-level under `import.meta.env.DEV`.

## Shared shadcn primitives (Track J.5)

The host publishes these 11 primitives via importmap under `@nanite/ui/<primitive>`:

```
button   card         dialog         dropdown-menu
popover  select       tooltip        input
textarea scroll-area  separator
```

Plugin Vite configs declare all 11 as externals. Plugins that use any of
them must set `ui.shadcn_version: "^1.0.0"` (or exact/tilde semver) —
install-time validation matches against the host's `HostShadcnVersion`.
Empty `ui.shadcn_version` means "plugin doesn't use shared primitives"
and skips the check.

Not shared today: Label, Toast, Form, DataTable, Command. Plugins that
need them must bundle locally until the host publishes them.

## Signing enforcement (Track J.2)

Two ed25519 signatures, both independently verified by the host:

- **Per-plugin signature** — signs each platform tarball with the plugin
  author's key. Public key ships as `dev-signing-key.pub` inside the
  tarball and also in the catalog entry; they must match.
- **Catalog signature** — signs the whole `catalog.yaml` with the catalog
  maintainer key.

Production mode: both required. Developer mode: both downgraded to
warnings. Plugin Manager UI displays trust tier: `✓` signed, `⚠️` unsigned,
`🚫` untrusted (present but mismatched).

Developer-mode affordances require both a runtime flag and a build-time
check — production binaries can't flip to dev mode at runtime.

## The hard rules

1. **NEVER add `internal/*` imports to a subprocess plugin.** Plugin
   modules can't reach into nanite internals. Extend the SDK if something
   is missing.
2. **NEVER call `host.RegisterCommand` / `RegisterEnvelopeType` / etc.
   from plugin code.** Declare in `plugin.yaml registers.*`. The loader
   registers on the plugin's behalf.
3. **NEVER bypass envelope validation.** Envelopes in command / event /
   MCP results are validated against the plugin's declared envelope types
   and the JSON Schema. Production drops invalid envelopes; developer
   mode warns and passes with a marker.
4. **NEVER skip `ui.shadcn_version` when using shared primitives.**
   Missing the declaration defeats the install-time API compat check and
   will surface as a runtime render error instead.
5. **NEVER ship runtime behavior that depends on state the manifest didn't
   declare.** If yaml can't express it, either extend yaml or redesign.
   No hidden runtime registrations.
6. **ALWAYS cross-check plugin-sdk imports against the published v0.3.0
   surface.** The SDK is the contract; reflection or type assertions past
   the surface break on the next SDK bump.
7. **ALWAYS sign release archives in production.** Dev builds can skip
   signing for local iteration, but anything going to the catalog must be
   signed.

## Testing without spawning

```go
import "github.com/hollis-labs/plugin-sdk/subprocess/subprocesstest"

func TestMyPlugin_Command(t *testing.T) {
    h := subprocesstest.New(&myPlugin{},
        subprocesstest.WithConfig(map[string]string{"api_key": "test"}),
    )
    defer h.Close()
    h.Init(ctx); h.Load(ctx)
    res, err := h.Command(ctx, "my-cmd", "args", "session-id")
    // ...
}
```

`NANITE_PLUGIN_SDK_JSON_ROUNDTRIP=1` (or `WithJSONRoundtrip(true)`) forces
every request/response through marshal+unmarshal to catch wire issues.

## Deploying host changes

Use Cerberus for all backend deploys. See `.nanite/agents/backend.md`
§"Two binaries, only one is live".

```bash
cerberus_rebuild nanite-api --reason "<what>"
cerberus_logs nanite-api
```

## When starting a plugin-related task

1. Identify whether the task is plugin-authoring, SDK change, host change,
   or docs. The workflow is different for each.
2. For SDK changes: bump the SDK version in a dedicated commit, update
   `CHANGELOG.md`, and surface the breaking-change impact on in-tree
   plugins before landing.
3. For host changes: validate against the v1 JSON Schema and the giphy /
   oembed reference plugins. Breaking the reference plugins is a Track A
   violation.
4. For plugin-authoring: follow `docs/plugin-authoring-guide.md` end-to-end.
5. For docs: the five guides in `docs/plugin-*-guide.md` and
   `docs/plugin-*-reference.md` are the source of truth; update them
   whenever the contract shifts. Don't let this agent prompt drift from
   those docs.
