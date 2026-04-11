# [Medium] Grouped: input-handling and error-handling observations

**Scope:** `internal/mcp/dev_tools.go`, `internal/mcp/general_tools.go`
**Topic:** Input validation, error handling, error-message discipline
**Date:** 2026-04-10

A cluster of small but related issues that don't individually justify their own file but collectively shape the trust-boundary posture. Each sub-finding has its own Problem/Evidence/Impact/Recommendation block.

---

## 11.1 — `NewDevToolsTransport` silently drops invalid paths

### Problem

`NewDevToolsTransport` iterates the input `allowedPaths`, calls `filepath.Abs` on each, and **silently drops** any path that returns an error. If every input path is invalid, the returned transport has `AllowedPaths: []` and every `isAllowed` call returns `path %q is outside allowed directories` — but the constructor reports success. The caller has no way to distinguish "tool configured correctly but asked to operate on a disallowed path" from "tool was misconfigured at construction time and nothing is allowed."

### Evidence

```go
// internal/mcp/dev_tools.go:24-33
func NewDevToolsTransport(allowedPaths []string) *DevToolsTransport {
    cleaned := make([]string, 0, len(allowedPaths))
    for _, p := range allowedPaths {
        abs, err := filepath.Abs(p)
        if err == nil {
            cleaned = append(cleaned, abs)
        }
    }
    return &DevToolsTransport{AllowedPaths: cleaned}
}
```

Today the only caller is `cmd/nanite/main.go:L398-L401` passing two `filepath.Join(homeDir, ...)` results — both always succeed on a real `homeDir` — so the bug is latent. It becomes active the first time an operator passes a configurable allowlist from YAML or env.

### Impact

Configuration error silently degrades to "nothing is allowed." Debugging is hard because the error messages at the `isAllowed` boundary blame the caller, not the construction.

### Recommendation

Return `(*DevToolsTransport, error)`:

```go
func NewDevToolsTransport(allowedPaths []string) (*DevToolsTransport, error) {
    if len(allowedPaths) == 0 {
        return nil, fmt.Errorf("dev tools: at least one allowed path required")
    }
    cleaned := make([]string, 0, len(allowedPaths))
    for _, p := range allowedPaths {
        abs, err := filepath.Abs(p)
        if err != nil {
            return nil, fmt.Errorf("dev tools: invalid allowed path %q: %w", p, err)
        }
        // Also verify the directory exists and is readable, to catch typos at boot.
        if info, err := os.Stat(abs); err != nil || !info.IsDir() {
            return nil, fmt.Errorf("dev tools: allowed path %q is not a directory", abs)
        }
        cleaned = append(cleaned, abs)
    }
    return &DevToolsTransport{AllowedPaths: cleaned}, nil
}
```

Update `main.go` to handle the error.

---

## 11.2 — Error messages echo user-supplied paths

### Problem

`isAllowed` and several handlers build error strings that include the user-supplied path verbatim. For a benign call this is helpful; for an attacker probing the filesystem, the echoed path confirms the existence (or non-existence) of arbitrary files outside the allowlist.

### Evidence

```go
// internal/mcp/dev_tools.go:52
return fmt.Errorf("path %q is outside allowed directories", path)
```

```go
// internal/mcp/dev_tools.go:177, 203, 321, 325, 347, 354, 367, etc.
return errorResult(fmt.Sprintf("open: %v", err)), nil
```

`%v` on `os.PathError` includes the full path. A caller with no knowledge of the filesystem can probe `dev_read(path="/etc/shadow")` and learn whether it exists by the error shape (`permission denied` vs `no such file`).

### Impact

Low — the allowlist is enforced, so the information leaked is "file exists at path X" / "file does not exist at path X" on paths the caller already had to supply. Still a gratuitous oracle.

### Recommendation

Centralize error formatting so that disallowed paths return a generic "outside allowed directories" without echoing the path, and `os.Open` / `os.Stat` errors on allowed paths preserve detail. Specifically:

- For `isAllowed` failures, return `path is outside allowed directories` — no `%q`.
- For filesystem errors on *allowed* paths, keep the detail.

Not a hill worth dying on; worth fixing while touching the file.

---

## 11.3 — `intArg` silently returns the default on unexpected types

### Problem

`intArg` accepts `float64` and `json.Number` but silently returns the default on any other input, including an actual `int` (which MCP dispatchers may deliver depending on the transport). The caller can pass `timeout: 300` literally in an MCP request and the value will be silently dropped in favor of the default.

### Evidence

```go
// internal/mcp/dev_tools.go:548-565
func intArg(args map[string]any, key string, def int) int {
    v, ok := args[key]
    if !ok {
        return def
    }
    switch n := v.(type) {
    case float64:
        return int(n)
    case json.Number:
        i, err := n.Int64()
        if err != nil {
            return def
        }
        return int(i)
    default:
        return def
    }
}
```

### Impact

Tool callers may be surprised when explicit integer parameters are ignored. Also hides misrouted types during debugging.

### Recommendation

Add an `int` case and a `string` case with `strconv.Atoi` fallback. Return an error for unrecognized types so the caller sees a clear failure rather than a silent revert to default:

```go
func intArg(args map[string]any, key string, def int) (int, bool) {
    v, ok := args[key]
    if !ok {
        return def, true
    }
    switch n := v.(type) {
    case int:
        return n, true
    case int64:
        return int(n), true
    case float64:
        return int(n), true
    case json.Number:
        i, err := n.Int64()
        if err != nil {
            return def, false
        }
        return int(i), true
    case string:
        i, err := strconv.Atoi(n)
        if err != nil {
            return def, false
        }
        return i, true
    default:
        return def, false
    }
}
```

Every caller then checks `ok` and returns a user-visible error for a bad type. Breaking change, but contained to this one package.

---

## 11.4 — `dev_edit` "first replacement line" calculation is misleading

### Problem

`callEdit` reports the "first at line N" hint by searching for the first line in `content` that contains `strings.Split(oldStr, "\n")[0]`. If `oldStr` is a multi-line block, the hint points at the first line of the match, which is correct. But if `oldStr`'s first line is a common substring — e.g. editing a file with many similar function signatures — the hint can point at an earlier, unrelated match.

### Evidence

```go
// internal/mcp/dev_tools.go:371-384
// Build a summary showing the line number of the first replacement.
lines := strings.Split(content, "\n")
lineNum := 0
for i, line := range lines {
    if strings.Contains(line, strings.Split(oldStr, "\n")[0]) {
        lineNum = i + 1
        break
    }
}
```

### Impact

Informational output is misleading. Not a correctness issue for the edit itself.

### Recommendation

Compute the line number from the actual replacement index. `strings.Index(content, oldStr)` gives the byte offset of the first match; convert that to line number by counting `\n` up to that offset:

```go
idx := strings.Index(content, oldStr)
lineNum := strings.Count(content[:idx], "\n") + 1
```

Or drop the hint entirely — it's a minor convenience.

---

## 11.5 — `parseDotPath` silently mis-parses malformed segments

### Problem

`parseDotPath(".items[bad]")` falls through to `segments = append(segments, pathSegment{key: part, index: -1})` — the whole token `"items[bad]"` becomes a key lookup, not an index. This is "best effort" but opaque: the caller gets "key `items[bad]` not found" instead of "invalid index `bad`." Harder to debug.

### Evidence

```go
// internal/mcp/general_tools.go:260-277
for _, part := range parts {
    // Check for array index: "items[0]"
    if idx := strings.Index(part, "["); idx >= 0 {
        key := part[:idx]
        indexStr := strings.TrimSuffix(part[idx+1:], "]")
        index, err := strconv.Atoi(indexStr)
        if err != nil {
            // Treat as plain key.
            segments = append(segments, pathSegment{key: part, index: -1})
            continue
        }
        ...
    } else {
        segments = append(segments, pathSegment{key: part, index: -1})
    }
}
```

### Impact

Cosmetic. Not a security or data-integrity bug.

### Recommendation

Return an error from `parseDotPath` on malformed segments. `callJSONParse` then surfaces the real reason. Minor improvement, worth a line or two.

---

## References

- `internal/mcp/dev_tools.go:L24-L33` — constructor
- `internal/mcp/dev_tools.go:L36-L53` — `isAllowed`
- `internal/mcp/dev_tools.go:L371-L384` — edit hint
- `internal/mcp/dev_tools.go:L548-L565` — `intArg`
- `internal/mcp/general_tools.go:L251-L280` — `parseDotPath`
