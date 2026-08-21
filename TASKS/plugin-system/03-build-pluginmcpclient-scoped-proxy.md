# Build PluginMCPClient — scoped proxy replacing raw GetService("mcp")

**Phase:** 2 — Tier 1 hygiene: scoped service proxies (`TASKS/plugin-system`)
**Status:** not-started
**Depends on:** `02` (reuses its per-plugin-identity-at-`GetService`-time pattern and the
plugin-ID slug validator it introduces — don't re-invent either here)
**Touches:** new file (e.g. `internal/plugin/pluginmcpclient.go`), `internal/plugin/host.go`
(`GetService`/`RegisterService`), `cmd/nanite/main.go` (the `"mcp"` service registration).
Reference-only: `internal/mcp/manager.go` (`*mcp.Manager`'s full API surface).

## Context

Sibling to `02` — same "Target design: capability model" section, same "Tier 1 gets scoped
proxies, not enforcement" decision, resolving audit finding 03 (`GetService("mcp")` grants
full `*mcp.Manager` access — tool execution, server add/remove/list, transport lifecycle) and
contributing to finding 07 (untyped `GetService`, alongside `02`).

This planning session's own research independently re-verified:

- **The specific offenders the audit named (`plugins/support-ticket`, `plugins/fragments-engine`)
  are both gone from this repo, not merely moved.** `support-ticket` was cut in full
  (`TASKS/phase-0/15c-cut-support-ticket.md`) — its own Work Log notes it was never actually a
  `plugins/support-ticket/` source directory in this repo, contradicting the audit's citation.
  `fragments-engine` was extracted into its own separate repo during "phase-2 track-a"
  (commit `069944be`, "remove fragments-engine + envelope emission sweep") — `plugins/` itself
  is now gitignored except for `plugins/repos.yaml` (a fetch catalog listing external repos,
  not vendored source). Confirmed by grep: no source file in this repo calls
  `GetService("mcp")` today. Same as `02`: this is new infrastructure, not a fix to a live
  offender.
- **Current `"mcp"` registration**: `cmd/nanite/main.go:305` registers the full
  `*mcp.Manager` (constructed via `initMCP`, `main.go:966`) under the name `"mcp"`.
- **`GetService`/`RegisterService` and the per-plugin-identity constraint are exactly as
  described in `02`'s Context** — same `host.go:541-570`, same `h.activePlugin` window. Don't
  re-derive this here; follow `02`'s established pattern for capturing a per-plugin-scoped
  instance once inside `Load()`.

## What to do

1. Define a `PluginMCPClient` interface (new file) offering a narrower, typed surface than the
   full `*mcp.Manager`. Per the audit's own recommendation (finding 03) and this decision's
   "hygiene not enforcement" framing for Tier 1: expose `AddServer` (so a builtin can register
   its *own* MCP servers, a real, used capability — see e.g. any builtin that adds a server
   today) and a typed `ExecuteTool`/`CallTool` — since builtins are trusted, this does not need
   a manifest-declared tool allowlist (that's the Tier 2 pattern in `06`, a genuinely different
   mechanism for a genuinely less-trusted tier). Do not expose `RemoveServer`,
   transport-lifecycle management, or other manager-wide administrative methods through this
   type unless a real, current builtin plugin needs one — if you find one during
   implementation that does, note it in the Work Log rather than silently widening the
   interface past what's actually used.
2. Wire `PluginMCPClient` as what `GetService("mcp")` returns going forward, following exactly
   `02`'s capture-once-inside-`Load()` pattern (same `h.activePlugin` validity window, same
   plugin-ID slug validator from `02` if the client type needs the calling plugin's ID for any
   reason — e.g. attributing tool-execution telemetry). Remove the raw `*mcp.Manager` from the
   `"mcp"` service-registry entry entirely — no opt-out escape hatch, same reasoning as `02`
   (a builtin needing raw manager access can import `internal/mcp` directly, same trust as any
   other core code).
3. Add a GLOSSARY.md entry for `PluginMCPClient` — check `02`'s entries first, keep terminology
   consistent (e.g. how both describe "Tier 1 hygiene, not enforcement").

## Done means

- No builtin plugin can obtain the raw `*mcp.Manager` via `GetService("mcp")` anymore —
  verified by a test asserting the returned type is `PluginMCPClient`, not `*mcp.Manager`.
- A test builtin plugin can add its own MCP server and execute a tool through
  `PluginMCPClient`, end-to-end against a real (test-scoped) MCP manager, not mocked.
- `PluginMCPClient`'s exposed method set is deliberately narrower than the full `*mcp.Manager`
  API — verified by a compile-time or reflection-based test confirming no `RemoveServer`/
  transport-management method is reachable through the returned type.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
