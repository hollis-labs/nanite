# Split Descriptor.Runtime into Protocol/Transport, add a real interrupt-capability field

**Phase:** 1 — Host foundation (`TASKS/agent-host-acp`)
**Status:** not-started
**Depends on:** none directly (parallel-safe with `01` — disjoint files)
**Touches:** `libs/go-agent-wrapper/adapters/adapter.go`, `adapters/claude/claude.go`,
`adapters/codex/codex.go`, `adapters/opencode/opencode.go`, `wrapper/wrapper.go`,
`wrapper/runtime_dispatch.go`. Repo: `libs/go-agent-wrapper` (sibling, NOT Nanite).

## Context

Both architecture docs identify the same real gap independently: `adapters.Descriptor`'s
`Runtime` field is a bare `string` that conflates two different axes — *protocol* (the shape
of the wire format: Claude's stream-json, Codex's app-server JSON-RPC, OpenCode's native
protocol, or ACP) and *transport* (the medium: stdio, TCP, HTTP+SSE, PTY). This collapse was
harmless while every runtime happened to imply a unique transport, but ACP breaks that
assumption — the same protocol (ACP) can run over stdio or TCP, and a provider's native
protocol is a separate axis from ACP entirely. `docs/engineering/architecture/17-acp.md`
requires this split land "before or alongside the ACP adapter, not after" — the ACP adapter
tasks (`09`, `10`, `13`-`15`) depend on this task.

**Verified directly against the current code** (planning-session research, not assumed):

- `adapters.Descriptor` struct, `adapters/adapter.go:29-45`:
  ```go
  type Descriptor struct {
      Provider string   // line 32
      Runtime  string   // line 37 — bare string
      Channels []runtimeevents.SourceChannel  // line 44
  }
  ```
- Every write site: `adapters/claude/claude.go:58` sets `"streaming-stdio"`;
  `adapters/codex/codex.go:55` sets `"jsonrpc-stdio"`; `adapters/opencode/opencode.go:58`
  sets `"http-sse"`. A fourth constant, `wrapper.RuntimePTY = "pty"`, exists in
  `wrapper/runtime_dispatch.go:14-24` but **no shipped adapter sets it today** — PTY runtime
  is wired in dispatch but has no concrete adapter, per `go-agent-wrapper`'s own CHANGELOG.
- Every read site: `wrapper/wrapper.go:207` (`runtimeCaps(desc.Runtime)`),
  `wrapper/wrapper.go:214` (`runtimeevents.Process{Runtime: desc.Runtime}`),
  `wrapper/wrapper.go:217` (`runtimeSourceChannel(desc.Runtime)`),
  `wrapper/wrapper.go:221` (`rawSourceChannel(desc.Runtime)`).
- Dispatch/mapping logic keyed on the same four string values:
  `wrapper/runtime_dispatch.go:14-24` (the constants), `:29-44` (`runtimeCaps`), `:51-62`
  (`runtimeSourceChannel`), `:70-75` (`rawSourceChannel`).

That's the complete call-site list — six files total, all in `go-agent-wrapper`. No other
file in the repo reads or writes `Descriptor.Runtime` (confirmed by grep).

**Second, related finding this task should also close**, from 17-acp.md's "Known
limitations" section: the doc calls for "a small capability-discovery vocabulary (something
like `interrupt: none | process | turn | steer`)... advertised per Protocol/Transport
combination, so the host can accurately report... instead of presenting a uniform `Stop()`
that quietly means different things underneath." This planning session already did the
verification 17-acp.md asked for at implementation time — the real, per-adapter answer,
traced directly into `agentkit/agentsessions` (where `Stop()` actually lives; **none of the
three `adapters/{claude,codex,opencode}` packages implement `Stop`/`Cancel`/`Interrupt`
themselves** — they're pure exec-shape descriptors):

- **Claude** (`streaming-stdio` → `agentsessions.streamingStdioSession`),
  `libs/agentkit/agentsessions/streaming_stdio_session.go:669-719`: closes stdin (comment:
  "well-behaved CLI agents... exit cleanly on EOF"), 2s grace, then `SIGTERM`, then 5s grace,
  then `SIGKILL`. **No wire-level cancel/interrupt frame is ever sent.**
- **Codex** (`jsonrpc-stdio` → `agentsessions.jsonRpcStdioSession`),
  `libs/agentkit/agentsessions/jsonrpc_stdio_session.go:771-822`: same shape — stdin close,
  local in-flight-call bookkeeping cleanup (not a wire message), then the identical
  2s-EOF-grace → `SIGTERM` → 5s-grace → `SIGKILL` escalation. **No JSON-RPC `turn/interrupt`
  (or equivalent) call is sent before killing the process.**
- **OpenCode** (`http-sse` → `agentsessions.serveHTTPSession`),
  `libs/agentkit/agentsessions/serve_http_session.go:524-564`: the one real exception — calls
  `POST {baseURL}/global/dispose` then `POST {baseURL}/session/{id}/abort` (native HTTP
  endpoints), cancels the SSE stream context, waits 500ms, *then* the same
  `SIGTERM`/`SIGKILL` escalation.

So today: Claude and Codex genuinely offer only `interrupt: process` (kill-only, no real
mid-turn cancel); OpenCode genuinely offers something closer to `interrupt: turn` (a real
native abort call happens before any signal). This is **not a regression from the current
state of anything** — Nanite's own bespoke code has the identical Claude/Codex limitation
today (see task `06`'s Context) and an existing TODO acknowledging it
(`internal/service/agent_deps.go:772-776`). This task's job is to make the *existing* gap
visible and queryable on the descriptor, not to close it — actually wiring a real interrupt
call for Claude/Codex (if it's even possible — Anthropic/OpenAI's own SDKs may expose such a
mechanism the current `agentkit/agentsessions` code simply doesn't call) is out of scope here;
flag it as a real follow-up candidate in your Work Log if you want to preserve it for a later
task, but don't build it as part of this one.

## What to do

1. Replace `Descriptor.Runtime string` (`adapters/adapter.go:37`) with two typed fields:
   ```go
   type Protocol string
   type Transport string

   const (
       ProtocolClaudeStreamJSON Protocol = "claude-stream-json"
       ProtocolCodexAppServer   Protocol = "codex-app-server"
       ProtocolOpenCodeNative   Protocol = "opencode-native"
       ProtocolACP              Protocol = "acp"

       TransportStdio  Transport = "stdio"
       TransportTCP    Transport = "tcp"
       TransportHTTPSSE Transport = "http-sse"
       TransportPTY    Transport = "pty"
   )
   ```
   (Exact type/const names are this task's own call — match this repo's existing naming
   conventions in `adapters/adapter.go`, not necessarily verbatim what's above.)
2. Add a third field for the interrupt-capability vocabulary from 17-acp.md's "Known
   limitations" section:
   ```go
   type InterruptCapability string
   const (
       InterruptNone    InterruptCapability = "none"
       InterruptProcess InterruptCapability = "process"  // kill-only, no wire-level cancel
       InterruptTurn    InterruptCapability = "turn"      // real native mid-turn cancel
       InterruptSteer   InterruptCapability = "steer"     // reserved, not used by any current adapter
   )
   ```
3. Update the three shipped adapters' `Describe()` (`adapters/claude/claude.go:58`,
   `adapters/codex/codex.go:55`, `adapters/opencode/opencode.go:58`) to set the new
   `Protocol`/`Transport`/`Interrupt` fields with the real, verified values from this task's
   Context: Claude = `{ProtocolClaudeStreamJSON, TransportStdio, InterruptProcess}`; Codex =
   `{ProtocolCodexAppServer, TransportStdio, InterruptProcess}`; OpenCode =
   `{ProtocolOpenCodeNative, TransportHTTPSSE, InterruptTurn}`.
4. Update all four read sites in `wrapper/wrapper.go` (lines 207, 214, 217, 221) and the
   dispatch/mapping functions in `wrapper/runtime_dispatch.go` (lines 14-24, 29-44, 51-62,
   70-75) to key off `Protocol`+`Transport` instead of the single `Runtime` string. The
   dispatch logic's actual behavior (which `agentsessions.Capabilities` flag gets set, which
   source channel is used) should be unchanged — this is a type-safety/clarity refactor, not
   a behavior change, for the three existing adapters.
5. Keep `wrapper.RuntimePTY`/the PTY dispatch path working even though no adapter ships it
   yet — map it to `TransportPTY` with whatever `Protocol` value is least surprising (there is
   no real PTY-speaking adapter to model this on; use your judgment and document the call).

## Done means

- `Descriptor.Runtime` (the bare string) is gone; `Protocol`+`Transport`+`Interrupt` fields
  exist in its place.
- All three shipped adapters (claude/codex/opencode) advertise the real, verified values from
  this task's Context, not placeholder ones.
- `wrapper/wrapper.go` and `wrapper/runtime_dispatch.go` compile against the new fields with
  no behavior change for the three existing adapters (their existing tests — including
  `adapters/adapter_test.go` and whatever `wrapper` package tests reference `Descriptor` —
  stay green, or are updated to reflect the type change without changing what they assert).
- `go build ./...` / `go test ./...` clean in `libs/go-agent-wrapper`.
- No new top-level DB concept is implied by this task — this is entirely internal to
  go-agent-wrapper's own descriptor type, consistent with 17-acp.md's own explicit framing
  ("`Protocol`/`Transport` are internal to the host descriptor, not a database concern").
