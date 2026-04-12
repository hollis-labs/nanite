# [Critical] GetService("store") grants full database access to any plugin

**Scope:** Plugin capability model
**Topic:** Security — privilege boundary
**Date:** 2026-04-11

## Problem

Any plugin that calls `host.GetService("store")` receives the full `*store.Store` object, which exposes every CRUD method in the store layer (sessions, messages, agents, skills, modes, templates, usage, bookmarks, artifacts, a2a, MCP servers, workflows, plugin settings, trigger rules, tags, entities) plus the raw `*sql.DB` handle. There is no scoping, no per-plugin access control, and no read-vs-write distinction.

## Evidence

**session-stats plugin** — `internal/plugin/builtin/sessionstats/plugin.go:L41-48`:

```go
if svc, err := host.GetService("store"); err == nil {
    type hasDB interface{ GetSQLDB() *sql.DB }
    if s, ok := svc.(hasDB); ok {
        db = s.GetSQLDB()
    } else if st, ok := svc.(*nanitestore.Store); ok {
        db = st.DB
    }
}
```

**support-ticket plugin** — `plugins/support-ticket/plugin.go:L46-58`:

```go
if svc, err := host.GetService("store"); err == nil {
    type hasDB interface{ GetSQLDB() *sql.DB }
    if s, ok := svc.(hasDB); ok {
        p.store.SetDB(s.GetSQLDB())
    } else {
        if st, ok := svc.(*nanitestore.Store); ok {
            p.store.SetDB(st.DB)
        }
    }
}
```

**Service registration** — `cmd/nanite/main.go:175`:

```go
pluginHost.RegisterService("store", s)
```

The `s` here is the full `*store.Store` instance used by the entire application.

## Impact

A malicious or buggy plugin can:

1. **Read any user's session data, messages, credentials, API keys** stored in the database.
2. **Modify or delete any record** — sessions, agents, messages, settings, workflows.
3. **Execute arbitrary SQL** via the raw `*sql.DB` handle — including DDL (DROP TABLE, ALTER TABLE).
4. **Corrupt the database** by running concurrent conflicting writes or disabling foreign keys.
5. **Exfiltrate all stored data** by reading every table.

This is the single most dangerous capability leak in the plugin system. Every other service registered on the host (`mcp`, `toolclient`, `container`, `tasks`) has similar issues, but `store` is the most impactful because it holds all persistent state.

## Recommendation

Introduce a scoped store proxy per plugin:

1. Define a `PluginStore` interface that exposes only the operations a plugin should have access to (its own config, its own tables created via schema registration).
2. Wrap the real store in a per-plugin proxy that enforces scope.
3. Register the proxy as the `"store"` service, not the raw store.
4. For plugins that need the raw DB (session-stats, support-ticket), require an explicit `"database"` service that is only registered for plugins with a `requires_database: true` manifest flag, and log a warning at load time.

Short-term mitigation: audit which plugins call `GetService("store")` and document the access pattern. This is already partially tracked in the plugin audit.

## References

- `internal/plugin/host.go:L374-384` — `GetService` implementation
- `cmd/nanite/main.go:L175-177` — service registration
- `internal/store/store.go` — full store API surface
- Prior audit: `api-privilege-boundary` finding 03 (plugin install path traversal)
