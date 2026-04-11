# [Low] Collateral observations in plugin code

**Scope:** plugin system
**Topic:** collateral
**Date:** 2026-04-10

Items noticed while reading the plugin system code for plan verification. None of these are ship-blockers; each is a small quality concern worth logging.

## L1 — Protocol version check citation is slightly off in plan

**Problem:** Plan §A.2 says:

> **Enforce protocol version handshake.** `internal/plugin/subprocess/plugin.go:133-138` reads `initResult` without comparing `initResult.Protocol` against `ProtocolVersion`

Lines 133-138 are actually the mutex-guarded field assignment after init returns. The check should be inserted between lines 131 and 133 (right after `if err != nil { return }`). Cosmetic — not wrong in intent, just off by a few lines.

**Evidence:** `internal/plugin/subprocess/plugin.go:L128-138`.

**Recommendation:** Update plan citation to `L119-131` (the init CallResult block) when doing a pass over the plan for execution.

---

## L2 — Dead ownership map: `connectorOwners`

**Problem:** `Host.connectorOwners map[string]string` is populated in `RegisterConnector` but never consulted anywhere, including in UnloadPlugin. It's dead bookkeeping until the unregister work from finding 05 lands.

**Evidence:** `internal/plugin/host.go:L64, L94, L123` (declared and initialized); grep for `connectorOwners` in the rest of the package shows only writes, no reads.

**Recommendation:** Either wire it up during finding 05's B.6a backfill, or remove it until there's a consumer.

---

## L3 — Transport `nextID` is atomic but ID correlation is not used

**Problem:** `transport.nextID = atomic.Int64` and every Call allocates a unique ID, but the ID is never used to route responses — the serialized Call/read pattern just assumes the next line from stdout is the response to the just-sent request. This is fragile (a plugin that emits a log line to stdout by mistake corrupts the transport) and the atomic type is signaling an intent that the code doesn't implement.

**Evidence:** `internal/plugin/subprocess/transport.go:L21-74`.

**Recommendation:** Covered by finding 02's concurrent transport rework. Until then, add a comment explaining the current design: "ID is allocated for future ID-based routing; currently correlation is positional under mu." Or just use a plain int64 counter since atomics aren't needed under a mutex.

---

## L4 — `Host.SetPluginConfig` is exported but only used internally

**Problem:** `Host.SetPluginConfig` is public (capitalized) but only called from `loader.go`. Public API surface for no consumer. Plan §I.4 mentions "Host.SetStore public method signature leak" but misses this one.

**Evidence:** `internal/plugin/host.go` — search for `SetPluginConfig`.

**Recommendation:** Make it unexported (`setPluginConfig`) as part of Track I.4's "public method leak" cleanup.

---

## L5 — `TriggerDispatcher.Dispatch` is fire-and-forget but `matchFilter`/`renderPayload` are synchronous

**Problem:** The reviewer-context notes that the filter/disabled checks in `Dispatch` are synchronous before the goroutine spawn. Current code at `triggers.go:L58-81` still runs `matchFilter` and `renderPayload` in the caller's goroutine — these are fast in the happy path but `template.Execute` on user-authored templates can loop or panic. If a plugin registers a bad trigger payload template, every event of that type blocks the emitter.

**Evidence:** `internal/plugin/triggers.go:L44-82`.

**Recommendation:** Move rule filtering and template rendering into the per-rule goroutine, not the dispatcher caller. Also covered by finding 04's recover() placement.

---

## L6 — `Manager.attemptRestart` uses `time.Sleep` instead of a cancellable select

**Problem:** `manager.go:L292 time.Sleep(backoff)` in the restart loop blocks up to MaxBackoff (30s). If the host is shutting down during that sleep, `Shutdown()` has to wait for the sleep to finish.

**Evidence:** `internal/plugin/subprocess/manager.go:L281-311`.

**Recommendation:** Replace with `select { case <-time.After(backoff): case <-h.ctx.Done(): return }`. Small fix, improves shutdown responsiveness.

---

## L7 — `ringBuffer.Write` is not safe for concurrent writes

**Problem:** `manager.go:L360` `ringBuffer.Write` mutates `rb.pos` and `rb.buf` without synchronization. `cmd.Stderr = stderr` means Go's exec.Cmd writes to it from an internal goroutine; if anyone else reads `rb.String()` during a write, it can see partial state. Low impact (only affects diagnostic output), but still a data race that `go test -race` will catch.

**Evidence:** `internal/plugin/subprocess/manager.go:L353-378`.

**Recommendation:** Add a `sync.Mutex` to `ringBuffer` and guard Write + String. Trivial fix.

---

## L8 — `parseEntrypoint` in `loader.go` doesn't handle quoted arguments

**Problem:** `strings.Fields(entrypoint)` splits on whitespace, so `"python3 my plugin.py"` becomes `["python3", "my", "plugin.py"]` instead of `["python3", "my plugin.py"]`. A plugin with a space in its path (common on macOS with iCloud-synced directories like `~/Library/Mobile Documents/`) will silently fail.

**Evidence:** `internal/plugin/loader.go:L177-183`.

**Recommendation:** Use `mvdan/sh` or `github.com/google/shlex` for shell-like splitting. Low priority but a trap waiting for a user.

---

## L9 — `allplugins.go` blank imports couple compile-in plugins to the binary

**Problem:** `internal/plugin/allplugins/allplugins.go` blank-imports every builtin plugin. Adding a new builtin means editing this file. Plan §I expects core plugins to stay compiled in, but doesn't specify whether this file stays authoritative or is generated. Risk of drift between `allplugins` and `plugins.yaml` / `core_plugins.yaml` (Track D.3).

**Evidence:** `internal/plugin/allplugins/allplugins.go:L10-27`.

**Recommendation:** During Track D.3's `core_plugins.yaml` work, add a build-time generator that produces `allplugins.go` from `core_plugins.yaml` so there's one source of truth.

---

## L10 — `Host.RegisterCommand` never validates command name collisions across plugins

**Problem:** Two plugins registering the same command name silently overwrite each other in `chat.CommandRegistry.Register` at `commands.go:L118-122`:

```go
func (r *CommandRegistry) Register(cmd SlashCommand, handler CommandHandler) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.commands[cmd.Name] = registeredCommand{SlashCommand: cmd, handler: handler}
}
```

No collision check, no warning. The second plugin's command silently wins. For the yaml-authoritative reserved-names list (plan §1 mentions `nanite/pkg/plugin/reserved.go`), this is fine for reserved names but not for plugin-to-plugin collisions.

**Evidence:** `internal/chat/commands.go:L117-122`.

**Recommendation:** Add a collision check during yaml-authoritative loader work (B.4). Emit a log warning + `SkippedRegistration` for the losing plugin.

---
