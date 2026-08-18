# Plugin System

## 1. Purpose

The plugin system lets code outside `internal/` extend Nanite along five axes — UI (envelope renderer components, dashboard widgets, right-rail panels, slot-mounted fragments, keybindings), tool surface (MCP servers), backend behavior (event hooks, CRUD resource handlers, HTTP routes, slash commands), and Stage-1 card-type detection rules — without a core code change for the common case. A plugin is either a Go struct compiled into the `nanite` binary (**builtin**) or a separate executable speaking JSON-RPC over stdio (**subprocess**). Both kinds describe *what they register* in a `plugin.yaml` manifest; a single host-side code path (`applyManifestRegistrations`) reads that manifest and performs the actual registration calls, so builtin and subprocess plugins are wired into the running system identically. On the frontend, a plugin's compiled JS bundle (if it has UI) is fetched and `import()`-ed at runtime from a host-served URL — there is no frontend build step that bakes plugin code into the Nanite UI bundle.

## 2. Key entry points/files

- `internal/plugin/loader.go` — `DiscoverPlugins` (scan a directory for `plugin.yaml`), `LoadDiscovered` (topological load by `dependencies`), `LoadRegisteredBuiltins` (load compiled-in builtins that have no on-disk `plugin.yaml`).
- `internal/plugin/config.go` — `PluginManifest` struct (the parsed `plugin.yaml` shape), `ParseManifest`, `PluginConfig`/`ConfigEntry` (the `config:` block resolution chain).
- `internal/plugin/host.go` — `Host`: the in-memory registries (envelopes, UI components, slots, keybindings, event hooks, CRUD handlers, connectors, services, task backends, providers, config, HTTP routes) and their `Register*`/`Unload`-time sweep logic (~1,700 lines; `UnloadPlugin` alone sweeps 16 categories).
- `internal/plugin/registrations.go` — `applyManifestRegistrations`: the yaml-authoritative pass that reads `manifest.Registers.*` and calls the matching `Host` methods; per-category `registerManifestCommands/Events/HTTPRoutes/MCPServers/CardRules/Panels` (subprocess-only — builtins register these directly from `Load()`).
- `internal/plugin/schemas/plugin.schema.v1.json` — the JSON Schema `plugin.yaml` v1 manifests are validated against at install time.
- `internal/plugin/install/` — `validate.go` (`ValidateManifest`, cross-ref checks — bundle asset existence, `shadcn_version`, `platforms`), `install.go` (the `Installer` state machine: verify signature → extract → validate → stage → commit).
- `internal/plugin/subprocess/` — `plugin.go` (`SubprocessPlugin`, `plugin-sdk` `Plugin` implementation backed by a live process), `protocol.go` (JSON-RPC method names/request-response shapes).
- `internal/plugin/manage.go` — `DisablePlugin`/`EnablePlugin` (rename `plugin.yaml` ↔ `plugin.yaml.disabled`), `PluginStatus`.
- `internal/plugin/allplugins/allplugins.go` — blank-import list wiring every compiled-in builtin's `init()` (which calls `RegisterPlugin(id, constructor)`) into the binary.
- `internal/plugin/builtin/*/` — the 12 compiled-in builtins (`adapter-claude`, `adapter-codex`, `adapter-gemini`, `adapter-nanite-native`, `adapter-opencode`, `agentwidgets`, `bookmarks`, `card-rules-demo`, `contextwidgets`, `debugwidgets`, `observabilitywidgets`, `sessionstats`).
- `internal/api/plugins.go` — plugin management REST routes: `GET /api/plugins/managed`, `POST /api/plugins/install(-local|-archive)`, `POST /api/plugins/uninstall`, `POST /api/plugins/disable`, and the `GET /api/plugins/{name}/ui/{file...}` static-bundle server.
- `internal/api/plugins_registry.go` — `GET /api/plugins/registry`: aggregates the host's live envelope/widget/slot/plugin-bundle state into one cached JSON payload, keyed by `host.RegistryVersion()`.
- `internal/api/catalog.go` — CRUD for `catalog_sources` rows and the signed-catalog fetch/list-available-plugins flow.
- `internal/plugin/catalog/` — `SignedFetcher`, `KeyRing` (Ed25519 verification of the catalog feed itself).
- `cmd/nanite/plugin_cmd.go`, `plugin_install_flow.go`, `plugin_dev_cmd.go` — the `nanite plugin <new|install|update|uninstall|list|disable|enable|logs|reload|watch|release>` CLI.
- `cmd/nanite/main.go` — `discoverAndLoadPlugins` (calls `DiscoverPlugins`/`LoadDiscovered`/`LoadRegisteredBuiltins` at boot), `resolvePluginsDir` (`NANITE_PLUGINS_DIR` env, default `./plugins`).
- `ui/src/lib/plugin-loader.ts` — `syncPluginRegistry`: the frontend runtime loader that `import()`s each plugin's ESM bundle and wires its named exports into dynamic envelope/widget/slot maps.
- `scripts/generate-plugin-imports.mjs` — despite the filename, this **only** generates the *core* (compiled-in, non-plugin) envelope registry from the `go-envelopes` manifest; see §3.3 and §7.
- `plugins/repos.yaml` (this repo, root-level) — the known-plugin name→GitHub-repo catalog consumed by `nanite plugin install <name>`.
- `docs/plugin-install-guide.md`, `docs/architecture/plugin-system.md` — prior-art docs; the latter is stale (see §8).

## 3. Flow

### 3.1 Startup: discovery and loading

`discoverAndLoadPlugins` runs once at boot, after the plugin `Host` and its core services (`store`, `engine`, `mcp`, `toolclient`, etc.) are registered but before the HTTP server starts serving:

1. `resolvePluginsDir()` resolves the **activation directory** — `NANITE_PLUGINS_DIR` env var, else `./plugins` relative to the process's working directory. In this checkout that resolves to `/Users/chrispian/dev/hollis-labs/apps/nanite/plugins/`, which today contains only `repos.yaml` and a cache file — no subdirectory has a `plugin.yaml`.
2. `plugin.DiscoverPlugins(pluginsDir)` lists subdirectories, skips any without a `plugin.yaml`, and `ParseManifest`s the rest. For `runtime: builtin` manifests it looks up a compiled-in constructor by `manifest.Identifier()` (`id`, falling back to `name`) in the compile-time registry and skips silently if none is found. For `runtime: subprocess` it requires `entrypoint` to be set and defers construction.
3. `plugin.LoadDiscovered(host, discovered)` topologically sorts by `dependencies` (Kahn's algorithm; a cycle is a hard error) and, per plugin, in order:
   - `NewPluginConfig(id, dir)` parses the manifest's `config:` block plus an optional `plugins/<id>/config.yaml` override file, and `host.SetPluginConfig` stores it so `GetConfig` works during `Load()`.
   - **Subprocess**: `newSubprocessPluginFromManifest` resolves the entrypoint to an absolute path (`exec.LookPath`, falling back to a path joined against the plugin dir), reads `config:` env-var/default values, and constructs a `subprocess.SubprocessPlugin` that will spawn the process and speak JSON-RPC over stdio.
   - **Builtin**: the looked-up constructor is called directly (`dp.Constructor()`), producing an in-process Go struct.
   - `host.LoadPlugin(p)` sets `host.activePlugin = id`, calls `p.Load(host)` **without holding the host lock** (deliberately — `Load()` calls back into `Register*` methods that acquire it), stores the plugin, bumps the registry version, and fires a `plugin.installed` event. For builtins, `Load()` is where any *imperative* registrations happen (a builtin can call `host.RegisterUIComponent` etc. directly instead of, or in addition to, declaring them in `registers:`).
   - `applyManifestRegistrations(host, manifest, p, dir)` — the yaml-authoritative pass, detailed in §3.2.
4. `plugin.LoadRegisteredBuiltins(host)` then loads every compiled-in builtin **not already loaded** in step 3 — i.e. every builtin with no matching `plugins/<id>/plugin.yaml` on disk. This is how all 12 builtins in this checkout actually load today, since the activation directory has no plugin subdirectories. If a builtin implements the `ManifestProvider` interface (`Manifest() *PluginManifest`, backed by a `//go:embed plugin.yaml` in the builtin's own package — `bookmarks` is a concrete example), the same `applyManifestRegistrations` pass runs against its embedded manifest so it gets identical declarative wiring to a discovered plugin, just with `pluginDir=""` (so schema-file-relative registrations like `envelopes[].schema` are skipped rather than resolved on disk).
5. `host.SetMCPRegistrar(mcpManager)` connects the host to the live `mcp.Manager` so any `registers.mcp_servers` entries from step 3 can call `AddPluginServer`; `mcpManager.AutoDiscover()` is re-run after all plugins load to pick up their tools. `srv.SetPluginsDir(pluginsDir)` tells the HTTP server where to serve plugin UI bundle files from.

### 3.2 What `registers:` fans out to

`applyManifestRegistrations` iterates `manifest.Registers.*` in a fixed order. Every category below is declared in `plugin.yaml`; only the ones marked "subprocess-only" require the plugin to actually be a live JSON-RPC process to complete (builtins that want the same behavior call the `Host` method directly from `Load()` instead, since they don't have a transport to proxy through):

| `registers.*` key | Host effect | Subprocess-only? |
|---|---|---|
| `envelopes[]` | `host.RegisterEnvelope` (side-map + `chat.RegisterEnvelopeType` for validation); if `schema` is set, the JSON Schema file is compiled and attached for strict envelope-payload validation | No |
| `components[]` | `host.RegisterUIComponent` (id/type/name/description only — no `Props`/`Handler`, those aren't expressible in YAML) | No |
| `slots[]` | `host.RegisterSlot` (mount-point id, slot name, component, priority, props) | No |
| `keybindings[]` | `host.RegisterKeybinding` (rejected on collision with a fixed core-binding set or another plugin's key) | No |
| `commands[]` | `host.RegisterCommand` with a handler that proxies to the subprocess via `MethodCommandExecute` | **Yes** |
| `events[]` | `host.RegisterEventHook` with a `subprocess.NewEventHook` that forwards to the process and threads results through the plugin's envelope filter and the session SSE consumer | **Yes** |
| `http_routes[]` | `host.RegisterHTTPHandler`, proxying each request over JSON-RPC (`MethodHTTPHandle`); auth/cookie/CSRF headers are stripped before forwarding, body capped at 10 MiB | **Yes** |
| `mcp_servers[]` | `mcp.Manager.AddPluginServer` — always registered at the fixed `plugin_stdio` trust tier | **Yes** |
| `card_rules[]` | `host.RegisterCardRule` — Stage-1 detection rules (regex or JSON-Schema match) that can only *add* new card types, never override a built-in one | No (schema-based rules need `pluginDir` to resolve the schema file, but registration itself works either way) |
| `panels[]` | `host.RegisterPanel` — right-rail tab entries; render function is a placeholder in the current implementation | No |
| `crud[]`, `agent_profiles[]` | Logged as deferred/not-yet-wired; the schema accepts them but `applyManifestRegistrations` does not act on them | N/A |

A failure partway through this pass triggers a best-effort `host.UnloadPlugin(id)` rollback so a partially-registered plugin doesn't stay "live" without also emitting `plugin.load_failed`.

### 3.3 Frontend consumption

Two independent paths put plugin-declared UI in front of the user, and they must not be confused:

- **Core envelopes** (compiled into the frontend bundle at build time): `scripts/generate-plugin-imports.mjs` reads the external `go-envelopes` module's manifest and writes `ui/src/generated/plugin-envelopes.ts` with `React.lazy` imports for every *core* type. As of the current code, this script explicitly does **not** touch plugin-registered envelopes at all — a comment in the script states "Runtime plugin envelopes are registered dynamically via the plugin registry ... they do not participate in codegen." Nothing in this script reads `plugin.yaml`.
- **Plugin UI** (loaded at runtime, no build step): `GET /api/plugins/registry` (`internal/api/plugins_registry.go`) walks the host's live `envelopes`/`uiComponents`/`slots`/`manifests` maps and returns one JSON payload — envelope type → `{plugin_id, component, version, schema_url}`, widget id → `{plugin_id, name}`, slot name → ordered entry list, plugin id → `{bundle_url, stylesheet_url, bundle_hash, react_version}` (bundle/stylesheet URLs are derived from the manifest's `ui.bundle_dir`/`ui.entry`/`ui.stylesheet`, served by the `GET /api/plugins/{name}/ui/{file...}` static handler; `bundle_hash` is the bundle file's mtime, used purely as a cache-buster). The response is cached and only recomputed when `host.RegistryVersion()` advances. `ui/src/lib/plugin-loader.ts`'s `syncPluginRegistry` consumes this payload: for each plugin with a `bundle_url` it `import()`s the ESM module directly in the browser, pulls the named exports the registry said to expect (`component` fields), and populates `dynamicEnvelopes`/`dynamicWidgets`/`dynamicSlotComponents` maps plus injects a `<link rel=stylesheet>` if the plugin declared one. `getEnvelopeComponent(type)` (the function `EnvelopeRenderer` actually calls) checks the static core registry first and falls back to `getDynamicEnvelope(type)` — the plugin path — only on a miss.

### 3.4 Install / uninstall / disable lifecycle (separate from boot-time discovery)

This is a distinct code path from §3.1 — it mutates the activation directory and (via the REST API) can hot-load into an already-running host:

- **Install** (`nanite plugin install <name|path>`, or `POST /api/plugins/install(-local|-archive)` from Settings → Plugins): an `install.Installer` state machine — `Source` (either a `catalogArchiveSource` that downloads a signed `.tar.gz` named by a catalog entry, or a `localDirSource`/uploaded archive) → `Verifier` (checksum + signature against a `KeyRing`) → `Extractor` → `Validator` (`ValidateManifest` against `plugin.schema.v1.json` plus cross-ref checks: does every referenced bundle asset file actually exist, is `shadcn_version` sane, are `platforms` valid) → `Staging` (extract into a staging root, then atomically rename into `<pluginsDir>/<id>/` only on full success — a failed update leaves the previous install untouched) → `Loader` (no-op for the CLI, which relies on a restart; the API path's loader hot-registers into the live `Host`).
- **Catalog lookup**: `nanite plugin install <name>` first tries `installFromCatalog` (fetches and signature-verifies a catalog YAML from `NANITE_CATALOG_URL` / the default `https://plugins.nanite.hollislabs.dev/catalog.yaml`, cached locally), and only falls back to `git clone github.com/hollis-labs/<name>.git` if the name isn't found there. Separately, the Settings-UI-facing "browse available plugins" flow reads DB-registered `catalog_sources` rows (§5) via `internal/api/catalog.go`, which can point at additional signed feeds beyond the one default URL.
- **Disable/Enable**: rename `plugins/<id>/plugin.yaml` ↔ `plugin.yaml.disabled` (`manage.go`), paired with `host.UnloadPlugin`/re-`LoadPlugin` to take live effect immediately in the running process — no restart needed for these two operations per `docs/plugin-install-guide.md`.
- **Uninstall**: `UnloadPlugin` then `rm -rf` the plugin's directory.
- **Unload sweep**: `Host.UnloadPlugin` removes the plugin's entries across ~16 registries (event hooks, UI components, slots, keybindings, connectors, services incl. CLI adapters, task backends, providers, config-schema markers, envelope types, card rules, panels, MCP servers via `MCPRegistrar.RemoveServersByPlugin`) and calls the plugin's own `Unload()`. One documented exception: HTTP route *forwarders* on the core `http.ServeMux` are never removed (Go's `ServeMux` doesn't support it) — they stay installed for the process lifetime and simply 404 once the underlying `pluginMux` entry is swept.

### 3.5 Diagram

```mermaid
sequenceDiagram
    participant Author as Plugin author
    participant Repo as plugin.yaml + code
    participant Install as Installer state machine
    participant Disk as plugins/&lt;id&gt;/
    participant Main as main.go boot
    participant Host as plugin.Host
    participant MCP as mcp.Manager
    participant API as GET /api/plugins/registry
    participant FE as ui/plugin-loader.ts

    Author->>Repo: author plugin.yaml (registers.*) + Go or subprocess code
    Repo->>Install: nanite plugin install <name> / POST /api/plugins/install
    Install->>Install: verify signature+checksum, extract, ValidateManifest
    Install->>Disk: atomic rename into pluginsDir/<id>/

    Main->>Disk: DiscoverPlugins() scans for plugin.yaml
    Main->>Host: LoadDiscovered() — topo sort by dependencies
    Host->>Host: LoadPlugin(p): activePlugin=id, p.Load(host)
    Host->>Host: applyManifestRegistrations(manifest)
    Host->>Host: envelopes/components/slots/keybindings (always)
    Host->>MCP: mcp_servers[] -> AddPluginServer (subprocess only)
    Main->>Host: LoadRegisteredBuiltins() — compiled-in, no on-disk dir needed

    FE->>API: fetch registry (cached on host.RegistryVersion())
    API->>Host: read envelopes/uiComponents/slots/manifests maps
    API-->>FE: {envelopes, widgets, slots, plugins:{bundle_url,...}}
    FE->>FE: import(bundle_url) per plugin, wire named exports
    FE->>FE: getEnvelopeComponent(type): core map, else dynamic map
```

## 4. Plugin manifest shape

`plugin.yaml` v1 (validated against `internal/plugin/schemas/plugin.schema.v1.json`) requires `schema_version: 1`, `id`, `name`, `version` (semver), `description`, `author`, `license`, `runtime` (`builtin` or `subprocess`), `protocol` (JSON-RPC protocol version, integer ≥1), and `nanite_compat.min`. `entrypoint` is required when `runtime: subprocess`.

Two real subprocess-plugin manifests exist in this workspace (sibling repos, not currently installed into this app's `plugins/` activation directory — see §8):

**`nanite-plugin-giphy/plugin.yaml`** (`/Users/chrispian/dev/hollis-labs/plugins/nanite-plugin-giphy/`):
```yaml
schema_version: 1
id: giphy
runtime: subprocess
entrypoint: ./giphy
load_type: auto
config:
  giphy_api_key: {type: string, required: false, env_var: GIPHY_API_KEY, description: "..."}
  giphy_rating:  {type: string, required: false, default: g}
registers:
  envelopes:
    - type: giphy-modal
      component: GiphyModalCard
      version: 1
      schema: envelopes/giphy-modal.schema.json
  commands:
    - name: giphy
      description: Search Giphy for a GIF and render it inline.
      args: [{name: query, type: string, required: true}]
  mcp_servers:
    - name: giphy
      tools: [search]
ui:
  bundle_dir: ui/dist
  entry: index.js
  stylesheet: style.css
  react_version: ^18.0.0
release:
  platforms: [darwin-arm64, darwin-amd64, linux-amd64, linux-arm64, windows-amd64]
```

**`nanite-plugin-oembed/plugin.yaml`** registers one envelope (`oembed-card` → `OEmbedCard`) plus one event subscription (`events: [{types: [message.sent], handler: onMessageSent}]`) — no commands, no MCP server. Its `config:` block is a single free-text `oembed_extra_providers` YAML-in-a-string field.

Both examples show the full shape a manifest can use: identity/versioning metadata, a `config:` schema (type/required/env_var/default/description per key, resolved keychain → DB → file override → schema default), `registers.*` (see the table in §3.2 for every key the schema accepts, plus `slots`, `keybindings`, `http_routes`, `card_rules`, `panels`, `crud`, `agent_profiles`), a `ui:` block (`bundle_dir`, `entry`, `stylesheet`, `assets_dir`, `react_version`, `shadcn_version`) describing the frontend bundle if any, and a `release:` block (`archive_url`/`checksum_url`/`signature_url`/`platforms`) describing the signed distributable. `dependencies`/`requires.plugins` express load-order constraints; `requires.mcp_servers`/`requires.features` are declared but not validated at load time. `tool_overrides` lets a manifest set a per-tool `load_type` (`auto` vs `opt-in`) that overrides the plugin-level default.

The support-ticket example CLAUDE.md and `docs/plugin-install-guide.md` both reference (`kb-result`, `ticket-form`, `ticket-confirmation`, `resolution-capture` envelope types; `TicketFormCard.tsx`/`TicketConfirmationCard.tsx` frontend components already exist in this repo) has no `plugin.yaml` or source repo present anywhere in this workspace — see §8.

## 5. Data model touched

- **`plugin_settings`** (`plugin_id TEXT PRIMARY KEY, settings TEXT (JSON), schema TEXT (JSON), icon TEXT, updated_at`) — written by `Host.SetConfig` (merges a key into the JSON blob) and `Host.RegisterConfigSchema` (stores the field-definition list a plugin declares); read by `Host.GetConfig` as the third step in its resolution chain (keychain secret → DB row → file-based `config.yaml` override → manifest default). **0 rows** in the live DB (`/Users/chrispian/.local/share/nanite/workspaces/default/main.db`) — consistent with no subprocess plugin (which is the kind that ships a `config:` block worth persisting, e.g. giphy's API key) currently being installed into this app's `plugins/` activation directory; the 12 loaded builtins don't appear to call `SetConfig`/`RegisterConfigSchema` for anything an operator has set yet.
- **`mcp_servers`** (`id, name, transport_type, command, url, args, env, enabled, trust_tier, env_allowlist, created_at, updated_at`) — the durable, DB-persisted MCP server registry, loaded at boot (`s.ListMCPServers()` in `main.go`) and fed into `mcp.Manager` with each row's stored `trust_tier`. This is distinct from a *plugin's* `registers.mcp_servers` entries, which register directly into the in-memory `mcp.Manager` (fixed at `plugin_stdio` tier) via `AddPluginServer` and are not necessarily persisted as a row here. **1 row** currently: `name="Agent Mux"`, `transport_type=stdio`, `command=/Users/chrispian/go/bin/mux`, `trust_tier=plugin_stdio` — see §8 for why this doesn't obviously trace back to an installed plugin.
- **`catalog_sources`** (`id, name, url, type, enabled, priority, public_key, created_at, updated_at`, unique on `url`) — the registry of signed **plugin** catalog feeds consulted by the "browse/install from catalog" flow (`internal/api/catalog.go`, `internal/plugin/catalog.SignedFetcher`). **1 row**: `id="official"`, `name="Hollis Labs"`, `url=https://raw.githubusercontent.com/hollis-labs/plugin-catalog/main/catalog.yaml`, `type=official`, `priority=100`, seeded by `internal/store/seed.go`. This table is plugin-catalog-specific — it is not the same catalog infrastructure covering skills or generic MCP-server discovery (see §7).

## 6. Configuration & manual-setup points

**Subprocess plugin, end to end** (the path for third-party/user-authored plugins):
1. Author a repo with `plugin.yaml` (`schema_version: 1`, identity fields, `runtime: subprocess`, `entrypoint`, `protocol`, `nanite_compat.min`), a `config:` block if it needs settings, and a `registers:` block for whatever it contributes.
2. Implement the entrypoint executable against the `plugin-sdk` JSON-RPC protocol (`internal/plugin/subprocess/protocol.go` — command execute, event handle, HTTP handle, MCP list/call-tool methods, as applicable to what `registers:` declares).
3. If `registers.envelopes` is non-empty: write the matching React component, and optionally a JSON Schema file at the path named by `schema` for strict payload validation.
4. If it ships UI at all: build an ESM bundle (`ui.bundle_dir`/`ui.entry`) whose named exports match every `component` name referenced in `registers.envelopes[]`/`components[]`/`slots[]` — no codegen step is involved; `plugin-loader.ts` resolves these purely at runtime via dynamic `import()`.
5. Add an entry to `plugins/repos.yaml` (`name`, `repo`, `type: core|user`) so `nanite plugin install <name>` can find it by short name, and/or publish it in a signed catalog (a `catalog_sources` row) for the "browse available plugins" GUI flow.
6. Install via `nanite plugin install <name|path>` (CLI) or Settings → Plugins → Install (`POST /api/plugins/install(-local|-archive)`); this runs the verify → extract → validate → stage → commit state machine and drops the manifest+code into `plugins/<id>/`.
7. The running host picks it up either on next process restart (`DiscoverPlugins` re-scans at boot) or, via the API-driven install/reload path, immediately in the live process (`nanite plugin reload <name>` / the dev-mode `window.__nanite_reloadPlugin`).
8. Configure it: set required `config:` keys via environment variable (highest precedence, per-key `env_var`), the Settings → Plugins config panel (persists to `plugin_settings`), or a hand-edited `plugins/<name>/config.yaml` override file.

**Builtin (compiled-in) plugin, end to end** (first-party/core plugins only — requires a rebuild):
1. `nanite plugin new` scaffolds `internal/plugin/builtin/<name>/` from `internal/plugin/scaffold/templates/builtin/` (or hand-write the package).
2. Implement the `plugin.Plugin` interface (`ID`, `Name`, `Version`, `Description`, `Dependencies`, `Load`, `Unload`, `Status`); optionally embed a `plugin.yaml` (`//go:embed plugin.yaml`) and implement `ManifestProvider.Manifest()` so `registers:` drives declarative wiring instead of imperative `Register*` calls scattered through `Load()` (the `bookmarks` builtin is the concrete example of this pattern).
3. `init()` calls `RegisterPlugin("<id>", constructor)` into the compile-time registry.
4. Add a blank import for the new package in `internal/plugin/allplugins/allplugins.go`.
5. Rebuild (`go build ./cmd/nanite/`) and deploy via Cerberus (`cerberus_resource_deploy nanite-api-service` + `cerberus_resource_reload nanite-api-service`, per this project's CLAUDE.md) — `LoadRegisteredBuiltins` picks up any compiled-in plugin that has no matching `plugins/<id>/plugin.yaml` on disk, so no activation-directory entry is strictly required for a builtin to load.

## 7. Cross-references

- **`07-tool-calling-mcp.md`** — owns the full MCP tool-discovery/naming/execution pipeline; this doc only covers how a plugin's `registers.mcp_servers` declaration reaches `mcp.Manager.AddPluginServer`. That sibling doc's §3.1 independently confirms: "no `plugin.yaml` currently in this repo actually populates `registers.mcp_servers`."
- **Envelope system** (per this project's CLAUDE.md) — owns the manifest/schema/backend/frontend sync mechanics for *core* envelope types (`internal/chat/envelope.go`, `ui/src/generated/plugin-envelopes.ts`, `scripts/generate-plugin-imports.mjs`). This doc covers the separate runtime path plugin-declared envelope types take (§3.3) — the two paths converge only at `getEnvelopeComponent()`.
- **Skills-and-knowledge** — likely owns any skill/MCP-server catalog infrastructure distinct from `catalog_sources` (which, per §5, is specifically the signed *plugin* catalog feed registry, seeded with one Hollis Labs URL).

## 8. Open questions

- **`plugin_settings` has 0 rows.** This is consistent with no subprocess plugin (the kind whose `config:` block is worth persisting — e.g., an API key) being installed into this app's `plugins/` activation directory today; all 12 currently-loaded plugins are compiled-in builtins loaded via `LoadRegisteredBuiltins` rather than `plugin.yaml`-driven discovery. Whether any builtin calls `SetConfig`/`RegisterConfigSchema` at all in practice wasn't traced further.
- **The one `mcp_servers` row ("Agent Mux", `trust_tier=plugin_stdio`, command pointing at a `mux` binary) doesn't obviously trace back to an installed plugin.** No `plugins/<id>/plugin.yaml` exists in this app's activation directory for anything named "agent-mux" or "mux" (a sibling source repo, `nanite-plugin-agent-mux`, exists on disk elsewhere in the workspace but is not listed in this app's `plugins/repos.yaml` and is not installed here). The `plugin_stdio` trust tier on a DB-persisted row is notable because `AddPluginServer` (the actual plugin-registration path) doesn't appear to write DB rows at all — it registers directly into the in-memory `mcp.Manager`. Whether this row was set up through a manual "Add MCP server" flow with the tier hand-picked, or through some other path not traced here, is unresolved from static inspection alone.
- **The support-ticket / IT-Support reference plugin is documented but not present.** `CLAUDE.md`, `docs/plugin-install-guide.md`, `.nanite/agents/plugin-dev-tasks.md` (status: "Complete"), and live frontend code (`TicketFormCard.tsx`, `TicketConfirmationCard.tsx`, a `kb-result` envelope builder hardcoded in `internal/chat/envelope.go`) all reference it, but no `plugin.yaml` or source repo for it exists anywhere searched in this workspace. It likely lives in a separate `hollis-labs/support-ticket` repo not checked out in this environment.
- **`docs/architecture/plugin-system.md` is stale and says so isn't obvious from reading it in isolation.** It states "`plugin.yaml` is not parsed by the loader" and "no plugin auto-discovery" — both now false per the current `internal/plugin/loader.go`/`registrations.go`. `.nanite/agents/plugin-dev-tasks.md` (dated 2026-03-28) independently flags this doc as having "significant drift" and defers the rewrite; it appears the rewrite never happened.
- **Two of the plugin-dev-tasks.md-tracked plugins are flagged with known frontend breakage** (as of that file's last update): giphy's `giphy-card` envelope missing from the frontend registry, and oembed's manifest `component` path pointing at the wrong location. Neither was independently re-verified here since neither plugin is currently installed into this app's `plugins/` directory to exercise.
- **Builtins with no on-disk `plugin.yaml` can't be `disable`d/`enable`d via the documented CLI mechanism**, which operates by renaming `plugins/<name>/plugin.yaml` — none of the 12 currently-loaded builtins has such a file in this checkout's `plugins/` directory (they load purely via `LoadRegisteredBuiltins`, which has no on-disk dependency). Whether builtins are expected to also ship an on-disk manifest for lifecycle-tooling purposes, or are simply considered always-on and outside that tooling's scope, wasn't resolved from the code alone.
