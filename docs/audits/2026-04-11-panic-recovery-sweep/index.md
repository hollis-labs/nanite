# Panic recovery mini-sweep — 2026-04-11

**Scope:** mini-sweep. Grep `recover()` across the whole Nanite tree at `~/Projects-apps/nanite/`. Produce an authoritative map of every goroutine spawn site, every panic boundary, and every recover gap. Closes the cross-cutting "No panic recovery" theme from `INDEX.md`. Excludes `vendor/`, generated files, and `_test.go` files (panics in tests are intentionally surfaced).

## Methodology

Commands run from repo root:

```
rg -n --type go '^\s*go\s+(func|[a-zA-Z_][a-zA-Z0-9_]*[\(\.])' -g '!vendor/' -g '!*_test.go'
rg -n --type go '\brecover\(\)' -g '!vendor/'
rg -n --type go 'panicSafe|safeInvoke|withRecover|SafeGo|defer.*recover|SafeCall|panicRecover|RecoverPanic' -g '!vendor/'
```

**Supplementary reads:** `internal/server/server.go` (middleware chain), `internal/plugin/host.go` (EmitEvent, EmitPreHook, ApplyFilter), `internal/plugin/filter.go` (filter chain runner), `internal/plugin/triggers.go` (trigger dispatcher), `internal/plugin/subprocess/manager.go` (process lifecycle), `internal/plugin/subprocess/transport.go` (RPC read goroutine), `internal/mcp/stdio_transport.go`, `internal/service/chat.go` + `internal/service/chat_generate.go` + `internal/service/delegation.go` + `internal/service/chat_tool_executor.go`, `internal/service/events_composite.go`, `internal/memory/extraction.go`, `internal/worker/manager.go`, `internal/workflow/executor.go` + `handlers.go`, `internal/service/a2a/subscribe.go`, `internal/sandbox/proxy.go`, `internal/skill/context.go`, `internal/coordination/badger.go`, `internal/plugin/catalog.go`, `cmd/nanite/main.go`, `pkg/provider/pty.go` + `subprocess.go` + `ollama.go` + `openai.go` + `anthropic.go` + `event_pipeline.go`.

No tooling commands (`go vet`, `-race`, etc.) — this is a grep-driven sweep, not a tests-and-tooling pass. `whole-repo-tooling-and-tests-sweep` (queued item 35) is the right place for that.

## Headline

**The Nanite tree contains exactly ONE `recover()` call in production code: `internal/server/server.go:126` — the HTTP middleware recover layer.** It is the outermost middleware (`recover → logging → basicAuth → cors → mux`), so it catches panics in synchronous HTTP handler code. It does NOT cover any of the 93 goroutines spawned from production code, and it does NOT cover any code path that is not an HTTP request handler (background workers, memory extraction, worker manager, chat/generateResponse, MCP stdio transports, plugin filter chains, plugin subprocess transports, etc.).

There are **zero** helper wrappers named `panicSafe`, `safeInvoke`, `withRecover`, `SafeGo`, `SafeCall`, `panicRecover`, or `RecoverPanic` anywhere in the tree. The "defense layer by name" grep came back empty — unlike `scope_guard.go`, where the layer was dead-code-by-name, here the layer simply does not exist in any form. **This is not "a gap" — it is the absence of any panic-recovery story.**

## Overall severity

**High (systemic).** A single panic in plugin-supplied code (filter handler, event hook, connector, trigger dispatcher, task backend) or in any goroutine that touches untrusted input (LLM-generated tool args, MCP tool result, PTY line, plugin RPC response) crashes the nanite host process. The three trust boundaries the reviewer-backend context explicitly flags — plugin code, MCP tool results, LLM-generated tool arguments — are all routed through goroutines that have zero recover coverage. The server's HTTP middleware recover provides a misleading sense of robustness: it protects the synchronous handler goroutine and nothing downstream of an event emission, a fire-and-forget spawn, or a goroutine spawned by `generateResponse`. The severity is not a single file-local Critical; it is a High-severity cross-cutting architectural absence.

Individual gap severities vary by blast radius — per-package notes in `goroutine-map.md`.

## Summary of gaps

Headline-level groupings, full per-package detail in `goroutine-map.md`:

- **Plugin extension surface (highest blast radius).** `Host.EmitEvent` goroutine (`internal/plugin/host.go:1090`) runs plugin `hook.Handle` with no recover. `Host.EmitPreHook` (`events.go:576`) runs `hook.Handle` synchronously on the caller's goroutine — no recover. `FilterRegistry.Apply` (`filter.go:147`) runs plugin `entry.Fn` with no recover. `TriggerDispatcher.sendWithRetry` (`triggers.go:150`) calls plugin `connector.Send` in a spawned goroutine with no recover. Any panic in any loaded plugin's hook/filter/connector/trigger code crashes the host.
- **Chat/generation core.** `chatServiceImpl.generateResponse` is spawned from `internal/service/chat.go:166` / `:210` / `:241` and `delegation.go:123`, with `defer close(ch)` but no `defer recover()`. Any panic anywhere in the 700-line function (which calls provider code, plugin filters, tool executors, context broker sources, envelope capture, auto-title/auto-tag) propagates to the top of the goroutine and crashes the host. Already tracked as `chat-engine` finding 02.
- **Fire-and-forget event emission.** `internal/service/events_composite.go` spawns 20 goroutines (one per emit helper) that each call `activity.Emit*` or `plugin.Emit*`. The `plugin.Emit*` paths land in `Host.EmitEvent`, which spawns more goroutines running plugin hooks. Each emit is a naked `go ...()`, no recover. Same shape in `internal/api/sessions.go`, `internal/api/agents.go`, `internal/api/artifacts.go`, `internal/api/bookmarks.go`, `internal/api/plugin_config.go`, `internal/api/settings.go`, `internal/service/chat.go`, `internal/service/chat_generate.go`. 20+ call sites.
- **MCP stdio transport reader goroutine.** `internal/mcp/stdio_transport.go:109` spawns a reader that blocks on `t.stdout.ReadBytes('\n')`. The reader handles bytes from an untrusted MCP server subprocess. No recover. Known from `mcp-client-transport` audit's implicit finding (not a numbered finding there, but the "zero recover() in all of internal/mcp/" observation).
- **Plugin subprocess transport reader.** `internal/plugin/subprocess/transport.go:107` — structurally identical to MCP stdio: reads untrusted bytes from a plugin subprocess, no recover. No prior finding; this sweep catalogs it for the first time.
- **Plugin subprocess manager lifecycle.** `internal/plugin/subprocess/manager.go:167` (waitCh writer), `:170` (`waitForExit`), `:176` (`healthLoop`) are long-lived goroutines for plugin subprocess management. None have recover. `waitForExit` calls `m.onCrash(exitErr)` — user-configurable crash callback — without protection.
- **Background workers in `startBackgroundWorkers`.** `cmd/nanite/main.go:431` / `:442` / `:454` / `:466` — four long-lived tickers for truncate cleanup, task snapshot, stale process reaper, stale worker reaper. Any panic from `container.Tasks.Snapshot`, `container.ProcessTracker.KillStale`, or `container.Workers.ReapStale` brings down the whole cleanup goroutine silently — and since these are per-binary daemons, there is no restart path.
- **Signal shutdown handler.** `cmd/nanite/main.go:268` — the SIGINT/SIGTERM handler goroutine calls `container.Shutdown()` + `os.Exit(0)`. No recover. A panic inside `container.Shutdown()` means the process neither shuts down cleanly nor exits — it dies on the panic, which is arguably acceptable here, but worth cataloging.
- **Memory extraction hooks.** `internal/memory/extraction.go:105` / `:136` spawn `extractPerTurn` / `extractPostCompact` as fire-and-forget. These call the utility LLM and write to the memory store. LLM response is attacker-influenceable via prompt injection; the write path has no recover.
- **Workflow engine step goroutines.** `internal/workflow/executor.go:232` and `internal/workflow/handlers.go:148` spawn step-handler goroutines. `step.Handler.Execute` can be a plugin-supplied handler (via task backend registration). No recover. A step-handler panic crashes the workflow engine goroutine.
- **Worker manager cleanup goroutine.** `internal/worker/manager.go:179` — anonymous `go func() { time.Sleep(30*time.Second); m.workers.Delete(workerID) }()`. Trivial body, low risk, but cataloged for completeness.
- **Catalog fetcher parallel goroutines.** `internal/plugin/catalog.go:102` spawns parallel fetchSource goroutines. `fetchSource` parses JSON from remote HTTP sources — an attacker controlling a catalog response could influence parsing. No recover.
- **A2A pubsub unsubscribe goroutine.** `internal/service/a2a/subscribe.go:62` waits on `ctx.Done()` and removes a subscriber. Trivial, low risk.
- **Sandbox proxy goroutines.** `internal/sandbox/proxy.go:50` runs `http.Server.Serve` (stdlib recovers in its own handler goroutine), `:122` / `:126` run `io.Copy` pipe goroutines (no plugin code, low risk — but `io.Copy` panicking on a nil reader is a class of bug that can happen on hijack failures).
- **Provider SSE readers.** `pkg/provider/*.go` — Anthropic, OpenAI, Ollama, Gemini, Mistral, Azure, OpenRouter, OpenZen, PTY, subprocess. 14 goroutine spawns total. All read streaming HTTP or subprocess output from the LLM provider. Each one parses JSON-like data that could be malformed. None have recover. The `StreamEvent` channels these feed into are consumed by `generateResponse`, so a panic here crashes `generateResponse`'s goroutine.
- **Skill context dynamic command.** `internal/skill/context.go:51` — `go func() { done <- cmd.Wait() }()`. Trivial body.
- **Badger GC loop.** `internal/coordination/badger.go:33` — long-lived GC goroutine. Calls into Badger; a panic there silently disables GC.

## Cross-references to prior audits

| Prior audit | Finding | Where it reappears in the map |
|---|---|---|
| `2026-04-10-plugin-system-plan-eval` | Finding 04 (Host.EmitEvent no panic recovery) | `internal/plugin/host.go:1090` in `goroutine-map.md`, marked `cross-ref: plugin-system-plan-eval#04` |
| `2026-04-10-chat-engine` | Finding 02 (zero recover() in generateResponse path) | `internal/service/chat.go:166/210/241` and `internal/service/delegation.go:123` in the map, marked `cross-ref: chat-engine#02` |
| `2026-04-10-mcp-client-transport` | Implicit observation ("zero recover() in all of internal/mcp/") | `internal/mcp/stdio_transport.go:109` in the map, marked `cross-ref: mcp-client-transport (implicit)` |

These three are NOT re-flagged as new findings. They anchor the map so the orchestrator can see the known gaps live in the same systemic absence the other 90 spawn sites confirm.

## New gaps cataloged by this sweep (not in any prior audit)

These are the spawn sites / panic boundaries where the absence of recover is being written down for the first time in an audit artifact. **This sweep does not file them as individual deep-review findings** — the deliverable shape is the map, not per-finding folders. The orchestrator decides whether any of these get promoted to standalone findings or rolled into the cross-cutting theme entry.

1. **`Host.EmitPreHook` synchronous plugin call (`internal/plugin/events.go:576`)** — a pre-hook is called on the caller's own goroutine. The chat `generateResponse` path calls `EmitPreHook("context.pre_compact", ...)` via `events_composite.go:122`. A pre-hook panic crashes the caller's goroutine, which in this path is `generateResponse` — already cross-referenced to `chat-engine#02`, but the synchronous-execution property is the distinct failure mode.
2. **`FilterRegistry.Apply` synchronous plugin-supplied filter (`internal/plugin/filter.go:147`)** — same shape as EmitPreHook. Filter handlers are plugin-supplied, called synchronously, no recover. Chat engine's 6 named filter points (`message.outgoing`, `response.incoming`, etc.) each run plugin code that can panic the caller goroutine.
3. **`TriggerDispatcher.sendWithRetry` connector panic (`internal/plugin/triggers.go:150`)** — spawned from `triggers.go:80` inside another spawn from `host.go:1105`. Two levels of goroutine, neither with recover, calling `connector.Send` which is plugin-supplied.
4. **Plugin subprocess `Transport.read` reader goroutine (`internal/plugin/subprocess/transport.go:107`)** — not noted in the plugin audit. Reader blocks on `t.r.ReadBytes('\n')` from an untrusted plugin subprocess. Panic → crash host. This is one of the bytes-in trust boundaries from the reviewer-backend context.
5. **Plugin subprocess `healthLoop` / `waitForExit` / waitCh writer (`internal/plugin/subprocess/manager.go:167/170/176`)** — three long-lived goroutines. `waitForExit` calls `m.onCrash` (user-supplied crash callback) without protection.
6. **Background worker goroutines in `startBackgroundWorkers` (`cmd/nanite/main.go:431/442/454/466`)** — four ticker goroutines. No recover. Silent death if the ticker body panics.
7. **Memory extraction hooks (`internal/memory/extraction.go:105/136`)** — fire-and-forget LLM extraction. No recover. A panic in `extractPerTurn` (which unmarshals LLM JSON output and writes to the store) crashes the extractor goroutine.
8. **Workflow step handlers (`internal/workflow/executor.go:232`, `internal/workflow/handlers.go:148`)** — step handlers are pluggable via task backend registration. No recover. A plugin-supplied step handler panic crashes the workflow engine.
9. **Catalog fetcher (`internal/plugin/catalog.go:102`)** — parallel remote HTTP fetches parsing JSON from untrusted catalog sources.
10. **Provider SSE readers (`pkg/provider/*.go`, 14 spawn sites)** — none have recover. A malformed or malicious provider response that triggers a parser panic crashes the read goroutine and closes the stream channel abnormally, propagating into `generateResponse`'s loop.
11. **`events_composite.go`'s 20 naked `go c.plugin.Emit*` calls** — each lands in `Host.EmitEvent`, which spawns more plugin-hook goroutines. Every event emission in the product is a panic surface. This class is already covered by cross-ref `plugin-system-plan-eval#04`, but the 20 call sites in `events_composite.go` aren't individually enumerated anywhere, so they appear in the map.

## Noticed but out of scope

Observations made during the sweep that are not panic-recovery gaps but are worth spawning as queue entries:

- **`internal/plugin/catalog.go:102` spawns `len(sources)` goroutines unbounded.** If a user configures 1000 catalog sources, this fan-outs 1000 parallel HTTP requests. No worker pool, no per-source rate limit. Not a recover gap — a resource/fan-out concern. Candidate queue entry: `plugin-catalog-fetcher` audit (concurrency + HTTP).
- **`internal/worker/manager.go:179` uses `go func() { time.Sleep(30*time.Second); ... }()` as a deferred delete.** This leaks one goroutine per worker for 30s per worker. Under shutdown, these goroutines do not observe cancellation — `m.workers.Delete` runs regardless. Not a panic concern, but a shutdown-cleanliness concern.
- **`pkg/provider/event_pipeline.go:145` and `:239` goroutines are in dead code** per `chat-engine` audit (`scope_guard.go` / `EventReactionPipeline` not wired into production). They appear in the map with a `dead` marker. If the pipeline is ever wired up, it needs recover.
- **`internal/mcp/stdio_transport.go:109`'s reader goroutine is leaked on timeout.** This is `mcp-client-transport` finding 01 (subprocess/goroutine/FD leak per timeout). Out of scope here — mentioned only because the spawn site is the same file:line.
- **`internal/plugin/host.go:1105` spawns `h.triggers.Dispatch(event)` inside `EmitEvent`'s goroutine.** `Dispatch` then spawns more goroutines per rule (`triggers.go:80`). Two levels of unbounded spawn per event. Not a recover gap — a spawn-amplification concern. Candidate queue entry note: every event dispatches to (unbounded) rule count, not a per-event worker pool.
- **Middleware recover catches panics in synchronous handler code, but logs only `%v` and returns a generic 500.** No structured logging, no stack trace capture, no OTel span recording. Operators debugging a panicked handler have only the panic value line. Minor — `observability` scope covers it.
- **`internal/sandbox/proxy.go:122`/`:126` `io.Copy` goroutines** ignore the copy error unconditionally. Not a panic issue — a silent-error issue. Covered by future sandbox/proxy audit if filed.

## Cross-cutting observations — theme update proposal

The existing "Concurrency teardown" theme in `INDEX.md` is adjacent but not the same thing. Panic recovery and cancellation teardown are distinct concerns; this sweep specifically calls out the first. Proposed new theme entry for the orchestrator to add to `INDEX.md` "Cross-cutting themes" (the orchestrator is the INDEX.md editor — this sweep does not touch that file):

> **No panic recovery anywhere except HTTP middleware.** Exactly one `recover()` call exists in production code (`internal/server/server.go:126`), in the outermost HTTP middleware. Every other goroutine in the tree — 93 production spawn sites covering plugin extension hooks, provider SSE readers, subprocess transports, chat generation, background workers, event emission, memory extraction, workflow step handlers — runs without recover. The absence is total, not partial. This is not a missing helper (no `safeInvoke` / `SafeGo` / `panicSafe` exists to be called) — the project has no panic-recovery story at all. Cross-cut to every audit that touches plugin-provided code being executed by host goroutines (plugin-system-plan-eval, chat-engine, mcp-client-transport, and the plugin-capability-model scope when it runs). Concrete refactor proposal: a `internal/safego` primitive (`safego.Go(ctx, label, fn)` + `safego.Call(fn)`) that wraps every goroutine spawn and every plugin-code invocation in a recover + structured log + OTel event, adopted via a golangci-lint custom rule that forbids bare `go ` in specific packages. Orthogonal to the `pathsafe.ResolveUnder` helper proposed by the dev-tools audit — both would benefit from being primitives that the codebase can lint-enforce.

The theme should replace the weaker "any panic in an event hook currently propagates" line in `internal/plugin/` §Events in the reviewer-backend context — that line is now known to be an understatement: _every_ goroutine propagates panics, not just event hooks.

## Deliverable files in this folder

- `index.md` — this file
- `goroutine-map.md` — per-package enumeration of every spawn site + every recover site + gap summary
