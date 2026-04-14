# Plugin Authoring Guide

This guide walks through authoring a Nanite plugin end-to-end, using the
[`giphy`](https://github.com/hollis-labs/nanite-plugin-giphy) plugin as the
worked example. It targets external plugin authors. For a field-by-field
reference of `plugin.yaml`, see [plugin-yaml-reference.md](./plugin-yaml-reference.md).
For the SDK API, see [plugin-sdk-reference.md](./plugin-sdk-reference.md).

## 1. What is a Nanite plugin

A Nanite plugin extends the host with any combination of slash commands,
MCP tools, HTTP routes, CRUD resources, UI slots, envelopes, keybindings,
agent profiles, and event hooks. There are two tiers:

- **Subprocess plugins (the default)** — built as a standalone binary and
  UI bundle, installed by the user, and spawned by the host as a child
  process. They communicate over JSON-RPC 2.0 on stdio. They have zero
  access to `internal/*` and can be installed, updated, and uninstalled
  without rebuilding Nanite.
- **Builtin plugins** — compiled into the Nanite binary. Used only for
  core functionality the project ships itself.

**Write your plugin as a subprocess plugin.** The builtin path is reserved
for internal use. Everything below assumes subprocess.

A subprocess plugin is three things on disk:

1. A Go binary that calls `subprocess.Serve(&myPlugin{})`.
2. A `plugin.yaml` that declares everything the plugin registers.
3. An optional ESM UI bundle under `ui/dist/`.

The Go binary does **not** call `host.RegisterCommand` / `RegisterEnvelope` /
`RegisterSlot` / etc. All registration is declarative in `plugin.yaml`; the
host reads it and registers on the plugin's behalf. The binary only serves
runtime request handlers (`Command`, `MCPCallTool`, `EventHandle`, `HTTPHandle`,
CRUD, etc.).

## 2. Prerequisites

- Go 1.22 or newer.
- Node 20 or newer (for the UI bundle, if your plugin ships UI).
- The `nanite` CLI, installed and on `PATH`.
- A running Nanite instance locally (for `nanite plugin watch` / `reload` /
  `install`).

## 3. Scaffold a new plugin

```bash
# Default output dir is ./plugins/<name>. Pass --output to override.
nanite plugin new --subprocess my-plugin
cd plugins/my-plugin
```

This generates:

```
plugins/my-plugin/
├── plugin.yaml              # v1 manifest
├── main.go                  # subprocess.Serve entrypoint
├── go.mod                   # module github.com/you/my-plugin
├── envelopes/               # envelope JSON Schemas
├── ui/
│   ├── src/index.tsx        # ESM entry
│   ├── vite.config.ts       # externalises react + @nanite/ui/*
│   ├── package.json
│   └── tsconfig.json
├── Makefile                 # cross-platform build + sign + archive
├── .github/workflows/release.yml
├── README.md
├── LICENSE
├── CHANGELOG.md
└── .gitignore
```

> **Note.** If your scaffold output looks different, the Track J.1 scaffold
> rewrite is still landing. The generated files described here are the
> Phase-2-final target; a stub scaffold may ship earlier. In the interim
> you can copy the `nanite-plugin-giphy` repo as a starting point.

## 4. Edit `main.go`

Giphy's `main.go` is the canonical pattern. The plugin struct satisfies
`subprocess.Plugin` (required) plus whichever capability interfaces you
need. Giphy implements `CommandHandler` (for `/giphy <query>`) and
`MCPHandler` (for the `giphy.search` tool):

```go
package main

import (
    "context"
    plugin "github.com/hollis-labs/plugin-sdk"
    "github.com/hollis-labs/plugin-sdk/subprocess"
)

type giphyPlugin struct {
    apiKey string
    rating string
}

func (p *giphyPlugin) Init(ctx context.Context, params subprocess.InitParams) (subprocess.InitResult, error) {
    p.apiKey = params.Config["giphy_api_key"]
    p.rating = params.Config["giphy_rating"]
    return subprocess.InitResult{
        ID:       "giphy",
        Name:     "Giphy",
        Version:  "0.1.0",
        Protocol: subprocess.ProtocolVersion,
    }, nil
}

func (p *giphyPlugin) Load(ctx context.Context) (subprocess.LoadResult, error) {
    return subprocess.LoadResult{}, nil
}

func (p *giphyPlugin) Unload(ctx context.Context) error { return nil }

func (p *giphyPlugin) Command(ctx context.Context, req subprocess.CommandRequest) (subprocess.CommandResult, error) {
    // ... return an envelope
}

func (p *giphyPlugin) MCPCallTool(ctx context.Context, req subprocess.MCPCallRequest) (subprocess.MCPCallResult, error) {
    // ... return tool output + optional envelope
}

func main() {
    if err := subprocess.Serve(&giphyPlugin{}); err != nil {
        os.Exit(1)
    }
}
```

Key points:

- `Init` receives `InitParams` with `Config`, `DataDir`, `CacheDir`,
  `LogLevel`, and `HostInfo`. Use `params.ResolvedDataDir()` /
  `params.ResolvedCacheDir()` to get safe paths.
- `Init` **returns** identity (`ID`, `Name`, `Version`, `Description`,
  `Protocol`). The manifest is still authoritative for the host; these
  values are compared against `plugin.yaml` and any mismatch is surfaced
  at install time.
- `Load` is where you open connections, seed caches, etc. To decline a
  yaml-declared registration at runtime, return a `LoadResult` with
  `SkippedRegistrations` populated — the host logs it and proceeds without
  that registration. There is no yaml fallback.
- `Unload` must release everything `Load` opened. The host enforces this
  on reload, uninstall, and shutdown.

**MCP tools: `mcp/list_tools` manual handler.** The plugin-sdk v0.3.0
dispatches `mcp/call_tool` automatically, but if you want to serve
`mcp/list_tools` with a custom tool catalog (e.g. tools that aren't in
`plugin.yaml registers.mcp_servers[].tools`), implement it as a custom
method on your plugin. The host will still read tool names from yaml by
default; the manual `mcp/list_tools` handler is only needed when a plugin
dynamically reshapes its tool list at runtime (see BLG-20260414-012).

## 5. Edit the UI

Your UI bundle lives under `ui/src/`. It builds to `ui/dist/index.js` and
(optionally) `ui/dist/style.css`. The host loads the ESM bundle at runtime
via an importmap.

**The 11 shared shadcn primitives.** The host publishes these primitives
as importmap entries under `@nanite/ui/<primitive>`:

- `button`
- `card`
- `dialog`
- `dropdown-menu`
- `popover`
- `select`
- `tooltip`
- `input`
- `textarea`
- `scroll-area`
- `separator`

Import them like any other module:

```tsx
import { Card, CardHeader, CardContent } from '@nanite/ui/card'
import { Button } from '@nanite/ui/button'

export function GiphyModalCard({ data }) {
  return (
    <Card>
      <CardHeader>{data.title}</CardHeader>
      <CardContent>
        <img src={data.gif_url} alt={data.query} />
        <Button>Close</Button>
      </CardContent>
    </Card>
  )
}
```

Declare these as Vite externals (the scaffold does this for you):

```ts
const external = [
  'react', 'react-dom', 'react-dom/client', 'react/jsx-runtime',
  '@nanite/ui/button', '@nanite/ui/card', '@nanite/ui/dialog',
  /* ...the rest of the 11... */
]
```

**Set `ui.shadcn_version` in `plugin.yaml`.** This is a semver range
(npm-style: exact, caret, or tilde) declaring which host primitive API
version your plugin was built against:

```yaml
ui:
  bundle_dir: ui/dist
  entry: index.js
  stylesheet: style.css
  react_version: ^18.0.0
  shadcn_version: ^1.0.0
```

Empty means "the plugin doesn't use shared primitives" and the install-time
check is skipped. Anything non-empty is validated against the host's
`HostShadcnVersion` at install time — incompatible ranges refuse the
install (or warn in developer mode).

Primitives that aren't on the shared list (Label, Toast, Form, DataTable,
Command, …) must be bundled by the plugin itself until the host publishes
them.

## 6. Developer flow

With Nanite running locally, iterate with:

```bash
# In one terminal: watch the plugin source tree, hot-reload on change.
nanite plugin watch ./my-plugin

# In another terminal: edit code / UI; the watcher triggers reload after
# a debounced change burst.
```

The watcher polls every 500ms, ignores `dist/`, `node_modules/`, `.git`,
`build/`, and `.cache/`, and debounces reloads at 300ms.

Manual reload:

```bash
nanite plugin reload my-plugin
```

In the browser console:

```js
window.__nanite_reloadPlugin('my-plugin')  // server reload + drop bundle cache
window.__nanite_pluginRegistry()           // live snapshot: envelopes, widgets,
                                           // slot components, loaded bundles,
                                           // load errors
```

Developer-mode logging from the loader is prefixed `[plugin-loader]` and
is info-level under `import.meta.env.DEV`; production builds no-op it.

## 7. Build a release

```bash
make release           # cross-compiles the 6 target platforms + builds UI
                       # + assembles archives + (optionally) signs
# or, equivalently:
nanite plugin release  # shells out to `make release`
```

The scaffold's Makefile builds for `darwin-arm64`, `darwin-amd64`,
`linux-amd64`, `linux-arm64`, `windows-amd64` (and `windows-arm64` where
the host ecosystem supports it). Output lands in `dist/archives/`:

```
dist/archives/my-plugin-0.1.0-darwin-arm64.tar.gz
dist/archives/my-plugin-0.1.0-darwin-arm64.tar.gz.sha256
dist/archives/my-plugin-0.1.0-darwin-arm64.tar.gz.sig     # if signing key set
```

See [plugin-packaging-guide.md](./plugin-packaging-guide.md) for the
archive layout, the six-platform build matrix, and signing key management.

## 8. Install locally

```bash
nanite plugin install ./dist/archives/my-plugin-0.1.0-darwin-arm64.tar.gz
```

The host stages the archive, verifies the signature (if required),
validates `plugin.yaml` against the v1 schema, checks `ui.shadcn_version`
and `ui.react_version` against the host, and atomically swaps it into
`~/.nanite/plugins/<id>/`. From that point on the plugin is live.

## 9. Signing

Production-mode installs require both:

1. A valid catalog signature (signed by catalog maintainers).
2. A valid per-plugin signature (signed by the plugin author's ed25519 key).

Developer mode downgrades both to warnings. See
[plugin-packaging-guide.md](./plugin-packaging-guide.md) for the ed25519
key workflow and `dev-signing-key.pub` conventions.

## 10. Publishing to the catalog

Once signed, submit to the Nanite catalog at
`plugins.nanite.hollis-labs.dev`. See
[plugin-catalog-guide.md](./plugin-catalog-guide.md) for the entry shape,
submission process, and deprecation flow.

## 11. Reference

- [plugin-sdk-reference.md](./plugin-sdk-reference.md) — SDK types, interfaces, lifecycle.
- [plugin-yaml-reference.md](./plugin-yaml-reference.md) — every manifest field.
- [plugin-packaging-guide.md](./plugin-packaging-guide.md) — build + sign + release.
- [plugin-catalog-guide.md](./plugin-catalog-guide.md) — catalog submission.
- [architecture/plugin-execution-plan-2026-04-10.md](./architecture/plugin-execution-plan-2026-04-10.md) — full design.
