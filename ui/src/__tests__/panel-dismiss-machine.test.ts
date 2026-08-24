/**
 * J8 v1 — dismiss state machine regression tests (CW-20260426-0006).
 *
 * Per the locked v1 Decision Log, the agent-controlled panels surface uses a
 * 4-state dismiss machine encoded purely in the layout store:
 *
 *   - closed         (no entry in panelOpenSource, no entry in dismissedByUser)
 *   - agent_opened   (panelOpenSource[id] === 'agent', no dismiss flag)
 *   - user_opened    (panelOpenSource[id] === 'user', no dismiss flag)
 *   - user_dismissed (dismissedByUser[id] === true)
 *
 * Transition contract:
 *   - agent_open  → if dismissed: NO-OP. Otherwise: agent_opened.
 *   - user_open   → user_opened (clears dismiss flag).
 *   - markPanelDismissed → user_dismissed (clears source attribution).
 *   - clearAllPanelDismissed → closed (per-panel) for all dismissed panels.
 *
 * Migrated 2026-05-01 chat-surface redesign — the bottom_chat_drawer surface
 * was retired in favor of ChatWorkingDrawer. The J8 source-attribution
 * machine no longer applies to that surface; only right-rail panels use it.
 * Routing through `bottom_chat_drawer` (the legacy key) still opens the
 * working drawer for back-compat, but without the dismiss-attribution gate.
 */

import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { resolvePanelMode } from "@/lib/panel-modes";
import { applyEnvelopePanelEffects, applyPanelSignal } from "@/lib/panel-signal";
import { useLayoutStore } from "@/stores/useLayoutStore";

// ---- Test helpers --------------------------------------------------------

const PRISTINE_PREFS = {
  panelEnabled: {},
  panelOrder: [],
  dismissedByUser: {},
  panelOpenSource: {},
};

function reset() {
  useLayoutStore.setState({
    rightRailOpen: false,
    rightRailTab: "widgets",
    panelPrefs: { ...PRISTINE_PREFS },
    panelEnvelopes: {},
    chatWorkingDrawer: { open: false, height: 200, activeTab: "scratchpad" },
  });
}

beforeEach(() => reset());
afterEach(() => reset());

// ---- 4-state machine: right-rail panels ---------------------------------

describe("dismiss state machine — right-rail panels", () => {
  it("agent_open from closed transitions to agent_opened", () => {
    useLayoutStore.getState().setPanelOpen("work", "agent");
    const s = useLayoutStore.getState();
    expect(s.rightRailOpen).toBe(true);
    expect(s.rightRailTab).toBe("work");
    expect(s.panelPrefs.panelOpenSource.work).toBe("agent");
  });

  it("user_open from closed transitions to user_opened", () => {
    useLayoutStore.getState().setPanelOpen("work", "user");
    expect(useLayoutStore.getState().panelPrefs.panelOpenSource.work).toBe("user");
  });

  it("user dismiss transitions to user_dismissed", () => {
    useLayoutStore.getState().setPanelOpen("work", "agent");
    useLayoutStore.getState().markPanelDismissed("work");
    const s = useLayoutStore.getState();
    expect(s.panelPrefs.dismissedByUser.work).toBe(true);
    // Source attribution cleared on dismiss — the panel is no longer
    // agent_opened or user_opened, it's user_dismissed.
    expect(s.panelPrefs.panelOpenSource.work).toBeUndefined();
  });

  it("agent_open is NO-OP when state is user_dismissed", () => {
    useLayoutStore.getState().markPanelDismissed("work");
    useLayoutStore.getState().setPanelOpen("work", "agent");
    const s = useLayoutStore.getState();
    // Dismiss flag survives; right-rail not opened to work tab.
    expect(s.panelPrefs.dismissedByUser.work).toBe(true);
    expect(s.panelPrefs.panelOpenSource.work).toBeUndefined();
    expect(s.rightRailTab).not.toBe("work");
  });

  it("user_open clears the dismiss flag (manual override)", () => {
    useLayoutStore.getState().markPanelDismissed("work");
    useLayoutStore.getState().setPanelOpen("work", "user");
    const s = useLayoutStore.getState();
    expect(s.panelPrefs.dismissedByUser.work).toBeUndefined();
    expect(s.panelPrefs.panelOpenSource.work).toBe("user");
    expect(s.rightRailTab).toBe("work");
  });

  it("clearAllPanelDismissed wipes the dismiss flag for all panels", () => {
    useLayoutStore.getState().markPanelDismissed("work");
    useLayoutStore.getState().markPanelDismissed("workflows");
    useLayoutStore.getState().clearAllPanelDismissed();
    const s = useLayoutStore.getState();
    expect(s.panelPrefs.dismissedByUser).toEqual({});
  });
});

// ---- panel_signal SSE event routing -------------------------------------

describe("applyPanelSignal", () => {
  it("action=open routes to setPanelOpen with source=agent", () => {
    applyPanelSignal({ action: "open", panel_id: "work" });
    const s = useLayoutStore.getState();
    expect(s.rightRailTab).toBe("work");
    expect(s.panelPrefs.panelOpenSource.work).toBe("agent");
  });

  it("action=open with bottom_chat_drawer opens the chat working drawer", () => {
    applyPanelSignal({ action: "open", panel_id: "bottom_chat_drawer" });
    expect(useLayoutStore.getState().chatWorkingDrawer.open).toBe(true);
  });

  it("action=close on dismissed user-opened panel is NO-OP for agent source", () => {
    useLayoutStore.getState().setPanelOpen("work", "user");
    applyPanelSignal({ action: "close", panel_id: "work", source: "agent" });
    expect(useLayoutStore.getState().rightRailTab).toBe("work");
  });

  it("action=mode=planning opens preset [work, workflows]", () => {
    applyPanelSignal({ action: "mode", mode: "planning" });
    const s = useLayoutStore.getState();
    expect(s.panelPrefs.panelOpenSource.work).toBe("agent");
    expect(s.panelPrefs.panelOpenSource.workflows).toBe("agent");
  });

  it("action=mode with unknown mode is silent NO-OP (forward-compat)", () => {
    applyPanelSignal({ action: "mode", mode: "future-mode" });
    const s = useLayoutStore.getState();
    expect(s.panelPrefs.panelOpenSource).toEqual({});
  });

  it("dismissed panels stay closed under mode signal", () => {
    useLayoutStore.getState().markPanelDismissed("work");
    applyPanelSignal({ action: "mode", mode: "planning" });
    const s = useLayoutStore.getState();
    expect(s.panelPrefs.dismissedByUser.work).toBe(true);
    expect(s.panelPrefs.panelOpenSource.work).toBeUndefined();
    // workflows is not dismissed so it should open.
    expect(s.panelPrefs.panelOpenSource.workflows).toBe("agent");
  });
});

// ---- Envelope target/mode side effects ----------------------------------

describe("applyEnvelopePanelEffects", () => {
  it("envelope.target=bottom_chat_drawer opens the chat working drawer", () => {
    applyEnvelopePanelEffects({
      kind: "envelope",
      version: 1,
      type: "document-viewer",
      target: "bottom_chat_drawer",
    });
    expect(useLayoutStore.getState().chatWorkingDrawer.open).toBe(true);
  });

  it("envelope.mode opens the preset panels", () => {
    applyEnvelopePanelEffects({
      kind: "envelope",
      version: 1,
      type: "document-viewer",
      mode: "planning",
    });
    const s = useLayoutStore.getState();
    expect(s.panelPrefs.panelOpenSource.work).toBe("agent");
    expect(s.panelPrefs.panelOpenSource.workflows).toBe("agent");
  });
});

// ---- Mode preset map ----------------------------------------------------

describe("resolvePanelMode", () => {
  it("planning returns [work, workflows]", () => {
    expect(resolvePanelMode("planning")).toEqual(["work", "workflows"]);
  });

  it("unknown mode returns null (forward-compat)", () => {
    expect(resolvePanelMode("future-mode-not-in-v1")).toBeNull();
  });

  it("empty string returns null", () => {
    expect(resolvePanelMode("")).toBeNull();
  });
});

// ---- A2 — render_target routing -----------------------------------------
// CW-20260428-0008. The placement field that's separate from the visibility
// field. Pushes envelopes into the named panel's inbox slot AND (for non-
// bottom-drawer panels) opens via the dismiss machine. Bottom-drawer routing
// no longer participates in dismiss attribution.

describe("applyEnvelopePanelEffects — render_target", () => {
  it("pushes envelope into panelEnvelopes and opens the chat working drawer", () => {
    const env = {
      kind: "envelope",
      version: 1,
      type: "info-card",
      title: "Hello",
      data: { title: "Hello", body: "world" },
      render_target: "bottom_chat_drawer",
    };
    applyEnvelopePanelEffects(env);
    const s = useLayoutStore.getState();
    expect(s.chatWorkingDrawer.open).toBe(true);
    expect(s.panelEnvelopes["bottom_chat_drawer"]).toHaveLength(1);
    expect(s.panelEnvelopes["bottom_chat_drawer"][0].title).toBe("Hello");
  });

  it("routes to right-rail panels by id (work, workflows)", () => {
    const env = {
      kind: "envelope",
      version: 1,
      type: "info-card",
      data: { title: "T", body: "b" },
      render_target: "work",
    };
    applyEnvelopePanelEffects(env);
    const s = useLayoutStore.getState();
    expect(s.panelEnvelopes["work"]).toHaveLength(1);
    expect(s.panelPrefs.panelOpenSource.work).toBe("agent");
  });

  it("multiple envelopes accumulate in arrival order (drawer policy decides display)", () => {
    applyEnvelopePanelEffects({
      kind: "envelope",
      version: 1,
      type: "info-card",
      title: "First",
      data: { title: "First", body: "1" },
      render_target: "bottom_chat_drawer",
    });
    applyEnvelopePanelEffects({
      kind: "envelope",
      version: 1,
      type: "info-card",
      title: "Second",
      data: { title: "Second", body: "2" },
      render_target: "bottom_chat_drawer",
    });
    const queue = useLayoutStore.getState().panelEnvelopes["bottom_chat_drawer"];
    expect(queue).toHaveLength(2);
    expect(queue[0].title).toBe("First");
    expect(queue[1].title).toBe("Second");
  });

  it("clearPanelEnvelopes empties the inbox for a panel", () => {
    applyEnvelopePanelEffects({
      kind: "envelope",
      version: 1,
      type: "info-card",
      data: { title: "T", body: "b" },
      render_target: "bottom_chat_drawer",
    });
    useLayoutStore.getState().clearPanelEnvelopes("bottom_chat_drawer");
    expect(useLayoutStore.getState().panelEnvelopes["bottom_chat_drawer"]).toBeUndefined();
  });
});
