# [High] Structured slog log handler exists but entire codebase uses unstructured log.Printf

**Scope:** Observability — structured logging discipline
**Topic:** Structured logging discipline
**Date:** 2026-04-11

## Problem

The `go-otel` library provides a `NewLogHandler` that wraps any `slog.Handler` to inject `trace_id` and `span_id` from context into every log record. The nanite codebase has 348 `log.Printf` calls across 69 files and exactly 3 `slog` calls in a single file (`internal/task/service.go`). The structured log handler is never instantiated.

This means:
- All log output is unstructured text — no JSON, no key-value pairs, no machine-parseable format
- Logs cannot be correlated with traces (no `trace_id` or `span_id` in log output)
- Log levels are conveyed by ad-hoc string prefixes like `"WARNING:"`, `"PANIC:"`, `"DEBUG"` rather than proper `slog.Level` values
- Log aggregation, filtering, and alerting require regex parsing

## Evidence

The handler exists in the library:

```go
// ../framework/libs/go-otel/logging.go:12-14
func NewLogHandler(inner slog.Handler) slog.Handler {
    return &traceLogHandler{inner: inner}
}
```

It injects `trace_id` and `span_id` into every record when a span context is available.

Nanite's logging reality (representative sample):

```go
// cmd/nanite/main.go:92
log.Printf("warning: OTel init failed: %v", otelErr)

// internal/server/server.go:119
log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))

// internal/server/server.go:127
log.Printf("PANIC: %v", err)

// internal/mcp/manager.go:129
log.Printf("mcp: failed to discover tools from %s: %v", name, err)

// internal/service/chat_generate.go:335
log.Printf("chat-service: tool-use iteration %d — %d tools, %d messages, ~%d tokens (ceiling=%d)",
    ls.iteration, len(tools), len(chatMessages), breakdown.Total, breakdown.Ceiling)
```

The only structured logging in the codebase:

```go
// internal/task/service.go (3 calls)
slog.Info("task created", "id", t.ID, "title", t.Title)
```

Quantified:
- `log.Printf` / `log.Println` / `log.Fatalf`: 348 calls across 69 files
- `fmt.Println` in server code: 0 (only in CLI commands — acceptable)
- `slog.*`: 3 calls in 1 file

## Impact

- **Trace-log correlation is impossible.** Even where spans exist (e.g., `generateResponse` creates a span), the `log.Printf` calls within that function have no trace_id. An operator seeing a log line cannot find the corresponding trace.
- **Log aggregation is fragile.** Parsing `log.Printf("mcp: failed to discover tools from %s: %v", name, err)` requires regex. Structured logging would emit `{"level":"error","msg":"failed to discover tools","server":name,"error":err}`.
- **Log levels are unreliable.** The string `"WARNING:"` in `log.Printf("WARNING: ...")` is not a queryable log level.
- **The `internal/plugin/logger.go` adapter manually prefixes level strings** (`"DEBUG"`, `"INFO"`, `"WARN"`, `"ERROR"`) to `log.Println` output. If the codebase used `slog`, this adapter could delegate directly to `slog.Logger` with proper levels.

## Recommendation

This is a codebase-wide migration. Recommended phased approach:

1. **Phase 1: Wire the handler.** At startup, create a structured slog handler (JSON for production, text for dev), wrap it with `feotel.NewLogHandler`, and set it as the default: `slog.SetDefault(slog.New(feotel.NewLogHandler(slog.NewJSONHandler(os.Stderr, nil))))`.
2. **Phase 2: Migrate hot paths first.** Convert `log.Printf` calls in `internal/service/chat_generate.go` (33 calls), `internal/mcp/manager.go` (16 calls), and `internal/server/server.go` (3 calls) to `slog.Info`/`slog.Error`/`slog.Warn` with context.
3. **Phase 3: Sweep remaining files.** The remaining 296 calls across 65 files can be converted incrementally. A linter rule (`revive` or `forbidigo`) can prevent new `log.Printf` calls.
4. **Plugin logger migration.** Replace `internal/plugin/logger.go`'s manual level prefixing with delegation to a `*slog.Logger`. The `keysAndValues` interface already matches `slog.Attr` patterns.

## References

- `../framework/libs/go-otel/logging.go:L12-41` — the unused handler
- `../framework/libs/go-otel/logging_test.go` — proves the handler works
- `internal/task/service.go` — the one file that uses slog (proves the import is available)
- `internal/plugin/logger.go` — hand-rolled level prefixing that slog would replace
