# Concurrency lifecycle map — per-package

Authoritative cancellation/teardown enumeration as of 2026-04-11. Line numbers against `audit-campaign-2026-04-11` HEAD. Production tree only (excludes `vendor/`, `_test.go`, generated files).

Walks the 93 spawn sites from `docs/audits/2026-04-11-panic-recovery-sweep/goroutine-map.md` and assesses each one's cancellation story + each `Close`/`Shutdown`/`Stop` method's soundness.

Severity legend: C = Critical, H = High, M = Medium, L = Low, I = Info. Severity reflects the lifecycle gap at the individual site, not a systemic call. Systemic severity is in `index.md`.

`cross-ref` tags point to prior audit findings. `new` means this sweep is the first audit artifact to catalog the lifecycle gap.

Shutdown-state legend:
- **ctx-gated**: body observes `ctx.Done()` (or an equivalent stop channel) on a timeline that matches its workload
- **indirect-ctx**: cancellation flows via a side-channel (process death, pipe close, wg completion of owned children)
- **untracked**: parent holds no handle to stop the goroutine; it runs until its body completes naturally
- **daemon**: long-lived, ticker-driven or infinite-loop; cancellation is a shutdown concern
- **request-scoped**: short-lived, bounded by a single caller's ctx

---

## internal/server

**Goroutine lifetimes:** none spawned in this package (SSE handlers run in the request goroutine).

**`Close`/`Shutdown` methods:**
- none in production code. `http.Server` lifecycle is owned by `cmd/nanite/main.go:287` (`srv.ListenAndServe()`); no corresponding graceful `Shutdown` call exists anywhere in the tree. **Contract soundness: missing.** The server runs until `os.Exit(0)` unconditionally. In-flight HTTP requests are dropped when the process exits.

**Gap summary for this package:** no spawn-site concerns. But the absence of a `http.Server.Shutdown()` call anywhere in the tree means inbound requests are not drained on SIGTERM. Minor.

---

## cmd/nanite

**Goroutine lifetimes:**
- `cmd/nanite/main.go:268` — signal handler — owner: no one (exit-only path) — cancellation: `sigCh` then `container.Shutdown()` then `os.Exit(0)` — teardown: terminal — severity: **M** — cross-ref: new. No max-total-timeout guard; a hung Shutdown hangs the host. A `time.AfterFunc(30*time.Second, func() { os.Exit(1) })` before calling Shutdown would bound it.
- `cmd/nanite/main.go:431` — truncate cleanup ticker — owner: `startBackgroundWorkers` caller (no handle returned) — cancellation: **none** — teardown: SIGKILL via `os.Exit(0)` — severity: **M** — cross-ref: new. Daemon. `container.Shutdown` cannot signal it.
- `cmd/nanite/main.go:442` — task snapshot ticker — owner: none — cancellation: **none** — teardown: SIGKILL — severity: **M** — cross-ref: new. Daemon. `container.Tasks.Snapshot()` can be in-flight when `container.Shutdown → container.Tasks.Snapshot` runs, causing concurrent Snapshot calls — ordering is undefined.
- `cmd/nanite/main.go:454` — stale process reaper ticker — owner: none — cancellation: **none** — teardown: SIGKILL — severity: **M** — cross-ref: new. Daemon. `container.ProcessTracker.KillStale` accessed concurrently with shutdown.
- `cmd/nanite/main.go:466` — stale worker reaper ticker — owner: none — cancellation: **none** — teardown: SIGKILL — severity: **M** — cross-ref: new. Daemon. `container.Workers.ReapStale → m.Cancel` — races with `m.Shutdown` during concurrent shutdown.

**`Close`/`Shutdown` methods:**
- none declared in `cmd/nanite`. The signal handler inlines `container.Shutdown()` + `os.Exit(0)`. **Contract soundness: partial.** Missing: max-total-timeout, ticker stop channels, stop-signal propagation to `startBackgroundWorkers` spawns.

**Gap summary for this package:** 5 spawn sites. 4 daemon tickers with no cancellation, one signal handler with no max-timeout. `startBackgroundWorkers` does not return a stop handle — the cleanest minimum fix is to have it accept a `context.Context` and propagate it into each ticker via `for { select { case <-ctx.Done(): return; case <-ticker.C: ... } }`.

---

## internal/api

**Goroutine lifetimes:** 14 spawn sites, all fire-and-forget event emissions. Pattern: `go a.Services.Plugins.EmitXxx(...)` or `go a.Services.Activity.EmitXxx(...)` inside an HTTP handler. Each one lands in `Host.EmitEvent` or an activity emitter, which themselves spawn more goroutines.

- `internal/api/artifacts.go:117` — owner: none — cancellation: **none** — teardown: fire-and-forget — severity: **M** — cross-ref: new (lifecycle), plugin-system-plan-eval#04 (panic). Spawned on the handler's defer path after the HTTP response was written — any shutdown in flight is ignored.
- `internal/api/artifacts.go:168` — same shape — severity: **M** — cross-ref: new.
- `internal/api/agents.go:245` — same shape — severity: **M** — cross-ref: new.
- `internal/api/bookmarks.go:51/:74/:99/:127` — four sites — severity: **M** each — cross-ref: new.
- `internal/api/sessions.go:69/:166/:189/:194/:236` — five sites — severity: **M** each — cross-ref: new.
- `internal/api/settings.go:140` — severity: **M** — cross-ref: new.
- `internal/api/plugin_config.go:104` — severity: **M** — cross-ref: new.

Each is a genuine fire-and-forget: the goroutine runs for as long as the emit helper takes (typically <10ms) and then exits. Leak risk is near-zero in steady state. **Shutdown risk is real:** the goroutine spawn doesn't observe process shutdown, so on SIGTERM, any in-flight emit is lost — which matters for `EmitSessionArchived`, `EmitArtifactCreated`, `EmitPluginInstalled` semantics (plugin hooks expect to be called; on shutdown they silently aren't).

**`Close`/`Shutdown` methods:** none. The `api.API` struct is a handler container, not a lifecycle owner.

**Gap summary for this package:** 14 fire-and-forget emits, 0 tracked. None are long-lived, none leak per-request, but collectively they represent the "plugin events don't ship during shutdown" class. Right fix is to route every emit through a shared `eventBus.Emit(ctx, ...)` primitive that lives on a tracked lifecycle owner — same refactor as the `lifecycle` proposal in `index.md`.

---

## internal/service (including service/a2a)

**Goroutine lifetimes:** 36 spawn sites. This is the highest-density package.

- `internal/service/chat.go:155` — fire-and-forget `go s.pluginHost.EmitMessageSent(...)` — owner: none — cancellation: **none** — teardown: request-fast-exit — severity: **M** — cross-ref: new. Same class as the api/ fire-and-forget emits.
- `internal/service/chat.go:166` — `go s.generateResponse(bgCtx, ...)` — owner: **nobody** — cancellation: **none** (bgCtx is `context.WithoutCancel(ctx)` so parent cancellation is severed) — teardown: internal `WithTimeout(5*time.Minute)` inside `generateResponse` — severity: **H** — cross-ref: new (lifecycle), chat-engine#02 (panic). This is the main chat loop. `chatServiceImpl.Shutdown` does not wait for it.
- `internal/service/chat.go:210` — `go s.generateResponse(bgCtx, ...)` in RetryLastMessage — same shape — severity: **H** — cross-ref: new.
- `internal/service/chat.go:241` — `go s.generateResponse(bgCtx, ...)` in SendAgentMessage — same shape — severity: **H** — cross-ref: new.
- `internal/service/chat.go:352` — `go s.pluginHost.EmitProviderFallback(...)` — fire-and-forget — severity: **M** — cross-ref: new.
- `internal/service/chat.go:359` — same — severity: **M** — cross-ref: new.
- `internal/service/chat_generate.go:203` — `go s.pluginHost.EmitContextAssembled(...)` — severity: **M** — cross-ref: new.
- `internal/service/chat_generate.go:223` — `go s.pluginHost.EmitAgentLoaded(...)` — severity: **M** — cross-ref: new.
- `internal/service/chat_generate.go:358` — `go s.pluginHost.EmitProviderError(...)` — severity: **M** — cross-ref: new.
- `internal/service/chat_generate.go:656` — `go s.pluginHost.EmitEnvelopeRendered(...)` — severity: **M** — cross-ref: new.
- `internal/service/chat_generate.go:760` — `go s.autoTitle(sessionID, userContent)` — owner: none — cancellation: **none** (autoTitle creates its own `context.Background()` with WithTimeout internally) — teardown: untracked — severity: **M** — cross-ref: new. Utility LLM call. Race with shutdown: may write a title to a session being deleted.
- `internal/service/chat_generate.go:762` — `go s.autoTags(sessionID)` — same shape — severity: **M** — cross-ref: new.
- `internal/service/chat_tool_executor.go:260` — `go func(ip indexedPlan) { ... s.executeSingleTool(...) }(ip)` — owner: caller — cancellation: **ctx-gated** via the caller's ctx passed through `executeSingleTool` — teardown: local `var wg sync.WaitGroup` with `wg.Wait()` at `:266` — severity: **L** — cross-ref: new. **This is the tracked goroutine pattern that works.** One of the few examples in the tree.
- `internal/service/delegation.go:123` — `go s.generateResponse(ctx, ...)` in DelegateTask — owner: delegation caller — cancellation: **none on shutdown** (same severance) — teardown: untracked — severity: **H** — cross-ref: new.
- `internal/service/delegation.go:264` — `go func(idx int, st chat.SubTask) { ... s.workers.SpawnFull(...) }(i, st)` — owner: caller — cancellation: **ctx-gated** — teardown: local wg (need to verify it waits) — severity: **M** — cross-ref: new.
- `internal/service/events_composite.go:28 through :132` — 20 naked fire-and-forget emit goroutines — owner: none — cancellation: **none** — teardown: fire-and-forget — severity: **M** each — cross-ref: new (lifecycle), plugin-system-plan-eval#04 (panic). Every emit in the product is routed through this composite, and every one is spawn-and-forget.
- `internal/service/a2a/subscribe.go:62` — `go func() { <-ctx.Done(); ... close(ch) }()` — owner: caller via ctx — cancellation: **ctx-gated** — teardown: documented contract ("caller MUST cancel ctx") — severity: **I** — cross-ref: new. **Sound.** One of the few correctly-implemented lifecycle shapes in the tree.

**`Close`/`Shutdown` methods:**
- `internal/service/container.go:510` — `(*Container).Shutdown()` — cancels: none directly (delegates to sub-shutdowns) — waits: none (synchronous sequential calls) — returns: after last sub-shutdown returns — **contract soundness: partial.** Calls `Workers.Shutdown`, `Chat.Shutdown`, `Tasks.Snapshot`, `Coord.Close`, `Conduit.Close`, `MCP.Close` in order. No max-total-timeout. No parallelism where safe. No per-subsystem observability. No guard against a sub-shutdown hanging the chain.
- `internal/service/chat.go:288` — `(*chatServiceImpl).Shutdown()` — cancels: process tracker only — waits: **nothing** — returns: after `KillAll` — **contract soundness: misleading.** The interface doc says "kills all tracked CLI processes" (accurate to doc). The method name is `Shutdown` (implies chat subsystem shutdown). In-flight `generateResponse` goroutines are untouched. No wg. No ctx to cancel.
- `internal/service/stream.go` — no Close method on `StreamManager` — **contract soundness: absent.** Holds 6 `sync.Map`s of live state. Expected to be implicit-exit-only.

**Gap summary for this package:** 36 spawn sites, ~3 with owner handles (tool-executor batch, a2a subscribe unsubscriber, delegation fan-out). 33 untracked. Two `Shutdown` methods of variable soundness. This is the largest lifecycle-gap surface in the repo.

---

## internal/plugin

**Goroutine lifetimes:**
- `internal/plugin/host.go:332` — `go h.EmitWidgetLoaded(...)` — fire-and-forget — severity: **M** — cross-ref: new (lifecycle).
- `internal/plugin/host.go:503` — `go h.EmitConfigChanged(...)` — same — severity: **M** — cross-ref: new.
- `internal/plugin/host.go:939` — `go h.EmitPluginInstalled(...)` — same — severity: **M** — cross-ref: new.
- `internal/plugin/host.go:1074` — `go h.EmitPluginUninstalled(id)` — same — severity: **M** — cross-ref: new. Spawned from `UnloadPlugin` after the plugin is already deleted from `h.plugins`. The emit goroutine runs on `h.ctx` (which is still alive at this point — `h.ctxCancel` only fires in `Shutdown`, and `UnloadPlugin` doesn't cancel).
- `internal/plugin/host.go:1090` — `go func(hook plugin.EventHook) { ... hook.Handle(ctx, event) ... }(hook)` inside `EmitEvent` — owner: `EmitEvent`'s local `var wg sync.WaitGroup` — cancellation: **ctx-gated** via `context.WithTimeout(h.ctx, 5*time.Second)` at `:1092` — teardown: `wg.Wait()` at `:1100` before `EmitEvent` returns — severity: **I** — cross-ref: plugin-system-plan-eval#04 (panic only). **Lifecycle is sound.** The one well-designed goroutine in the plugin host.
- `internal/plugin/host.go:1105` — `go h.triggers.Dispatch(event)` — owner: none — cancellation: **none at dispatch level** (inside Dispatch, `sendWithRetry` observes `td.host.ctx.Done()`) — teardown: untracked — severity: **M** — cross-ref: new (lifecycle). The dispatcher-level goroutine is fire-and-forget relative to EmitEvent; the spawn-amplification happens inside Dispatch.
- `internal/plugin/triggers.go:80` — `go td.sendWithRetry(connector, payload, rule.ID)` — owner: none — cancellation: **indirect** via `td.host.ctx.Done()` in the retry backoff select at `triggers.go:172` — teardown: untracked — severity: **M** — cross-ref: new. Retry observes ctx but the initial `connector.Send` at `:154` is a blocking call with a 30-second `WithTimeout` derived from `host.ctx` — so shutdown does flow through, via the inner ctx, but there's no wg to ensure the goroutine has exited before `Host.Shutdown` returns.
- `internal/plugin/catalog.go:102` — `go func(s CatalogSource) { cf.fetchSource(ctx, s); ... }(src)` — owner: `Fetch` via channel — cancellation: **ctx-gated inside fetchSource** but the collector loop at `:116` blocks on channel receive without `ctx.Done()` — teardown: channel-drain, no wg — severity: **M** — cross-ref: new. If the caller cancels ctx mid-fetch, `Fetch` still waits for every per-source goroutine to post a result before returning.
- `internal/plugin/subprocess/manager.go:167` — `go func() { waitCh <- cmd.Wait() }()` — owner: `waitCh` single-reader — cancellation: **indirect** (exits when `cmd.Wait` returns) — teardown: implicit via process exit — severity: **L** — cross-ref: new. Single-purpose, one-shot.
- `internal/plugin/subprocess/manager.go:170` — `go m.waitForExit(stderr)` — owner: none — cancellation: **none** — teardown: implicit via `waitCh` — severity: **L** — cross-ref: new. Exits when the process exits (via `<-m.waitCh`).
- `internal/plugin/subprocess/manager.go:176` — `go m.healthLoop(hctx)` — owner: `m.healthCancel` — cancellation: **ctx-gated** via `hctx` — teardown: `Stop` calls `m.healthCancel()` at `:193` before anything else — severity: **I** — cross-ref: new. **Sound.**
- `internal/plugin/subprocess/transport.go:107` — `go func() { line, err := t.r.ReadBytes('\n'); ch <- readResult{line, err} }()` — owner: `read` caller via buffered `ch` — cancellation: **indirect** via `t.Close()` which closes the underlying reader and unblocks `ReadBytes` — teardown: implicit — severity: **I** — cross-ref: plugin-system-plan-eval#03 (fix in place). **Sound since the plugin-system-plan-eval fix landed at `transport.go:131-136`** (timeout and ctx.Done both call `t.Close()`).

**`Close`/`Shutdown` methods:**
- `internal/plugin/host.go:1170` — `(*Host).Shutdown()` — cancels: `h.ctxCancel()` — waits: **nothing** — returns: after unload loop — **contract soundness: broken.** Holds `h.mu.Lock()` across `p.Unload()` (plugin-system-plan-eval#01, not re-flagged). Additionally: `h.ctxCancel()` fires *after* the unload loop, not before. Correct order is cancel → unload → wait-for-goroutines → return. Current order lets EmitEvent goroutines run against a live `h.ctx` during plugin unload.
- `internal/plugin/subprocess/manager.go:183` — `(*Manager).Stop()` — cancels: `m.healthCancel()` then `MethodUnload` RPC — waits: `<-waitCh` with `ShutdownTimeout` — returns: after force-kill fallback — **contract soundness: sound.** Reference implementation. The only `Stop` in the tree that does cancel → wait → force-kill → re-wait.
- `internal/plugin/subprocess/transport.go:41` — `(*Transport).Close()` — cancels: `rClose.Close()` on underlying reader — waits: nothing — returns: immediately — **contract soundness: sound** (specifically designed to unblock the `read` goroutine; wait is delegated to the caller of `read`).

**Gap summary for this package:** 11 spawn sites, 4 with proper owner handles (EmitEvent wg, subprocess healthLoop cancel, subprocess waitCh, subprocess transport Close). `Host.Shutdown` is broken (known, plugin #01). `subprocess.Manager.Stop` is the only sound Stop in the tree.

---

## internal/chat

**Goroutine lifetimes:** none spawned in this package. All chat engine logic executes on the caller's goroutine.

**`Close`/`Shutdown` methods:** none.

**Gap summary for this package:** no concerns intrinsic to this package. Its lifecycle is inherited from `internal/service/chat*.go` callers, which are broken (see above).

---

## internal/mcp

**Goroutine lifetimes:**
- `internal/mcp/stdio_transport.go:109` — `go func() { line, err := t.stdout.ReadBytes('\n'); readCh <- readResult{line, err} }()` — owner: `call` caller via buffered `readCh` — cancellation: **none that works** — teardown: **leaked on timeout** — severity: **H** — cross-ref: **mcp-client-transport#01**. On timeout path at `:127-129` the code only sets `t.started = false` — does NOT call `t.stdin.Close()`, does NOT kill the process, does NOT close stdout. The reader goroutine remains blocked on `ReadBytes` against a now-orphaned pipe. **Not re-flagged** — cross-ref to prior audit. The plugin-subprocess transport (above) has the fix; this one does not.

**`Close`/`Shutdown` methods:**
- `internal/mcp/manager.go:451` — `(*Manager).Close()` — cancels: each transport's Close (for those implementing Closer) — waits: nothing — returns: after the loop — **contract soundness: partial + pattern-adjacent.** Holds `m.mu.Lock()` across every transport's `Close()`. Line-for-line same shape as `(*plugin.Host).Shutdown` (plugin-system-plan-eval#01). Currently latent — no transport's Close recurses into Manager. Becomes deadlock if any future transport emits an event, refreshes tool list, or records status from inside Close. **New finding.**
- `internal/mcp/stdio_transport.go:185` — `(*StdioTransport).Close()` — cancels: `t.stdin.Close()` then `t.cmd.Process.Kill()` — waits: `t.cmd.Wait()` unbounded — returns: after Wait — **contract soundness: partial.** The unbounded `cmd.Wait()` can block forever on a wedged subprocess. No timeout. Pair with the fact that the timeout path at `call:127-129` doesn't even call Close — there are two paths that leak, for different reasons.

**Gap summary for this package:** 1 spawn, 1 lifecycle gap (cross-ref mcp-client-transport#01). Manager.Close has an independent mutex-across-inner-Close pattern worth flagging as a new class instance.

---

## internal/mcpserver

**Goroutine lifetimes:** none. `mcpserver.Server.Serve()` handles JSON-RPC over stdio synchronously.

**`Close`/`Shutdown` methods:** none.

**Gap summary:** no spawn-site gaps. The `nanite mcp` binary mode is a separate process with its own exit path.

---

## internal/memory

**Goroutine lifetimes:**
- `internal/memory/extraction.go:105` — `go h.extractor.extractPerTurn(sessionID, content)` inside `perTurnHook.Handle` — owner: none — cancellation: **none** (extractor uses `context.Background()` with its own 15-second WithTimeout) — teardown: untracked — severity: **M** — cross-ref: new. Fire-and-forget LLM call. Discards the `_ context.Context` passed to `Handle`. No shutdown link.
- `internal/memory/extraction.go:136` — `go h.extractor.extractPostCompact(...)` — same shape — severity: **M** — cross-ref: new.

**`Close`/`Shutdown` methods:** none.

**Gap summary for this package:** 2 spawn sites, 0 tracked. LLM extraction calls without shutdown link. A running extractor on shutdown means the utility LLM call is dropped mid-request; the store write may land or may not, depending on whether the goroutine reaches the `store.CreateMemory` call before `os.Exit(0)` fires.

---

## internal/worker

**Goroutine lifetimes:**
- `internal/worker/manager.go:96` — `go m.heartbeatLoop(workerID, heartbeatStop)` — owner: `heartbeatStop` channel closed by `cleanup()` — cancellation: **ctx-gated** via `case <-stop` in the loop at `:372` — teardown: implicit (close channel → select fires) — severity: **L** — cross-ref: new. **Lifecycle is sound at the per-worker level.** No wg at the Manager level, though — if `SpawnFull` is leaked (its calling goroutine panics before reaching `cleanup()`), the heartbeat loop leaks too.
- `internal/worker/manager.go:179` — `go func() { time.Sleep(30*time.Second); m.workers.Delete(workerID) }()` — owner: none — cancellation: **none** (time.Sleep is uncancellable without a timer) — teardown: untracked — severity: **M** — cross-ref: new. **Broken.** One goroutine per SpawnFull completion. Under worker churn, N of these can accumulate. On shutdown, abandoned.

**`Close`/`Shutdown` methods:**
- `internal/worker/manager.go:314` — `(*Manager).Shutdown()` — cancels: per-worker `w.cancel()`, `SetStatus(StatusCancelled)` — waits: **nothing** — returns: after the Range closure — **contract soundness: partial.** Cancels, does not wait. Concurrent `SpawnFull` calls may still be blocked on `DelegateTask` when Shutdown returns. The pending-delete goroutines are not affected.

**Gap summary for this package:** 2 spawn sites, 1 sound (heartbeat), 1 broken (deferred delete). Shutdown is a partial — cancels without waiting. `List`/`Cancel`/`ReapStale`/`ActiveCount` are correctly sync.Map-based and don't have their own goroutine lifetimes.

---

## internal/workflow

**Goroutine lifetimes:**
- `internal/workflow/executor.go:232` — `go func(s *Step, ss *StepState) { ... e.executeWithRetry(stepCtx, s, stepInput) ... }(step, ss)` — owner: local `var wg sync.WaitGroup` at `executor.go:172` — cancellation: **ctx-gated** via `context.WithTimeout(ctx, timeout)` at `:239` — teardown: `wg.Wait()` at `:302` before the level advances — severity: **I** — cross-ref: new. **Sound.**
- `internal/workflow/handlers.go:148` — `go func(idx int, s Step) { ... s.Handler.Execute(ctx, subInput) ... }(i, step)` in `ParallelStep.Execute` — owner: local `var wg sync.WaitGroup` at `handlers.go:144` — cancellation: **ctx-gated** — teardown: `wg.Wait()` at `:161` — severity: **I** — cross-ref: new. **Sound.**

**`Close`/`Shutdown` methods:** none. Workflow engine is stateless per-call.

**Gap summary for this package:** 2 spawn sites, both wg-tracked with proper cancellation. Both the step-fan-out at the executor level and the ParallelStep sub-fan-out are correctly implemented. This is the cleanest lifecycle shape in the tree, along with `plugin.Host.EmitEvent` and `service.chat_tool_executor`.

---

## internal/sandbox

**Goroutine lifetimes:**
- `internal/sandbox/proxy.go:50` — `go func() { defer p.wg.Done(); p.server.Serve(ln) }()` — owner: `p.wg` — cancellation: **indirect** via `p.server.Shutdown(ctx)` in `Stop()` — teardown: `p.wg.Wait()` at `proxy.go:68` — severity: **I** — cross-ref: new. **Sound at the Serve-goroutine level.**
- `internal/sandbox/proxy.go:122` — `go func() { defer copyWg.Done(); io.Copy(targetConn, clientConn) }()` inside `handleConnect` — owner: local `copyWg` — cancellation: **none** (blocks on io.Copy until TCP closes) — teardown: local `copyWg.Wait()` at `:130` then `handleConnect` returns — severity: **M** — cross-ref: new. After `handleConnect` returns, the copies are in scope only via the local wg which has already been Wait'd. But wait — actually `copyWg.Wait()` at :130 DOES wait. So by the time handleConnect returns, the copy goroutines HAVE finished. Re-read... yes, `copyWg.Wait()` at :130 is inside `handleConnect`, so the copies are actually awaited before the handler returns. The gap is only that the handler itself is detached from `p.wg` (it's an HTTP handler, tracked by `http.Server` until hijack). **Correction: the copy goroutines do not leak individually, but `http.Server.Shutdown` does not wait for hijacked handlers, so handleConnect can be running past Stop's return.** Severity: **M**, not because of the copy goroutines but because the handler itself is past-server-shutdown.
- `internal/sandbox/proxy.go:126` — paired copy goroutine — same analysis — severity: **M** — cross-ref: new.

**`Close`/`Shutdown` methods:**
- `internal/sandbox/proxy.go:61` — `(*Proxy).Stop()` — cancels: `p.server.Shutdown(ctx)` with 5s timeout — waits: `p.wg.Wait()` — returns: after wg — **contract soundness: partial.** `http.Server.Shutdown` does not wait for hijacked connections (this is stdlib behavior, documented). The CONNECT tunnel handlers can be running when Stop returns. `p.wg` only tracks the Serve goroutine, not the hijacked handlers.

**Gap summary for this package:** 3 spawn sites. 1 sound (Serve), 2 partial (hijacked copy goroutines are tracked locally but their parent handler is detached from p.wg past-hijack). Stop is partial — correct for non-hijacked traffic, incomplete for CONNECT tunnels. Not a severe gap, but a real "Stop returns before all goroutines exit" case.

---

## internal/coordination

**Goroutine lifetimes:**
- `internal/coordination/badger.go:33` — `go s.gcLoop()` in `NewBadgerStore` — owner: `s.stopGC` channel — cancellation: **ctx-gated** via `case <-s.stopGC` in the loop at `:114` — teardown: `close(s.stopGC)` in `Close()` at `:104` then `s.db.Close()` at `:105` — severity: **M** — cross-ref: new. **Racy.** `Close` does NOT wait for the loop to observe the stop signal before calling `db.Close()`. If the loop is mid-`RunValueLogGC(0.5)` when `db.Close` runs, behavior depends on Badger v4 internals.

**`Close`/`Shutdown` methods:**
- `internal/coordination/badger.go:103` — `(*BadgerStore).Close()` — cancels: `close(s.stopGC)` — waits: **nothing** (missing `<-doneCh` or wg) — returns: after `db.Close()` — **contract soundness: racy.** Fix is a `done := make(chan struct{})` sibling to `stopGC`, closed by gcLoop on return, wait before `db.Close()`.

**Gap summary for this package:** 1 spawn site, 1 teardown race in Close.

---

## internal/skill

**Goroutine lifetimes:**
- `internal/skill/context.go:51` — `go func() { done <- cmd.Wait() }()` — owner: `done` channel single-reader — cancellation: **indirect** (exits when `cmd.Wait` returns; `exec.CommandContext` kills process on ctx cancel) — teardown: implicit — severity: **L** — cross-ref: new. Single-purpose.

**`Close`/`Shutdown` methods:** none.

**Gap summary for this package:** 1 spawn, sound for its scope.

---

## pkg/provider

**Goroutine lifetimes:** 14 streaming reader spawn sites. Each spawns a goroutine that reads SSE / stream-JSON / PTY output and feeds a `StreamEvent` channel.

- `pkg/provider/anthropic.go:420` — `go a.readSSEWithTracking(...)` — owner: stream channel consumer — cancellation: **ctx-gated inside readSSE** via a ctx parameter that flows from the caller — teardown: `defer close(ch)` (need to verify) — severity: **L–M** — cross-ref: new. Shape follows the stdlib pattern of "reader goroutine closes channel on exit; consumer ranges over channel." Sound IF the caller's ctx cancellation actually kills the HTTP request or the JSON decode. Anthropic's uses `http.Request` with the ctx, which cancels the HTTP body read on ctx done. **Indirect-ctx soundness.**
- `pkg/provider/anthropic.go:448` — nested `go a.readSSE(ctx, body, inner)` — same shape — severity: **L** — cross-ref: new.
- `pkg/provider/openai.go:95` — same shape — severity: **L** — cross-ref: new.
- `pkg/provider/ollama.go:95` — same shape — severity: **L** — cross-ref: new.
- `pkg/provider/gemini.go:89` — same shape — severity: **L** — cross-ref: new.
- `pkg/provider/mistral.go:82` — same shape — severity: **L** — cross-ref: new.
- `pkg/provider/azure_openai.go:100` — same shape — severity: **L** — cross-ref: new.
- `pkg/provider/openrouter.go:80` — same shape — severity: **L** — cross-ref: new.
- `pkg/provider/openzen.go:87` — same shape — severity: **L** — cross-ref: new.
- `pkg/provider/pty.go:126` — `go func() { defer close(ch); defer ptmx.Close(); scanner ... }()` — owner: stream channel consumer — cancellation: **indirect-ctx** via `exec.CommandContext(ctx, ...)` at `:105` (ctx cancel kills the CLI, closes PTY, unblocks scanner) — teardown: `defer close(ch)` and `defer ptmx.Close()` — severity: **M** — cross-ref: new. The `select { case <-ctx.Done(): ... }` branch at `:135` only runs *after* the scanner returns, so in-line cancellation works only via process death. **Functions correctly in practice, but the body does not observe cancellation directly.**
- `pkg/provider/pty.go:195` — nested `go func() { cmd.Wait(); close(done) }()` inside `killProcess` — owner: local `done` — cancellation: implicit — teardown: local select with 5s force-kill — severity: **L** — cross-ref: new. Latent double-`cmd.Wait` risk with `:172` (see `index.md` gap 13).
- `pkg/provider/subprocess.go:111` — non-PTY subprocess reader — same shape as pty.go:126 — severity: **M** — cross-ref: new.
- `pkg/provider/event_pipeline.go:145` — dead code per chat-engine#01 — severity: **DEAD** — cross-ref: chat-engine#01.
- `pkg/provider/event_pipeline.go:239` — dead code — severity: **DEAD** — cross-ref: chat-engine#01.

**`Close`/`Shutdown` methods:** none on the provider types. Providers hold no goroutine state past the lifetime of an individual `StreamChat` call; the reader goroutine's lifetime is the channel's lifetime.

**Gap summary for this package:** 14 spawn sites, 12 live. Each relies on the caller's ctx flowing through the HTTP request body read (for API providers) or through `exec.CommandContext` (for PTY/subprocess). Cancellation is indirect-ctx but functional. **Lifecycle concern: none of the providers hold a Close method**, so on provider-level shutdown (which doesn't exist), there's no way to drain in-flight streams. Acceptable in practice because the streams are owned by their callers, not by the provider — but the absence of a lifecycle contract is worth noting.

---

## Packages with no concurrency concerns

Checked and confirmed empty of goroutines and `Close/Shutdown/Stop/Cancel` methods that warrant inclusion:

`internal/agent`, `internal/agent/override`, `internal/agent/builtin`, `internal/agentvalidation`, `internal/assets`, `internal/brand`, `internal/builders`, `internal/config`, `internal/context`, `internal/contextbroker`, `internal/crossapp`, `internal/envelope`, `internal/filter`, `internal/mcpconfig`, `internal/permission`, `internal/plugin/allplugins`, `internal/plugin/builtin/*`, `internal/plugin/scaffold`, `internal/secrets`, `internal/shell`, `internal/store/migrations`, `internal/task`, `internal/tool`, `internal/tool/broker`, `internal/toolclient`, `internal/truncate`, `internal/version`, `internal/worktree`.

`internal/store/store.go` has a `Close()` method at `:64` that calls `s.db.Close()` on the underlying SQLite handle — no goroutines owned by the store, no lifecycle gap.

`internal/task/service.go` has a `Cancel(ctx, id)` method that cancels a tracked task entity, not a goroutine — not in this sweep's scope.

---

## Summary counts

- Total production `go` spawn sites walked: **93** (matches panic-recovery sweep exactly)
- Spawn sites with sound lifecycle (parent holds proper handle): **~13**
  - `plugin.Host.EmitEvent` hook wg (1 site)
  - `plugin.subprocess.Manager.healthLoop` (1)
  - `plugin.subprocess.Manager.waitCh` (2 — writer + `waitForExit` reader)
  - `plugin.subprocess.Transport.read` reader (1)
  - `service.chat_tool_executor` batch wg (1, representing all concurrent tool goroutines in one scope)
  - `service.a2a.subscribe` unsubscribe waiter (1)
  - `workflow.executor` level wg (1)
  - `workflow.handlers.ParallelStep` wg (1)
  - `worker.Manager.heartbeatLoop` stop channel (1, per-worker)
  - `sandbox.Proxy.Serve` wg (1)
  - `coordination.BadgerStore.gcLoop` stop channel (1, but Close doesn't wait — partial)
- Spawn sites untracked / fire-and-forget / daemon-without-stop: **~80**
- `Shutdown`/`Close`/`Stop` methods audited: **12**
  - Sound: **3** (`plugin.subprocess.Manager.Stop`, `plugin.subprocess.Transport.Close`, `store.Store.Close`)
  - Partial: **7** (`Container.Shutdown`, `chatServiceImpl.Shutdown`, `worker.Manager.Shutdown`, `mcp.Manager.Close`, `mcp.StdioTransport.Close`, `sandbox.Proxy.Stop`, signal handler exit path)
  - Broken / racy: **2** (`plugin.Host.Shutdown` — known plugin #01; `coordination.BadgerStore.Close`)
- New lifecycle gaps cataloged (beyond prior audit cross-refs): **16** (see `index.md`)
- Cross-references to prior audits: **3** (plugin #01, plugin #03, mcp-client-transport #01)

Systemic severity (per `index.md`): **High.**
