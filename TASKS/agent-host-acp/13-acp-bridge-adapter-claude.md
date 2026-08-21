# ACP bridge adapter — Claude

**Phase:** 4 — ACP bridge adapters for non-native agents (`TASKS/agent-host-acp`)
**Status:** not-started — **blocked on `12`'s operator-approved bridge decision. Do not
dispatch until that decision is recorded in `TASKS/ESCALATIONS.md`.**
**Depends on:** `12`
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
- `go build ./...` / `go test ./...` clean in `libs/go-agent-wrapper`.
