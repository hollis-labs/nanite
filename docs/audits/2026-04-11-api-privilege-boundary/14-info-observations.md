# [Info] Observations and praise

**Scope:** Full API surface
**Topic:** Various
**Date:** 2026-04-11

## Plugin UI bundle path traversal guard — well done

`internal/api/plugins.go:L63-76` — the plugin UI file serving endpoint has a correct path traversal guard:

```go
baseDir := filepath.Join(pluginsDir, name, "ui")
target := filepath.Clean(filepath.Join(baseDir, file))
if !strings.HasPrefix(target, filepath.Clean(baseDir)+string(filepath.Separator)) && target != filepath.Clean(baseDir) {
    http.Error(w, "forbidden", http.StatusForbidden)
    return
}
```

This is the correct pattern — clean both sides, check prefix with trailing separator. This should be replicated in the artifact and plugin install handlers.

## Archive extraction has zip-slip guards — well done

Both `extractTarGz` (L690-741) and `extractZip` (L744-787) in `plugins.go` check for path traversal in archive entries. This is correct and prevents zip-slip attacks on plugin archive installation.

## Catalog install has checksum + signature verification — well done

`handleCatalogInstall` (catalog.go:L248-421) verifies checksums and Ed25519 signatures when available. The download has a 100MB size limit. This is the most mature install path.

## Agent validation on create/update — good

`handleCreateAgent` and `handleUpdateAgent` both call `agentvalidation.ValidateAgentConfig()` before persisting. This is good input validation practice.

## MCP server import has body size limit

`handleImportMCPServers` uses `io.LimitReader(r.Body, 1<<20)` (1 MB). This is good. The workflow run endpoint similarly limits to 64KB.

## Session-level SSE dedup — good pattern

The message stream handler at `messages.go:L163-169` implements session-level SSE deduplication with takeover semantics. This prevents duplicate streams per session and should be the model for the other SSE endpoints.

## Auth implementation quality

The basic auth implementation uses `crypto/subtle.ConstantTimeCompare` for credential checking, preventing timing attacks. The skip list is correctly restrictive (only `/api/health`). The no-op behavior when credentials are unset is a reasonable dev-mode UX.

## Core plugin uninstall protection

`handleUninstall` correctly checks repos.yaml type and rejects uninstall of core plugins (L427-435). This prevents accidentally removing system plugins via the API.
