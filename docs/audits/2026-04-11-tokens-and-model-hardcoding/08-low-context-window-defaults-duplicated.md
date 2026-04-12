# [Low] Context window default (200000) duplicated in three packages

**Scope:** context budget computation
**Topic:** Antipatterns
**Date:** 2026-04-11

## Problem

The default context window size of 200,000 tokens is defined as separate constants in two packages and appears as a raw literal in provider Capabilities() methods.

## Evidence

```go
// internal/toolclient/config.go:13
const DefaultContextWindowTokens = 200000

// internal/chat/context_client.go:22
const DefaultContextWindow = 200000
```

Both are used as fallbacks when no provider-specific context window is available. They represent the same value for the same purpose but are defined independently.

Additionally, `pkg/provider/anthropic.go:769`, `openrouter.go:232`, and `openzen.go:239` hardcode `200000` as the context window in their Capabilities() return values.

## Impact

Minimal today. The values agree. If the default context window needs to change (e.g., a future provider with a smaller default is added), the independent constants would need to be updated in multiple places.

## Recommendation

Extract a single `DefaultContextWindowTokens` constant in a shared location (e.g., the provider package, since it defines the semantics). Both `toolclient` and `chat` packages import from the shared constant.

## References

- `internal/toolclient/config.go:L13`
- `internal/chat/context_client.go:L22`
- `pkg/provider/anthropic.go:L769`
- `pkg/provider/openrouter.go:L232`
- `pkg/provider/openzen.go:L239`
