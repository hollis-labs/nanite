# Native-vs-ACP side-by-side comparison harness

**Phase:** 5 — Verification & hardening (`TASKS/agent-host-acp`)
**Status:** not-started
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
