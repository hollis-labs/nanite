# Agent Client Protocol (ACP)

## Two roles, not one feature

ACP (<https://agentclientprotocol.com>, JSON-RPC 2.0 over stdio, actively versioned — v1 stable, v2 in draft) is easy to conflate into a single "we should adopt ACP" line item. It isn't one — it's a wire protocol that plays two structurally different roles here, and this review's own source material corrected itself on exactly this point mid-conversation:

- **ACP-as-server**: an ACP-speaking editor/GUI (Zed natively, JetBrains via `acp.json`, Avante.nvim/CodeCompanion.nvim) treats a Hollis app's session as "the agent." **Shipped as of ADR 0034 (2026-05-12)** — Tether's `mux acp` (`internal/acpadapter` + `internal/acpsvc`): `initialize`/`authenticate`/`session/new`/`session/prompt`/`session/resume`/`session/close` worked at MVP; `session/list`/`session/load`/`set_mode` were declined; `session/cancel` was wire-honored but the MVP never wired it through to the underlying provider's own interrupt call. **That snapshot is now ~3 months stale and unvalidated against the current build — treat every specific behavior claim from it as needing revalidation, not as current fact.** Out of scope to change here regardless — it's prior art, not a target.
- **ACP-as-client**: a Hollis host process drives an underlying CLI agent (Claude, Codex, OpenCode, Pi, Copilot CLI) over ACP instead of that agent's bespoke wire format. **Nothing in the portfolio does this today.** This is what the transcript that prompted this review was actually asking for, and it's this doc's real subject.

These are mirror images of the same protocol, not two maturities of the same feature — Tether shipping the server role says nothing about whether the client role is easy, only that the wire-protocol scaffolding for *a* role already exists once, in Go, in-house.

## Why ACP-as-client is worth adding (validated, not assumed)

Checked directly rather than taken from secondhand claims:

- **Native ACP agents are real and current**: `opencode acp` (OpenCode's documented, shipped ACP subprocess mode) and GitHub Copilot CLI's `--acp` (stdio + TCP transports, public preview since 2026-01-28). Either can be driven by a thin passthrough adapter — no translation layer needed.
- **Bridges for non-native agents already exist in the wild, more than one shape of them**: `beyond5959/acp-adapter` (Go, embeddable, one library bridging Codex/Claude Code/Pi to ACP together). Separately, per-provider bridges also exist and appear more actively maintained: `agentclientprotocol/claude-agent-acp` (wraps the official Claude Agent SDK; adopted under the ACP org itself, updated within days at last check) and a `codex-acp` bridge (wraps Codex's own native `app-server` JSON-RPC). Neither Anthropic nor OpenAI ships native ACP support directly (OpenAI has an open feature request, `openai/codex#9085`, still unresolved) — both rely on a bridge translating to/from the vendor's own native protocol.
- **`session/update`'s already-standardized shape maps almost 1:1 onto `go-runtime-events`**, which [16-agent-host.md](16-agent-host.md) documents as already shipped: message/thought chunks → `agent.delta`, `tool_call`/`tool_call_update` → `agent.tool_use`/`agent.tool_result`, permission requests → `agent.permission_requested`/`resolved`. An ACP-driving adapter feeds the *existing* activity bridge — it doesn't need a new event vocabulary invented for it.

## The host's ACP client — not a shared client+server lib

Tether's `internal/acpadapter` (ACP-*server* wire code — framing, dispatcher, auth) and what the host needs (an ACP-*client* capability) are separate concerns; an earlier draft of this doc conflated them into one "extract a shared `go-acp`" proposal. They don't share an extraction target:

- Tether's ACP-server code stays exactly what it is — prior art for the server role, untouched here. If some Hollis app later wants the reverse (external ACP clients driving a Hollis session), extracting `internal/acpadapter` into a shared server-side lib is the natural move *then* — a separate, not-currently-scoped feature.
- What the host needs is the client shape from the source conversation, and it's the right one:

  ```
  Hollis Host API              (go-agent-wrapper's Adapter/RuntimeAdapter seam)
        │
        ▼
  ACP client abstraction       (stable: Launch/Prompt/Cancel/Events — one Go interface)
        │
        ▼
  ACP implementation/adapters  (swappable per underlying agent)
  ```

  The abstraction is the "intent" tier — defined once, next to the host, following the same driver pattern already used elsewhere in this codebase (`memory_write`/`knowledge_write` → provider-agnostic; `go-providers.CLIAdapter` → per-CLI concrete adapters). Underneath it, two kinds of implementation:
  - **Native ACP agents** (OpenCode via `opencode acp`, Copilot CLI via `--acp`): the implementation is a thin, direct ACP wire connection to the real subprocess. No bridging.
  - **Non-native agents** (Claude, Codex, Pi): the implementation is a third-party library that speaks ACP *on the CLI's behalf* — `beyond5959/acp-adapter` is the concrete example (embeddable Go, ships Codex/Claude/Pi backends). **`go-agent-wrapper` consumes this as a dependency.** There's no case for Hollis hand-rolling ACP-bridging logic for CLIs someone's already bridged — the implementation tier is exactly where an off-the-shelf adapter belongs; only the abstraction above it needs to be Hollis-owned.

## Where this plugs into the host and Nanite's schema

A new `Adapter`/`RuntimeAdapter` implementation per underlying agent, in whichever host [16-agent-host.md](16-agent-host.md) lands on (today's candidate: `go-agent-wrapper`'s `adapters/` package, alongside `adapters/claude`/`adapters/codex`/`adapters/opencode`).

**ACP is a protocol, not a transport — this doc's first draft got that wrong.** The original framing proposed `Descriptor.Runtime = "acp"`, treating ACP as one more value alongside `streaming-stdio`/`jsonrpc-stdio`/`http-sse`. But those three aren't peers of ACP — they're *transports/wire-shapes*, while ACP is a *protocol* that can itself ride over more than one transport (stdio today; TCP too, per Copilot CLI's `--acp` supporting both). And a provider's native protocol (Codex's `app-server`, Claude's stream-json) is an orthogonal axis from ACP, not a value on the same enum — the same provider can be reached either way. [16-agent-host.md](16-agent-host.md) confirms this conflation already exists in the shipped `Descriptor.Runtime` field (a bare string currently mixing both axes) and proposes splitting it into `Protocol` (`claude-stream-json` / `codex-app-server` / `opencode-native` / `acp`) and `Transport` (`stdio` / `tcp` / `http-sse` / `pty`). Under that shape:

```
Claude native        protocol: claude-stream-json   transport: stdio
Claude via ACP       protocol: acp                   transport: stdio
Codex native         protocol: codex-app-server      transport: stdio
Codex via ACP        protocol: acp                   transport: stdio
OpenCode native ACP  protocol: acp                   transport: stdio
Copilot ACP daemon   protocol: acp                   transport: tcp
```

That split should land before or alongside the ACP adapter, not after — otherwise the ACP adapter itself has to be shoehorned into the same overloaded field it's exposing the problem in.

**No new top-level `agents.runtime_kind` value.** This part of the original framing holds regardless of the `Protocol`/`Transport` split above — ACP-driven agents still route through the existing `cli` value; `Protocol`/`Transport` are internal to the host descriptor, not a database concern. Consistent with "one runtime, several doors" ([00-overview.md](00-overview.md)) and with [02-agent-launching.md](02-agent-launching.md)'s explicit rejection of a proliferating string-prefix convention in favor of one typed field. Per-agent protocol/transport selection belongs in the same DB-configurable surface as the existing `agent_context_resolvers` pattern, not a new schema axis.

## Decision matrix left for planning

Narrower than it first looks, since the shape above settles most of it — and all five named providers get ACP:

- **Native-ACP providers** (OpenCode via `opencode acp`, Copilot CLI via `--acp`): direct connection through the ACP client abstraction. Settled shape, low risk.
- **Claude, Codex, Pi**: a bridge as the implementation tier — either one multi-provider library (`beyond5959/acp-adapter`, covering all three) or per-provider bridges (`claude-agent-acp`, `codex-acp`, ...). Settled shape; the open question is narrowly *which* bridge(s) to pin, including whether one multi-provider lib or several single-provider ones is the better bet — left undecided, see below.
- **Migration is additive, not a cutover.** Claude's and Codex's existing native adapters (`protocol: claude-stream-json`/`codex-app-server`, both `transport: stdio`) stay available as a parallel, selectable option — not deprecated, not replaced. ACP becomes available for every provider from day one; which protocol/transport a given agent actually runs on is a per-agent config choice (same descriptor seam as any other adapter), and the move to ACP-as-default happens opportunistically per agent as it proves out — not a forced flag-day. That also means real side-by-side comparison is possible per provider (activity fidelity, interrupt fidelity, tool reporting, latency) before anything defaults to ACP.
- **Which bridge(s) to pin** is left undecided for now — deliberately, given how young and fast-moving this ecosystem is (the field itself widened mid-review: single-vendor bridges turned up that didn't exist in the first pass). One real criterion worth weighing at implementation time: whether a given bridge actually wires ACP's `session/cancel` through to the provider's own native interrupt call (both Claude and Codex have one — see below) rather than just acknowledging cancellation at the wire level and letting the turn finish anyway. Re-check library state and behavior at implementation time rather than trusting this review's snapshot.

## Known limitations carried forward, not solved by adding ACP

- **Real mid-turn interrupt is provider-dependent, not universally unsolved — the earlier draft of this doc overstated the ceiling.** Both Claude Code and Codex already have a genuine native interrupt mechanism: Claude's Agent SDK exposes `Query.interrupt()` (a `control_request` over stream-json stdin, with a `cancel_turn` frame and an advertised `interrupt_receipt_v1` capability), and Codex's `app-server` has `turn/interrupt` as a first-class JSON-RPC method — turns are explicitly documented as "the unit of interruption and rollback." So the capability genuinely exists for at least these two. What's unverified is whether a given ACP bridge actually calls that native mechanism when it receives `session/cancel`, versus just acknowledging the cancel at the wire level and letting the turn finish naturally regardless — the latter is what Tether's own current (now-stale) MVP does, per its own ADR, and that's evidence of an implementation gap in one specific hand-rolled build, not proof of a protocol-level ceiling. The genuine, narrower limitation this doc should carry forward is exactly what it was scoped to: **the host can only offer what the provider's own protocol exposes, mediated by however faithfully the chosen bridge maps to it.** For a provider with no native interrupt of its own (unverified for Pi at review time), or a bridge that doesn't wire the mapping through, cancellation degrades to natural completion — same as a hard process kill being the only guaranteed-clean fallback in that case. This is a per-bridge, per-provider fact to verify at implementation time, not something to assume either way. Tracked as the same open item in [16-agent-host.md](16-agent-host.md). Rather than treating every adapter's `Stop()`/cancel as identically capable, the descriptor should be able to say so — a small capability-discovery vocabulary (something like `interrupt: none | process | turn | steer`, exact naming TBD at implementation time) advertised per `Protocol`/`Transport` combination, so the host can accurately report "Claude native: turn-level cancel" vs. "provider X: process-kill only" instead of presenting a uniform `Stop()` that quietly means different things underneath. Same principle as the rest of this doc: abstract the intent (cancel this turn), not the capability (pretending everything can).
- **Tool-call/fs/terminal proxying is a per-agent unknown, not a settled no.** ACP lets the *agent* ask the *client* to do filesystem/terminal work on its behalf (`session/request_permission` before a risky action, `fs/read_text_file`/`fs/write_text_file`, `terminal/*`) — the design assumption being an editor that owns live buffers/undo history/a visible terminal panel and should mediate those operations rather than have the agent touch disk directly. Tether declined all of this MVP because it was safe to: Claude and Codex already do their own fs/terminal work internally regardless of what any client offers, so no request was ever coming. Nanite's host sits on the *other* side of this exchange — as the client driving someone else's agent implementation (OpenCode, Copilot CLI, or Claude/Codex/Pi via `beyond5959/acp-adapter`'s translation) — and it's genuinely unverified whether those agents behave the same way Claude/Codex do (do their own fs/terminal work regardless, functioning fine when the client declines those capabilities during `initialize`) or actually expect the client to serve them (in which case the host would need a small in-process fs/terminal server, scoped to the existing sandbox boundary, to keep that agent fully functional). This has to be checked against each real agent's behavior at implementation time, not assumed — if it turns out to be required, it's meaningfully more scope than "plug in an ACP client," worth flagging as a possible hidden cost rather than folding silently into the adapter work.
- **Attach/detach to independently-launched processes stays out of scope**, consistent with [16-agent-host.md](16-agent-host.md) and explicit prior direction.

## References

- Tether ADR 0034 (ACP surface), ADR 0035 (`mux mcp` daemon-client routing), `apps/tether/docs/acp.md`, `apps/tether/docs/mcp.md` — prior art; unchanged by this doc.
- ACP spec: <https://agentclientprotocol.com>.
