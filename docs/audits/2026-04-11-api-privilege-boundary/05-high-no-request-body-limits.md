# [High] Most POST/PUT handlers have no request body size limit

**Scope:** All API handlers
**Topic:** Security
**Date:** 2026-04-11

## Problem

The shared `a.decode(r, &v)` helper calls `json.NewDecoder(r.Body).Decode(v)` on the raw request body with no size limit. The same pattern appears in plugin management handlers and catalog handlers that use `json.NewDecoder(r.Body).Decode(...)` directly. Only 3 endpoints out of 50+ POST/PUT handlers apply any body size limit.

## Evidence

`internal/api/api.go:L317-320` — shared decode helper, no limit:

```go
func (a *API) decode(r *http.Request, v any) error {
    defer r.Body.Close()
    return json.NewDecoder(r.Body).Decode(v)
}
```

**Endpoints with limits (good):**
- `handleImportMCPServers`: `io.LimitReader(r.Body, 1<<20)` (1 MB) — `mcp_servers.go:138`
- `handleRunWorkflow`: `io.LimitReader(r.Body, 64*1024)` (64 KB) — `workflows.go:84`
- `handleUploadArtifact` / `handleInstallArchive`: `r.ParseMultipartForm(32<<20)` (32 MB) — multipart form limit

**Endpoints without limits (sample of highest-risk):**
- `handleSendMessage` — message content can be arbitrarily large
- `handleCreateAgent` — system_prompt, description, settings, constraints, tools all unbounded
- `handleUpdateSettings` — ext_settings map unbounded
- `handleInstall` / `handleInstallLocal` — plugin management, unbounded
- `handleEmitEvent` — event data map unbounded
- `handleCreateMCPServer` — command, args, env, URL all unbounded
- `handleShellExec` — command string unbounded

## Impact

An attacker can send a multi-gigabyte POST body to any unprotected endpoint, consuming server memory until OOM. This is particularly acute because:
1. Go's `json.Decoder` reads the entire body into memory for objects/maps.
2. The server has no read timeout (see finding 04), so the attacker can stream the body slowly to maximize resource holding time.
3. Multiple concurrent requests multiply the impact.

## Recommendation

Apply `http.MaxBytesReader` in the shared decode helper:

```go
func (a *API) decode(r *http.Request, v any) error {
    r.Body = http.MaxBytesReader(nil, r.Body, 1<<20) // 1 MB default
    defer r.Body.Close()
    return json.NewDecoder(r.Body).Decode(v)
}
```

For the plugin management and catalog handlers that bypass `a.decode`, apply `http.MaxBytesReader` individually. For file upload endpoints that need larger limits, use the multipart form limit (already in place). For the message send endpoint, if messages genuinely need to be large, use a higher limit (e.g., 10 MB) but still enforce one.

## References

- CWE-400: Uncontrolled Resource Consumption
- `mcp-client-transport` audit finding: "no response-body size cap" — same DoS class, different direction (inbound vs outbound).
- Related: `04-high-no-http-server-timeouts.md` (compounds this vector)
