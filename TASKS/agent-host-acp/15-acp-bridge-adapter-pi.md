# ACP bridge adapter — Pi

**Phase:** 4 — ACP bridge adapters for non-native agents (`TASKS/agent-host-acp`)
**Status:** not-started — **blocked on `12`'s operator-approved bridge decision. Do not
dispatch until that decision is recorded in `TASKS/ESCALATIONS.md`.**
**Depends on:** `12`
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
