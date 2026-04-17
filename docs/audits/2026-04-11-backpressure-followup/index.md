# Backpressure follow-up audit — 2026-04-11

## Scope

**Scope string:** `backpressure-followup`

**Interpretation:** Site-by-site map of backpressure coverage across all subprocess/stream reading paths in Nanite. Follow-on from the initial PTY build. Not a subsystem audit — a cross-cutting category audit that touches multiple packages.

**Sites audited (read in full):**

| # | Site | File(s) | Verdict |
|---|------|---------|---------|
| 1 | PTY bridge | `pkg/provider/pty.go` | Bounded scanner (1MB), buffered channel (64), missing ctx-aware send |
| 2 | Subprocess bridge | `pkg/provider/subprocess.go` | Same as PTY |
| 3 | MCP stdio transport | `internal/mcp/stdio_transport.go` | Unbounded `ReadBytes`, goroutine leak on timeout |
| 4 | Plugin subprocess transport | `internal/plugin/subprocess/transport.go` | Unbounded `ReadBytes`, goroutine cleanup on timeout (better than MCP) |
| 5 | Sandbox exec | `internal/sandbox/exec.go` | Well-bounded: `limitedBuffer` (1MB), synchronous, timeout |
| 6 | HTTP MCP transport | `internal/mcp/http_transport.go` | Bounded by 60s `http.Client.Timeout`, `json.Decoder` reads only one object |
| 7 | Provider SSE readers (8 providers) | `pkg/provider/{anthropic,openai,ollama,gemini,mistral,azure_openai,openrouter,openzen}.go` | Anthropic: 1MB scanner buffer. Others: default 64KB scanner (bounded but inconsistent) |
| 8 | Event pipeline | `pkg/provider/event_pipeline.go` | 64-slot buffered channel, ctx-aware select, clean |
| 9 | Event emission / trigger dispatch | `internal/plugin/host.go`, `triggers.go`, `event_stream.go` | Fire-and-forget goroutines, unbounded spawn per event, non-blocking drop on SSE |
| 10 | Presence broadcast | `internal/service/stream.go` | 32-slot buffered channel, non-blocking drop with logging |
| 11 | Message stream | `internal/service/stream.go` | 128-slot buffered channel, clean |

**Sampled:**
- `internal/api/messages.go`, `event_stream.go`, `presence.go` — SSE consumer side
- `internal/service/chat.go` — `generateResponse` stream creation
- `pkg/provider/openai.go` (representative for OpenAI-family SSE readers)

**Skipped:**
- Provider `Complete()` response bodies (unbounded `json.Decoder` + `io.ReadAll` on error paths) — already filed as `provider-abstractions` finding 02
- Handler-side request body limits — already filed as `api-privilege-boundary` finding

## Methodology

**Categories applied:**

| Category | Applied | Notes |
|---|---|---|
| Memory & Resources | Primary | Buffer bounds, read size limits |
| Concurrency | Primary | Goroutine lifecycle, channel blocking, context propagation |
| Backpressure mechanics | Primary | What happens when consumer < producer |
| Security | Secondary | Untrusted input size as DoS vector |
| Error handling | Sampled | Timeout/cancel error paths |
| Standards & Tooling | Deferred | Scoped cross-cutting review — `go vet`, `-race`, etc. deferred to `whole-repo-tooling-and-tests-sweep` |

**Cross-audit preflight:** Read `INDEX.md` and index files for:
- `2026-04-10-mcp-client-transport` — unbounded `bufio.ReadBytes` response, goroutine leak on timeout. Confirmed and extended with explicit backpressure framing.
- `2026-04-11-provider-abstractions` — unbounded response bodies across providers. Confirmed, not re-flagged except for scanner buffer inconsistency (new finding).
- `2026-04-11-panic-recovery-sweep` — goroutine map of all spawn sites. Used as traversal checklist.

## Findings

### By severity

**Critical (0)**
- _none_

**High (2)**
- [01 — MCP stdio transport unbounded ReadBytes with goroutine leak](01-high-stdio-transport-unbounded-readbytes.md)
- [02 — Plugin subprocess transport unbounded ReadBytes](02-high-plugin-subprocess-transport-unbounded-readbytes.md)

**Medium (3)**
- [03 — SSE provider scanners use default 64KB buffer without explicit limit](03-medium-sse-provider-scanners-no-custom-buffer.md)
- [04 — PTY and subprocess bridge channel sends block on slow consumer](04-medium-pty-subprocess-channel-send-blocks-on-slow-consumer.md)
- [05 — Trigger dispatch spawns unbounded goroutines per event](05-medium-trigger-dispatch-unbounded-goroutine-spawning.md)

**Low (1)**
- [06 — Event stream subscriber drops events silently](06-low-event-stream-subscriber-drops-events-silently.md)

**Info (2)**
- [07 — Sandbox exec has proper backpressure controls](07-info-sandbox-exec-well-bounded.md)
- [08 — HTTP MCP transport bounded by http.Client timeout](08-info-http-transport-bounded-by-http-client-timeout.md)

### By topic

**Subprocess stdio reads (unbounded buffers)**
- [01 — MCP stdio transport unbounded ReadBytes](01-high-stdio-transport-unbounded-readbytes.md)
- [02 — Plugin subprocess transport unbounded ReadBytes](02-high-plugin-subprocess-transport-unbounded-readbytes.md)

**SSE/stream reading**
- [03 — SSE provider scanners default buffer](03-medium-sse-provider-scanners-no-custom-buffer.md)
- [08 — HTTP MCP transport bounded by timeout](08-info-http-transport-bounded-by-http-client-timeout.md)

**Channel backpressure (producer > consumer)**
- [04 — PTY/subprocess channel send blocks on slow consumer](04-medium-pty-subprocess-channel-send-blocks-on-slow-consumer.md)
- [06 — Event stream subscriber drops silently](06-low-event-stream-subscriber-drops-events-silently.md)

**Goroutine spawning / event dispatch**
- [05 — Trigger dispatch unbounded goroutines](05-medium-trigger-dispatch-unbounded-goroutine-spawning.md)

**Well-bounded (reference implementations)**
- [07 — Sandbox exec](07-info-sandbox-exec-well-bounded.md)

## Recommended next steps

1. **Fix findings 01 + 02 (High).** Replace `bufio.ReadBytes` with a bounded scanner or `io.LimitReader` wrapper in both `internal/mcp/stdio_transport.go` and `internal/plugin/subprocess/transport.go`. Fix the goroutine leak in MCP stdio transport by closing the reader on timeout (matching what plugin subprocess already does).

2. **Fix finding 04 (Medium).** Add `ctx.Done()` select arms to channel sends in `pkg/provider/pty.go` and `subprocess.go` to ensure cancellation is honored when the channel is full.

3. **Normalize scanner buffers (finding 03).** Add `scanner.Buffer()` calls to the 6 remaining provider SSE readers. Extract a shared helper.

4. **Add dispatch concurrency limit (finding 05).** A semaphore on `TriggerDispatcher` prevents goroutine accumulation under pathological event bursts.

5. **Follow-up scope: `backpressure-integration-test`.** Write integration tests that exercise slow-consumer scenarios for PTY, MCP stdio, and SSE paths. Confirm that context cancellation, timeout, and graceful shutdown all work under backpressure.

## Known issues skipped

- Unbounded `io.ReadAll` on error response bodies across all providers — tracked in `docs/audits/2026-04-11-provider-abstractions/02-high-unbounded-response-bodies.md`.
- No panic recovery in goroutine spawn sites — tracked in `docs/audits/2026-04-11-panic-recovery-sweep/`.
- `scope_guard.go` and `event_pipeline.go` dead code — tracked in `docs/audits/2026-04-10-chat-engine/` and `2026-04-11-provider-abstractions/`.

## Noticed but out of scope

- `internal/service/stream.go:CreateStream` allocates a 128-slot buffered channel per message stream. These are never reclaimed except by GC after `CloseStream` removes the `sync.Map` entry. Under sustained load (many concurrent streams), this is a transient memory spike but not a leak. Follow-up scope: `stream-lifecycle-audit`.
- `internal/memory/extraction.go` spawns fire-and-forget goroutines for per-turn and post-compact extraction with no backpressure. If many turns arrive faster than extraction completes, extractions pile up. Follow-up scope: `memory-extraction-concurrency`.
- `internal/workflow/executor.go` spawns step-handler goroutines without a concurrency limit, similar to the trigger dispatch pattern. Follow-up scope: `workflow-concurrency`.
