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
 * The bottom_chat_drawer surface mirrors the right-rail semantics through
 * setBottomDrawerOpen (separate state slot).
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
    bottomChatDrawerOpen: false,
    panelPrefs: { ...PRISTINE_PREFS },
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

// ---- 4-state machine: bottom_chat_drawer --------------------------------

describe("dismiss state machine — bottom_chat_drawer", () => {
  it("agent open from closed transitions to agent_opened (true + source=agent)", () => {
    useLayoutStore.getState().setBottomDrawerOpen(true, "agent");
    const s = useLayoutStore.getState();
    expect(s.bottomChatDrawerOpen).toBe(true);
    expect(s.panelPrefs.panelOpenSource.bottom_chat_drawer).toBe("agent");
  });

  it("user dismiss (close with source=user) transitions to user_dismissed", () => {
    useLayoutStore.getState().setBottomDrawerOpen(true, "agent");
    useLayoutStore.getState().setBottomDrawerOpen(false, "user");
    const s = useLayoutStore.getState();
    expect(s.bottomChatDrawerOpen).toBe(false);
    expect(s.panelPrefs.dismissedByUser.bottom_chat_drawer).toBe(true);
    expect(s.panelPrefs.panelOpenSource.bottom_chat_drawer).toBeUndefined();
  });

  it("agent open after user dismiss is NO-OP", () => {
    useLayoutStore.getState().setBottomDrawerOpen(true, "user");
    useLayoutStore.getState().setBottomDrawerOpen(false, "user"); // user dismiss
    useLayoutStore.getState().setBottomDrawerOpen(true, "agent");
    const s = useLayoutStore.getState();
    expect(s.bottomChatDrawerOpen).toBe(false);
    expect(s.panelPrefs.dismissedByUser.bottom_chat_drawer).toBe(true);
  });

  it("agent close on user-opened drawer is NO-OP (user-overrides-agent)", () => {
    useLayoutStore.getState().setBottomDrawerOpen(true, "user");
    useLayoutStore.getState().setBottomDrawerOpen(false, "agent");
    const s = useLayoutStore.getState();
    expect(s.bottomChatDrawerOpen).toBe(true);
    expect(s.panelPrefs.panelOpenSource.bottom_chat_drawer).toBe("user");
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

  it("action=open with bottom_chat_drawer routes to bottom drawer", () => {
    applyPanelSignal({ action: "open", panel_id: "bottom_chat_drawer" });
    expect(useLayoutStore.getState().bottomChatDrawerOpen).toBe(true);
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
  it("envelope.target opens the named drawer with source=agent", () => {
    applyEnvelopePanelEffects({
      kind: "envelope",
      version: 1,
      type: "document-viewer",
      target: "bottom_chat_drawer",
    });
    expect(useLayoutStore.getState().bottomChatDrawerOpen).toBe(true);
    expect(useLayoutStore.getState().panelPrefs.panelOpenSource.bottom_chat_drawer).toBe("agent");
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

  it("envelope can carry both target and mode (independent effects)", () => {
    applyEnvelopePanelEffects({
      kind: "envelope",
      version: 1,
      type: "document-viewer",
      target: "bottom_chat_drawer",
      mode: "planning",
    });
    const s = useLayoutStore.getState();
    expect(s.bottomChatDrawerOpen).toBe(true);
    expect(s.panelPrefs.panelOpenSource.work).toBe("agent");
    expect(s.panelPrefs.panelOpenSource.workflows).toBe("agent");
  });

  it("dismiss machine still gates envelope.target opens", () => {
    useLayoutStore.getState().markPanelDismissed("bottom_chat_drawer");
    applyEnvelopePanelEffects({
      kind: "envelope",
      version: 1,
      type: "document-viewer",
      target: "bottom_chat_drawer",
    });
    expect(useLayoutStore.getState().bottomChatDrawerOpen).toBe(false);
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

// ---- End-to-end scenario from the J8 ticket ------------------------------

describe("J8 v1 concrete scenario — document in chat drawer + plan in work panel", () => {
  it("agent loads md → bottom drawer opens; mode=planning → work+workflows open", () => {
    // Step 1: agent emits envelope with target=bottom_chat_drawer
    applyEnvelopePanelEffects({
      kind: "envelope",
      version: 1,
      type: "document-viewer",
      target: "bottom_chat_drawer",
    });
    expect(useLayoutStore.getState().bottomChatDrawerOpen).toBe(true);

    // Step 2: agent signals mode=planning
    applyPanelSignal({ action: "mode", mode: "planning" });
    let s = useLayoutStore.getState();
    expect(s.panelPrefs.panelOpenSource.work).toBe("agent");
    expect(s.panelPrefs.panelOpenSource.workflows).toBe("agent");

    // Step 3: user dismisses Work
    useLayoutStore.getState().markPanelDismissed("work");

    // Step 4: agent's subsequent emit does NOT re-open Work
    applyPanelSignal({ action: "open", panel_id: "work" });
    s = useLayoutStore.getState();
    expect(s.panelPrefs.dismissedByUser.work).toBe(true);
    expect(s.panelPrefs.panelOpenSource.work).toBeUndefined();

    // Step 5: user sends new message → dismiss state resets
    useLayoutStore.getState().clearAllPanelDismissed();

    // Step 6: agent can re-open Work
    applyPanelSignal({ action: "open", panel_id: "work" });
    s = useLayoutStore.getState();
    expect(s.panelPrefs.panelOpenSource.work).toBe("agent");
  });
});
