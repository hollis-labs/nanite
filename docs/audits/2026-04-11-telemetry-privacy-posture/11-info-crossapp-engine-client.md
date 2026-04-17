# [Info] CrossApp engine client sends UI commands to local Engine server

**Scope:** Cross-app communication
**Topic:** Outbound network — internal services
**Date:** 2026-04-11

## Problem

No issue. This is a documentation finding for completeness.

## Evidence

`internal/crossapp/engine_client.go:L26-31`:
```go
func engineAPIURL() string {
    if u := os.Getenv("ENGINE_API_URL"); u != "" {
        return u
    }
    return "http://127.0.0.1:8085"
}
```

Sends UI navigation/refresh commands to a local Engine instance. Default: `localhost:8085`. Can be overridden via `ENGINE_API_URL`.

Data sent: `{"type":"navigate","target":"#/path","params":{...}}` — UI routing commands only. No user content.

## Impact

Default is localhost. Only activated when Engine is running locally. If `ENGINE_API_URL` points to a remote host, UI command payloads (page names, filter parameters) leave the machine. No conversation content involved.

## Recommendation

No action required. This is expected localhost-to-localhost communication for the Engine integration.

## References

- `internal/crossapp/engine_client.go:L1-98`
