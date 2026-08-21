# ACP bridge adapter — Codex

**Phase:** 4 — ACP bridge adapters for non-native agents (`TASKS/agent-host-acp`)
**Status:** not-started — **blocked on `12`'s operator-approved bridge decision. Do not
dispatch until that decision is recorded in `TASKS/ESCALATIONS.md`.**
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
