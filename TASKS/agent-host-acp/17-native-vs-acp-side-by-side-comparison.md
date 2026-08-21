# Native-vs-ACP side-by-side comparison harness

**Phase:** 5 — Verification & hardening (`TASKS/agent-host-acp`)
**Status:** implemented — real, executed comparison run for Claude (native `adapters/claude`
vs. bridge-mediated `adapters/claudeacp`), covering all four named dimensions with real
numbers from real subprocesses. See Work Log.
**Depends on:** `07` (native host migration validated), `09`, `10` (native ACP adapters);
`13`, `14`, `15` (bridge adapters, if not deferred)
**Touches:** likely a new, small test/tooling addition (exact location is this task's own
call — a `go test` harness, or a small CLI/script, whichever fits this project's existing
conventions best) plus a written comparison report. Repo: Nanite + `libs/go-agent-wrapper`.

## Context

`docs/engineering/architecture/17-acp.md` frames the additive migration model as
specifically enabling this: "That also means real side-by-side comparison is possible per
provider (activity fidelity, interrupt fidelity, tool reporting, latency) before anything
defaults to ACP." This task is that comparison, run for real once both paths exist for at
least one provider that has both (Claude, Codex, or OpenCode — whichever bridge/native pairs
actually landed by this task's dispatch time).

This is the batch's closing verification step, closest in spirit to
`TASKS/scheduling/06-schedule-fire-telemetry.md`'s and `TASKS/teams/`'s own end-of-batch
dogfeed-and-compare discipline — not a new feature, a confidence check before this batch is
declared done.

## What to do

1. For at least one provider with both a native adapter and a real ACP adapter (native +
   bridge, or native + native-ACP for OpenCode), run matched sessions through both paths with
   the same real prompt/task and compare:
   - **Activity fidelity**: does the ACP path's `runtimeevents` output carry the same
     granularity of information as the native path (tool calls, deltas, permission events)?
   - **Interrupt fidelity**: per tasks `02`/`09`/`10`/`13`-`15`'s already-recorded
     `InterruptCapability` values, confirm the recorded capability matches what actually
     happens when `Stop()`/`Cancel()` is invoked mid-turn on each path.
   - **Tool reporting**: does the ACP path correctly surface tool_call/tool_call_update events
     with equivalent detail to the native path's tool-use events?
   - **Latency**: rough wall-clock comparison for a real turn, both paths — not a rigorous
     benchmark, just enough to flag if ACP is dramatically slower/faster for any provider.
2. Write up findings as a short comparison report (where this lives — a doc, or this task's
   own Work Log — is your call; keep it discoverable for whoever eventually decides per-agent
   ACP defaults later).
3. Do not make any "default to ACP" decision as part of this task — 17-acp.md is explicit that
   defaulting happens "opportunistically per agent as it proves out — not a forced flag-day."
   This task produces the evidence that decision would be based on, not the decision itself.

## Done means

- A real, executed comparison (not a paper analysis) exists for at least one provider with
  both native and ACP paths available.
- Findings cover all four named dimensions (activity fidelity, interrupt fidelity, tool
  reporting, latency).
- No default-protocol decision is made as part of this task.
- This batch (`TASKS/agent-host-acp/`) is ready to be marked complete in `TASKS/INDEX.md`
  once this task and its fresh-Reviewer pass both close clean.

## Work Log (2026-08-21)

Read this task file in full, `docs/engineering/architecture/17-acp.md`, this batch's
`README.md`, `docs/engineering/GLOSSARY.md`, and task `07`'s full Work Log (both the original
run and the re-run addendum) before touching anything, per this task's own weight as the
batch's closing verification step.

### Provider chosen: Claude (`adapters/claude` vs. `adapters/claudeacp`) — and why not Codex

The dispatch prompt named Claude and Codex as the two pairs with "the most interesting
contrast" (native `InterruptProcess` vs. ACP-bridged `InterruptTurn`), leaving the final pick
to this task's own judgment. Checked both before choosing:

- **Codex's shipped `adapters/codex` package is genuinely unvalidated ground.** It always
  selects Codex's app-server JSON-RPC runtime shape (`ProtocolCodexAppServer`/
  `TransportStdio` → agentkit's jsonrpc-stdio runtime). Nanite's own production code
  (`internal/runtime/agent/wrapper_adapter.go`, this repo) explicitly does NOT use that
  shape — its own doc comment states plainly: "cmd/nanite registers `provider.NewCodexAdapter()`
  ... Nanite runs Codex ... as agentkit's subprocess-per-turn 'adapter runtime' today, a
  materially different runtime shape than the shipped adapters select." Driving a real
  app-server turn requires composing `thread/start`/`sendUserTurn`-style JSON-RPC calls
  (`agentsessions.jsonRpcStdioSession.Call`) — `wrapper.Config.AutoFireFirstTurn`/`SendInput`
  are documented raw-bytes escape hatches, not app-server turn composition, and no code
  anywhere in this batch (Nanite or `go-agent-wrapper`) has ever driven a real turn through
  that shape. Task `19`'s own fix (missing `--skip-git-repo-check`) was for the *exec-mode*
  `CodexAdapter.BuildArgs`, confirming Nanite's real Codex path is exec mode, not app-server.
  Standing up real app-server turn composition from scratch was judged out of proportion for a
  verification task whose job is to compare paths that already exist, not build a third one.
- **Claude's native shape (`adapters/claude`, streaming-stdio) is exactly what Nanite's
  production `nativeAdapter` runs unmodified** — task `07`'s dogfeed (both the original run and
  the re-run) already validated it end-to-end against a real `claude` binary. Claude's ACP
  bridge (`adapters/claudeacp`) also already had real, live-verified single-path precedent from
  task `13`'s own `live_test.go` and package-doc citations. Comparing two paths that both
  already have independent real-world grounding, contemporaneously, in the same run, is exactly
  what this task asks for.

Chose to build the harness as `go test`-with-skip files (`TestLive*`, skip-not-fail on missing
`claude`/`npx`/auth), matching this repo's own established convention for exactly this kind of
real-subprocess verification (`adapters/{claudeacp,codexacp,opencodeacp,piacp}/live_test.go`)
rather than inventing a separate CLI/script shape.

### Where the harness lives

New package `sidebyside/` in `libs/go-agent-wrapper` (top-level, alongside `acp/`, `activity/`,
`wrapper/`, etc. — a test-only package, matching this repo's own convention that a directory's
only files can legitimately be `_test.go`):

- `sidebyside/doc.go` — package doc: why Claude, what each test does, real-vs-mock discipline.
- `sidebyside/claude_live_test.go` — `TestLiveClaudeNativeVsACP_ActivityAndToolReporting`: one
  plain turn + one real-tool-use turn, driven through both `wrapper.Wrapper`+`adapters/claude`
  (real `claude` subprocess) and `claudeacp.Client` (real `npx claude-agent-acp` bridge), same
  prompts, contemporaneous timestamps.
- `sidebyside/interrupt_live_test.go` — `TestLiveClaudeNativeVsACP_InterruptFidelity`: a long
  (2000-word-essay) prompt on both paths, `Stop()`/`Cancel()` invoked mid-generation, wall-clock
  latency measured to actual termination/turn-end on both.

Commit `df9621a` on `main` in `libs/go-agent-wrapper` (committed directly, no worktree — this
task's own "no worktree needed" framing, same pattern tasks `01`-`15` already used in that
repo), pushed to `origin/main`. `go build ./...`, `go vet ./...`, `gofmt -l .` all clean;
`go test ./...` clean across the full repo (18 packages, including this new one), run against
real `claude`/`codex`/`opencode` binaries and real `npx` bridges already confirmed present and
authenticated on this machine per this batch's own established baseline.

### Real environment

`claude 2.1.239`, `npx`/`node` v22.12.0 (mise-managed), real `@agentclientprotocol/claude-agent-acp`
bridge (npx-resolved, pinned per task `13`), real, already-configured Claude credentials (the
same auth the `claude` CLI itself uses). No scratch DB/HTTP harness needed — this task drives
`go-agent-wrapper`'s own seams directly (`wrapper.Wrapper` and `acp.Client`), one level below
Nanite's HTTP surface, matching how tasks `09`/`10`/`13`-`15` verified their own adapters.

### Findings — all four dimensions, real numbers, reproduced across 3 independent runs

**1. Activity fidelity — a real, previously-undocumented gap, not just "same information,
different shape".** The native path's `event_translator.go` maps `llmtypes.EventDelta` 1:1 from
go-providers' `parseClaudeAssistant`, which parses each whole `"assistant"` stream-json JSON
line into ONE `agent.delta` — because Claude's own `-p --output-format stream-json --verbose`
mode flushes a *complete* generated message as a single JSON line, not incrementally. A real
2000-word-essay turn through the native path produced **exactly one `agent.delta` event**,
containing the entire essay, arriving only once generation had already finished server-side —
confirmed directly (this task's first harness iteration hung for 60s waiting for a second delta
that structurally could never arrive; see `interrupt_live_test.go`'s own doc comment for the
full diagnosis). The ACP path, by contrast, streams genuine incremental `agent_message_chunk`
deltas — reflected in this run's own turn-1 numbers (plain "pong" turn): native's
first-delta-to-completed gap was 18-23ms (near-simultaneous — one chunk arrives right at
completion), vs. ACP's 515-536ms (real incremental streaming even for a one-word reply), and
independently confirmed by `adapters/claudeacp`'s own package doc (a real Bash tool call
producing 4 separate `tool_call_update` refinement frames — see finding 3 below). **Practical
consequence**: any UI/consumer relying on the native path's `agent.delta` stream for progressive
rendering sees nothing until the whole response is ready; the ACP path gives genuine
progressive rendering. This is a real capability difference this task surfaced, not assumed
from the docs.

**2. Interrupt fidelity — recorded capability values confirmed accurate, with real numbers.**
Both paths' own already-recorded `Descriptor.Interrupt`/`InterruptCapability` were reconfirmed
live and match what actually happens:

| | Recorded capability | Measured mid-generation latency (Stop/Cancel call → confirmed termination) |
|---|---|---|
| Native (`adapters/claude`) | `InterruptProcess` | **~2.7-2.8s** (reproduced 2/2 explicit runs: 2.749s, 2.766s) |
| ACP (`adapters/claudeacp`) | `InterruptTurn` | **5-14ms** (reproduced 3/3 runs: 8.3ms, 13.6ms, 5.4ms) |

Native's ~2.7s breaks down exactly as `agentkit/agentsessions/streaming_stdio_session.go`'s
`Stop()` predicts: stdin close (child mid-generation does not respond to stdin EOF, so the full
~2s EOF grace elapses) → SIGTERM → child dies ~0.7-0.8s later, well inside the 5s SIGTERM grace
(never needed SIGKILL in any run). Crucially — and this compounds finding 1 — because the
native path never surfaces partial generation progress, this harness (and, by extension, any
real caller) has **no activity-based signal to time a mid-turn `Stop()` against**; the harness
had to fall back to a blind fixed-delay gate (`session.ready` + 3s) rather than "wait for N real
deltas" (the discipline that works for every ACP adapter's own live tests). Confirmed via
`sink.snapshot()` immediately before each `Stop()` call that `session.idle` had NOT yet fired
(i.e., a genuine mid-generation interrupt, not accidentally stopping an already-idle session) in
both explicit interrupt runs. ACP's Cancel() latency independently reproduces
`adapters/claudeacp`'s own package-doc citation (~7ms from a raw probe) closely.

**3. Tool reporting — both paths correctly report the real tool call and its real result;
granularity differs.** Same real prompt on both paths (`Use the Bash tool to run \`echo
<unique-marker>\` ...`), unique per-run marker text to rule out a stale/cached response:

| | `agent.tool_use` events | `agent.tool_result` events | Real output marker observed |
|---|---|---|---|
| Native | 1 | 1 | yes (both runs) |
| ACP | 1 (`tool_call`) | 4 (`tool_call_update`) | yes (both runs) |

Native emits exactly one tool_use (from the stream-json `tool_use` content block) and one
tool_result (from the app's `TypedEventCallback`/`pevents.ToolResult` path). ACP emits one
`tool_call` followed by multiple `tool_call_update` refinement frames — matching
`adapters/claudeacp`'s own package-doc finding verbatim ("a real Bash tool call ... produced ONE
`tool_call` notification followed by FOUR `tool_call_update` notifications — intermediate
refinements ... carrying neither `status` nor `rawOutput` at all, and only the final one
carrying both"). Both paths correctly surfaced the real command output end to end (both
successfully reported the unique marker text back in the final reply) — the ACP path is simply
more granular about the tool call's own lifecycle (start → in-progress refinements → final
result) than the native path's single before/after pair.

**4. Latency — no dramatic difference; ACP is modestly faster to first content, comparable
overall.** Three independent full runs (plain turn + tool-use turn, both paths, real numbers):

| Turn | Native total | ACP total |
|---|---|---|
| Plain ("pong") | 2.17s / 2.37s / 3.18s | 1.56s / 1.64s / 1.96s |
| Tool-use (real Bash echo) | 2.98s / 3.26s / 3.98s | 2.87s / 3.11s / 3.63s |

ACP was faster to first content on the plain turn in all 3 runs (bridge/SDK startup apparently
edges out spawning the real `claude` CLI directly), but tool-use-turn totals were close and
overlapping (no run showed either path more than ~1s apart) — not the "dramatically
slower/faster" signal this task's Done-means explicitly says to flag. No latency red flag for
either path at this scale.

### Explicitly not done (per this task's own instruction)

No "default to ACP" or "default to native" decision was made or implied anywhere in this Work
Log or the harness's own code/comments — every finding above is descriptive (what actually
happens), not prescriptive (what should be the default). Per `17-acp.md`'s own framing, that
call is left for a later, per-agent, opportunistic decision.

### Correction against this task file's own framing

The task file's "Depends on" line lists `13`-`15` as "bridge adapters, if not deferred" — by
this task's dispatch time all three (`13`/`14`/`15`) were already `implemented`, so no
deferral applied; all three were available as candidates, and Claude (`13`) was the one
actually exercised, per the provider-choice rationale above.

### Batch-completion note

Per this task's own "Done means," `TASKS/agent-host-acp/` is ready for the Orchestrator to mark
complete in `TASKS/INDEX.md` once this task and its fresh-Reviewer pass both close clean — that
edit is explicitly the Orchestrator's, not this task's, per the process notes this task was
dispatched under. Sibling task `16` (fs/terminal proxying audit) was `not-started` as of this
task's own dispatch and is not a listed dependency of `17` — its own completion is a separate,
parallel concern for the Orchestrator to track before declaring the whole batch done.
