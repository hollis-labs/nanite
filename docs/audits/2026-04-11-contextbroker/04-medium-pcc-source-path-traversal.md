# [Medium] PCCSource scope parameter allows path traversal

**Scope:** contextbroker
**Topic:** Security
**Date:** 2026-04-11

## Problem

`PCCSource.resolveProjectDir` joins the user-supplied `intent.Scope` directly with `BasePath` using `filepath.Join` without validating that the resulting path stays within `BasePath`. If `intent.Scope` contains `../` sequences, the resolved path escapes the PCC directory and the source reads arbitrary `.md` files from the filesystem.

## Evidence

```go
// internal/contextbroker/source_pcc.go:L143-L166
func (s *PCCSource) resolveProjectDir(scope string) string {
    if scope == "" {
        // ...auto-detect...
    }

    // Try exact match first.
    dir := filepath.Join(s.BasePath, scope)
    if info, err := os.Stat(dir); err == nil && info.IsDir() {
        return dir
    }

    return ""
}
```

`filepath.Join` normalizes `..` but does not prevent escape. If `scope = "../../etc"`, then `dir = ".nanite/pcc/global/../../etc"` which normalizes to `".nanite/etc"` or further up.

The scope comes from `session.ProjectID` (set in `context_client.go:L324`). In the current codebase, `ProjectID` is a DB-stored value typically set during session creation from the API. A user can set an arbitrary project ID when creating a session.

After resolving the directory, `Fetch` reads all `.md` files in it:

```go
// source_pcc.go:L83-L97
entries, err := os.ReadDir(projectDir)
// ...
for _, entry := range entries {
    if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
        continue
    }
    filePath := filepath.Join(projectDir, entry.Name())
    content, err := os.ReadFile(filePath)
```

## Impact

An attacker who controls `session.ProjectID` (via the session creation API) can read `.md` files from any directory on the filesystem accessible to the process. The content is returned as context items, which flow into the system prompt and are visible in the LLM's response.

Severity is Medium (not High) because: (1) only `.md` files are read, (2) the base path is a relative path so it's relative to cwd (typically the project root), (3) the API is behind basic auth when configured, and (4) in the current single-user deployment model the user has broader filesystem access anyway. In a multi-user deployment this would be High.

## Recommendation

Validate that the resolved path is within the base path:

```go
func (s *PCCSource) resolveProjectDir(scope string) string {
    if scope == "" {
        // ... auto-detect ...
    }

    dir := filepath.Join(s.BasePath, scope)
    // Ensure the resolved path is within BasePath.
    absBase, _ := filepath.Abs(s.BasePath)
    absDir, _ := filepath.Abs(dir)
    if !strings.HasPrefix(absDir, absBase+string(filepath.Separator)) && absDir != absBase {
        return ""
    }

    if info, err := os.Stat(dir); err == nil && info.IsDir() {
        return dir
    }
    return ""
}
```

## References

- `internal/contextbroker/source_pcc.go:L143-L166` — `resolveProjectDir`
- `internal/contextbroker/source_pcc.go:L83-L97` — file read loop
- `internal/chat/context_client.go:L324` — `Scope: session.ProjectID`
- Prior audit: `2026-04-10-chat-engine/06-high-context-broker-unsanitized-injection.md` — the unsanitized injection path this feeds into
