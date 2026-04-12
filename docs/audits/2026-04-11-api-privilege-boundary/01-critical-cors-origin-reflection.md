# [Critical] CORS reflects any Origin header, not just localhost

**Scope:** HTTP middleware
**Topic:** Security
**Date:** 2026-04-11

## Problem

The CORS middleware echoes back any non-empty `Origin` header as `Access-Control-Allow-Origin` with `Access-Control-Allow-Credentials: true`. This is not restricted to localhost or development mode.

## Evidence

`internal/server/server.go:L97-113`

```go
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        origin := r.Header.Get("Origin")
        // Allow localhost origins for development.
        if origin != "" {
            w.Header().Set("Access-Control-Allow-Origin", origin)
            w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
            w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
            w.Header().Set("Access-Control-Allow-Credentials", "true")
        }
        // ...
    })
}
```

The comment says "Allow localhost origins for development" but the code does not check whether the origin is actually localhost. Any origin — including `https://evil.com` — gets reflected back with credentials allowed.

## Impact

When basic auth is enabled (production mode), a malicious website can make cross-origin credentialed requests to the Nanite API if the user has an active session in their browser. The browser will attach Basic Auth credentials (cached from a prior authenticated request) to cross-origin requests because `Access-Control-Allow-Credentials: true` is set. This enables:

1. Full API access from any website the user visits — reading sessions, messages, settings, installing plugins, executing shell commands.
2. Plugin installation from a malicious page (`POST /api/plugins/install`) which runs arbitrary code in-process.
3. Shell command execution via `POST /api/sessions/{id}/shell-exec`.

When basic auth is NOT enabled (local dev mode), the CORS policy is moot since there's no auth to bypass, but the intent to restrict to localhost is clearly documented and not implemented.

## Recommendation

Replace the unconditional origin reflection with an actual localhost check:

```go
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        origin := r.Header.Get("Origin")
        if origin != "" && isLocalhostOrigin(origin) {
            w.Header().Set("Access-Control-Allow-Origin", origin)
            w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
            w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
            w.Header().Set("Access-Control-Allow-Credentials", "true")
        }
        // ...
    })
}

func isLocalhostOrigin(origin string) bool {
    u, err := url.Parse(origin)
    if err != nil { return false }
    host := u.Hostname()
    return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
```

If non-localhost origins need to be supported in production, use an explicit allowlist configured via environment variable, not origin reflection.

## References

- OWASP CORS Misconfiguration: https://owasp.org/www-project-web-security-testing-guide/latest/4-Web_Application_Security_Testing/11-Client-side_Testing/07-Testing_Cross_Origin_Resource_Sharing
- The `reviewer-backend.md` context explicitly flags CORS config at `server.go:93-109` as a review priority.
- Related: `02-critical-emit-event-unvalidated.md` (the event emission endpoint is reachable via this CORS hole).
