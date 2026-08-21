# ACP bridge adapter — Codex

**Phase:** 4 — ACP bridge adapters for non-native agents (`TASKS/agent-host-acp`)
**Status:** implemented
**Depends on:** `12`
**Touches:** new `adapters/codexacp/` (or similar) in `libs/go-agent-wrapper`, alongside the
existing native `adapters/codex/` (untouched — additive). Repo: `libs/go-agent-wrapper`
(sibling, NOT Nanite).

## Context

Per `docs/engineering/architecture/17-acp.md`: Codex has no native ACP mode — OpenAI has an
open, unresolved feature request (`openai/codex#9085`). The implementation tier is whichever
bridge library task `12` pins (candidates at review time: `beyond5959/acp-adapter`'s Codex
backend, or a standalone `codex-acp` bridge wrapping Codex's own native `app-server` JSON-RPC
— task `12`'s own findings supersede this list).

**Real capability question this task must verify, not assume**: does the pinned bridge wire
ACP's `session/cancel` through to Codex's real native interrupt (per 17-acp.md, the
`app-server`'s own `turn/interrupt` JSON-RPC method, if it genuinely exists and works as
documented) — or does it just acknowledge-and-let-finish? This planning session's own research
(task `02`) confirmed Nanite's/go-agent-wrapper's *current* non-ACP Codex adapter does **not**
call any such mechanism (stdin-close + local bookkeeping + SIGTERM/SIGKILL only, no
`turn/interrupt` call) — if the bridge task `12` pinned genuinely calls it, this is a real,
concrete interrupt-capability improvement over native Codex, worth stating clearly in the
resulting `Descriptor`.

Additive, not a replacement — existing native `adapters/codex/` stays available in parallel
(task `11` wires per-agent selection).

## What to do

1. Integrate task `12`'s pinned bridge library as a dependency of `go-agent-wrapper`.
2. Implement a new `adapters.RuntimeAdapter` using it, implementing task `08`'s ACP client
   abstraction, launching Codex via the bridge.
3. Produce a `Descriptor{Protocol: ProtocolACP, Transport: TransportStdio, Interrupt: <real,
   verified value>}`.
4. Confirm `session/update` events map onto `runtimeevents` correctly for a real
   bridge-driven Codex session.

## Done means

- A working bridge-driven Codex ACP adapter exists, implementing task `08`'s abstraction.
- The existing native `adapters/codex` package is untouched — both remain independently
  selectable via task `11`.
- A real (not mocked) bridge-driven Codex session completes at least one real turn with
  correctly-translated `runtimeevents` activity — verified live.
- The adapter's real `Interrupt` capability is verified directly, not assumed, and reflected
  accurately in the `Descriptor`.
- `go build ./...` / `go test ./...` clean in `libs/go-agent-wrapper`.

## Work Log (2026-08-21)

**Correction to this task file's own candidate list**: this file's Context section (lines
14-17) still names `beyond5959/acp-adapter`'s Codex backend and a generic "standalone
`codex-acp` bridge" as candidates. That framing is stale — task `12` is resolved, and its
actual, operator-approved decision (recorded in `TASKS/ESCALATIONS.md`'s 2026-08-21 "Task 12
resolved" entry) pins the specific npm package `agentclientprotocol/codex-acp` (which
supersedes the now-archived `zed-industries/codex-acp` — its own README states development
moved there) for Codex, `agentclientprotocol/claude-agent-acp` for Claude, `svkozak/pi-acp` for
Pi, and explicitly rejects `beyond5959/acp-adapter`. Followed the resolved decision, not this
file's own stale candidate list, per the dispatch brief.

**What was built**: `libs/go-agent-wrapper`, new package `adapters/codexacp/` (worktree
`libs/go-agent-wrapper-wt-task14`, branch `agent-host-acp/task-14-codex-acp-bridge`, commit
`4e52099`, pushed to origin). `Adapter.Name() == "codex-acp"` (distinct from the native
adapter's `"codex"`), `Describe()` returns `Provider: "codex"`, `Protocol: ProtocolACP`,
`Transport: TransportStdio`, `Interrupt: InterruptTurn` via `acp.DescriptorFor`. `Client`
(implementing task `08`'s `acp.Client`) spawns `npx -y
@agentclientprotocol/codex-acp@1.6.2` (version pinned by default — deliberately, since the
package is actively maintained/fast-moving; `WithBridgeVersion`/`WithBridgePackageSpec`
override for callers who want to track latest or a local package) and owns the subprocess
directly (same `os/exec` + id-keyed pending-map JSON-RPC pattern tasks `09`/`10` established),
because `go-providers.CLIAdapter`'s contract has the identical seam gap tasks `09`/`10` already
documented — no hook for a bidirectionally-real, response-correlated session once a caller
spawns the process through it. `cliAdapter` (this package's `adapters.RuntimeAdapter` glue)
follows the same pass-through shape (real `Detect`/`BuildArgs`, `ParseLine` returning `(nil,
nil)`) tasks `09`/`10`/the native `adapters/codex` already use for this exact situation — not
reinvented, per the dispatch brief's explicit instruction.

**Real design finding not anticipated by the task brief**: decompiling the published npm
package's own source (`dist/index.js`, no separate GitHub checkout needed — `npm pack
@agentclientprotocol/codex-acp@1.6.2` was enough) found that the bridge does NOT always drive
the caller's real system `codex` CLI — it spawns `codex app-server` via a `CODEX_PATH` env var
the bridge itself reads; if unset, it silently falls back to running its own bundled
`@openai/codex` npm dependency (pinned `^0.148.0` in the bridge's own `package.json`, a
DIFFERENT version than this machine's real system `codex` CLI, which reports `codex-cli
0.147.0`). Left unaddressed, the ACP-bridge adapter and the native `adapters/codex` adapter
could silently diverge onto two different Codex builds. Fixed by having `Client.Launch`
explicitly resolve and pass `CODEX_PATH` in the spawned bridge's environment, using the exact
same resolution precedence the native adapter's underlying go-providers resolver already uses
(`WithCodexBinary`/`WithClientCodexBinary` override, else the `CODEX_CLI_PATH` env var, else a
real `exec.LookPath("codex")`) — so both Codex adapters drive the same real install by default,
falling back to the bridge's own bundled dependency only if none of those resolve. Documented
at length in the package doc comment since it's a real, non-obvious behavior a future reader
would otherwise have to rediscover.

**Real wire/spawn behavior found** (source-verified against the published `codex-acp@1.6.2`
npm tarball, then live-verified — not assumed from the bridge's own README, matching tasks
`09`/`10`'s own precedent): the bridge's README states it "starts the Codex App Server,
translates ACP requests into Codex operations" — confirmed literally: `startCodexConnection`
in `dist/index.js` spawns a real `codex app-server` child process via `node:child_process`.
`initialize` takes an INTEGER `protocolVersion` (1, same convention `opencodeacp` found for
OpenCode's own bridge). `session/new` triggers real session authentication automatically from
the real `codex` CLI's own on-disk credentials (`~/.codex/auth.json`) with no separate
`authenticate` call needed when those credentials already exist on the host machine.
`session/update`'s discriminator field is `update.sessionUpdate` (same convention as
`opencodeacp`), with `agent_message_chunk`/`agent_thought_chunk` sharing the same
`content.type`/`content.text` shape (confirmed both from the bridge's own `zContentChunk` zod
schema and live, via a real reasoning-summary delta observed during a tool-invoking turn:
`"**Checking for boot-prompt file**"`). **A real, source-AND-live-verified per-bridge wire
divergence from `opencodeacp`**: codex-acp's `tool_call`/`tool_call_update` payloads use
`rawInput`/`rawOutput` field names, not opencode's bridge's `result`/`isError` — confirmed both
by decompiling the `zToolCallUpdate` zod schema and by a real `ls`-tool live turn through this
package's own `Client` end to end, whose `tool_call_update` correctly decoded a real
`rawOutput` payload (`{"exit_code":0,"formatted_output":"go.mod\ngo.sum\nmain.go\n"}`) into
`runtimeevents`' `result` field. `session/request_permission` was never observed live for a
plain shell tool call (Codex executed it directly — consistent with `17-acp.md`'s documented
expectation that Codex/Claude do their own fs/terminal work regardless of declared client
capabilities) but is wired for correctness regardless, mirroring `opencodeacp`'s treatment.

**Real live end-to-end verification** (not mocked, matching tasks `09`/`10`'s own precedent —
run against the real `npx @agentclientprotocol/codex-acp` bridge spawning the real system
`codex` CLI `0.147.0` with real ChatGPT-authenticated credentials already present on the
implementation machine):
1. A raw JSON-RPC probe (bypassing the Go package, driving the bridge subprocess directly)
   confirmed the `initialize` → `session/new` → `session/prompt` handshake, a real "pong" turn
   completion, a real tool-invoking (`ls`) turn with `tool_call`/`tool_call_update` events, and
   a real mid-generation cancel (2000-word-essay prompt, cancelled after 10 real delta chunks:
   the `session/prompt` response carrying `stopReason: "cancelled"` arrived ~12ms later — a
   genuine mid-turn abort, not acknowledge-and-let-finish).
2. The actual Go `Client`/package tests: `go test ./adapters/codexacp/... -v` — both
   `TestLiveClientCompletesOneRealTurn` (real "pong" turn, real `agent.delta`/`turn.completed`
   events) and `TestLiveClientCancelAbortsMidGeneration` (real mid-generation cancel; repeated
   runs measured ~12ms and ~19ms terminal-event latency after `Cancel`, both comfortably under
   the 10s discriminating threshold) pass for real, not skipped.
3. A separate throwaway Go program (outside the repo, in the scratchpad) drove `Client`
   directly through a real tool-invoking turn (`ls` + report first filename) and printed every
   translated `runtimeevents.Event` as JSON — confirmed `process.started` → `session.ready` →
   `turn.started` → `agent.delta` (thought) → `agent.tool_use` → `agent.tool_result` (with the
   real `rawOutput`→`result` mapping) → `agent.delta` (message, streamed word-by-word) →
   `turn.completed`, end to end through the real package code, not a mock.

**Real, verified `Interrupt` capability** (verified two ways, neither alone, per the task's own
"verified directly, not assumed" requirement): (1) source — decompiling `dist/index.js` found
the bridge's `session/cancel` handler (`interruptSessionTurn`/`interruptPromptTurn`) calls
`codexAcpClient.turnInterrupt({threadId, turnId})`, which sends a real `turn/interrupt`
JSON-RPC 2.0 request (`CodexAppServerClient.turnInterrupt`: `sendRequest({method:
"turn/interrupt", params})`) to the spawned `codex app-server` process — the exact mechanism
`17-acp.md` names as Codex's real native interrupt surface, which neither
`agentkit/agentsessions` nor the native `adapters/codex` currently calls (that adapter's
`Stop()` is stdin-close + SIGTERM/SIGKILL only, `InterruptProcess`); (2) live — both the raw
probe and the real Go `Client` test measured the turn's terminal event arriving ~12-19ms after
`Cancel`, well before the model would have naturally finished a 2000-word essay. Per this
task's own scope note (echoing the operator's reframing recorded in task `12`'s resolution):
this is a real, accurately-reported capability improvement (`InterruptTurn` vs. native Codex's
`InterruptProcess`), stated clearly in the `Descriptor` as instructed, but was NOT treated as a
pass/fail gate on the task — the actual goal is ACP protocol uniformity for future
config-based provider extensibility with an honest per-adapter capabilities map.

**Node.js/npm/npx runtime requirement**: documented explicitly and at length in the package doc
comment (`adapters/codexacp/doc.go`'s "Node.js/npm/npx runtime requirement" section), per the
operator's framing recorded in `TASKS/ESCALATIONS.md`'s 2026-08-21 "Task 12 resolved" entry —
an explicit, reversible choice (the `acp.Client` interface already isolates callers from the
concrete implementation), not a permanent stack commitment. Verified present on the
implementation machine: Node v22.12.0, npm/npx 10.9.0.

**Build/test status**: `go build ./...`, `go vet ./...`, `gofmt -l adapters/codexacp/`
(clean, no output), and `golangci-lint run ./adapters/codexacp/...` (0 issues) all clean in
`libs/go-agent-wrapper`. `go test ./...` clean across the whole repo (16 packages, including
the new `adapters/codexacp` at 2 real live tests plus unit/fake-subprocess tests), and `go test
./adapters/codexacp/... -race -count=1` clean. The existing native `adapters/codex/` package is
a zero-diff (`git diff --stat -- adapters/codex/` empty).

**Commit**: `libs/go-agent-wrapper`, branch `agent-host-acp/task-14-codex-acp-bridge`, commit
`4e52099`, pushed to `origin`. No version tag cut on this feature branch — three sibling tasks
(`13`/`14`/`15`) are concurrently developing on separate worktrees/branches off the same `main`
tip; cutting a tag here (rather than after the Orchestrator merges all three) risked a tag
collision/ordering conflict with a sibling doing the same independently. Left tagging to the
Orchestrator's merge step, consistent with this task's own instruction that "the Orchestrator
merges it."
