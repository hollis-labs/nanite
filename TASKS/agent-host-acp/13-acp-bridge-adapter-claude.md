# ACP bridge adapter — Claude

**Phase:** 4 — ACP bridge adapters for non-native agents (`TASKS/agent-host-acp`)
**Status:** implemented
**Depends on:** `12` (resolved — `agentclientprotocol/claude-agent-acp`, see
`TASKS/ESCALATIONS.md`'s 2026-08-21 "Task 12 resolved" entry)
**Touches:** new `adapters/claudeacp/` (or similar) in `libs/go-agent-wrapper`, alongside the
existing native `adapters/claude/` (untouched — additive). Repo: `libs/go-agent-wrapper`
(sibling, NOT Nanite).

## Context

Per `docs/engineering/architecture/17-acp.md`: Claude has no native ACP mode — Anthropic
doesn't ship one directly. The implementation tier is whichever bridge library task `12`
pins (candidates at review time: `beyond5959/acp-adapter`'s Claude backend, or
`agentclientprotocol/claude-agent-acp` standalone — task `12`'s own findings supersede
this list, use whatever it actually decided).

**Real capability question this task must verify, not assume**: does the pinned bridge wire
ACP's `session/cancel` through to Claude's real native interrupt (per 17-acp.md, the Agent
SDK's own `Query.interrupt()` — a `control_request`/`cancel_turn` frame) — if the SDK genuinely
exposes this — or does it just acknowledge-and-let-finish? This planning session's own
research (task `02`) confirmed Nanite's/go-agent-wrapper's *current* non-ACP Claude adapter
does **not** call any such mechanism (stdin-close + SIGTERM/SIGKILL only) — if the bridge task
`12` pinned genuinely does better, this is a real, concrete interrupt-capability improvement
for ACP-driven Claude over native Claude, worth stating clearly in the resulting `Descriptor`.

This is **additive**, not a replacement — the existing native `adapters/claude/` package
(Claude's own stream-json protocol) stays available in parallel, per 17-acp.md's explicit
"migration is additive, not a cutover" framing (task `11` wires per-agent selection between
them).

## What to do

1. Integrate task `12`'s pinned bridge library as a dependency of `go-agent-wrapper`.
2. Implement a new `adapters.RuntimeAdapter` using it, implementing task `08`'s ACP client
   abstraction, launching Claude via the bridge.
3. Produce a `Descriptor{Protocol: ProtocolACP, Transport: TransportStdio, Interrupt: <real,
   verified value — test whether `session/cancel` genuinely interrupts a real in-progress
   turn, don't assume from the bridge's own documentation>}`.
4. Confirm `session/update` events map onto `runtimeevents` correctly for a real
   bridge-driven Claude session.

## Done means

- A working bridge-driven Claude ACP adapter exists, implementing task `08`'s abstraction.
- The existing native `adapters/claude` package is untouched — both remain independently
  selectable via task `11`.
- A real (not mocked) bridge-driven Claude session completes at least one real turn with
  correctly-translated `runtimeevents` activity — verified live.
- The adapter's real `Interrupt` capability is verified directly (does `session/cancel`
  genuinely abort mid-turn, or just acknowledge?) and reflected accurately in the
  `Descriptor` — this is a required verification, not optional, per 17-acp.md's own explicit
  framing of this as "one real criterion worth weighing."

## Work Log

**Superseded framing, corrected first:** this file's own Context section (candidate list,
"required verification... per 17-acp.md's own explicit framing") predates task `12`'s actual
resolution. The real, operator-recorded decision (`TASKS/ESCALATIONS.md`'s 2026-08-21 "Task 12
resolved" entry) pins `agentclientprotocol/claude-agent-acp` for Claude and explicitly
reframes real interrupt capability as NOT the deciding criterion for this whole ACP effort —
worth verifying and reporting accurately (done below), but not a pass/fail gate. Proceeded
under that corrected framing per the dispatching brief.

**Repo/branch:** `libs/go-agent-wrapper`, worktree `libs/go-agent-wrapper-wt-task13`, branch
`agent-host-acp/task-13-claude-acp-bridge`. New package `adapters/claudeacp/` (7 files:
`doc.go`, `client.go`, `translate.go`, `adapter.go`, `client_test.go`, `adapter_test.go`,
`live_test.go`). `adapters/claude/` (native) is byte-for-byte untouched — confirmed via
`git status --short`/`git diff --stat` showing only the new `adapters/claudeacp/` directory
added. No `go.mod`/`go.sum` change — the bridge is a spawned Node.js subprocess (`npx -y
@agentclientprotocol/claude-agent-acp`), not a Go import, same pattern
`adapters/opencodeacp`/`adapters/copilotacp` already established for their own native
subprocess targets.

**Adapter design.** `claudeacp.Client` mirrors `adapters/opencodeacp.Client`'s structure
almost exactly (self-contained JSON-RPC 2.0-over-stdio engine: id-keyed pending-response map,
a `readLoop` classifying response/notification/server-request frames, `emit`/`closeEvents`
guarded against a concurrent process-exit race) — deliberately, since the underlying wire
protocol is the same ACP spec both providers speak, and task 09/10 already worked out the
right shape for this seam. `claudeacp.Adapter` implements `adapters.Adapter` +
`adapters.RuntimeAdapter`, `Name() = "claude-acp"` (no collision with native `adapters/claude`'s
`"claude"`), `Describe()` returns `acp.DescriptorFor(..., "claude", adapters.TransportStdio)`.
`CLIAdapter()` returns the same real-Detect/real-BuildArgs/pass-through-ParseLine `cliAdapter`
glue shape task 09/10 already established for the identical, independently-rediscovered
architecture-seam gap (`go-providers.CLIAdapter` has no hook for a bidirectionally-real,
response-correlated JSON-RPC session once agentkit owns the spawned process's stdin) — not
re-litigated, just followed.

One real design addition beyond the opencodeacp/copilotacp precedent, forced by this specific
bridge's own shape: `Client.resolveCommand()` resolves a `(binary, args)` pair, not just a
binary path, because the default spawn target is `npx -y <package>`, not a direct binary — an
extra indirection layer neither prior adapter has. Three independent override levers, each with
a functional option + an env var (`CLAUDE_ACP_NPX_PATH`, `CLAUDE_ACP_BRIDGE_PACKAGE`,
`CLAUDE_ACP_BRIDGE_PATH`) so an operator can pin an exact bridge version or bypass npx entirely
(spawn an already-`npm i -g`-installed `claude-agent-acp` binary directly) without a code
change — the reversibility lever the operator's own Task-12 framing asked to be documented
plainly, not just asserted.

**Real wire/spawn behavior found for `claude-agent-acp` 0.70.0** (verified two ways: direct
inspection of the real npm-packed `dist/acp-agent.js` — not a scraped doc — and live Node.js
JSON-RPC probes against the real spawned subprocess using real, already-configured Claude
credentials on the implementation machine):

- The bridge wraps `@anthropic-ai/claude-agent-sdk` directly — it does NOT shell out to the
  `claude` CLI binary (except under its own separate `--cli` passthrough flag, unused here). No
  subcommand is needed (unlike `opencode acp`) — the bare `npx -y
  @agentclientprotocol/claude-agent-acp` invocation is itself the ACP stdio server.
- `initialize`/`session/new`/`session/prompt` round-trip exactly like `adapters/opencodeacp`'s
  own verified shapes: integer `protocolVersion: 1`, `{cwd, mcpServers: []}` for `session/new`,
  a generic `{stopReason, usage}` `session/prompt` response (Claude's usage object uses
  different field names — `inputTokens`/`outputTokens`/`cachedReadTokens`/`cachedWriteTokens`/
  `totalTokens` — than OpenCode's, but both decode through the same untyped-passthrough `usage`
  payload key, so no adapter-side schema is needed for either). Live-verified: a real one-word
  prompt ("Reply with exactly one word: pong") produced the literal text "pong" via two
  `agent_message_chunk` deltas ("p", "ong") and a `{"stopReason":"end_turn",...}` response.
- **Two real, concrete divergences from `adapters/opencodeacp`'s own verified shapes were
  found and handled explicitly** (documented in `doc.go`/`translate.go`, and exercised by a
  dedicated fake-subprocess test in `client_test.go` so a future regression would be caught,
  not silently reintroduced):
  1. `agent_thought_chunk` nests the thought text under `content: {type: "text", text: "..."}`
     — the SAME shape `agent_message_chunk` uses — not OpenCode's flat top-level `thought`
     string field. Source-verified via the bridge's real `thinking`/`thinking_delta` branch in
     `acp-agent.js`; not live-triggered on demand (model/effort-dependent), so this one
     divergence is source-verified rather than wire-captured, but against the literal bytes of
     the exact npm-packed version this adapter spawns. Decoding OpenCode's flat `thought` field
     here would have silently produced an always-empty `phase:"thought"` delta for every real
     Claude thinking chunk — exactly the class of silent-degradation bug task 11's own review
     caught in the native ACP session backend (reasoning content silently dropped). Caught here
     before it shipped, not after a review pass.
  2. `tool_call_update` carries `rawOutput` (not OpenCode's `result`) for the tool's final
     output, and carries no boolean `isError` field at all — error state is folded entirely
     into `status: "failed"` vs `"completed"`. Live-verified directly: a real Bash tool call
     (`echo hello-from-tool-probe`, run by asking Claude to use its Bash tool) produced ONE
     `tool_call` notification followed by FOUR `tool_call_update` notifications — three
     intermediate refinements (streaming input completion, a human-readable title) carrying
     neither `status` nor `rawOutput`, and only the final one carrying both. `handleNotification`
     emits a `KindAgentToolResult` event for every `tool_call_update` frame regardless of
     whether it's the terminal one (matching `adapters/opencodeacp`'s own unconditional
     per-frame forwarding policy and 17-acp.md's plain "tool_call_update → agent.tool_result"
     mapping) — flagged plainly in the doc comment that downstream consumers should expect more
     than one `agent.tool_result` event per real Claude tool call, most partial.
- `session/request_permission` (a server-initiated request) has the same structural shape
  `adapters/opencodeacp` already handles generically (`{options, sessionId, toolCall:
  {toolCallId, rawInput, ...}}`), confirmed by direct source inspection of the bridge's
  `requestPermissionFromClient` call sites — reused the identical generic
  emit-request/respond-cancelled/emit-resolved handling, no new logic needed. Live-verified
  that Claude does NOT invoke this method (nor the declared-false `fs`/`terminal` client
  capabilities) for at least the one real Bash tool-call shape tested — it executes tools
  internally regardless of declared client capabilities, consistent with 17-acp.md's documented
  expectation, though not exhaustively confirmed for every ACP-proxyable operation (e.g. file
  edits, which 17-acp.md separately flags as a genuinely per-agent unknown).
- `session/cancel` is a bare notification (no `id`, no response), confirmed both by spec
  conformance and live behavior.

**Real, verified Interrupt capability — `adapters.InterruptTurn`, verified two ways, not
assumed:**
1. **Source-verified**: the bridge's real `dist/acp-agent.js` `cancel()` method calls `await
   session.query.interrupt()` — the Claude Agent SDK's own genuine mid-turn interrupt call —
   with an `AbortController`-based force-cancel backstop armed alongside it in case a wedged
   query doesn't yield in time. This directly corroborates the research finding already
   recorded in `TASKS/ESCALATIONS.md`'s "Task 12 resolved" entry ("source-verified real
   `session/cancel` → `query.interrupt()` wiring with an `AbortController` fallback") — this
   task independently re-verified it against the actual npm-packed source rather than trusting
   that prior finding at face value.
2. **Live-verified**: a real, deliberately long (2000-word-essay) prompt was cancelled after
   observing 3 real `agent_message_chunk` deltas (via both a standalone Node.js probe and the
   real Go `TestLiveClientCancelAbortsMidGeneration` test). The `session/prompt` response
   (`{"stopReason":"cancelled","usage":{...all zero...}}`) arrived 5.6–7ms after `session/cancel`
   was sent across multiple runs — not after the model would have naturally finished a
   2000-word essay. Genuine mid-turn abort, not acknowledge-and-let-finish.

Per the operator's own explicit framing (context this task was dispatched under, and recorded
in `TASKS/ESCALATIONS.md`'s "Task 12 resolved" entry): this is a real, concrete interrupt
improvement over the native (non-ACP) `adapters/claude`'s `InterruptProcess`-only behavior
(stdin-close + SIGTERM/SIGKILL, no wire-level interrupt at all) for the ACP-bridged path
specifically — worth stating accurately in the `Descriptor` (done: `Interrupt:
adapters.InterruptTurn`) — but per that same framing, not itself the reason this task or the
whole ACP effort exists; the actual goal is protocol-level uniformity with an honest
per-adapter capabilities map.

**Live end-to-end verification — real commands/output, not mocked:**
- `go test ./adapters/claudeacp/... -v -run '^Test' -count=1` → `ok` (all unit tests +
  fake-subprocess tests + both live tests pass). Live test log lines:
  - `TestLiveClientCompletesOneRealTurn`: `real claude-agent-acp turn completed; message text
    observed: "pong"; terminal payload: {"stop_reason":"end_turn","usage":{"cachedReadTokens":
    24462,"cachedWriteTokens":16703,"inputTokens":2,"outputTokens":4,"totalTokens":41171}}`
  - `TestLiveClientCancelAbortsMidGeneration`: `turn terminal event (turn.completed) arrived
    5.618458ms after Cancel — payload: {"stop_reason":"cancelled","usage":{"cachedReadTokens":0,
    "cachedWriteTokens":0,"inputTokens":0,"outputTokens":0,"totalTokens":0}}`
- Also independently verified via two standalone Node.js JSON-RPC probe scripts (spawning the
  exact same `npx -y @agentclientprotocol/claude-agent-acp` command the Go adapter spawns,
  driven by hand-written JSON-RPC framing, no Go code involved) before writing any Go code —
  confirms the wire behavior the Go adapter was built against was real, not inferred from the
  Go test's own assumptions.
- `go build ./...`, `go vet ./...` clean. `gofmt -l .` clean (no output). Full-repo `go test
  ./... -count=1` clean across all 16 packages. `go test ./adapters/claudeacp/... -race
  -count=1` clean (no data races).

**Node.js/npm/npx runtime requirement — documented explicitly** in `doc.go`'s package comment
(a dedicated "Node.js/npm/npx runtime requirement" section) and echoed in `client.go`'s
`defaultBridgePackage` doc comment: this specific adapter (`adapters/claudeacp`, not the native
`adapters/claude`, and not any other provider's non-bridge-mediated ACP adapter) requires
Node.js/npm/npx at runtime, spawning `npx -y @agentclientprotocol/claude-agent-acp` by default.
Framed explicitly as a real but reversible choice, per the operator's own Task-12 framing:
`acp.Client`'s interface already isolates every caller from which concrete implementation sits
behind it, so swapping to a pure-Go bridge later is a new `acp.Client` implementation, not a
rearchitecture. The three env var / functional-option overrides
(`CLAUDE_ACP_NPX_PATH`/`CLAUDE_ACP_BRIDGE_PACKAGE`/`CLAUDE_ACP_BRIDGE_PATH`) give a concrete,
already-built escape hatch (pin a version, or bypass npx entirely once the bridge is installed
some other way) rather than leaving the "reversible" claim purely aspirational.

**Confirmation the native `adapters/claude` package stays untouched**: `git status
--short`/`git diff --stat` on the worktree before committing showed only the new
`adapters/claudeacp/` directory added — zero lines changed anywhere else in the repo,
`adapters/claude/*.go` byte-for-byte identical to `main`.

**Deviations from the task file's literal "What to do"**: none of substance. Step 1's "task
`12`'s pinned bridge library" is read as the actual resolved decision
(`agentclientprotocol/claude-agent-acp`), not this file's own stale candidate list, per the
"decision vs. rationale" distinction this project's process already establishes. Everything
else (steps 2–4) was executed as scoped.

**Commit:** `10ce8a5` on branch `agent-host-acp/task-13-claude-acp-bridge` in
`libs/go-agent-wrapper` (worktree `libs/go-agent-wrapper-wt-task13`). Branch pushed to
`origin` (no tag cut — versioning/merge-to-main is the Orchestrator's own step per this task's
dispatch instructions, not performed here).
- `go build ./...` / `go test ./...` clean in `libs/go-agent-wrapper`.
