# Goroutine spawn + recover map

Authoritative repo-wide enumeration as of 2026-04-11. Line numbers against `audit-campaign-2026-04-11` HEAD. Production tree only (excludes `vendor/`, `_test.go`, generated files).

Severity legend (per the `deep-review` rubric): C = Critical, H = High, M = Medium, L = Low, I = Info. Severities here reflect the gap at the individual spawn site, not the systemic High called in `index.md`.

`cross-ref` tags point to prior audit findings that already flagged the gap. `new` means this sweep is the first audit artifact to write the site down.

---

## internal/server

**Goroutine spawn sites:** none.

**`recover()` call sites:**
- `internal/server/server.go:126` — in `recoverMiddleware` — guards: the outermost HTTP middleware layer (`recover → logging → basicAuth → cors → mux`, composed in `server.go:60`) — active: **yes, this is the only production recover() in the repo**. Catches synchronous panics in every HTTP handler. Does not reach any goroutine spawned by a handler.

**Gap summary:** the only working recover in the tree; correctly positioned; catches synchronous handler panics only.

---

## cmd/nanite

**Goroutine spawn sites:**
- `cmd/nanite/main.go:268` — `go func() { ... signal.Notify ... container.Shutdown() ... os.Exit(0) }()` in `cmdServe` — recover: **NO** → gap severity: **L** → cross-ref: new. (Panic inside `container.Shutdown()` dies instead of shutting down; low impact because the process was exiting anyway.)
- `cmd/nanite/main.go:431` — `go func() { truncate.Cleanup(); ticker ... }()` in `startBackgroundWorkers` — recover: **NO** → gap severity: **M** → cross-ref: new. Long-lived daemon. Panic kills cleanup silently with no restart.
- `cmd/nanite/main.go:442` — `go func() { ticker ... container.Tasks.Snapshot(...) }()` — recover: **NO** → gap severity: **M** → cross-ref: new. Long-lived daemon. Badger→SQLite flush.
- `cmd/nanite/main.go:454` — `go func() { ticker ... container.ProcessTracker.KillStale(...) }()` — recover: **NO** → gap severity: **M** → cross-ref: new. Long-lived daemon. Stale subprocess reaper.
- `cmd/nanite/main.go:466` — `go func() { ticker ... container.Workers.ReapStale(...) }()` — recover: **NO** → gap severity: **M** → cross-ref: new. Long-lived daemon. Stale worker reaper.

**`recover()` call sites:** none.

**Gap summary:** 5 spawn sites, 0 recover. 4 are long-lived background daemons whose silent death means cleanup permanently stops with no visible signal.

---

## internal/server (SSE handlers, not a spawn site)

Note: SSE endpoints (`messages.go`, `presence.go`, `event_stream.go`, `workflows.go`, `plugins.go`) run in the request handler goroutine, not in spawned goroutines. They rely on the server-level `recoverMiddleware` for panic protection. Confirmed via `rg 'go func|\bgo\s' internal/api/messages.go internal/api/presence.go internal/api/event_stream.go` — no spawns. The `api/sessions.go`, `api/agents.go`, `api/artifacts.go`, `api/bookmarks.go`, `api/plugin_config.go`, `api/settings.go` spawns are fire-and-forget event emissions (see `internal/api` below), not SSE.

---

## internal/api

**Goroutine spawn sites:** (all fire-and-forget event emission — pattern: `go a.Services.Plugins.EmitXxx(...)` or `go a.Services.Activity.EmitXxx(...)`; each lands in `Host.EmitEvent` / `ActivityEmitter.Emit*` which themselves spawn more goroutines in `internal/plugin/host.go:1090` and `internal/service/events_composite.go`.)

- `internal/api/artifacts.go:117` — `go a.Services.Plugins.EmitArtifactCreated(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04 (same class)
- `internal/api/artifacts.go:168` — `go a.Services.Plugins.EmitArtifactCreated(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/api/agents.go:245` — `go a.Services.Plugins.EmitAgentSwitched(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/api/bookmarks.go:51` — `go a.Services.Plugins.EmitMessageBookmarked(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/api/bookmarks.go:74` — `go a.Services.Plugins.EmitMessageUnbookmarked(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/api/bookmarks.go:99` — `go a.Services.Plugins.EmitMessageUnbookmarked(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/api/bookmarks.go:127` — `go a.Services.Plugins.EmitMessageBookmarked(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/api/sessions.go:69` — `go a.Services.Activity.EmitSessionCreated(...)` — recover: **NO** → gap severity: **M** → cross-ref: new. Activity emitter path (Engine GUI), not plugin hooks — lower blast radius.
- `internal/api/sessions.go:166` — `go a.Services.Plugins.EmitSessionArchived(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/api/sessions.go:189` — `go a.Services.Activity.EmitSessionEnded(...)` — recover: **NO** → gap severity: **M** → cross-ref: new
- `internal/api/sessions.go:194` — `go a.Services.Plugins.EmitSessionArchived(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/api/sessions.go:236` — `go a.Services.Plugins.EmitModeChanged(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/api/settings.go:140` — `go a.Services.Plugins.EmitConfigChanged("user", ...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/api/plugin_config.go:104` — `go a.Services.Plugins.EmitConfigChanged(pluginID, ...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04

**`recover()` call sites:** none.

**Gap summary:** 14 spawn sites, all fire-and-forget plugin/activity event emissions from HTTP handler paths. Because the spawn is inside `go` from the handler goroutine, the outer `recoverMiddleware` does NOT catch these panics — the goroutine has already detached from the handler stack by the time the panic fires. Every one of these is a plugin-panic → host crash vector.

---

## internal/service (including service/a2a)

**Goroutine spawn sites:**
- `internal/service/chat.go:155` — `go s.pluginHost.EmitMessageSent(...)` in `HandleMessage` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/service/chat.go:166` — `go s.generateResponse(bgCtx, ...)` in `HandleMessage` — recover: **NO** → gap severity: **C** → cross-ref: **chat-engine#02**. This is THE main chat generation path. `generateResponse` (700+ lines) has `defer close(ch)` + `defer cancel()` + `defer span.End()` but no `defer recover()`.
- `internal/service/chat.go:210` — `go s.generateResponse(bgCtx, ...)` in `RetryLastMessage` — recover: **NO** → gap severity: **C** → cross-ref: chat-engine#02
- `internal/service/chat.go:241` — `go s.generateResponse(bgCtx, ...)` in `SendAgentMessage` — recover: **NO** → gap severity: **C** → cross-ref: chat-engine#02
- `internal/service/chat.go:352` — `go s.pluginHost.EmitProviderFallback(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/service/chat.go:359` — `go s.pluginHost.EmitProviderFallback(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/service/chat_generate.go:203` — `go s.pluginHost.EmitContextAssembled(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/service/chat_generate.go:223` — `go s.pluginHost.EmitAgentLoaded(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/service/chat_generate.go:358` — `go s.pluginHost.EmitProviderError(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/service/chat_generate.go:656` — `go s.pluginHost.EmitEnvelopeRendered(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/service/chat_generate.go:760` — `go s.autoTitle(sessionID, userContent)` — recover: **NO** → gap severity: **M** → cross-ref: new. Utility LLM call; fire-and-forget.
- `internal/service/chat_generate.go:762` — `go s.autoTags(sessionID)` — recover: **NO** → gap severity: **M** → cross-ref: new. Utility LLM call; fire-and-forget.
- `internal/service/chat_tool_executor.go:260` — `go func(ip indexedPlan) { ... s.executeSingleTool(...) }(ip)` — recover: **NO** → gap severity: **H** → cross-ref: chat-engine#02 (same goroutine tree). Each tool executes via broker → MCP transport → tool result. Malicious tool result panicking a parser crashes the tool-batch goroutine (and the tool-batch goroutine is itself running inside `generateResponse`).
- `internal/service/delegation.go:123` — `go s.generateResponse(ctx, ...)` in `DelegateTask` — recover: **NO** → gap severity: **C** → cross-ref: chat-engine#02
- `internal/service/delegation.go:264` — `go func(idx int, st chat.SubTask) { ... s.workers.SpawnFull(...) }(i, st)` in `DelegateAndAggregate` — recover: **NO** → gap severity: **H** → cross-ref: new. Worker spawn, plugin-adjacent via adapter plugins.
- `internal/service/events_composite.go:28` — `go c.activity.EmitSessionStart(...)` — recover: **NO** → gap severity: **M** → cross-ref: new
- `internal/service/events_composite.go:31` — `go c.plugin.EmitSessionStart(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/service/events_composite.go:37` — `go c.activity.EmitSessionEnded(...)` — recover: **NO** → gap severity: **M** → cross-ref: new
- `internal/service/events_composite.go:40` — `go c.plugin.EmitSessionEnd(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/service/events_composite.go:46` — `go c.activity.EmitAgentAssigned(...)` — recover: **NO** → gap severity: **M** → cross-ref: new
- `internal/service/events_composite.go:52` — `go c.activity.EmitResponseComplete(...)` — recover: **NO** → gap severity: **M** → cross-ref: new
- `internal/service/events_composite.go:55` — `go c.plugin.EmitMessageSent(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/service/events_composite.go:61` — `go c.activity.EmitToolCall(...)` — recover: **NO** → gap severity: **M** → cross-ref: new
- `internal/service/events_composite.go:65` — `go c.plugin.EmitToolCalled(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/service/events_composite.go:72` — `go c.plugin.EmitToolFailed(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/service/events_composite.go:78` — `go c.activity.EmitRateLimitHit(...)` — recover: **NO** → gap severity: **M** → cross-ref: new
- `internal/service/events_composite.go:84` — `go c.activity.EmitCircuitBreakerTripped(...)` — recover: **NO** → gap severity: **M** → cross-ref: new
- `internal/service/events_composite.go:90` — `go c.activity.EmitContextBudgetExceeded(...)` — recover: **NO** → gap severity: **M** → cross-ref: new
- `internal/service/events_composite.go:96` — `go c.activity.EmitError(...)` — recover: **NO** → gap severity: **M** → cross-ref: new
- `internal/service/events_composite.go:102` — `go c.plugin.EmitMessageReceived(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/service/events_composite.go:108` — `go c.activity.EmitModeChanged(...)` — recover: **NO** → gap severity: **M** → cross-ref: new
- `internal/service/events_composite.go:111` — `go c.plugin.EmitModeChanged(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/service/events_composite.go:117` — `go c.activity.EmitPreCompact(...)` — recover: **NO** → gap severity: **M** → cross-ref: new
- `internal/service/events_composite.go:122` — `go c.plugin.EmitPreHook("context.pre_compact", ...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04. **Note:** `EmitPreHook` itself runs plugin hooks synchronously on the caller's goroutine (`internal/plugin/events.go:574-585`), so the double-`go` here doesn't add a boundary — the hook executes on this spawned goroutine with no recover.
- `internal/service/events_composite.go:132` — `go c.plugin.EmitContextCompacted(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/service/a2a/subscribe.go:62` — `go func() { <-ctx.Done(); ... close(ch) }()` in `pubsub.subscribe` — recover: **NO** → gap severity: **L** → cross-ref: new. Trivial cleanup body; panic unlikely but not impossible (map mutation under lock).

**`recover()` call sites:** none.

**Gap summary:** 36 spawn sites, 0 recover. The chat/service package is the largest panic surface in the repo — includes the three `generateResponse` spawns (C), 14 plugin-emit spawns (H each), and 13 activity-emit spawns (M each). One of the generateResponse paths can transitively spawn `chat_tool_executor.go:260` tool-batch goroutines that run further without protection.

---

## internal/plugin

**Goroutine spawn sites:**
- `internal/plugin/host.go:332` — `go h.EmitWidgetLoaded(...)` in `RegisterUIComponent` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/plugin/host.go:503` — `go h.EmitConfigChanged(id, key, valStr)` in config setter — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/plugin/host.go:939` — `go h.EmitPluginInstalled(...)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/plugin/host.go:1074` — `go h.EmitPluginUninstalled(id)` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04
- `internal/plugin/host.go:1090` — `go func(hook plugin.EventHook) { ... hook.Handle(ctx, event) ... }(hook)` inside `EmitEvent` — recover: **NO** → gap severity: **H** → cross-ref: **plugin-system-plan-eval#04** (primary anchor). THIS is the canonical "plugin hook panic crashes host" site the prior audit already called out.
- `internal/plugin/host.go:1105` — `go h.triggers.Dispatch(event)` inside `EmitEvent` — recover: **NO** → gap severity: **H** → cross-ref: plugin-system-plan-eval#04. `Dispatch` itself spawns more goroutines at `triggers.go:80`.
- `internal/plugin/triggers.go:80` — `go td.sendWithRetry(connector, payload, rule.ID)` — recover: **NO** → gap severity: **H** → cross-ref: new. Spawned from inside the host.go:1105 goroutine. `sendWithRetry` calls plugin-supplied `connector.Send` (`triggers.go:154`) synchronously — any panic in connector code crashes the trigger goroutine.
- `internal/plugin/catalog.go:102` — `go func(s CatalogSource) { cf.fetchSource(ctx, s); ... }(src)` in `CatalogFetcher.Fetch` — recover: **NO** → gap severity: **M** → cross-ref: new. HTTP catalog fetch + JSON parse of untrusted remote response.
- `internal/plugin/subprocess/manager.go:167` — `go func() { waitCh <- cmd.Wait() }()` — recover: **NO** → gap severity: **L** → cross-ref: new. Single-line body, trivial panic surface.
- `internal/plugin/subprocess/manager.go:170` — `go m.waitForExit(stderr)` — recover: **NO** → gap severity: **M** → cross-ref: new. `waitForExit` calls `m.onCrash(exitErr)` — user-configurable crash callback — without protection. Also drives restart loop.
- `internal/plugin/subprocess/manager.go:176` — `go m.healthLoop(hctx)` — recover: **NO** → gap severity: **M** → cross-ref: new. Long-lived health check ticker calling `transport.Call(MethodHealth, ...)` against untrusted plugin subprocess.
- `internal/plugin/subprocess/transport.go:107` — `go func() { line, err := t.r.ReadBytes('\n'); ch <- readResult{line, err} }()` in `Transport.read` — recover: **NO** → gap severity: **M** → cross-ref: new. **Per-call** reader goroutine. Reads untrusted bytes from plugin subprocess. Leaks on timeout (plugin-system-plan-eval#03 closes the pipe, which resolves the leak but not the panic-safety). A panic inside `ReadBytes` would crash this goroutine, but `ReadBytes` on a pipe is stdlib code — unlikely to panic itself. The risk is via `ch <- readResult{}` on a closed channel, which panics.

**Non-goroutine panic boundaries without recover (plugin-supplied code called synchronously):**
- `internal/plugin/events.go:576` — `err := hook.Handle(ctx, event)` inside `EmitPreHook` — plugin hook called synchronously on the caller's goroutine — no recover → gap severity: **H** → cross-ref: plugin-system-plan-eval (adjacent; not flagged there as a synchronous variant). Used by `events_composite.go:122` (pre-compact hook) and potentially by `EventMessageSending` / `EventToolExecuting` (per reviewer-backend context). Caller-side panic.
- `internal/plugin/filter.go:147` — `result, err := entry.Fn(visible, ctx)` inside `FilterRegistry.Apply` — plugin-supplied filter function, synchronous, caller's goroutine — no recover → gap severity: **H** → cross-ref: new. Filter chains execute in six named points across the chat pipeline; a plugin panicking a filter crashes the caller (typically `generateResponse`).
- `internal/plugin/triggers.go:154` — `connector.Send(ctx, payload)` inside `sendWithRetry` — plugin-supplied connector, synchronous, caller goroutine — no recover → gap severity: **H** → cross-ref: new.
- `internal/plugin/subprocess/manager.go:271` (`m.onCrash(exitErr)` inside `waitForExit`) — user-configurable crash callback called synchronously — no recover → gap severity: **M** → cross-ref: new.

**`recover()` call sites:** none.

**Gap summary:** 11 goroutine spawn sites plus 4 synchronous plugin-code call sites. THE highest-density package for plugin-code-crashes-host gaps. The primary `Host.EmitEvent` spawn (`host.go:1090`) is the plugin-system-plan-eval#04 anchor; the rest either funnel through it or are independent parallel paths with identical shape. `EmitPreHook` and `FilterRegistry.Apply` are the two synchronous-call variants that add a distinct class (caller-goroutine panic, not spawned-goroutine panic) — worth an orchestrator note.

---

## internal/chat

**Goroutine spawn sites:** none directly in `internal/chat/`. The chat engine's generation path lives in `internal/service/chat_generate.go` (see §internal/service above). `internal/chat/` contains the orchestrator, envelope, commands, activity types — all synchronous helpers called from `service/` goroutines.

**`recover()` call sites:** none.

**Gap summary:** no spawns in this package. All panic exposure comes from being called by `service/chat_generate.go`'s goroutine — which has no recover. Cross-ref: chat-engine#02.

---

## internal/mcp

**Goroutine spawn sites:**
- `internal/mcp/stdio_transport.go:109` — `go func() { line, err := t.stdout.ReadBytes('\n'); readCh <- readResult{line, err} }()` in `StdioTransport.call` — recover: **NO** → gap severity: **M** → cross-ref: **mcp-client-transport (implicit)**. Reader goroutine for untrusted MCP subprocess. Leaked on timeout per mcp-client-transport finding 01.

**`recover()` call sites:** none.

**Gap summary:** 1 spawn, 0 recover. The entire `internal/mcp/` package (manager, http transport, stdio transport, self_tools_transport, dev_tools, general_tools, memory_tools, code_exec_tools) has zero recover calls despite being a direct trust boundary for tool-arg and tool-result data. Cross-ref `mcp-client-transport` audit's implicit observation.

---

## internal/mcpserver

**Goroutine spawn sites:** none. `mcpserver.Server` handles JSON-RPC over stdio synchronously in `Serve()`.

**`recover()` call sites:** none.

**Gap summary:** no spawn-site gaps. A panic in `buildMCPTool` / `handleListTools` / `handleCallTool` crashes the mcpserver goroutine. Since `nanite mcp` is a separate binary mode (JSON-RPC stdio), a crash ends the subprocess and the external MCP client sees a pipe close — blast radius limited to one session.

---

## internal/memory

**Goroutine spawn sites:**
- `internal/memory/extraction.go:105` — `go h.extractor.extractPerTurn(sessionID, content)` inside `perTurnHook.Handle` — recover: **NO** → gap severity: **M** → cross-ref: new. Utility LLM call + memory store write. Fire-and-forget from plugin event hook.
- `internal/memory/extraction.go:136` — `go h.extractor.extractPostCompact(sessionID, tokensSaved)` inside `postCompactHook.Handle` — recover: **NO** → gap severity: **M** → cross-ref: new. Same shape.

**`recover()` call sites:** none.

**Gap summary:** 2 spawn sites, 0 recover. LLM-output parsing + store writes without panic protection. A malformed JSON that slips past `json.Unmarshal` and panics in a downstream assertion would crash the memory extractor goroutine.

---

## internal/worker

**Goroutine spawn sites:**
- `internal/worker/manager.go:96` — `go m.heartbeatLoop(workerID, heartbeatStop)` in `SpawnFull` — recover: **NO** → gap severity: **M** → cross-ref: new. Per-worker ticker. Long-lived until `cleanup()` closes the stop channel.
- `internal/worker/manager.go:179` — `go func() { time.Sleep(30*time.Second); m.workers.Delete(workerID) }()` — recover: **NO** → gap severity: **L** → cross-ref: new. Trivial body.

**`recover()` call sites:** none.

**Gap summary:** 2 spawn sites, 0 recover. Heartbeat loop is the concerning one — if it panics, the worker appears stuck (no heartbeat updates) until the stale reaper in `cmd/nanite/main.go:466` notices — and that reaper goroutine has no recover either.

---

## internal/workflow

**Goroutine spawn sites:**
- `internal/workflow/executor.go:232` — `go func(s *Step, ss *StepState) { ... e.executeWithRetry(stepCtx, s, stepInput) ... }(step, ss)` — recover: **NO** → gap severity: **H** → cross-ref: new. Step handlers are registered via `Step.Handler` — some handlers are plugin-supplied (via `Host.RegisterTaskBackend` / the plugin SDK). A plugin-supplied step handler panic crashes the workflow engine goroutine.
- `internal/workflow/handlers.go:148` — `go func(idx int, s Step) { ... s.Handler.Execute(ctx, subInput) ... }(i, step)` in `ParallelStep.Execute` — recover: **NO** → gap severity: **H** → cross-ref: new. Same shape — plugin-supplied handler, no protection.

**`recover()` call sites:** none.

**Gap summary:** 2 spawn sites, 0 recover. Workflow steps are a plugin extension point (task backends). Panic-unsafe.

---

## internal/sandbox

**Goroutine spawn sites:**
- `internal/sandbox/proxy.go:50` — `go func() { ... p.server.Serve(ln) ... }()` — recover: **NO** → gap severity: **L** → cross-ref: new. `http.Server` recovers panics in its own handler goroutines (stdlib default), so this goroutine body itself rarely panics — only on bugs in `Serve()` itself, which is effectively zero.
- `internal/sandbox/proxy.go:122` — `go func() { defer copyWg.Done(); io.Copy(targetConn, clientConn) }()` in `handleConnect` CONNECT tunnel — recover: **NO** → gap severity: **L** → cross-ref: new. `io.Copy` doesn't panic on its own; the nested goroutine is in a hijacked connection after the outer handler returns.
- `internal/sandbox/proxy.go:126` — paired copy goroutine — recover: **NO** → gap severity: **L** → cross-ref: new. Same shape.

**`recover()` call sites:** none.

**Gap summary:** 3 spawn sites, 0 recover. Low risk — `http.Server` has its own recover, and the CONNECT copy goroutines hold only `io.Copy` calls. Cataloged for completeness.

---

## internal/coordination

**Goroutine spawn sites:**
- `internal/coordination/badger.go:33` — `go s.gcLoop()` in `NewBadgerStore` — recover: **NO** → gap severity: **M** → cross-ref: new. Long-lived GC goroutine. Panic silently kills the BadgerDB GC loop; ongoing writes would work but disk bloat would grow until the next process restart.

**`recover()` call sites:** none.

**Gap summary:** 1 spawn, 0 recover. Long-lived daemon with silent failure mode.

---

## internal/skill

**Goroutine spawn sites:**
- `internal/skill/context.go:51` — `go func() { done <- cmd.Wait() }()` in dynamic context command runner — recover: **NO** → gap severity: **L** → cross-ref: new. Single-line body; `cmd.Wait()` panic is not a known class.

**`recover()` call sites:** none.

**Gap summary:** 1 spawn, 0 recover. Trivial body.

---

## pkg/provider

**Goroutine spawn sites:** (all are streaming readers for LLM provider responses; each feeds a `StreamEvent` channel consumed by `generateResponse` or a test)
- `pkg/provider/anthropic.go:420` — `go a.readSSEWithTracking(ctx, resp.Body, ch, span)` — recover: **NO** → gap severity: **H** → cross-ref: new. Primary provider path. SSE reader + JSON block parsing of attacker-influenceable LLM output. A parser panic propagates into the channel consumer's goroutine (`generateResponse`).
- `pkg/provider/anthropic.go:448` — `go a.readSSE(ctx, body, inner)` — nested reader inside `readSSEWithTracking` — recover: **NO** → gap severity: **H** → cross-ref: new.
- `pkg/provider/openai.go:95` — `go o.readSSE(ctx, resp.Body, ch)` — recover: **NO** → gap severity: **H** → cross-ref: new
- `pkg/provider/ollama.go:95` — `go o.readStream(ctx, resp.Body, ch)` — recover: **NO** → gap severity: **H** → cross-ref: new
- `pkg/provider/gemini.go:89` — `go g.readSSE(ctx, resp.Body, ch)` — recover: **NO** → gap severity: **H** → cross-ref: new
- `pkg/provider/mistral.go:82` — `go m.readSSE(ctx, resp.Body, ch)` — recover: **NO** → gap severity: **H** → cross-ref: new
- `pkg/provider/azure_openai.go:100` — `go az.readSSE(ctx, resp.Body, ch)` — recover: **NO** → gap severity: **H** → cross-ref: new
- `pkg/provider/openrouter.go:80` — `go o.readSSE(ctx, resp.Body, ch)` — recover: **NO** → gap severity: **H** → cross-ref: new
- `pkg/provider/openzen.go:87` — `go oz.readSSE(ctx, resp.Body, ch)` — recover: **NO** → gap severity: **H** → cross-ref: new
- `pkg/provider/pty.go:126` — `go func() { defer close(ch); defer ptmx.Close(); scanner ... p.adapter.ParseLine(line) ... }()` — recover: **NO** → gap severity: **H** → cross-ref: new. PTY output reader. `adapter.ParseLine` is CLI-adapter specific (claude/codex/gemini/aider/junie/etc.) — each adapter parses its CLI's stream format. A parser panic here crashes the PTY goroutine.
- `pkg/provider/pty.go:195` — `go func() { cmd.Wait(); close(done) }()` inside `killProcess` — recover: **NO** → gap severity: **L** → cross-ref: new. Trivial body.
- `pkg/provider/subprocess.go:111` — `go func() { defer close(ch); ... scanner ... s.adapter.ParseLine(line) ... }()` — recover: **NO** → gap severity: **H** → cross-ref: new. Non-PTY subprocess reader, same shape as `pty.go:126`.
- `pkg/provider/event_pipeline.go:145` — `go func() { defer close(monitoredStream); ... p.shouldTerminate(ctx, event) ... }()` — recover: **NO** → gap severity: **DEAD** → cross-ref: chat-engine#01 (scope_guard / EventReactionPipeline dead code). Not wired into production. If wired, would need recover.
- `pkg/provider/event_pipeline.go:239` — `go func() { defer close(stream); ... p.provider.Complete(...) ... }()` fallback path — recover: **NO** → gap severity: **DEAD** → cross-ref: chat-engine#01. Same dead-code status.

**`recover()` call sites:** none.

**Gap summary:** 14 spawn sites (12 live + 2 dead). Every provider-stream-reader path is panic-unsafe. Because these readers parse LLM response bodies and CLI subprocess output — both of which contain attacker-influenceable content via prompt injection — a malicious response that triggers a parser panic crashes the reader goroutine, closes the stream channel abnormally, and propagates into `generateResponse` (which itself has no recover, so the panic would just kill the generate goroutine when the channel is received in an unexpected way — though technically a panic in goroutine A does not propagate across a channel to goroutine B; rather, A crashes and closes the channel, and B sees `ok == false`).

---

## Packages with no concurrency concerns (no goroutine spawns, no recover calls)

Checked and confirmed empty via the sweep greps:

- `internal/agent/`, `internal/agent/override/`, `internal/agent/builtin/`
- `internal/agentvalidation/`
- `internal/assets/` (embedded assets only)
- `internal/brand/`
- `internal/builders/`
- `internal/chat/` (see note above — synchronous helpers only)
- `internal/config/`
- `internal/context/`
- `internal/contextbroker/`
- `internal/crossapp/`
- `internal/envelope/`
- `internal/filter/`
- `internal/mcpconfig/`
- `internal/permission/`
- `internal/plugin/allplugins/`, `internal/plugin/builtin/*/`, `internal/plugin/scaffold/`
- `internal/secrets/`
- `internal/shell/`
- `internal/store/`, `internal/store/migrations/`
- `internal/task/`
- `internal/tool/`, `internal/tool/broker/`
- `internal/toolclient/`
- `internal/truncate/`
- `internal/version/`
- `internal/worktree/`
- `internal/mcpserver/` (see note above — serve loop is synchronous)

Note: some of these packages are called FROM goroutines spawned elsewhere (e.g., `internal/store/` is called from every spawn above that writes state). Their lack of recover is inherited from the caller's absence, not an intrinsic package concern. A panic inside `store.CreateMessage` called from `generateResponse` is a `generateResponse` panic, not a `store` panic, from the recover-coverage perspective.

---

## Summary counts

- Total production `go` spawn sites: **93**
- Total production `recover()` call sites: **1** (`server.go:126`)
- Spawn sites with recover coverage: **0**
- Gap coverage: **93 / 93 uncovered** (100%)
- Dead-code spawn sites: **2** (`event_pipeline.go:145`, `:239`)
- Spawn sites marked **Critical** (generateResponse): **4** (`chat.go:166`, `:210`, `:241`, `delegation.go:123`)
- Spawn sites marked **High** (plugin-hook / filter / provider stream / tool exec / workflow step): **~50**
- Spawn sites marked **Medium** (background daemons, activity emits, subprocess readers, memory extraction, Badger GC): **~30**
- Spawn sites marked **Low** (trivial bodies, stdlib-recovered paths, dead code): **~9**

Systemic severity (per `index.md`): **High**.
