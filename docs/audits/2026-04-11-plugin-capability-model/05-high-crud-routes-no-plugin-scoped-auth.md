# [High] Plugin CRUD routes have no plugin-scoped authorization

**Scope:** Plugin capability model
**Topic:** Security — authorization gap
**Date:** 2026-04-11

## Problem

When a plugin calls `host.RegisterCRUDHandler("tickets", handler)`, the host wires 5 HTTP routes under `/api/plugins/tickets/*`. These routes inherit the server's basic-auth middleware (if configured), but there is no per-plugin or per-resource authorization. Any authenticated user can access any plugin's CRUD endpoints, and there is no mechanism for a plugin to declare that its routes require elevated permissions.

Additionally, the `RegisterHTTPHandler` path (used by fragments-engine) wires routes directly onto the mux with zero authorization — the handler receives raw requests.

## Evidence

`internal/plugin/host.go:L201-244` — CRUD route registration:

```go
basePath := fmt.Sprintf("/api/plugins/%s", resourceType)

h.registerRoute(fmt.Sprintf("GET %s", basePath), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    h.mu.RLock()
    h2 := h.crudHandlers[resourceType]
    h.mu.RUnlock()
    h.handleCRUDList(w, r, h2)
}))
```

No authorization check, no permission validation, no rate limiting on the handler closures.

`internal/plugin/host.go:L172-174` — RegisterHTTPHandler:

```go
func (h *Host) RegisterHTTPHandler(pattern string, handler http.Handler) {
    h.registerRoute(pattern, handler)
}
```

The handler is mounted directly. The server's middleware chain (recover -> logging -> basicAuth -> CORS -> mux) applies, so basic auth covers it if enabled. But basic auth is a single shared credential — there is no per-plugin or per-resource scoping.

## Impact

1. Any authenticated caller (or any caller if auth is disabled, which is the default in dev) can access any plugin's CRUD endpoints.
2. A plugin registering sensitive data (e.g., support tickets with PII) has no way to restrict access.
3. `RegisterHTTPHandler` lets a plugin mount routes at arbitrary paths — including paths that shadow core API routes (e.g., `POST /api/sessions`). First-registered handler wins in `http.ServeMux`, and CRUD routes are registered at load time before core routes if a plugin loads early.

## Recommendation

1. Add an optional `Permission` field to CRUD handler registration. If set, the host wraps the handler in a middleware that checks the permission before dispatching.
2. For `RegisterHTTPHandler`, validate that the pattern starts with `/api/plugins/` and reject patterns that could shadow core routes.
3. Document that CRUD endpoints are protected only by the server's global auth, and that plugins should implement their own authorization if needed.

## References

- `internal/plugin/host.go:L186-244` — RegisterCRUDHandler
- `internal/plugin/host.go:L172-174` — RegisterHTTPHandler
- `internal/plugin/crud.go` — CRUD dispatch (no auth checks)
- Prior audit: `api-privilege-boundary` — CORS and auth findings
