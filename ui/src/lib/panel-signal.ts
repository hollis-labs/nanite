/**
 * J8 v1 — agent-driven panel signal handling (CW-20260426-0006).
 *
 * Two entry points:
 *   - applyPanelSignal: handles a `panel_signal` SSE event payload
 *     (open / close / mode actions emitted by nanite_panel_open / _close /
 *     signal_mode tools).
 *   - applyEnvelopePanelEffects: handles an envelope's `target` and `mode`
 *     fields when the envelope arrives via the plugin_envelope SSE channel.
 *
 * Both routes funnel through the layout store so the 4-state dismiss machine
 * stays the single source of truth for "should this open actually happen".
 *
 * Design note: imports useLayoutStore directly (no React hook usage) so it
 * can be called from inside SSE handlers in useChat.ts without forcing the
 * caller into a React render path.
 */

import { resolvePanelMode } from "@/lib/panel-modes";
import type { Envelope } from "@/lib/types";
import { useLayoutStore } from "@/stores/useLayoutStore";

/** Payload riding on a `panel_signal` SSE event (mirrors PanelSignal in Go). */
export interface PanelSignalPayload {
  action: "open" | "close" | "mode";
  panel_id?: string;
  mode?: string;
  source?: "agent" | "user";
}

/**
 * Handle a panel_signal SSE event payload. The action vocabulary is small:
 *   - open: open the named panel (or bottom_chat_drawer) attributed to source.
 *   - close: close the named panel (or bottom_chat_drawer) attributed to source.
 *   - mode: resolve the mode against the preset map and open each panel in it.
 *
 * Per the J8 contract, source defaults to 'agent' for stream-event payloads
 * (the user-side path always goes through direct UI interactions, not SSE).
 */
export function applyPanelSignal(sig: PanelSignalPayload): void {
  const source = sig.source ?? "agent";
  const layout = useLayoutStore.getState();

  switch (sig.action) {
    case "open": {
      if (!sig.panel_id) return;
      openPanelById(sig.panel_id, source);
      break;
    }
    case "close": {
      if (!sig.panel_id) return;
      closePanelById(sig.panel_id, source);
      break;
    }
    case "mode": {
      if (!sig.mode) return;
      const preset = resolvePanelMode(sig.mode);
      if (!preset) return; // unknown mode → silent no-op (forward-compat).
      for (const id of preset) {
        openPanelById(id, source);
      }
      break;
    }
    default:
      // Unknown action — silently drop for forward-compat.
      break;
  }
  // Reference layout to silence "unused-var" complaints in some toolchains.
  void layout;
}

/**
 * Apply panel-related side effects from an envelope arriving via the chat
 * stream. Reads three independent fields:
 *
 *   - `target` (J8) — visibility hint: open the named panel.
 *   - `render_target` (A2) — placement hint: route the envelope into the
 *     named panel's inbox slot AND open the panel. Skipped when the
 *     dismiss machine refuses the open (matches `target` semantics).
 *   - `mode` (J8) — workspace preset: open the panels in the preset map.
 *
 * All three go through the layout store with source='agent' so the dismiss
 * machine stays the single source of truth for whether the open actually
 * happens.
 *
 * Called from useChat's plugin_envelope handler. The envelope is also
 * persisted into the chat store as a normal PluginEnvelopeItem; this helper
 * only deals with panel side effects.
 */
export function applyEnvelopePanelEffects(envelope: Envelope): void {
  if (envelope.target) {
    openPanelById(envelope.target, "agent");
  }
  if (envelope.render_target) {
    routeEnvelopeToPanel(envelope.render_target, envelope);
  }
  if (envelope.mode) {
    const preset = resolvePanelMode(envelope.mode);
    if (preset) {
      for (const id of preset) {
        openPanelById(id, "agent");
      }
    }
  }
}

/**
 * Push the envelope into the named panel's inbox slot and open the panel.
 * The dismiss machine still gates the open — if the user has dismissed the
 * panel since the last conversational trigger, the open is a no-op. We
 * still push the envelope into the slot so a subsequent user-open lands on
 * the freshest content (Q5 — drawer-owned policy).
 */
function routeEnvelopeToPanel(panelId: string, envelope: Envelope): void {
  const store = useLayoutStore.getState();
  store.pushPanelEnvelope(panelId, envelope);
  openPanelById(panelId, "agent");
}

/**
 * Bottom-chat-drawer is a separate UI surface from the right-rail panel host
 * (per J8 v1 ticket: "wire panel_open dispatch differently for it"). Route
 * those opens through setBottomDrawerOpen; everything else goes through
 * setPanelOpen which pipes through the right-rail tab strip.
 */
function openPanelById(id: string, source: "agent" | "user"): void {
  const store = useLayoutStore.getState();
  if (id === "bottom_chat_drawer") {
    store.setBottomDrawerOpen(true, source);
    return;
  }
  store.setPanelOpen(id, source);
}

function closePanelById(id: string, source: "agent" | "user"): void {
  const store = useLayoutStore.getState();
  if (id === "bottom_chat_drawer") {
    store.setBottomDrawerOpen(false, source);
    return;
  }
  // Right-rail close: respect the user-overrides-agent rule. An agent close
  // on a user-opened panel is a NO-OP. A user close marks the panel
  // dismissed (via markPanelDismissed) so subsequent agent opens are gated.
  if (source === "user") {
    store.markPanelDismissed(id);
    if (store.rightRailTab === id) {
      store.setRightRail(false);
    }
    return;
  }
  // Agent close: refuse if the user manually opened this panel.
  if (store.panelPrefs.panelOpenSource[id] === "user") {
    return;
  }
  if (store.rightRailTab === id) {
    store.setRightRail(false);
  }
}
