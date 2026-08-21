# Native ACP adapter — OpenCode

**Phase:** 3 — ACP client abstraction & native adapters (`TASKS/agent-host-acp`)
**Status:** not-started
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
