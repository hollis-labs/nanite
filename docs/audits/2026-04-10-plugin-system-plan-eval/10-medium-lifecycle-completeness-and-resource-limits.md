# [Medium] Plan under-specifies plugin lifecycle edge cases and has no resource limits

**Scope:** plan completeness
**Topic:** plan-completeness / lifecycle
**Date:** 2026-04-10

## Problem

A plugin-system migration-and-readiness plan should cover the full lifecycle edge-case matrix — panic in `Init`, panic in `OnShutdown`, double-`Shutdown`, plugin-disable-while-in-flight-call, plugin that never responds to `plugin/unload`, runaway subprocess consuming all memory, plugin that spawns its own goroutines without cleanup, config reload mid-operation, and so on. The plan's lifecycle section is mostly "Register / Unregister" symmetry and one sharp edge about event hooks. Real plugin lifecycles are messier.

Related: there are no resource limits anywhere in the plan. A subprocess plugin can consume unbounded CPU, memory, and file descriptors. The plan's subprocess manager doesn't set ulimits, doesn't track CPU time, doesn't cap memory, doesn't set an OOM score, and doesn't isolate the plugin from Nanite's own resource pool.

## Evidence

**Lifecycle edges the plan doesn't cover:**

1. **Panic in plugin Init** — current code at `subprocess/plugin.go:L110-117` calls `Start` then does `InitParams`; if the subprocess panics during init (crashes the process), `CallResult[InitResult]` returns an error and `Stop()` is called. That's correct. But for **in-process builtin plugins**, the plan expects them to route through the yaml-authoritative loader. If the builtin's `Load()` panics, there is no recover — it crashes the host. The plan doesn't add a recover here.

2. **Panic in Unload** — similarly, `p.Unload()` at `host.go:L1031` has no recover. A panicking Unload kills the host during shutdown. Same during hot-uninstall.

3. **Double-Unload** — `UnloadPlugin` doesn't check whether the plugin was already unloaded (it only checks existence in `h.plugins`). `UnloadPlugin` then `Shutdown()` is a double-call for every plugin. Current code handles this because `Shutdown()` iterates the live map, which has already been cleared by UnloadPlugin — but the subprocess Manager's `Stop()` at `manager.go:L183-228` is idempotent-ish via state check. The plan doesn't formalize "plugins must be safe to Unload multiple times" as a contract.

4. **Plugin that never responds to `plugin/unload`.** `Manager.Stop()` sends `plugin/unload` over the transport with a timeout, then waits on `waitCh` with another timeout, then SIGKILLs. Timeline from `manager.go:L202-219`:
   - Send plugin/unload RPC with ShutdownTimeout (default 5s)
   - Select on waitCh or time.After(ShutdownTimeout) — that's ANOTHER 5s
   - SIGKILL
   - Wait on waitCh forever
   
   Total shutdown time is up to 10 seconds per plugin. If the user has 10 plugins loaded and hits Ctrl-C, nanite takes 100 seconds to shut down. Not in plan.

5. **`plugin/unload` RPC blocking behind serialized transport.** If an `mcp/call_tool` is in-flight taking 20s, the `plugin/unload` RPC queues behind it. See finding 02.

6. **Plugin config reload.** The plan §B.9 extends InitParams with `DataDir` / `CacheDir` / `LogLevel`. What happens when config changes mid-session? The plan doesn't say. Does it trigger a reload? A restart? Nothing? The plan's "Plugin lifecycle event stream" at B.8 emits `plugin.enabled/disabled/updated` but doesn't define the wire-level semantics of a config reload.

7. **Plugin crash → restart loop amplification.** `Manager.attemptRestart` retries with backoff up to MaxRestarts (default 3). After that, the plugin is dead forever until nanite is restarted. The plan's hot install/uninstall should enable re-install after max-restart without a nanite restart; currently it can't, because there's no path from "plugin crashed too many times" back to a fresh install attempt.

**Resource limits the plan doesn't mention:**

1. No `setrlimit` on subprocess Start — no CPU time cap, no memory cap, no file descriptor cap.
2. No cgroup/jobobject isolation on Linux/Windows.
3. No stderr size cap beyond the 4KB ring buffer (`manager.go:L146-147`) — a chatty plugin's stderr is silently dropped past 4KB; the plan's "Plugin Manager UI displays ... load failures" at G.5 relies on this stderr for diagnostics.
4. No rate limit on RPC calls — a plugin that floods `event/handle` notifications can overwhelm the host event loop.
5. No heartbeat / deadman switch — if the plugin process is alive but unresponsive (infinite loop), the 30s health check will fail, but there's no automatic restart on health failure (current health check just logs — see finding 02).

## Impact

For a beta release for developer friends:

- A buggy plugin can consume unbounded memory and take the whole tooling stack with it (OOM kill).
- A plugin in an infinite loop hangs the health check but never recovers.
- A crash loop is silent (max 3 restarts, then nothing).
- Shutdown time scales with number of plugins. Annoying but not fatal.
- No way to recover from "plugin is wedged" without killing nanite.

The plan should decide which of these are in-scope for beta and which are deferred, explicitly. Right now they're just absent.

## Recommendation

Add a new section §B.X or §3.Y titled "Lifecycle hardening and resource limits." Baseline items for beta:

1. **Recover in `LoadPlugin` and `UnloadPlugin` around `p.Load(h)` and `p.Unload()`.** Convert panic to error and return; emit a plugin lifecycle event for observability.

2. **Cap total shutdown time.** `Shutdown()` runs plugin unloads in parallel with a hard total deadline of, say, 15 seconds. After that, SIGKILL all subprocesses unconditionally and move on.

3. **Health check failure should escalate.** Configurable: after N consecutive health failures, mark plugin `StateCrashed` and let the restart logic run.

4. **Stderr ring buffer size bump and persistence.** 4KB is too small. Grow to 64KB or stream to a per-plugin log file so `nanite plugin logs <id>` has something to show.

5. **Basic resource limits on subprocess Start.** At least:
   - Set `Pdeathsig` on Linux so the subprocess dies if the parent dies.
   - Set `Setrlimit` for RLIMIT_AS (address space) and RLIMIT_NOFILE (file descriptors). Defaults: 512MB AS, 256 nofile. Configurable per-plugin in plugin.yaml (new `resources:` section).
   - On macOS, use `launchctl`-style resource limits via the sandbox (intersects with sandbox-hardening audit, out of scope here).

6. **Deferred to post-beta (but document):** cgroup v2 integration on Linux, macOS `sandbox-exec` resource slices, Windows job objects.

7. **Explicit plugin contract doc.** Track J.4 should include `plugin-lifecycle-contract.md` documenting what "Unload" must guarantee, whether double-Unload is OK, how long it has to respond, what happens on timeout, etc.

## References

- `internal/plugin/subprocess/manager.go:L183-228` — Stop with nested timeouts
- `internal/plugin/subprocess/manager.go:L146-147` — 4KB stderr ring buffer
- `internal/plugin/host.go:L920, L1031` — no recover around plugin Load/Unload
- Plan §B.9 — InitParams extension (no config reload semantics)
- Plan §B.8 — lifecycle event stream (no reload event)
- Plan §G.6 — CLI commands (no "nanite plugin restart" or "nanite plugin kill")
- Finding 04 — related panic handling in event dispatch
