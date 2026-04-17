# [Info] No API keys or passwords found logged directly

**Scope:** Observability — PII in logs
**Topic:** PII in log fields
**Date:** 2026-04-11

## Problem

Not a problem. This finding documents the positive result of the PII-in-logs sweep.

## Evidence

Grepped the entire nanite codebase for patterns where API keys, passwords, tokens, or secrets could appear in log statements:

- `log.Printf.*apiKey`: 0 matches
- `log.Printf.*password`: 0 matches
- `log.Printf.*secret`: 0 matches
- `log.Printf.*credential`: 0 matches
- `slog.String("key", ...)` with sensitive values: 0 matches

Provider registration logs key presence but not values:

```go
// cmd/nanite/main.go:334
log.Printf("%s provider registered (key from keychain)", spec.name)
```

The keyring module logs the key name but not the value:

```go
// internal/secrets/keyring.go:35
log.Printf("secrets: delete %q: %v", key, err)
```

Here `key` is the keyring entry name (e.g., `"nanite-anthropic-api-key"`), not the API key value.

Error messages from provider API calls (e.g., Anthropic 4xx responses) are logged via `err.Error()` on the `APIError` type, which contains `StatusCode` and `Message` (the response body). The `provider-abstractions` audit finding 07 already flags that Anthropic error bodies could theoretically contain reflected API key context. That is a provider-side concern, not a nanite logging pattern concern.

No `slog.String("key", apiKey)` or equivalent patterns exist anywhere.

## Impact

The logging codebase is clean of direct secret exposure. The indirect risks (raw error bodies, raw CLI output lines) are covered by finding 07 in this audit and finding 07-08 in the `provider-abstractions` audit.

## Recommendation

Maintain this discipline. When migrating to `slog` (finding 03), establish a convention that sensitive field names (`api_key`, `password`, `token`, `secret`, `credential`, `authorization`) should never appear as `slog.String` keys with raw values. A custom `slog.Handler` wrapper could enforce this at runtime.

## References

- `cmd/nanite/main.go:L334` — provider registration log
- `internal/secrets/keyring.go:L35` — keyring delete log
- `provider-abstractions` audit findings 07, 08 — indirect key leak risks
