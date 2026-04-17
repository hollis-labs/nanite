# [Medium] Default model fallback scattered across 6 call sites

**Scope:** model resolution
**Topic:** Antipatterns
**Date:** 2026-04-11

## Problem

The fallback model `claude-sonnet-4-20250514` is hardcoded as a string literal in 6 independent locations. When the default model changes (e.g., a new Sonnet release), every site must be found and updated manually. This is a maintenance debt pattern, not a bug today.

## Evidence

Each of the following independently falls back to `"claude-sonnet-4-20250514"`:

1. `cmd/nanite/main.go:160` — utility model resolution
2. `internal/service/chat_generate.go:120` — generate response model resolution
3. `internal/service/delegation.go:54` — worker delegation model resolution
4. `internal/service/chat.go:116` — ChatService constructor default
5. `internal/service/container.go:417` — memory extraction utility model
6. `pkg/provider/anthropic.go:291` and `:678` — Anthropic provider default (provider-scoped, arguably correct)

Additionally, `pkg/provider/openzen.go:46` and `:175` default to the same model string for OpenZen.

The `internal/builders/agent_builder.go:63,83` also hardcodes it as the interactive prompt default and empty-string fallback.

Total: **10 distinct locations** referencing the same string literal with no shared constant.

## Impact

When Anthropic releases a new default model (e.g., `claude-sonnet-4.5-20260101`), a developer must grep for the old model ID and update all 10 sites. Missing one creates silent inconsistency: some code paths use the old model, others the new one. The builder interactive prompt would show a stale suggestion.

## Recommendation

Extract a single constant:

```go
// internal/defaults/defaults.go (or wherever project constants live)
const DefaultModel    = "claude-sonnet-4-20250514"
const DefaultProvider = "anthropic"
```

All 6+ fallback sites reference the constant. Provider-internal defaults (anthropic.go, openzen.go) can use a provider-scoped constant that may differ from the system default.

## References

- `cmd/nanite/main.go:160`
- `internal/service/chat_generate.go:120`
- `internal/service/delegation.go:54`
- `internal/service/chat.go:116`
- `internal/service/container.go:417`
- `pkg/provider/anthropic.go:291`, `:678`
- `pkg/provider/openzen.go:46`, `:175`
- `internal/builders/agent_builder.go:63`, `:83`
