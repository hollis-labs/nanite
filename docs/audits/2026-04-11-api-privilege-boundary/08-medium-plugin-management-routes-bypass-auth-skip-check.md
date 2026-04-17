# [Medium] Plugin management routes registered outside main API — auth coverage depends on timing

**Scope:** Auth middleware, route registration
**Topic:** Security
**Date:** 2026-04-11

## Problem

Plugin management routes (`/api/plugins/install`, `/api/plugins/install-local`, etc.) and catalog routes (`/api/plugins/catalog/*`) are registered via `RegisterPluginManagementRoutes` and `RegisterCatalogRoutes` in `server.go:SetPluginsDir()`. These are called after `New()` constructs the server, but the middleware chain wraps the `mux` at `ListenAndServe()` time. This means the routes ARE covered by auth — but the registration path is fragile.

More importantly, the auth middleware skip list is implicit. The middleware checks:

```go
if r.URL.Path == "/api/health" {
    next.ServeHTTP(w, r)
    return
}
if !strings.HasPrefix(r.URL.Path, "/api/") {
    next.ServeHTTP(w, r)
    return
}
```

This means ALL `/api/` routes require auth (when enabled) except `/api/health`. This is correct. However, the skip list is not documented anywhere, and the comment on `basicAuthMiddleware` only mentions `/api/health`. If someone adds a new public endpoint (e.g., OAuth callback, webhook receiver) under `/api/`, they must know to add it to the skip list — there's no compile-time enforcement.

## Evidence

`internal/server/auth.go:L24-35`:

```go
return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    // Exempt /api/health from auth.
    if r.URL.Path == "/api/health" {
        next.ServeHTTP(w, r)
        return
    }
    // Only protect /api/ routes.
    if !strings.HasPrefix(r.URL.Path, "/api/") {
        next.ServeHTTP(w, r)
        return
    }
    // ...
})
```

`internal/server/server.go:L50-56` — routes registered after construction:

```go
func (s *Server) SetPluginsDir(dir string) {
    s.pluginsDir = dir
    api.RegisterPluginManagementRoutes(s.mux, dir, s.store, s.pluginHost)
    api.RegisterCatalogRoutes(s.mux, s.store, dir, s.pluginHost)
}
```

## Impact

No current auth bypass exists. This is a structural fragility finding:
- The highest-privilege routes (plugin install, plugin management) are registered via a side channel that bypasses the standard `RegisterRoutes()` function.
- Future maintenance could introduce an auth bypass if a new public route is added without awareness of the middleware-based skip list.

## Recommendation

1. Move plugin management route registration into `api.RegisterRoutes()` (or call it from there) so all route registration is centralized.
2. Add a comment block at the top of `auth.go` documenting the auth policy: "All /api/ routes require auth except /api/health. To add a new exempt route, add it to the skip list below and document the reason."
3. Consider making the skip list explicit (a `[]string` or `map[string]bool`) rather than inline `if` checks.

## References

- `reviewer-backend.md` priority: "Missing entries = unauth access to protected routes."
- The auth implementation itself is well-done — constant-time comparison, proper 401 handling, no-op when credentials aren't configured. The skip list just needs documentation and centralization.
