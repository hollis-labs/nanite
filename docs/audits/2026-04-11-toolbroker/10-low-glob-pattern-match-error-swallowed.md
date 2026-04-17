# [Low] MatchPattern swallows path.Match errors

**Scope:** internal/toolclient
**Topic:** Error handling
**Date:** 2026-04-11

## Problem

`MatchPattern()` discards the error from `path.Match()`, treating any malformed pattern as a non-match.

## Evidence

`internal/toolclient/permissions.go:L150-L157`:

```go
func MatchPattern(pattern, name string) bool {
	// Handle prefix glob: "mcp__engine__*" matches "mcp__engine__engine_task_create"
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(name, strings.TrimSuffix(pattern, "*"))
	}
	matched, _ := path.Match(pattern, name)  // error discarded
	return matched
}
```

`path.Match` returns `ErrBadPattern` when the pattern is syntactically invalid (e.g., unmatched `[` brackets). By discarding the error, a malformed pattern in an allow list or deny list silently fails to match, which could cause tools to be incorrectly allowed or denied.

## Impact

Low practical impact because most patterns in the wild use the prefix-glob shortcut (`*` suffix) which is handled by the `strings.HasPrefix` branch and never reaches `path.Match`. However, if an administrator uses a bracket expression like `mcp__engine__engine_task_[create` (unmatched bracket), the pattern silently becomes a no-op instead of flagging the configuration error.

## Recommendation

Log the error from `path.Match`:

```go
matched, err := path.Match(pattern, name)
if err != nil {
    log.Printf("toolclient: WARNING malformed glob pattern %q: %v", pattern, err)
    return false
}
return matched
```

## References

- `internal/toolclient/permissions.go:L150-L157` — `MatchPattern()`
- Go stdlib `path.Match` documentation — `ErrBadPattern` on invalid syntax
