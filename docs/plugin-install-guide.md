# Plugin Install / Uninstall Guide

Conduit plugins can be managed via the **GUI** (Settings → Plugins) or the **CLI** (`conduit plugin`). All operations take effect immediately — no restart required.

## GUI (Settings → Plugins)

Open Settings → Plugins tab. The page shows all plugins:

- **Active** (green) — installed and running
- **Disabled** (yellow) — installed but inactive
- **Available** (gray) — in the registry, not yet installed

Actions per plugin:
- **Core plugins** (e.g. session-stats): Always active, no actions
- **User plugins**: Install, Uninstall, Disable, Enable buttons

Changes are immediate — the agent dropdown, MCP tools, and CRUD endpoints update without restarting Conduit.

## CLI

```bash
# List all installed plugins
conduit plugin list

# Install from the hollis-labs GitHub org
conduit plugin install support-ticket

# Disable (keeps files, removes from runtime)
conduit plugin disable support-ticket

# Re-enable
conduit plugin enable support-ticket

# Uninstall (removes files + cleans up agent profiles)
conduit plugin uninstall support-ticket
```

The CLI auto-restarts Conduit via Cerberus after each operation. Use `--no-restart` to skip:

```bash
conduit plugin install support-ticket --no-restart
```

## REST API

```
GET    /api/plugins/managed    — list all plugins (installed + available)
POST   /api/plugins/install    — {"name": "support-ticket"}
POST   /api/plugins/uninstall  — {"name": "support-ticket"}
POST   /api/plugins/disable    — {"name": "support-ticket"}
POST   /api/plugins/enable     — {"name": "support-ticket"}
```

All mutation endpoints hot-swap plugins at runtime: agent profiles are seeded/removed from the DB, CRUD handlers and MCP tools are registered/unregistered in-process.

## How it works

### Architecture

```
internal/plugin/builtin/     ← Go source, always compiled into the binary
plugins/                     ← Activation directory (controls what's loaded)
  repos.yaml                 ← Registry of known plugins (core vs user)
  support-ticket/            ← Cloned from GitHub on install
    plugin.yaml              ← Manifest (presence = active)
    plugin.yaml.disabled     ← Renamed manifest (presence = disabled)
  session-stats/
    plugin.yaml
```

### Hot-swap mechanism

Plugin code is compiled into the binary. The `plugins/` directory controls activation:

- **Install**: `git clone` into `plugins/`, then `LoadPlugin()` into the running host — seeds agent profile, registers CRUD/MCP/events
- **Disable**: `Uninstall()` removes agent profile from DB, `UnloadPlugin()` removes from host registry, rename `plugin.yaml` → `plugin.yaml.disabled`
- **Enable**: Rename back to `plugin.yaml`, then `LoadPlugin()` — re-seeds agent, re-registers everything
- **Uninstall**: `Uninstall()` + `UnloadPlugin()`, then `rm -rf` the plugin directory

Routes are registered once (Go's `http.ServeMux` doesn't support removal). CRUD handler closures do indirect lookups from the handler map, so they pick up new handlers on re-load.

### Plugin registry (`plugins/repos.yaml`)

```yaml
plugins:
  - name: support-ticket
    repo: hollis-labs/support-ticket
    type: user
  - name: session-stats
    repo: hollis-labs/session-stats
    type: core
```

- `core` plugins ship with Conduit and can't be uninstalled
- `user` plugins can be installed/uninstalled/disabled

## For developers: adding a new plugin

1. Create Go package at `internal/plugin/builtin/<name>/`
2. Implement `plugin.Plugin` interface (`ID`, `Name`, `Version`, `Load`, `Unload`)
3. Optionally implement `plugin.Uninstallable` for cleanup on uninstall
4. Add `init()` that calls `hostplugin.RegisterPlugin("<name>", constructor)`
5. Add blank import to `internal/plugin/allplugins/allplugins.go`
6. Create a GitHub repo with `plugin.yaml`, UI components, agent profiles
7. Add to `plugins/repos.yaml`
8. `plugin.yaml` fields: `name`, `version`, `description`, `short_desc`, `author`, `url`, `config`, `registers`

## Setup Notes

**Import path for compiled plugins:** `allplugins.go` must import from `plugins/<name>` (the submodule path), not from `internal/plugin/builtin/<name>`. Both paths register the same plugin name in `init()`, so importing both will panic. When setting up a new machine, verify the import in `internal/plugin/allplugins/allplugins.go` points to the submodule.

## Troubleshooting

**Plugin not appearing after install?**
- Check `conduit plugin list` — status should be `active`
- Check Conduit logs for load errors: `cerberus logs conduit-api`

**Agent not in dropdown after enable?**
- Hard-refresh the browser (Cmd+Shift+R) if the cache-bust didn't trigger
- Check `/api/agents` directly to confirm the agent is in the DB

**Config not working?**
- Env vars take priority: `echo $SUPPORT_DATABASE_URL`
- Config file: `plugins/<name>/config.yaml`
- Missing required config with no default logs an error at startup
