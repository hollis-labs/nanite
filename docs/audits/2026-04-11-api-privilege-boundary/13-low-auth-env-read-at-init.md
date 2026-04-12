# [Low] Basic auth credentials read from environment once at middleware construction — no rotation

**Scope:** Auth middleware
**Topic:** Security
**Date:** 2026-04-11

## Problem

`basicAuthMiddleware` reads `NANITE_AUTH_USER` and `NANITE_AUTH_PASSWORD` from environment variables when the middleware closure is constructed (server startup). Credential changes require a full server restart.

## Evidence

`internal/server/auth.go:L15-22`:

```go
func basicAuthMiddleware(next http.Handler) http.Handler {
    user := os.Getenv(brand.Env("AUTH_USER"))
    pass := os.Getenv(brand.Env("AUTH_PASSWORD"))

    // If no credentials configured, skip auth entirely (local dev mode).
    if user == "" && pass == "" {
        return next
    }
    // ...
}
```

The `user` and `pass` variables are captured in the closure and never re-read.

## Impact

Low severity. This is expected behavior for most Go services, but worth noting:
1. Credentials cannot be rotated without downtime.
2. If credentials are accidentally set to empty (e.g., env var cleared), the middleware becomes a permanent no-op until restart.

## Recommendation

No immediate action required. If credential rotation becomes a requirement, move the env read inside the handler function (small performance cost per request) or add a config reload mechanism.

## References

- The auth implementation is otherwise well-done: constant-time comparison, proper 401 responses, correct skip logic.
