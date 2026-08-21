# Build the ACP client abstraction (Launch/Prompt/Cancel/Events)

**Phase:** 3 — ACP client abstraction & native adapters (`TASKS/agent-host-acp`)
**Status:** implemented
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

## Work Log (2026-08-21)

Implemented entirely in `libs/go-agent-wrapper` (sibling repo), directly on top of `main` at
`4eed6c7` (`v0.4.0`). No Nanite-side code changes — this task's `Touches:` line was accurate.

**Interface design.** New `acp` package (`acp/doc.go`, `acp/client.go`), alongside `adapters/`:

```go
type Client interface {
	Launch(ctx context.Context, params LaunchParams) error
	Prompt(ctx context.Context, prompt string) error
	Cancel(ctx context.Context) error
	Events() <-chan runtimeevents.Event
	InterruptCapability() adapters.InterruptCapability
	Close(ctx context.Context) error
}
```

Beyond the four verbs the task names explicitly (`Launch`/`Prompt`/`Cancel`/`Events`), I added
two supporting methods, both judged in-scope under "methods covering Launch/Prompt/Cancel/
Events... exact method signatures are this task's own design call":
- `InterruptCapability() adapters.InterruptCapability` — the literal mechanism item 1(c) asks
  for ("wire it through the same InterruptCapability vocabulary task 02 added"). A real,
  compile-time Go dependency on `adapters.InterruptCapability`, not a parallel enum.
- `Close(ctx) error` — session-level teardown, distinct from `Cancel`'s turn-level scope,
  named `Close` (not `Stop`) specifically to avoid colliding with Nanite's own `Session.Stop`
  concept while remaining its semantic analog. Without it there's no way to release resources
  Launch acquired; `Launch` itself must return promptly (not block for the whole session) since
  ACP's `session/cancel` is turn-scoped and the session must remain promptable afterward — see
  the doc's own naming-correction note and `GLOSSARY.md`'s `Turn.Cancel` vs. `Session.Stop`
  entry, both quoted verbatim in the interface's doc comments.

`Client.Events()` returns `<-chan runtimeevents.Event` directly (Kind/Payload/TurnID populated,
ID/Sequence/SessionID/App left zero for the caller to fill via its own
`Emitter`/`activity.Bridge`) — the literal `runtimeevents` type, not a parallel shape, mirroring
the existing `(kind, payload)` split `wrapper/event_translator.go` already uses for every other
adapter.

`LaunchParams` (`Cwd`, `Env`, `SystemPrompt`, `SessionIDPreset`) mirrors existing
`adapters.ResolveContext`/`wrapper.Config` conventions rather than inventing new vocabulary.

**`acp.DescriptorFor(client, providerName, transport) adapters.Descriptor`** — the one place a
concrete ACP-backed `Adapter.Describe()` threads `Client.InterruptCapability()` into
`Descriptor.Interrupt`; `Protocol` is always `adapters.ProtocolACP`, `Transport` is threaded
through (stdio for native direct-wire agents, tcp for daemon modes like Copilot CLI's `--acp`).
Pure struct composition — no protocol logic.

**Wiring into `wrapper.Wrapper`'s dispatch.** Investigated whether an ACP-backed `Adapter`
would need a *new* `wrapper.Wrapper` execution path or could ride the existing
`RuntimeAdapter.CLIAdapter()` → agentkit path. Concluded the latter: ACP is JSON-RPC 2.0 over
stdio, the same wire framing agentkit's `JsonRpcStdio` runtime already speaks generically for
Codex's app-server (confirmed by reading `agentkit/agentsessions/jsonrpc_stdio_session.go` —
its reader loop is framing-generic, gated only on `json.Unmarshal` succeeding, not on any
Codex-specific method names). Added the `ProtocolACP`+`TransportStdio` case to
`wrapper/runtime_dispatch.go`'s `runtimeCaps`/`runtimeSourceChannel`/`legacyRuntimeToken`
(`RuntimeACPStdio = "acp-stdio"`, new — no legacy predecessor since ACP didn't exist pre-split).
This is the "wiring" item 2 of "What to do" and the "consumed by `wrapper.Wrapper` the same way
any other adapter is" Done-means bullet require, read as: task 02 already generalized the
dispatch *mechanism* (Protocol/Transport-keyed table), so adding one new table entry for the
new Protocol is the expected incremental step, not a restructure. `ProtocolACP`+`TransportTCP`
is deliberately left unmapped — agentkit has no TCP-session runtime kind — with a dedicated
test (`TestRuntimeCapsACPTCPUnmapped`) pinning that as a documented, intentional gap rather than
an accidental one.

**Verification.** Extended the existing table-driven tests in `runtime_dispatch_test.go` (same
pattern as the other three real adapters) plus a new `acp/client_test.go` (fake `Client` +
`TestClientContract`/`TestDescriptorFor`/`TestDescriptorForTCPTransport`) plus a new
`wrapper/wrapper_acp_test.go`: a fake/no-op `acp.Client`, composed into a fake
`adapters.RuntimeAdapter` via `acp.DescriptorFor` and a minimal `provider.CLIAdapter` shim
(`BuildArgs` calls `Client.Launch`; `ParseLine` calls `Client.Prompt` and drains `Events()`),
driven end-to-end through the *real* `wrapper.Wrapper.Run()` → agentkit jsonrpc-stdio path
against a trivial fake shell script (JSON-echo, no real ACP shape). Confirms: no
`ErrUnknownRuntime`; `Client.Launch`/`Prompt` genuinely invoked from the spawn/parse-line path
(not just declared side by side); `Client.Events()` output reaches `runtimeevents.Event` values
in the sink via the existing translation path; `Process.Runtime` carries the new
`RuntimeACPStdio` token. Zero ACP wire-format knowledge appears anywhere in the fakes (no
`session/new`/`session/prompt` method names, no JSON-RPC request/response shapes) — confirmed
this stays within the task's explicit "no concrete ACP connection logic" boundary; that's
tasks `09`/`10`/Phase 4.

**Build/test status.** `go build ./...`, `go vet ./...`, `go test ./...` (including `-race` on
`acp`/`wrapper`) clean. Found and documented (not fixed — unrelated to this task) a
pre-existing flaky test: `TestRunEndToEndAdapterRuntime` (`wrapper/wrapper_integration_test.go`)
intermittently fails "sequence not monotonic" under repeated runs (`-count=20`). Reproduced
independently on a clean `v0.4.0` checkout (`4eed6c7`) in an isolated git worktree, confirming
it predates this task's changes entirely — logged in `go-agent-wrapper`'s own `CHANGELOG.md`
v0.5.0 entry as a "Notes" item, not filed as a Nanite escalation since it's an internal
`go-agent-wrapper` test-suite concern with no Nanite-visible symptom yet.

**GLOSSARY.md check.** Read `docs/engineering/GLOSSARY.md` (Nanite repo) before naming anything.
No new Nanite-facing vocabulary introduced — `acp.Client`/`LaunchParams`/`DescriptorFor` are
`go-agent-wrapper`-internal Go identifiers, not new Nanite domain concepts, so no glossary entry
added. Confirmed the existing `Turn.Cancel` vs. `Session.Stop` vs. ACP `session/cancel` entry
already covers the naming discipline this task's `Cancel` method needed to honor, and quoted it
directly in the interface's doc comments rather than re-deriving it.

**Version.** Cut `v0.5.0` (from `v0.4.0`) — a new public package + a new `Descriptor`
Protocol/Transport dispatch-table entry is real, additive surface, consistent with this
repo's own per-task tagging discipline (`v0.2.0`→`v0.3.0`→`v0.4.0` each correspond to one
landed `TASKS/agent-host-acp` item) and task `01`'s "no operator sign-off needed for a version
number" precedent. No breaking change; `go.mod` untouched (no new dependency — `acp` imports
only `adapters`, already-in-module, and `go-runtime-events`, already required).

**Commit/push.** Commit `4b100bd` on `go-agent-wrapper`'s `main`; tag `v0.5.0` at `e11c05d`.
Both pushed to `origin` and verified via `git ls-remote` matching local SHAs — per this batch's
established discipline (`TASKS/ESCALATIONS.md`'s "Critical process finding" entry) of not
leaving landed work unpushed.
