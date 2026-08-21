# Native ACP adapter — OpenCode

**Phase:** 3 — ACP client abstraction & native adapters (`TASKS/agent-host-acp`)
**Status:** implemented
**Depends on:** `08`
**Touches:** new `adapters/opencodeacp/` (or similar) in `libs/go-agent-wrapper`, alongside
`adapters/opencode/` (the existing native-protocol adapter, untouched — this is additive).
Repo: `libs/go-agent-wrapper` (sibling, NOT Nanite).

## Context

`docs/engineering/architecture/17-acp.md`: "Native ACP agents are real and current:
`opencode acp` (OpenCode's documented, shipped ACP subprocess mode)... Either can be driven by
a thin passthrough adapter — no translation layer needed." This is the lower-risk half of
Phase 3/4's decision matrix — "Settled shape, low risk," per the doc's own framing, unlike the
bridge-library decision Phase 4's `12` gates on.

This adapter is **additive, not a replacement** for the existing `adapters/opencode` package
(OpenCode's own native, non-ACP protocol) — per 17-acp.md's explicit "migration is additive,
not a cutover" framing: both stay available, selectable per-agent (task `11`).

Re-verify `opencode acp`'s actual current CLI invocation/behavior at implementation time
rather than trusting this task file's citation of the architecture doc — the doc itself
flags this ecosystem as fast-moving, and this specific claim wasn't independently
re-verified by this planning session's own research passes (which focused on `go-agent-
wrapper` and Nanite's own code, not external CLI tools).

## What to do

1. Confirm `opencode acp`'s real current invocation and wire behavior (subprocess spawn args,
   stdio framing) directly — check OpenCode's own current documentation/`--help` output
   rather than assuming the architecture doc's characterization is still accurate.
2. Implement a new `adapters.RuntimeAdapter` (thin passthrough — task `08`'s abstraction
   should make this a direct, untranslated wire connection, no bridge library needed) that
   launches `opencode acp` as a subprocess and speaks ACP directly over its stdio.
3. Produce a `Descriptor{Protocol: ProtocolACP, Transport: TransportStdio, Interrupt: <real,
   verified value — check whether OpenCode's ACP mode actually wires `session/cancel` through
   to a real abort, following the same verification discipline task `02` used for the native
   OpenCode adapter's `Stop()`, don't assume>}`.
4. Confirm `session/update` events map onto `runtimeevents` correctly (per task `08`'s wiring)
   for a real OpenCode ACP session — message chunks, tool calls, permission requests.

## Done means

- A working `opencode acp`-driving adapter exists, implementing task `08`'s ACP client
  abstraction, producing a correctly-populated `Descriptor`.
- The existing native `adapters/opencode` package is untouched — both remain independently
  selectable.
- A real (not mocked) `opencode acp` session, launched and driven through this adapter,
  completes at least one real turn with correctly-translated `runtimeevents` activity —
  verified live, not just via a wire-protocol unit test.
- The adapter's real `Interrupt` capability (does `session/cancel` actually abort, or just
  acknowledge-and-let-finish?) is verified directly, not assumed, and reflected accurately in
  the `Descriptor`.
- `go build ./...` / `go test ./...` clean in `libs/go-agent-wrapper`.

## Work Log (2026-08-21)

Implemented entirely in `libs/go-agent-wrapper` (sibling repo, worktree
`go-agent-wrapper-wt-task09`, branch `agent-host-acp/task-09-opencode-acp`), directly on top of
`main` at `4b100bd` (`v0.5.0`, task 08's landing commit). No Nanite-side code changes — this
task's `Touches:` line was accurate. New package: `adapters/opencodeacp/` (`doc.go`, `client.go`,
`translate.go`, `adapter.go`, `client_test.go`, `adapter_test.go`, `live_test.go`).

**Step 1 — re-verified `opencode acp`'s real invocation and wire behavior directly, per the
task's own mandate not to trust the architecture doc's citation.** `opencode acp --help`
(against a real `opencode 1.15.6` binary already on this machine) confirmed the subcommand
exists ("start ACP (Agent Client Protocol) server"). Rather than trusting a scraped
agentclientprotocol.com spec summary, I drove the real binary directly with a throwaway Python
JSON-RPC probe (raw stdin/stdout, no ACP library) across five rounds, capturing the actual wire
shapes:
- **Framing**: newline-delimited JSON-RPC 2.0 — confirmed both by the spec text ("Messages are
  delimited by newlines... MUST NOT contain embedded newlines") and by watching the real binary
  do exactly that. This matches agentkit's own `bufio.Scanner`-based `jsonrpc_stdio_session.go`
  reader assumption.
- **`initialize`**: sending `protocolVersion` as the *integer* `1` (not the string a scraped spec
  summary implied) round-trips cleanly — the real response echoes `"protocolVersion":1`.
- **`session/new`**: `{cwd, mcpServers}` → `{sessionId, configOptions, ...}`.
- **`session/load`** (resume): requires `sessionId`, `cwd`, **and `mcpServers`** — the last is a
  *required* field the scraped spec summary never mentioned; the real binary rejects the call
  with a Zod validation error (`"Invalid input: expected array, received undefined"`) without it.
  Confirmed via a live round-trip (create a session, then successfully re-load it by id).
- **`session/prompt`**: response carries `stopReason` — confirmed via a real "reply with the
  word pong" turn.
- **`session/update` notifications**: keyed on **`update.sessionUpdate`** as the discriminator
  field — **not `type`**, which is what a third-party spec-summary fetch suggested. Ground truth
  from the real binary won over the scraped doc, per this task's own re-verification mandate.
  Observed variants live: `available_commands_update`, `agent_message_chunk` (message content),
  `agent_thought_chunk` (reasoning), `tool_call`/`tool_call_update` (a real shell-tool
  invocation, `echo hello-from-tool`, was driven end-to-end and observed), `usage_update`.
- **`session/cancel`**: confirmed to be a notification (no `id`, no response) exactly as spec'd.
- No `session/request_permission` (or `fs/*`/`terminal/*`) call was observed for a plain shell
  tool-call turn — OpenCode executed the bash tool directly without asking the client, consistent
  with (but not exhaustive proof of) 17-acp.md's suspicion that OpenCode, like Claude/Codex, does
  its own tool execution internally regardless of declared client capabilities. Flagged as
  empirically confirmed for exactly one tool-call shape, not universally, in the code doc
  comments — this is task `16`'s own subject, not fully settled here.

**Step 2/3 — implemented `opencodeacp.Client`, a real `acp.Client`.** Spawns and owns
`opencode acp` directly via `os/exec` (no bridge library): its own id-allocator/pending-response
map/notification-dispatcher, real bidirectional JSON-RPC. `Launch` performs
`initialize` → (`session/load` with graceful fallback to `session/new` when `SessionIDPreset` is
set, else `session/new` directly) and returns once ready. `Prompt` sends `session/prompt`
asynchronously (returns once accepted, not once complete, per task 08's interface contract) and a
background goroutine emits `turn.completed`/`turn.failed` when the response arrives. `Cancel`
sends the `session/cancel` notification. `Close` closes stdin, waits briefly, then kills the
process; the `Events` channel closes exactly once (mutex-guarded against the
Close-vs-process-exit race, not panic/recover). `session/update` → `runtimeevents` mapping lives
in `translate.go`, following 17-acp.md's explicit correspondence exactly (message/thought chunks
→ `agent.delta`; `tool_call`/`tool_call_update` → `agent.tool_use`/`agent.tool_result`;
`session/request_permission`, a server-initiated *request* not a notification, →
`agent.permission_requested`/`resolved`, answered with a well-formed ACP "cancelled" outcome
since no interactive approval mechanism is wired into this Client — that belongs to Nanite's own
policy layer, upstream of this package). Informational `session/update` variants
(`available_commands_update`, `usage_update`, `plan`) are deliberately left unmapped, matching
`go-providers`' own `EventParser` convention of skipping informational lines rather than forcing
them into an ill-fitting `Kind`.

`Adapter` (`adapters.Adapter` + `adapters.RuntimeAdapter`) wraps `Client`: `Name()` returns
`"opencode-acp"` — deliberately distinct from the existing native adapter's `"opencode"` (both
share `Descriptor.Provider == "opencode"`, the same upstream identity, via `acp.DescriptorFor`'s
own convention) — `Describe()` threads a fresh, unlaunched `Client`'s
`InterruptCapability()` through `acp.DescriptorFor`, and `Resolve()` returns the informational
exec `Spec` (`opencode acp`, mirroring `adapters/codex`'s and `adapters/opencode`'s own pattern
of Resolve being informational-only on the agentkit dispatch path).

**A genuine architecture-seam finding, not assumed away**: `CLIAdapter()` needed to return a
`provider.CLIAdapter` to satisfy `adapters.RuntimeAdapter`, but I found — and verified by reading
`agentkit/agentsessions/jsonrpc_stdio_session.go` and `go-providers/provider/pty_codex.go`
directly — that go-providers' `CLIAdapter` interface (`Detect`/`BuildArgs`/`ParseLine`) has no
seam for a bidirectionally-real, response-correlated JSON-RPC session once agentkit spawns the
process: `BuildArgs` is called once, before spawn, with no stdin access; `ParseLine` gets stdout
lines but no way to write back. The one existing precedent in this exact repo —
`adapters/codex`'s own app-server `CLIAdapter` (`provider.NewCodexAdapterAppServer`) — has the
*identical* gap: its `ParseLine` is a documented no-op ("JSON-RPC framing, request correlation,
and event mapping live in the consumer runtime" — which, on inspection, doesn't actually exist
either; `agentsessions.Manager.JsonRpcCall`/`Session.(JsonRpcCaller).Call` is the only real
bidirectional-call mechanism in agentkit, and it's exposed at the `Manager`/`Session` level, never
through `wrapper.Wrapper`, which stores its `Session` privately with no passthrough). So **no
shipped adapter in this repo — including the existing Codex one — has real Wrapper.Run()-driven
JSON-RPC turn-taking working today.** Rather than inventing a workaround that would either
double-spawn the real `opencode acp` process (my `Client` owning one, agentkit spawning a second,
unused one) or diverge from established precedent, I mirrored `adapters/codex`'s exact shape:
real `Detect`/`BuildArgs` (so the one real process agentkit *would* spawn is the genuine article,
not a duplicate) and a pass-through `ParseLine`. This is honest about — not silently hiding — the
current state: **real, live-verified ACP driving in this task goes through `Client` directly, not
through `wrapper.Wrapper.Run()`.** This doesn't change the task's own instruction (implement
`adapters.RuntimeAdapter`; verify a real turn "driven through this adapter") — it changes how I
satisfied it: `CLIAdapter()` is structurally complete and dispatch-table-compatible (no
`ErrUnknownRuntime`), and the live verification target is `Client`'s own real, direct interface,
consistent with 17-acp.md's own framing of this as "a thin, direct wire connection" the host
drives. Flagging this as a genuine follow-up candidate: extending `wrapper.Wrapper` with a
`JsonRpcCall`-style passthrough (mirroring `agentsessions.Manager`'s own) would be needed before
*either* the Codex app-server adapter or an ACP adapter can be driven for real through
`Wrapper.Run()` — worth a dedicated task before task `11` wires per-agent protocol selection
through the wrapper, or before Phase 4's bridge adapters need the same seam.

**Step 4 / live verification — real, not mocked.** Two new tests in `live_test.go`, skipping
(not failing) when `opencode` isn't on PATH or Launch fails for an environment reason:
- `TestLiveClientCompletesOneRealTurn`: `Client.Launch` → `Client.Prompt("Reply with exactly one
  word: pong")` against the real binary → observed `agent.delta` (message text "pong") →
  `turn.completed` with real usage numbers. Passed for real: `message text observed: "pong"`.
- `TestLiveClientCancelAbortsMidGeneration`: a genuine 2000-word-essay prompt, waits for real
  generation activity (≥2 delta chunks), sends `Cancel`, asserts the turn's terminal event
  arrives well before natural completion. Passed for real, twice independently (once via a
  throwaway Python probe during investigation, once via the actual Go `Client`): **turn.completed
  arrived ~35ms after `Cancel`**, both times, with generation only a few short chunks in — a
  genuine mid-turn abort, not acknowledge-and-let-finish. `InterruptCapability()` therefore
  returns `adapters.InterruptTurn`, matching the native OpenCode adapter's own already-verified
  tier (same underlying OpenCode abort mechanism reached via a different wire protocol).

Both live tests also passed under `-race`.

CI-safe coverage (`client_test.go`, `adapter_test.go`) exercises the same code paths against a
small `sh` fake-ACP-shaped script (not the real binary) — full handshake, notification mapping
(delta/tool_use/tool_result), Launch-twice/Prompt-before-Launch/Close-idempotency error paths,
`Describe()`/`Resolve()`/interface-satisfaction — so `go test ./...` stays meaningful and green
in an environment without `opencode` installed or authenticated.

**GLOSSARY.md check.** Read `docs/engineering/GLOSSARY.md` (Nanite repo) before naming anything;
grepped for `opencodeacp`/`opencode-acp`/`OpenCodeACP` — no collision. No new Nanite-facing
vocabulary introduced (the package is `go-agent-wrapper`-internal); confirmed the existing
`Turn.Cancel` vs. `Session.Stop` vs. ACP `session/cancel` glossary entry already covers the
naming discipline `Client.Cancel` needed to honor (it routes to `session/cancel`, mapped onto
`Turn.Cancel`'s concept, never `Session.Stop`) and quoted the same discipline in doc comments
rather than re-deriving it.

**Build/test status.** `go build ./...`, `go vet ./...` clean. `go test ./...` clean except one
pre-existing, previously-documented flaky test (`TestRunEndToEndAdapterRuntime`,
`wrapper/wrapper_integration_test.go`, "sequence not monotonic") — confirmed unrelated to this
task: `git diff --stat HEAD` shows zero files touched outside the new `adapters/opencodeacp/`
directory, and task 08's own Work Log already documented this exact test as flaky and
pre-existing on a clean `v0.4.0` checkout, independent of any change in this batch. Re-ran it in
isolation (`-count=5`) and reproduced the same "sequence not monotonic" failure with a different
index each time, consistent with the pre-existing race task 08's CHANGELOG entry describes.
`go test ./... -race` (excluding that known-flaky test and the live tests, run separately) and
the live tests under `-race` are both clean.

**Version.** Cut `v0.6.0` (from `v0.5.0`) — a new public package (`adapters/opencodeacp`) is
real, additive surface, consistent with this repo's own per-task tagging discipline. No breaking
change; `go.mod` untouched (no new dependency — the new package imports only `acp`, `adapters`
(this module), `go-providers/provider`, `go-llm-types`, and `go-runtime-events`, all already
required).

**Commit/push.** Commit and tag pushed to `origin` per this batch's established discipline
(`TASKS/ESCALATIONS.md`'s "Critical process finding" entry) — see the commit SHA reported
alongside this task's completion message.

### Decisions locked

- `opencodeacp.Client` owns and spawns its `opencode acp` subprocess directly (no dependency on
  go-agent-wrapper's agentkit/CLIAdapter seam for the actual wire protocol) — the only viable
  shape given `LaunchParams` carries spawn ingredients (Cwd/Env), not "attach to an existing
  connection" parameters, and given the CLIAdapter interface has no bidirectional-call seam (see
  the architecture-seam finding above).
- `Adapter.Name()` is `"opencode-acp"`, distinct from the native adapter's `"opencode"`, while
  `Descriptor.Provider` stays `"opencode"` for both — same upstream agent identity, distinct
  adapter identifiers for logs/config/policy-rule-key purposes.
- `session/update`'s discriminator field is `sessionUpdate`, not `type` — real wire behavior
  overrides the scraped spec summary.
- `session/request_permission` gets a generic, non-interactive "cancelled" deny (no approval
  handler wired into this Client) rather than a raw JSON-RPC protocol error — a well-formed ACP
  response the agent can react to gracefully, consistent with how `Wrapper.Run`'s own generic
  `JsonRpcRequestHook` denies every server-initiated request today for lack of a configured
  handler.

### Follow-up candidates

- Extend `wrapper.Wrapper` with a `JsonRpcCall`-style passthrough (mirroring
  `agentsessions.Manager.JsonRpcCall`) so a `CLIAdapter` bridge (Codex app-server's existing one,
  or this task's ACP one) can actually be driven through `Wrapper.Run()` for real turn-taking,
  not just dispatch-table compatibility. Currently no shipped jsonrpc-stdio adapter in this repo
  has that wired through.
- `session/load`'s exact param shape was verified for the minimal case (`sessionId`, `cwd`,
  `mcpServers`) but not exhaustively (e.g. behavior on a genuinely stale/expired session id
  wasn't tested).
- fs/terminal proxying (`fs/read_text_file`, `terminal/*`) was never observed live for OpenCode
  ACP, but only against one tool-call shape (a plain shell echo) — task `16`'s own audit should
  not treat this as exhaustively settled.

### Known limitations

- `CLIAdapter()`'s `ParseLine` is a deliberate pass-through (matching `adapters/codex`'s own
  precedent) — driving a real ACP session through `wrapper.Wrapper.Run()` does not work today for
  this adapter, by design, pending the `JsonRpcCall`-passthrough follow-up above. This does not
  block task `11` (per-agent protocol/transport selection) from routing to this adapter's
  `Client` directly, if that task's own factory-level integration calls `Client` methods rather
  than going through `Wrapper.Run()`.
