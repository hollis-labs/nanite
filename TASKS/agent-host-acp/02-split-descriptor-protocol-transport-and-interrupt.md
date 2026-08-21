# Split Descriptor.Runtime into Protocol/Transport, add a real interrupt-capability field

**Phase:** 1 — Host foundation (`TASKS/agent-host-acp`)
**Status:** reviewed
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

## Work log

**Repo/branch:** `libs/go-agent-wrapper`, worktree
`libs/go-agent-wrapper-wt-task02`, branch `agent-host-acp/task-02-split-descriptor`, commit
**`371c9d0`** (parent `a248ab4`).

### What landed

1. **`adapters/adapter.go`** — replaced `Descriptor.Runtime string` with three typed fields
   and their const blocks:
   - `type Protocol string` with `ProtocolClaudeStreamJSON = "claude-stream-json"`,
     `ProtocolCodexAppServer = "codex-app-server"`, `ProtocolOpenCodeNative =
     "opencode-native"`, `ProtocolACP = "acp"` — matching the task brief verbatim — plus one
     addition not in the brief's example, **`ProtocolPTYRaw = "pty-raw"`**, needed to answer
     "What to do" item 5 (see below).
   - `type Transport string` with `TransportStdio`, `TransportTCP`, `TransportHTTPSSE =
     "http-sse"`, `TransportPTY = "pty"` — matching the brief verbatim.
   - `type InterruptCapability string` with `InterruptNone`, `InterruptProcess`,
     `InterruptTurn`, `InterruptSteer` — matching the brief verbatim.
   - `Descriptor` gained `Protocol`, `Transport`, `Interrupt` fields alongside the unchanged
     `Provider`/`Channels`. `Runtime` is fully gone (confirmed via grep — zero remaining
     `Descriptor.Runtime` or `desc.Runtime` references anywhere in the repo).

2. **The `ProtocolPTYRaw` naming call (item 5 of "What to do")** — the task said "map
   [PTY] to `TransportPTY` with whatever `Protocol` value is least surprising... use your
   judgment and document the call." None of the four given `Protocol` constants (all
   structured wire formats) fit a bare PTY byte stream, so I added a fifth,
   `ProtocolPTYRaw Protocol = "pty-raw"`, documented as "no structured wire protocol at all —
   raw terminal bytes" and explicitly called out as unused by any shipped adapter. This keeps
   the PTY dispatch entry in `runtime_dispatch.go`'s table well-typed and discoverable rather
   than reusing `""` (the empty-value sentinel, which I reserved for the pre-existing
   "adapter"/subprocess-per-turn fallback shape instead — see next point) or forcing PTY onto
   one of the four structured-protocol constants, which would have been actively misleading.

3. **Three shipped adapters' `Describe()`** (`adapters/claude/claude.go`,
   `adapters/codex/codex.go`, `adapters/opencode/opencode.go`) updated to the exact verified
   values from the task's Context:
   - Claude: `{ProtocolClaudeStreamJSON, TransportStdio, InterruptProcess}`
   - Codex: `{ProtocolCodexAppServer, TransportStdio, InterruptProcess}`
   - OpenCode: `{ProtocolOpenCodeNative, TransportHTTPSSE, InterruptTurn}`
   Doc comments on each `Describe()` now name the specific `agentsessions` session type and
   cite the interrupt behavior verified in this task's Context (stdin-close +
   SIGTERM/SIGKILL-only for Claude/Codex; native `/global/dispose` +
   `/session/{id}/abort` HTTP calls before the same escalation for OpenCode).

4. **`wrapper/runtime_dispatch.go`** — rewritten to key off `(adapters.Protocol,
   adapters.Transport)` pairs instead of one string:
   - `runtimeCaps(protocol, transport)` — same five outcomes as before (PTY/streaming-stdio/
     jsonrpc-stdio/http-sse/adapter-fallback), same `ErrUnknownRuntime` error path for
     unrecognized pairs.
   - `runtimeSourceChannel(protocol, transport)` — preserves the original behavior exactly,
     including a detail worth flagging: the pre-split code had **no dedicated case for
     `http-sse`** — it fell through to the same `default: return ChannelStdio` as every other
     unrecognized token. I kept that as-is (OpenCode's typed channel is still `ChannelStdio`,
     not a new dedicated http-sse channel) since the task requires "no behavior change... for
     the three existing adapters," and `runtimeevents.SourceChannel` doesn't even define an
     http-sse constant.
   - `rawSourceChannel(protocol, transport)` — re-expressed to check `transport ==
     TransportPTY` alone rather than requiring an exact `(protocol, transport)` tuple match.
     This is a real, deliberate generalization beyond a literal one-to-one port: raw
     stdin/stdout/stderr byte-stream events are observed at the pipe layer regardless of which
     structured protocol is parsing them, so keying this specific function on the Transport
     axis alone is more correct than keying it on the full tuple — and it's provably
     behavior-identical for all four existing call sites (PTY is the only transport that ever
     produced `ChannelPTY` here; everything else produced `ChannelStdio` either way). Documented
     inline.
   - Added `legacyRuntimeToken(protocol, transport)` — new helper, not explicitly requested by
     the task but needed to satisfy an implicit constraint: `wrapper.go:214` mirrors
     `desc.Runtime` into `runtimeevents.Process.Runtime`, a plain `string` field in the
     **separate `go-runtime-events` repo**, explicitly out of this task's touch list. Since
     that field can no longer be filled from a now-nonexistent `desc.Runtime`, I derive the
     exact same historical string value (`"pty"`/`"streaming-stdio"`/`"jsonrpc-stdio"`/
     `"http-sse"`/`"adapter"`) from the new `Protocol`+`Transport` pair, so any downstream
     consumer of that mirrored field (none exist today — zero adopters per 16-agent-host.md)
     sees byte-identical output to before this refactor.
   - Kept the four legacy string constants (`RuntimePTY`, `RuntimeStreamingStdio`,
     `RuntimeJSONRPCStdio`, `RuntimeHTTPSSE`, `RuntimeAdapter`) exported under their original
     names, per the task's explicit instruction to "Keep `wrapper.RuntimePTY`... working" —
     repurposed as the legacy-token vocabulary `legacyRuntimeToken` returns from, rather than
     as dispatch keys.

5. **`wrapper/wrapper.go`** — all four read sites (`runtimeCaps`, the `Process{Runtime: ...}`
   literal, `runtimeSourceChannel`, `rawSourceChannel`) updated to pass `desc.Protocol,
   desc.Transport` (or the new `legacyRuntimeToken(...)` call for the `Process.Runtime`
   mirror). Doc comment above `Run()` updated to reference the new fields.

6. **Test updates** (not in the task's `Touches` list, but required for compilation and to
   satisfy "Done means"'s own requirement that existing tests "stay green, or are updated to
   reflect the type change without changing what they assert"):
   - `adapters/adapter_test.go`, `adapters/{claude,codex,opencode}/*_test.go`: assertions on
     `desc.Runtime` replaced with assertions on `desc.Protocol`/`desc.Transport`/
     `desc.Interrupt` against the same real values `Describe()` now returns — strictly
     stronger coverage than before (the old tests never checked interrupt capability at all,
     because the field didn't exist).
   - `wrapper/wrapper_test.go`'s `stubAdapter` and `wrapper/wrapper_integration_test.go`'s
     `fakeRuntimeAdapter` updated to the new Descriptor shape (the latter now leaves
     `Protocol`/`Transport` at their zero values to represent the same subprocess-per-turn
     fallback shape the old `Runtime: RuntimeAdapter` literal selected).
   - `wrapper/runtime_dispatch_test.go` rewritten in full for the new function signatures;
     added `TestRawSourceChannel` and `TestLegacyRuntimeToken` (neither existed before —
     `rawSourceChannel` had no dedicated test previously) for coverage on the two new/changed
     surfaces.

### Verification

- `go build ./...`, `go vet ./...`, `go test ./...`, and `go test -race ./...` all clean in
  `libs/go-agent-wrapper-wt-task02` (11 packages, all passing).
- `gofmt -l .` — no output (all files already formatted).
- Grepped the full repo post-change for `desc.Runtime`, `Descriptor{...Runtime`, and any bare
  `.Runtime string` — zero hits against `adapters.Descriptor`. The one remaining `Runtime:
  "pty"` hit (`activity/bridge_test.go:18`) is a literal against `runtimeevents.Process`
  (the separate go-runtime-events package's own, still-string `Runtime` field) — unrelated to
  this task's scope, confirmed correct to leave untouched.

### Deviations from the literal brief

- Added a fifth `Protocol` constant (`ProtocolPTYRaw`) not present in the brief's illustrative
  code block — the brief itself flagged this as "this task's own call" for the PTY case (item
  5) and explicitly invited using judgment. See point 2 above for the reasoning.
- Added `legacyRuntimeToken` — not mentioned in the brief's "What to do" list — to preserve
  the `runtimeevents.Process.Runtime` mirror's exact output. The brief's "Done means" section
  requires "no behavior change... for the three existing adapters," and the emitted-event
  string is observable behavior even though it isn't explicitly named as a call site in the
  brief's own read-site list at `wrapper/wrapper.go:214`. Flagging this explicitly since it's
  the one place this task reaches (read-only) into a field owned by a different repo
  (go-runtime-events) without modifying that repo itself.
- `rawSourceChannel`'s re-keying onto `Transport` alone instead of a `(Protocol, Transport)`
  tuple is a real design choice beyond a literal 1:1 port (see point 4 above) — flagging it
  here rather than letting it pass as an unremarked implementation detail, even though it is
  provably behavior-identical for all current call sites.
- Did not touch `CHANGELOG.md` — it wasn't in the task's `Touches` list, has no existing
  "Unreleased" section to append to, and the shared file is a plausible collision point with
  the parallel `01` task in the sibling worktree; leaving it for the Orchestrator's merge step.

### Follow-up candidates (explicitly out of scope for this task, flagged per the brief's own instruction)

- **Real mid-turn interrupt for Claude/Codex is not implemented** — this task only makes the
  existing kill-only limitation visible/queryable via `InterruptProcess`; it does not wire a
  real interrupt call. Per the brief's own Context section: Claude's Agent SDK exposes
  `Query.interrupt()` and Codex's `app-server` has `turn/interrupt` as documented native
  mechanisms neither `agentkit/agentsessions.streamingStdioSession` nor
  `.jsonRpcStdioSession` currently calls. Closing that gap is real, separate work — touches
  `libs/agentkit`, not `go-agent-wrapper` — and would also need to reconcile with Nanite's own
  existing bespoke-code TODO at `internal/service/agent_deps.go:772-776` (same limitation,
  independently noted).

### Notes on the parallel `01` task

Per the assignment brief, this task is parallel-safe with `01` (disjoint files) and I did not
touch or inspect the `libs/go-agent-wrapper-wt-task01` worktree. No coordination was needed —
confirmed the file sets don't overlap by checking this task's own diff (`adapters/adapter.go`,
`adapters/{claude,codex,opencode}/*.go`, `wrapper/wrapper.go`, `wrapper/runtime_dispatch.go`,
plus the test files listed above).

**Orchestrator merge note (2026-08-21):** Independently re-verified this task's claims
directly (git log/diff/grep-for-Runtime/build/vet/test/race in the worktree) before merging —
all confirmed, including that `Descriptor.Runtime` has zero remaining references anywhere in
the repo. Merged to `libs/go-agent-wrapper`'s `main` via `git merge --no-ff` as commit
`83524e0`, after task `01`. Post-merge `go build`/`go vet`/`go test`/`go test -race` all clean
across all 11 packages. Worktree and branch removed after merge.

## Review notes

Fresh Reviewer (no shared context with this task's worker), 2026-08-21 — **PASS**, reviewed
together with task `01` as Phase 1's logical section, against `libs/go-agent-wrapper`'s merge
commit `83524e0`. Independently re-verified against source, not the Work Log's narrative:
Claude/Codex `InterruptProcess` and OpenCode `InterruptTurn` values checked directly against
`agentkit/agentsessions/{streaming_stdio_session,jsonrpc_stdio_session,serve_http_session}.go`
— accurate. `runtimeCaps`/`runtimeSourceChannel`/`rawSourceChannel` behavior-preservation
claim checked line-by-line against the pre-split switch for all three shipped adapters' real
`(Protocol, Transport)` pairs, including the `http-sse`-has-no-dedicated-channel-case detail —
confirmed identical. Both flagged deviations (`ProtocolPTYRaw`, `legacyRuntimeToken`) judged
reasonable, in-scope, not scope creep. `git diff a248ab4 HEAD --stat` shows exactly the files
named in this task's `Touches` list — no stray scope expansion. No `GLOSSARY.md` collision.
Independently ran `go build`/`go vet`/`go test`/`go test -race -count=1` clean.

One non-blocking, explicitly-not-confident finding, logged as its own `TASKS/ESCALATIONS.md`
heads-up entry (2026-08-21, "Phase 1 review: `legacyRuntimeToken`'s empty-Descriptor
zero-value mirrors a different string than pre-refactor") rather than silently passed over:
`legacyRuntimeToken`'s `protocol=="" && transport==""` branch returns `"adapter"` where the
old code would have mirrored a literal `Runtime: ""` Descriptor as `""` — but no real or test
call site, before or after this task, ever actually constructs that zero-value Descriptor, so
this doesn't affect any current behavior. Does not change the PASS verdict.
