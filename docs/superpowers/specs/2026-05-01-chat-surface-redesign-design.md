# Chat Surface Redesign Spec

**Date:** 2026-05-01
**Status:** Draft (pending user review)
**Scope:** `ChatMain`, drawer architecture, alert region, composer chrome, width rules

## Overview

Redesign of the chat surface drawer model and composer. Three regions replace today's single-bottom-drawer + inline-alerts + busy composer toolbar layout:

- **`ChatPrimaryDrawer`** — top, persistent reference cards (Documents, Reports, Diffs, Tools, Pins).
- **`ChatWorkingDrawer`** — bottom (above composer), persistent mini-cards (Scratchpad, Terminal 1, Terminal 2, Artifacts, Session Context) + dynamic transient card-tabs.
- **`ChatAlertOverlay`** — z-stacked overlay anchored bottom-flush with composer; replaces today's four inline alert blocks. No layout reflow on show/dismiss.

Composer chrome is consolidated: a single `+` trigger replaces six toolbar icons; the four-segment effort dial collapses to a single cycle pill; the "⌘↵ to send" hint is removed; the send icon is replaced with a heavier glyph. Width unifies at `w-[85%] max-w-7xl` across header / transcript / composer.

Pure frontend work. No backend or API changes.

## Naming Convention

`<scope><role><display>` — PascalCase. Generalizes across surfaces:

| segment | examples |
|---|---|
| **scope** | `Chat`, `Sidebar`, `Rail`, `Header`, `Settings` |
| **role** *(optional)* | `Primary`, `Working`, `Alert`, `Session`, `Workspace` |
| **display** | `Drawer`, `Overlay`, `Modal`, `Inline`, `Slideover`, `Popover`, `Strip`, `Grid`, `List` |

Display type reflects actual positioning behavior — `Drawer` is in-flow with reflow on open; `Overlay` is z-stacked, no reflow.

## Architecture

### `ChatPrimaryDrawer`
- Position: between `ChatHeader` and `ChatTranscript`.
- Reuses today's `BottomChatDrawer` chrome with the tab strip flipped to the **bottom edge** of the drawer (becomes the handle when collapsed).
- Closed state: tab strip only. Click a tab to expand. Drag to resize. Double-click handle or click close-arrow icon to retract.
- Top-border ribbon: `--color-primary`.

### `ChatWorkingDrawer`
- Position: between `ChatTranscript` and `ChatComposer`.
- Chrome based on the existing alert-banner pattern: rounded top corners, square bottom (flush against composer), top accent ribbon, bordered surface.
- Default expanded height ≈ 2–3× the alert-banner height. Drag to resize. Double-click handle or click close-arrow icon to retract.
- Width: same as transcript / composer (`w-[85%] max-w-7xl`).
- Top-border ribbon: `--color-success` (differentiates from `ChatPrimaryDrawer`).

### `ChatAlertOverlay`
- Absolute-positioned overlay anchored bottom-flush with composer top edge. **Anchors regardless of `ChatWorkingDrawer` open/closed state.**
- Width: 85% of chat column. Height: fixed reference (~80px or content-driven), **not** literally relative to `ChatWorkingDrawer` height (relative breaks when drawer is closed).
- Square bottom corners (flush). Rounded top corners.
- Top-border ribbon by alert severity: `--color-info` / `--color-warning` / `--color-danger`.
- Z-stacked above `ChatWorkingDrawer`. **No layout reflow on show/dismiss.**

## Tab Assignments

### `ChatPrimaryDrawer` (fixed tabs)
| Tab | Source | Notes |
|---|---|---|
| Documents | existing in `BottomChatDrawer` | |
| Reports | existing | report-card envelopes |
| Diffs | existing | diff-card envelopes |
| **Tools** *(new)* | replaces `ToolCallDrawer` | running-pip indicator on tab during active calls; reads `useChatStore.toolCalls` |
| Pins | existing in `BottomChatDrawer` | DB-backed pinned-card list |
| _dynamic pinned-card tabs_ | existing | per-session, capped at 10 (`BOTTOM_DRAWER_PIN_CAP` to be renamed) |

### `ChatWorkingDrawer` (fixed tabs)
| Tab | Source | Notes |
|---|---|---|
| Scratchpad | existing in `BottomChatDrawer` | |
| **Terminal 1** *(new)* | replaces composer's `shell-running` banner | streamed `!command` shell-exec output |
| **Terminal 2** *(new, dev-mode only)* | new | full interactive shell; gated by `developer_mode` |
| Artifacts | existing in `BottomChatDrawer` | |
| Session Context | existing in `BottomChatDrawer` | user-editable text area; persists across compaction |

### `ChatWorkingDrawer` (dynamic tabs)
- Append after fixed tabs in arrival order.
- Replaces today's singular `Cards` tab. The `Cards` tab is **retired**.
- Routing key `render_target=bottom_chat_drawer` is preserved; payload now creates a tab instead of replacing a slot.
- Per-tab data shape:

```ts
interface DynamicCardTab {
  id: string;          // card:<uuid>
  label: string;       // from envelope title; fallback = envelope-type display name + short timestamp
  payload: Envelope;
  focused: boolean;    // agent-emitted defaults true; promotes to active tab on drawer open
  pinned: boolean;     // when true, promotes via existing POST /drawer-cards endpoint
  createdAt: number;
}
```

- **Activation rule:** when drawer opens, active tab = most-recently-arrived `focused: true` card. Manual user selection sticks until next agent-emit-with-focus arrives.
- **Lifecycle:** persists for the session. User can dismiss (X on tab) or pin to promote to DB-backed via the existing `POST /drawer-cards` endpoint. Same lifecycle as today's pinned cards once promoted.

### `ChatAlertOverlay` (transient content)
| Source | Severity | Today's location |
|---|---|---|
| `sessionTakeover` | info | inline in `ChatMain.tsx` |
| `streamStalled` | warning | inline in `ChatMain.tsx` |
| `circuitOpen` | warning | inline in `ChatMain.tsx` |
| `statusMessage` | info | inline in `ChatMain.tsx` |

Each alert payload is read from existing `useChat` hook state. No new state added for alerts themselves.

## Tab Strip Behavior (shared by both drawers)

- **Order:** fixed tabs first, dynamic tabs after (oldest left → newest right).
- **Native scrollbar hidden.**
- **Pagination arrows** (left/right) appear on overflow. Click paginates the strip.
- **Label truncation:** ~14 chars with `title` attribute carrying full text.
- Implemented as a shared `ChatDrawerTabStrip.tsx` component used by both drawers.

## Composer Cleanup

### Width unification
- `ChatHeader` outer wrapper, `ChatTranscript` inner container, `ChatComposer` wrapper: `max-w-3xl` → `w-[85%] max-w-7xl mx-auto`.
- The 4 inline-alert wrapper `<div>`s in `ChatMain.tsx` are removed entirely (alerts move to `ChatAlertOverlay`); their previous `max-w-3xl` wrapper goes with them.
- `ChatAlertOverlay` itself is sized independently — 85% of chat column, anchored bottom-flush with composer (see Architecture).
- 7.5% L/R margin on each side from `mx-auto`.
- Caps at 1280px on 4K+ screens.

### Bottom gap
- Outer wrapper `pb-4` + composer `pb-4` (combined ≈ 32px) → halved to ≈ 16px total. Footer disclaimer kept.

### DEV indicator
- Stays in current top-right position.
- `border-radius: 999px` → `4px` (matches `rounded-[4px]` token).
- Editor area gains conditional `pr-[88px]` when `developer_mode=true` so editor text wraps cleanly to its left rather than running underneath the indicator.

### Toolbar — left side consolidation
- Single `+` trigger replaces: Layout, Attach, Slash, Mention, Shell-mode, Auto-switch.
- Click `+` → slide-up popover anchored above the trigger, holding those six controls. Same wiring, just behind one trigger.
- Plugin slots (`composer-toolbar`) render inside the popover.

### Effort dial
- Four-segment `LOW NORM HIGH MAX` → single cycle pill labeled with current level **uppercase**.
- Click cycles `LOW → NORM → HIGH → MAX → LOW`.
- Tooltip shows level title from existing `EFFORT_LEVELS`.
- No glyph on the pill.

### Send button
- "⌘↵ to send" hint span removed.
- `ArrowUp size=15 strokeWidth=2.2` → bolder icon (final pick at implementation: `ArrowBigUp`, `SendHorizontal` filled, or `Send` filled — eyeball at real size).
- Stop button (when streaming) unchanged.

### Banners that move out of composer chrome
- Shell-running banner → `Terminal 1` tab content in `ChatWorkingDrawer`.
- Shell-info drawer (when typing `!`): stays inline (it's the shell-input hint, not a banner).
- Drag-drop banner: stays inline (contextual to drop zone).
- Work toast / chat toast: stay inline (transient flashes).
- Shell-approval strip: stays inline (interactive, tight feedback loop).

### Submit-arrow bug
- Existing bug report: "the arrow to submit icon doesn't work."
- Source wiring (`onClick={onSend} → handleSend → onSend(text)`) reads correct.
- Defer to implementation phase to repro and root-cause. Suspect editor stale-text or focus race.

## Migration Map

### Files moved / morphed
| Existing | Becomes | Notes |
|---|---|---|
| `BottomChatDrawer.tsx` | `ChatPrimaryDrawer.tsx` | Same chrome reused, tab strip flipped to bottom edge. New position. Tab list updated. |
| `ToolCallDrawer.tsx` (most of it) | **Retired** | Tools content moves to a tab in `ChatPrimaryDrawer`. |
| `ToolCallDrawer.tsx` condensed banner sub-component | `ToolCallBanner.tsx` (extracted) | Reusable component, preserved for future re-use. |
| 4 inline alert blocks in `ChatMain.tsx` | `ChatAlertOverlay.tsx` | Same alert payloads, rendered as overlay. |
| Composer's `shell-running` banner | `Terminal 1` tab content in `ChatWorkingDrawer` | |
| Singular `Cards` tab in `BottomChatDrawer` | **Retired** — replaced by dynamic card-tabs | Routing key kept; behavior changes from slot-replace to tab-append. |

### New files
| File | Role |
|---|---|
| `ui/src/components/drawers/ChatPrimaryDrawer.tsx` | Top drawer (relocated from `BottomChatDrawer`). |
| `ui/src/components/drawers/ChatWorkingDrawer.tsx` | New, alert-banner-chrome-based mini-card drawer. |
| `ui/src/components/drawers/ChatAlertOverlay.tsx` | New, absolute-positioned alert overlay. |
| `ui/src/components/chat/ChatDrawerTabStrip.tsx` | Shared tab strip with horizontal scroll, hidden scrollbar, pagination arrows, label truncation. |
| `ui/src/components/chat/ToolCallBanner.tsx` | Extracted from old `ToolCallDrawer`. |

### State changes (`useLayoutStore.ts`)
- New: `chatPrimaryDrawerOpen / chatPrimaryDrawerHeight / chatPrimaryDrawerActiveTab`.
- New: `chatWorkingDrawerOpen / chatWorkingDrawerHeight / chatWorkingDrawerActiveTab`.
- New: `chatWorkingDrawerCardTabs: DynamicCardTab[]` — FE-only state for transient cards (pinned cards still DB-backed via existing API).
- Retire `bottomDrawerOpen` etc. Clean rename — drop old persisted localStorage keys (defaults to closed on first load post-migration).

### File locations
All new drawer components in `ui/src/components/drawers/`. Shared subcomponents (`ChatDrawerTabStrip.tsx`, `ToolCallBanner.tsx`) in `ui/src/components/chat/`.

### No backend changes required
- `render_target=bottom_chat_drawer` routing key kept.
- Pinned-card API (`POST /drawer-cards`, `DELETE /drawer-cards/{id}`) unchanged.
- Alert payloads read from existing `useChat` hook state.

## Open Questions (deferred to implementation)

1. Choice of bolder send icon (`ArrowBigUp` vs `SendHorizontal` vs `Send` filled). Eyeball at real size.
2. `ChatAlertOverlay` exact height — fixed reference (~80px) vs content-driven. Lean toward content-driven with a min ~64px.
3. Card-tab label fallback rule when envelope has no title — proposed: envelope-type display name + short timestamp.
4. Drawer drag bounds — proposed: min = tab-strip-only (handle visible); max = 50% viewport height.
5. Submit-arrow bug repro and root-cause.
6. Pinned-card API location coupling — `POST /drawer-cards` may implicitly assume "the bottom drawer" semantics. Read handler during impl; tweak if needed.

## Risks

1. **Z-index contention.** `ChatAlertOverlay` shares z-space with model-picker portal (`z-[9999]`), slash/mention popovers, the new `+` popover, tooltips, and modals. Implementation needs a deterministic stack order. Proposed: alert overlay at `z-30`, in-toolbar popovers at `z-40`, modals at `z-50`+, portaled dropdowns at `z-[9999]`.
2. **Plugin toolbar slot visibility.** Plugins registered for `composer-toolbar` slot will render inside the `+` popover instead of the visible toolbar. Plugin authors lose direct visibility. Surface in a release note. No API change.
3. **Alert overlay obscuring `ChatWorkingDrawer` content.** Stream-stalled and circuit-open already have explicit dismiss/reconnect actions. Status-message auto-clears. Acceptable.

## Testing

### Manual (browser, must-pass before merge)
- Drawer open/close: tab click, double-click handle, drag resize, close-arrow icon — both drawers.
- Tab pagination: load 6+ dynamic card-tabs; arrows appear; paginate left/right; native scrollbar hidden.
- Alert overlay z-stack: fire each alert type with `ChatWorkingDrawer` closed AND open. No layout reflow on show/dismiss.
- Width responsiveness: 1024 / 1440 / 1920 / 2560.
- DEV indicator text-wrap: long message in editor with `developer_mode=true`.
- Composer popovers (`+` and effort cycle): click-outside dismiss, keyboard accessibility, z-stack with model picker.
- Send button: confirms the existing bug is fixed.

### Regression watch
- Pinned-card lifecycle (`POST/DELETE /drawer-cards`).
- Envelopes with `render_target=bottom_chat_drawer` create dynamic tabs (not replace a slot).
- Multi-session streaming presence pips unaffected.
- Existing keyboard shortcuts (Cmd+B, Cmd+L, Cmd+/, Cmd+N, Cmd+K, Cmd+]/[, Cmd+D, Cmd+.) still wired.

### Automated
None proposed — Nanite frontend doesn't have Playwright today; adding it is its own project. Defer.

## Decisions locked

- **Hybrid drawer architecture (Option C + layered alert).** Top drawer for persistent reference content, bottom drawer for working mini-cards, alert overlay z-stacked above the bottom drawer. Rationale: splits on persistent-vs-transient semantic boundary; eliminates layout reflow when alerts fire.
- **Tools tab in `ChatPrimaryDrawer`.** Tool-call activity moves to a tab in the top drawer (not a tab in `ChatWorkingDrawer`, not inline-only). Rationale: keeps tool history accessible without dedicating a whole drawer to it; running-pip on the tab preserves the live-during-streaming signal today's auto-show provides.
- **Preserve condensed banner sub-component from `ToolCallDrawer`.** Extracted as `ToolCallBanner.tsx` for future re-use before retiring the parent drawer. Rationale: user explicitly flagged this for re-use elsewhere.
- **Naming convention `<scope><role><display>` (PascalCase).** Generalizes across surfaces. Display type reflects actual positioning (`Drawer` vs `Overlay`).
- **Dynamic card-tabs replace the singular `Cards` tab in `ChatWorkingDrawer`.** Each agent-emitted transient envelope becomes its own tab. Tabs persist for the session; user can dismiss or pin (existing `POST /drawer-cards` endpoint promotes to DB-backed).
- **Width: `w-[85%] max-w-7xl mx-auto` across header, transcript, alerts, composer.** Replaces `max-w-3xl`. Caps at 1280px on 4K+ screens.

## Follow-up candidates

- **Rename `render_target=bottom_chat_drawer` routing key** to match new naming (e.g. `chat_working_drawer` or `chat.working`). Out of scope for this redesign — touch when convenient.
- **Apply naming convention to Sidebar / Right Rail / Header / Settings surfaces** opportunistically when those areas are touched.
- **A11y deep pass** — keyboard nav of new tab strips, ARIA labels for drawer state, focus management between editor and Terminal 2. Per `frontend.md` known follow-up area.
- **Playwright/E2E test infrastructure** — would benefit several recent UI projects, not just this one. Separate scoping.
- **`+` menu icons (voice / screenshot / template / memory-recall)** — deferred.

## Known limitations / preserved tech debt

- **Plugin toolbar slot visibility regression.** Plugins registering for the `composer-toolbar` slot render inside the `+` popover instead of the visible toolbar. Acceptable trade for the cleanup; needs release-note callout.
- **`BOTTOM_DRAWER_PIN_CAP` constant name** is location-coupled. Renaming to `CHAT_PRIMARY_DRAWER_PIN_CAP` should happen with the rest of the rename, but if pinned-card API is location-coupled (open question 6), backend change may be deferred.
- **Submit-arrow bug** is preserved as a known issue for implementation phase; design does not solve it.
- **No automated test coverage** for the new layout-reflow stability behavior. Manual verification only until Playwright is adopted.

## Out of scope

- Renaming the `render_target=bottom_chat_drawer` routing key.
- Aligning Sidebar / Right Rail / Header / Settings naming to new convention.
- New `+` menu icons (voice, screenshot, template insert, memory recall).
- Backend changes (none required for this design).
- Playwright/E2E test infrastructure.
- A11y deep pass — captured as a follow-up.
