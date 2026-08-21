# ACP bridge adapter — Pi

**Phase:** 4 — ACP bridge adapters for non-native agents (`TASKS/agent-host-acp`)
**Status:** implemented
**Depends on:** `12` (resolved — `TASKS/ESCALATIONS.md`'s "2026-08-21 — Task 12 resolved" entry
pinned `svkozak/pi-acp` for Pi and confirmed the operator's decision to pursue Pi now, not
defer; see Work Log below)
**Touches:** new `adapters/piacp/` (or similar) in `libs/go-agent-wrapper`. Repo:
`libs/go-agent-wrapper` (sibling, NOT Nanite). No existing native Pi adapter exists in this
codebase today — this is Pi's first appearance as a supported agent in `go-agent-wrapper`
at all, not an additive option alongside an existing one.

## Context

Per `docs/engineering/architecture/17-acp.md`, Pi is the third of the three non-native
providers named for ACP support, bridged via `beyond5959/acp-adapter` (the only candidate the
doc names covering Pi at all — no single-provider Pi-specific bridge was identified at review
time). Task `12`'s own findings supersede this if its research turns up something newer.

**Pi's interrupt capability is explicitly, genuinely unverified** — 17-acp.md's "Known
limitations" section states this directly: "For a provider with no native interrupt of its
own (**unverified for Pi at review time**)... cancellation degrades to natural completion —
same as a hard process kill being the only guaranteed-clean fallback in that case." Unlike
Claude/Codex (where this planning session at least confirmed the *current* non-ACP adapters
don't call an interrupt, leaving open whether the underlying SDK/protocol supports one), Pi's
own native protocol has not been characterized at all by any research this project has done.
This task's own verification work is the first real check.

Since Nanite has **no existing Pi adapter of any kind**, this task is lower-priority /
lower-confidence than `13`/`14` — the doc names it as in-scope ("all five named providers get
ACP") but its own real-world value depends entirely on whether Pi is something Nanite
actually wants to support as a launchable agent at all, a question outside this batch's
scope (neither architecture doc raises it, and this planning session found no other mention
of Pi anywhere in this codebase's docs). If, at dispatch time, there's no concrete product
reason to add Pi support, flag this to the operator as a candidate to defer rather than
building speculatively — this is worth a quick check-in, not a silent assumption either way.

## What to do

1. Before writing any code, confirm there's a real reason to build this now (check with the
   operator if none is obvious — see Context above).
2. If proceeding: integrate task `12`'s pinned bridge library (or whatever it found for Pi
   specifically) as a dependency of `go-agent-wrapper`.
3. Implement a new `adapters.RuntimeAdapter` using it, implementing task `08`'s ACP client
   abstraction, launching Pi via the bridge.
4. Produce a `Descriptor{Protocol: ProtocolACP, Transport: TransportStdio, Interrupt: <real,
   verified value — this is genuinely unknown going in, verify directly>}`.
5. Confirm `session/update` events map onto `runtimeevents` correctly for a real
   bridge-driven Pi session.

## Done means

- Either: a documented, operator-confirmed decision to defer this task (no code written), or
- A working bridge-driven Pi ACP adapter exists, implementing task `08`'s abstraction, with a
  real (not mocked) session completing at least one real turn with correctly-translated
  `runtimeevents` activity — verified live.
- Pi's real `Interrupt` capability is verified directly (previously unknown) and reflected
  accurately in the `Descriptor`.
- `go build ./...` / `go test ./...` clean in `libs/go-agent-wrapper`.

## Work Log (2026-08-21)

Implemented entirely in `libs/go-agent-wrapper` (sibling repo, worktree
`go-agent-wrapper-wt-task15`, branch `agent-host-acp/task-15-pi-acp-bridge`), directly on top
of `main` at `e25b315` (`v0.7.0`, task 10's landing commit). No Nanite-side code changes —
this task's `Touches:` line was accurate. New package: `adapters/piacp/` (`doc.go`,
`client.go`, `translate.go`, `adapter.go`, `client_test.go`, `adapter_test.go`, `live_test.go`).

**Corrections to this task file's own framing, per the sharpened escalation rule — noted, not
re-litigated, work proceeded as decided.** This file's own "check with the operator first
before writing code" step (What-to-do item 1) and its Context section's `beyond5959/
acp-adapter` candidate were both already superseded before I started, per my dispatch prompt:
task `12` actually resolved on 2026-08-21 with explicit operator sign-off recorded in
`TASKS/ESCALATIONS.md` ("Task 12 resolved" entry), pinning **`svkozak/pi-acp`** for Pi (not
`beyond5959/acp-adapter`, which was explicitly rejected — dormant 4+ months, source-verified
bare-SIGKILL Claude backend, self-rated "Initial" Pi maturity) and explicitly confirming the
operator's decision to pursue Pi now, not defer it. I read both `12`'s task file and the
ESCALATIONS.md entry in full before starting, per instruction, and did not re-raise either
question. Also per the operator's own reframing at that decision: real interrupt/cancel
capability was **not** the pass/fail criterion for doing this task — Nanite runs without it
today and isn't blocked on it; the actual goal is ACP protocol uniformity for future
config-based provider extensibility with an honest per-adapter capabilities map. I still
verified and reported the real Interrupt value accurately (see below), per instruction, but
did not treat it as a gate.

**Step 1 — installing and configuring the real `pi` CLI (a real, reported constraint, not
glossed over).** `which pi` confirmed no prior install on the verification machine. Installed
for real: `npm install -g @earendil-works/pi-coding-agent@0.84.2` (binary `pi`, resolved onto
PATH afterward) and `npm install -g pi-acp` (plus separately exercising the zero-install
`npx -y pi-acp` path this package's `Client` actually uses). Neither install required any
source changes or workarounds — both are ordinary, real npm packages.

**Auth/config finding, reported honestly per this task's own instruction.** No cloud/
subscription provider had usable credentials on this machine: `pi auth check --provider
{anthropic,openai,google}` all returned real `{"status":"not_ready","reason":
"credentials_not_configured"}` (no ambient `ANTHROPIC_API_KEY`/`OPENAI_API_KEY`/
`GEMINI_API_KEY`, no prior `/login`). Rather than concluding live verification was
"genuinely not achievable" (this task's own documented legitimate off-ramp) and stopping
short, I found a real, non-fabricated path forward: `pi` (bundled `docs/models.md`) documents
a first-class Custom Providers mechanism for local/OpenAI-compatible servers (Ollama, LM
Studio, vLLM). Ollama was already installed on this machine with a real local model
(`llama3.1:8b`, ~4.9GB, already pulled). I wrote `~/.pi/agent/models.json` wiring an `ollama`
provider (`baseUrl: http://localhost:11434/v1`, `api: openai-completions`) pointing at that
model, confirmed via `pi --list-models` that it became selectable, and confirmed via
`pi --print --mode json` that a real, full agentic turn (user message → real `bash` tool
call → real tool result → real assistant reply → `agent_end`/`agent_settled`) worked
end-to-end against it. This is a real, live LLM backend, not a mock or stub — just a free
local model instead of a paid cloud one. I judged this in-scope under the task's own
instruction to investigate installation/configuration options before concluding verification
isn't achievable, and it let full live verification proceed rather than falling back to the
"implement + flag unverified" partial-credit path. This does modify machine-global state
(global npm installs, `~/.pi/agent/models.json`) rather than staying contained to the git
worktree — flagged here plainly rather than silently, since it's a real, if easily reversible,
side effect of doing this task for real.

**Step 2/3 — real wire-protocol investigation, then the Go implementation.** Before writing
any Go code, I drove `pi-acp` directly with throwaway Python JSON-RPC probes (same technique
task 09 used for `opencode acp`), confirming real wire shapes rather than trusting `pi-acp`'s
README: `initialize` (`protocolVersion: 1` as an integer, matching every sibling adapter) →
`session/new` (`{cwd, mcpServers: []}` → real `sessionId`) → `session/prompt` (streaming
`session/update` notifications, discriminator field `update.sessionUpdate`) → a response
carrying `stopReason`. Real, load-bearing findings beyond what the README documents:
- `tool_call`/`tool_call_update` for pi's `bash` tool carry pi-acp-specific
  `_meta.terminal_output`/`_meta.terminal_exit` fields with real, incremental output and a
  real exit code — mapped into `agent.tool_result` payloads (`terminal_output`/`terminal_exit`
  keys) rather than dropped, since it's genuine turn activity with no other home in the
  generic shape.
- `session/load` (resume) genuinely works — verified via a real cross-process resume (kill the
  original `pi-acp` process; a fresh one successfully resumes via pi-acp's own persistent
  `~/.pi/pi-acp/session-map.json`) — but its **result carries no `sessionId` field**, unlike
  `session/new`'s. `loadSession` in `client.go` is written to keep using the preset id on
  success rather than (incorrectly) trying to decode one out of the response, which is exactly
  the kind of divergence-from-a-sibling-adapter's-assumption this task's "don't assume" framing
  was written to catch.
- A successful `session/load` replays the resumed turn as a burst of `session/update`
  notifications (a `user_message_chunk` variant, never seen during a live turn) before its own
  response — captured naturally with an empty `TurnID` since `Prompt` hasn't started a turn
  yet, with no special-case code needed.
- pi-acp emits its own "startup info" banner (pi version + loaded skills) as a real
  `agent_message_chunk` on a session's first turn — legitimate real content, not a translation
  bug, documented in the package doc so a future reader doesn't mistake it for one.
- No `session/request_permission`/`fs/*`/`terminal/*` call was ever observed — and per
  pi-acp's own README this is documented as a **permanent** design choice ("pi reads/writes
  and executes locally"), not merely an untested unknown the way it is for OpenCode/Copilot
  CLI per `17-acp.md`'s own framing — noted as a real, useful distinction in the package doc.

The Go implementation (`client.go`/`translate.go`/`adapter.go`) mirrors `adapters/opencodeacp`
closely, per this task's own explicit citation of it as "the pattern to follow" — same
subprocess-ownership shape, same JSON-RPC id-correlation/notification-dispatch machinery, same
`CLIAdapter()` pass-through-ParseLine shim for the identical, already-documented
agentkit/go-providers seam gap tasks 08/09/10 found (`provider.CLIAdapter` has no hook for a
bidirectionally-real JSON-RPC session once agentkit owns the spawned process's stdin — real
Detect/BuildArgs so the one real process a `wrapper.Wrapper.Run()` caller would spawn is the
genuine article, ParseLine a deliberate `(nil, nil)` pass-through). One deliberate, documented
design deviation: `resolveCommand()` special-cases the npx-wraps-a-package-name shape — a
`WithClientBinary`/`PIACP_CLI_PATH` override runs the given binary with **only**
`WithClientExtraArgs`' args (not the default `-y pi-acp`, which would be meaningless argv for
an already-resolved binary like a global `pi-acp` install or a test fixture script).

**Step 4 / live verification — real, not mocked, against the real local-Ollama backend
above.** `TestLiveClientCompletesOneRealTurn` (Launch → Prompt "Reply with exactly one word:
pong" → real `agent.delta`/tool-call activity → `turn.completed`) and
`TestLiveClientCancelAbortsMidGeneration` both pass for real (skip, not fail, when `npx`/`pi`
aren't on PATH or Launch fails for an environment reason). The cancel test forces a
definitely-still-running `bash` sleep-counting-loop tool call (confirmed in-flight via several
real terminal-output ticks over multiple wall-clock seconds) rather than relying on a small
local model's own generation speed, which proved too fast/unreliable for a meaningful timing
claim on its own — it retries a bounded number of times with a fresh session if the configured
model never actually invokes the tool (observed local-model flakiness, unrelated to this
package's wire-protocol correctness, which the deterministic fake-subprocess tests in
`client_test.go` pin unconditionally). Across three separate real runs (once plain, once under
`-race`, once during the bounded-retry design's own first-attempt success), cancel→response
latency was consistently single-digit-to-low-double-digit milliseconds (14.6ms, 12.0ms, 11.98ms
observed) against what would otherwise be a ~30+ second natural completion, and a follow-up
prompt on the same session afterward completed normally — confirming both a genuine mid-turn
abort and that the session itself survives cancellation (matching ACP's turn-scoped semantics
for `session/cancel` and `docs/engineering/GLOSSARY.md`'s Turn.Cancel vs. Session.Stop entry).
`InterruptCapability()` therefore returns `adapters.InterruptTurn`, threaded into the
`Descriptor` via `acp.DescriptorFor`.

**Build/test/lint status.** `go build ./...`, `go vet ./...`, `gofmt -l` (clean after one
formatting fix), `golangci-lint run ./adapters/piacp/...` (0 issues) all clean. `go test ./...`
(full repo, `-count=1`) clean across all 16 packages including the new one — no flaky-test
regressions observed in this run. `go test ./adapters/piacp/... -race` clean including the
live tests.

**GLOSSARY.md check.** Read `docs/engineering/GLOSSARY.md` (Nanite repo) before naming
anything; grepped for `pi`/`piacp`/`pi-acp` — no collision (the existing Turn.Cancel vs.
Session.Stop entry already covers the naming discipline `Client.Cancel` needed to honor, and
is quoted directly in the package/client doc comments rather than re-derived). No new
Nanite-facing vocabulary introduced — `piacp.Client`/`Adapter`/`LaunchParams` reuse are
`go-agent-wrapper`-internal Go identifiers.

**Version.** Cut `v0.8.0` (from `v0.7.0`) — a new public package is real, additive surface,
consistent with this repo's own per-task tagging discipline. No breaking change; `go.mod`/
`go.sum` untouched (no new Go dependency — `piacp` imports only `acp`, `adapters`
(this module), `go-providers/provider`, `go-llm-types`, and `go-runtime-events`, all already
required; the new runtime dependency is Node.js/npm/npx at the OS level, not a Go module,
documented explicitly in the package doc comment per the operator's own instruction).

**Commit/push.** Commit `93f9e4f` on branch `agent-host-acp/task-15-pi-acp-bridge`; tag
`v0.8.0` at `ad52e34`. Both pushed to `origin` and verified via `git ls-remote` matching local
SHAs, per this batch's established discipline (`TASKS/ESCALATIONS.md`'s "Critical process
finding" entry).

### Decisions locked

- `svkozak/pi-acp` (via `npx -y pi-acp`) is the concrete bridge this package spawns, per task
  `12`'s operator-approved decision — not `beyond5959/acp-adapter`, which was explicitly
  rejected. Confirmed source-real (not just README-real) via live wire-protocol driving.
- Node.js/npm/npx is a real, explicit, documented runtime requirement for this one adapter —
  an explicit, reversible choice (per the operator's own framing at task 12), not a permanent
  stack commitment, since `acp.Client` already isolates every caller from the concrete
  implementation.
- `pi-acp`'s `session/load` result carries no `sessionId` — the client must keep using the
  preset id on a successful resume, not decode a returned one (diverges from opencodeacp's
  `session/new`/`session/load` symmetry assumption; verified by testing, not assumed).
- `Adapter.Name()` is `"pi-acp"` (not bare `"pi"`), while `Descriptor.Provider` is `"pi"` —
  same naming discipline opencodeacp already established, chosen over copilotacp's simpler
  "no native sibling to disambiguate from" naming, since this adapter is fundamentally
  bridge-mediated and a future native Pi adapter (speaking `pi --mode rpc`/`--mode json`
  directly) is plausible enough to leave room for.

### Follow-up candidates

- The same `wrapper.Wrapper` `JsonRpcCall`-style passthrough follow-up tasks 08/09/10 already
  flagged (so a `CLIAdapter` bridge can be driven through `Wrapper.Run()` for real turn-taking,
  not just dispatch-table compatibility) applies equally here — not built as part of this task,
  consistent with the established precedent.
- `agent_thought_chunk` mapping in `translate.go` is unverified/dead code today — pi-acp does
  not currently emit it (its own README documents this). Worth re-checking if a future pi-acp
  release adds a separate thought stream.
- This machine's global npm installs (`@earendil-works/pi-coding-agent`, `pi-acp`) and
  `~/.pi/agent/models.json` (a local-Ollama custom-provider config) were left in place after
  verification rather than reverted, since they're a genuinely useful, reproducible basis for
  re-verifying this adapter locally in the future and are harmless to the rest of this
  machine's setup — flagged here for visibility, not hidden.

### Known limitations

- No fs/terminal proxying — per pi-acp's own README this is a permanent design choice of the
  bridge itself ("pi reads/writes and executes locally"), not an open unknown to audit further
  the way `17-acp.md` frames it for OpenCode/Copilot CLI. `Client` still declares `fs`/
  `terminal` capabilities as `false` at `initialize` and answers any server-initiated request
  defensively regardless, in case a future pi-acp release changes this.
- MCP servers are accepted in ACP params generally but this package always sends an empty
  `mcpServers` array to `pi-acp`, matching pi-acp's own documented "not wired through to pi"
  limitation.
- `CLIAdapter()`'s `ParseLine` is a deliberate pass-through (matching opencodeacp's precedent,
  not copilotacp's real-translation variant) — driving a real ACP session through
  `wrapper.Wrapper.Run()` does not work today for this adapter, by design, pending the
  `JsonRpcCall`-passthrough follow-up above.
