# [Medium] Middleware order places CORS inside auth — preflight fails when auth is enabled

**Scope:** HTTP middleware chain
**Topic:** Security
**Date:** 2026-04-11

## Problem

The middleware chain is `recover -> logging -> basicAuth -> cors -> mux`. This means CORS preflight (`OPTIONS`) requests must pass basic auth before reaching the CORS handler. Browsers send preflight requests without credentials — the auth middleware will reject them with 401, causing the browser to block the actual request.

## Evidence

`internal/server/server.go:L60`:

```go
handler := s.recoverMiddleware(s.loggingMiddleware(basicAuthMiddleware(s.corsMiddleware(s.mux))))
```

Execution order (outermost first): `recover` -> `logging` -> `basicAuth` -> `cors` -> `mux`.

`internal/server/auth.go:L32-35` — auth checks all `/api/` routes:

```go
if !strings.HasPrefix(r.URL.Path, "/api/") {
    next.ServeHTTP(w, r)
    return
}
```

An `OPTIONS /api/sessions` preflight request has path `/api/sessions` which matches the `/api/` prefix. The middleware requires Basic Auth. The browser's preflight request does not include credentials. Result: 401.

## Impact

When basic auth is enabled, the browser cannot make CORS preflight requests, effectively breaking the entire API for browser clients that are on a different origin (e.g., a Vite dev server on port 5173 talking to the API on port 8090). This is currently masked because:

1. In local dev, auth is disabled (credentials unset), so the auth middleware is a no-op.
2. In production, the SPA is served from the same origin (embedded), so no CORS preflight is needed.

If auth is ever enabled in a cross-origin deployment, the API breaks completely for browser clients.

## Recommendation

Move CORS middleware outside auth, or exempt `OPTIONS` from auth:

**Option A — Reorder middleware (recommended):**
```go
handler := s.recoverMiddleware(s.loggingMiddleware(s.corsMiddleware(basicAuthMiddleware(s.mux))))
```

**Option B — Exempt OPTIONS in auth middleware:**
```go
if r.Method == http.MethodOptions {
    next.ServeHTTP(w, r)
    return
}
```

The `reviewer-backend.md` context documents the expected order as `recover -> logging -> basicAuth -> cors -> mux` and says "Any reorder is a finding." The current order matches the documented order. However, the documented order is itself incorrect for production use with auth enabled. The fix is to change both the code and the documentation.

## References

- MDN CORS Preflight: https://developer.mozilla.org/en-US/docs/Glossary/Preflight_request
- `reviewer-backend.md`: "Middleware order: `recover -> logging -> basicAuth -> cors -> mux`. Any reorder is a finding."
- The scope brief listed verifying the middleware order as a priority. The order matches documentation but the documentation is wrong.
