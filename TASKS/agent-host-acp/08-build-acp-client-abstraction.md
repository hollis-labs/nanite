# Build the ACP client abstraction (Launch/Prompt/Cancel/Events)

**Phase:** 3 — ACP client abstraction & native adapters (`TASKS/agent-host-acp`)
**Status:** not-started
**Depends on:** `02` (Descriptor Protocol/Transport/Interrupt split); recommended after `07`
(host migration validated) for risk-sequencing reasons, not a hard technical blocker.
**Touches:** new package in `libs/go-agent-wrapper` (e.g. `acp/`), alongside `adapters/`.
Repo: `libs/go-agent-wrapper` (sibling, NOT Nanite).

## Context

`docs/engineering/architecture/17-acp.md` is explicit that this batch's real subject is
ACP-as-*client* — a Hollis host process driving an underlying CLI agent over ACP instead of
that agent's bespoke wire format. "Nothing in the portfolio does this today." ACP-as-*server*
(Tether's `mux acp`, `internal/acpadapter`) is prior art, explicitly out of scope, untouched
by this batch.

The doc specifies the shape directly — not left open for this task to invent:

```
Hollis Host API              (go-agent-wrapper's Adapter/RuntimeAdapter seam)
      │
      ▼
ACP client abstraction       (stable: Launch/Prompt/Cancel/Events — one Go interface)
      │
      ▼
ACP implementation/adapters  (swappable per underlying agent)
```

"The abstraction is the 'intent' tier — defined once, next to the host, following the same
driver pattern already used elsewhere in this codebase (`memory_write`/`knowledge_write` →
provider-agnostic; `go-providers.CLIAdapter` → per-CLI concrete adapters)."

Two kinds of implementation sit underneath this one interface (built by separate tasks — this
task builds only the interface + wiring, not any implementation): **native ACP agents**
(OpenCode via `opencode acp`, Copilot CLI via `--acp` — tasks `09`/`10`, thin direct wire
connections, no bridging) and **non-native agents** (Claude, Codex, Pi — Phase 4, a
third-party bridge library speaking ACP on the CLI's behalf).

`session/update`'s already-standardized ACP shape "maps almost 1:1 onto `go-runtime-events`"
per 17-acp.md: message/thought chunks → `agent.delta`, `tool_call`/`tool_call_update` →
`agent.tool_use`/`agent.tool_result`, permission requests →
`agent.permission_requested`/`resolved`. An ACP-driving adapter should feed the *existing*
activity bridge (task `06`'s migrated `wrapper/event_translator.go` path) — it does not need a
new event vocabulary invented for it.

No new top-level `agents.runtime_kind` DB value is implied by anything in this task — per both
docs, ACP-driven agents route through the existing `cli` value; `Protocol`/`Transport` (task
`02`) are internal to the host descriptor.

## What to do

1. Define the ACP client abstraction as a single Go interface, in a new package alongside
   `adapters/` (e.g. `acp/client.go`), following the doc's own naming: methods covering
   `Launch`, `Prompt`, `Cancel`, and an `Events` stream/channel — exact method signatures are
   this task's own design call, but must satisfy: (a) any implementation can be plugged in
   swappably (native direct-connection or bridge-mediated) without the caller knowing which;
   (b) `Events` output maps cleanly onto `runtimeevents.Event` (reuse existing types from
   `go-runtime-events`, don't invent a parallel event shape); (c) `Cancel` surfaces whatever
   real capability the underlying ACP `session/cancel` call has — wire it through the same
   `InterruptCapability` vocabulary task `02` added to `Descriptor`, since a given ACP
   implementation's real cancel behavior is a per-adapter fact, not a protocol-level
   guarantee (per 17-acp.md's "Known limitations" section — a bridge may or may not wire
   `session/cancel` through to the provider's native interrupt).
2. Wire this new abstraction into the existing `adapters.Adapter`/`RuntimeAdapter` seam from
   task `02` — an ACP-backed agent should still produce a `Descriptor` with
   `Protocol: ProtocolACP` and the appropriate `Transport` (`stdio` or `tcp`, per whichever
   real agent it's launching), consumed by `wrapper.Wrapper` the same way any other adapter
   is.
3. Do not implement any concrete ACP connection logic in this task — that's tasks `09`/`10`
   (native) and Phase 4 (bridged). This task builds the interface and its wiring into the
   existing `Adapter`/`Descriptor`/`wrapper.Wrapper` seams only.
4. Check `docs/engineering/GLOSSARY.md` before introducing any new name.

## Done means

- A single, well-defined ACP client Go interface exists in `go-agent-wrapper`, next to
  `adapters/`.
- It plugs into the existing `Adapter`/`RuntimeAdapter`/`Descriptor` seam from task `02`
  without requiring changes to `wrapper.Wrapper`'s own dispatch logic beyond what task `02`
  already generalized (`Protocol`/`Transport`-keyed, not runtime-string-keyed).
- Events surface through the existing `runtimeevents` types, not a parallel vocabulary.
- No concrete ACP wire-connection code exists yet (that's `09`/`10`/Phase 4) — this task is
  interface-and-wiring only, verifiable via a fake/no-op implementation satisfying the
  interface plus a test proving it dispatches through `wrapper.Wrapper` correctly.
- `go build ./...` / `go test ./...` clean in `libs/go-agent-wrapper`.
