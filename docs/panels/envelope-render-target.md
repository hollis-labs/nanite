# Envelope render-target — A2 / CW-20260428-0008

Status: implemented in sprint `collab-ui-v1` (Phase A).

## What this fixes

J8 (CW-20260426-0006) wired `Envelope.Target` as a **visibility-only** signal: when the agent passed `target: "bottom_chat_drawer"`, the FE opened that drawer, but the envelope itself **always rendered inline** in the chat transcript via `ChatMessage.tsx` → `EnvelopeRenderer`. The bottom drawer had no inbox slot for routed envelopes.

This made requests like *"put the report card in the bottom drawer"* impossible to satisfy: even with the J8 routing wired correctly (CW-20260428-0007 / Gap A merged at `af781b9`), the drawer would only **open** while the card kept rendering inline.

A2 closes the gap by introducing a **separate, additive** routing field that names where a card should *render*, distinct from the visibility hint that names a panel to *open*.

## Why this was missed in J8 / J9 / J10

Worth a brief post-mortem because the gap is structural.

J8 introduced `Envelope.Target` framed as a *visibility signal*: "open this drawer". The naming itself is ambiguous — `target` reads to most people as *render destination*. UX expectations followed the natural reading, code followed the original intent, and the two diverged silently.

J9 (right-rail v2) and J10 (`BottomChatDrawer`) layered on top without revisiting the framing — both assumed envelopes rendered inline by definition. The drift only surfaced via UAT (chat session **c106**), where a user asked for a card "in the bottom drawer" and got a card in chat plus an open drawer.

The fix uses two separate fields rather than overloading `target`. Trying to make one field mean both "open" and "render here" silently changes the semantics of every existing call site that relied on the visibility-only behavior.

## Locked decisions (2026-04-28 brainstorm)

- **Q1 — Inbox slot model:** single generic envelope slot per panel; the drawer owns layout (replace / stack / tab). No typed `accepts: [...]` lists in v1.
- **Q2 — Suppress-inline contract:** envelope gains a separate `RenderTarget` field (NOT polymorphic with the existing `Target`). When set, chat renders a small clickable stub ("📎 Card sent to Bottom drawer"); full render goes to the named drawer slot. Stub click opens the panel. Stub preserves transcript fidelity for compaction / search / reload navigation.
- **Q3 — Render lifetime on reload:** transient render-target envelopes do NOT auto-restore on session reload. The chat-history stub persists (envelopes are already in the transcript); clicking it re-routes the card. Pinned cards (Phase C / C1) persist via `bottom_drawer_pinned_cards`.
- **Q4 — Plugin-shipped panels + trust:** plugin panels CAN receive render-target envelopes, gated by the same `resolvePanelAccess` H1 trust check that gates `panel_signal` emission today. Untrusted-agent route to a plugin panel falls back to inline + a `render_target_blocked` hint on the envelope. Built-in panels (`bottom_chat_drawer`, `work`, `workflows`) always accept.
- **Q5 — Multiple envelopes per panel:** drawer-owned policy. Bottom drawer (per Phase C / C1): latest transient replaces previous; user pin promotes to a durable tab.

## Schema-level default render target (2026-04-28, post-Gap A merge)

Each envelope schema in `internal/envelope/schemas/<type>.schema.json` gains an optional top-level `default_render_target` field (string). When set, the runtime stamps `Envelope.RenderTarget` at envelope-build time **iff the agent did not provide one**. Explicit agent override always wins.

**Why schema-level (not a config table):** the routing default is intrinsic to the card type. The schema author already owns the shape of the card; bundling the routing default with the schema avoids a separate config that has to stay in sync.

### v1 default render target table

| Envelope type | `default_render_target` |
| --- | --- |
| `giphy-modal`, `document-viewer`, `report-card`, `info-card`, `list-card`, `metric-card`, `progress-card`, `table-card`, `timeline-card`, `diff-card` | `bottom_chat_drawer` |
| `approval-card`, `proposal-card`, `confirmation-card`, `question-form`, `error-report` | (none — inline) |
| `chat-loop-terminated`, `elicitation-prompt`, `subagent-spawn-approval` | (none — inline) |
| `kb-result`, `ticket-form`, `ticket-confirmation`, `resolution-capture` | declared by plugin manifest, not by host schemas |

The 10 passive renderables route to `bottom_chat_drawer` so the chat stays focused on dialog while reference content lands in the drawer. Decision-flow + runtime envelopes stay inline so the user reads them in chat where they need to act.

## Wire shape

Backend `chat.Envelope`:

```go
type Envelope struct {
    // ...existing fields...

    // Target: visibility hint (J8 v1). "open this drawer".
    Target string `json:"target,omitempty"`

    // RenderTarget: routing destination (A2). "render this card here".
    // Empty = inline. Non-empty = route to a named drawer/panel slot.
    // Independent of Target — both can be set on the same envelope.
    RenderTarget string `json:"render_target,omitempty"`

    // RenderTargetBlocked: filled by the backend when an explicit
    // render_target was rejected by the trust gate. Carries a short reason
    // ("untrusted_plugin_panel", "unknown_panel"). The FE surfaces it as a
    // small inline pill so the agent's intent is debuggable.
    RenderTargetBlocked string `json:"render_target_blocked,omitempty"`
}
```

FE `Envelope` (TypeScript) mirrors this exactly.

## Tool surface

`nanite_show_card` (CW-20260428-0019) accepts `render_target?: string` alongside the existing `target?` and `mode?`. When the agent omits `render_target`:

1. Backend reads `default_render_target` from the per-type schema cache.
2. If set, stamps it onto the emitted envelope.
3. Otherwise leaves `RenderTarget` empty → inline render.

Agent override wins: explicit `render_target` always preempts the schema default. The agent can also pass `render_target: ""` to force-inline a card whose default would route to a drawer.

## Trust gate

`callShowCard` validates `render_target` at the tool boundary:

- **Built-in panels** (`bottom_chat_drawer`, `work`, `workflows`): always allowed.
- **Plugin-shipped panels:** gated by `resolvePanelAccess(ctx, panelID)` — the same H1 trust check `nanite_panel_open` uses. Trusted callers pass through; untrusted callers get the render_target dropped and a `render_target_blocked: "untrusted_plugin_panel"` hint stamped on the envelope.
- **Unknown panel IDs:** dropped with `render_target_blocked: "unknown_panel"`.

The trust check happens only when the agent supplied an explicit `render_target`. Schema defaults are by construction limited to built-in IDs, so they bypass the gate.

## FE plumbing

- New `panelEnvelopes` slice on `useLayoutStore` (plain object `Record<string, Envelope[]>`, NOT persisted across reload per Q3).
- `applyEnvelopePanelEffects` in `panel-signal.ts` extended: when an envelope arrives with `render_target` set and the dismiss-machine allows the open, the envelope is pushed into `panelEnvelopes[render_target]` and the panel is opened via the existing `openPanelById` path. Dismiss-machine gating still applies — a user-dismissed drawer does not auto-open.
- `BottomChatDrawer` adds a 5th tab / inbox region that pulls the latest envelope from `panelEnvelopes["bottom_chat_drawer"]` and renders it through `EnvelopeRenderer`. Replace-previous policy per Q5.
- `ChatMessage` adds a skip-inline branch: when `envelope.render_target` is set and points to a panel that accepted the envelope, render a `<RenderTargetStub>` link instead of the full envelope. The full inline render still appears for inline envelopes and for blocked routings (so the user sees *something* even if the routing was denied).

## Out of scope (followups)

- Drawer card lifecycle — transient/pinned with a 10-pin cap and tab UI — Phase C / CW-20260428-0012.
- Right-rail panels growing inbox slots beyond `bottom_chat_drawer` (`work`, `workflows` widget host, plugin panels) — same shape as the bottom-drawer slot, deferred.
- Inspector-side blocked-route indicator: a richer debug view of `render_target_blocked` envelopes. The data is on the wire today; the inspector polish is separate.
- Auto-restore for pinned routed envelopes — depends on Phase C pin storage.

## References

- J8: `docs/panels/...` (commit `20597d0`)
- J9 right-rail v2: commit `672e8bf`
- J10 `BottomChatDrawer`: commit `ac3d22a`
- Gap A — `target`/`mode` propagation through tool boundary: CW-20260428-0007 (`af781b9`)
- A3 — generic `nanite_show_card`: CW-20260428-0019 (`3187da6`)
- A4 — `nanite_giphy_search` data tool: CW-20260428-0020 (`e4afd07`)
- Sprint: SP-20260428-0001
- Vanta decision: `decisions.nanite.cw_20260428_0008.envelope_render_target_separate_field`
