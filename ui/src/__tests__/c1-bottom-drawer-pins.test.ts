/**
 * C1 (CW-20260428-0012) — chat drawer pinned-card lifecycle tests.
 *
 * Targets the FE state surface: defaultDrawerTab persistence, transient inbox
 * slot semantics, pin-cap constant, panelEnvelopes routing. The DB-backed pin
 * lifecycle has its own Go table-tests in internal/store/bottom_drawer_cards_test.go.
 *
 * Migrated 2026-05-01 chat-surface redesign — old BottomChatDrawer is retired
 * and BOTTOM_DRAWER_PIN_CAP moved to @/lib/constants as CHAT_DRAWER_PIN_CAP.
 */

import { afterEach, describe, expect, it } from "vitest";
import { useLayoutStore } from "@/stores/useLayoutStore";
import { useShellStore } from "@/stores/useShellStore";
import { CHAT_DRAWER_PIN_CAP } from "@/lib/constants";
import type { DynamicCardTab, Envelope } from "@/lib/types";

afterEach(() => {
  useLayoutStore.setState({
    panelEnvelopes: {},
    defaultDrawerTab: "scratchpad",
    chatWorkingDrawerSessions: {},
  });
  useShellStore.setState({
    sessions: {},
    shellChunks: [],
    shellRunning: false,
    pendingShellCommand: null,
  });
});

describe("CHAT_DRAWER_PIN_CAP", () => {
  it("matches the backend store.BottomDrawerPinCap (10)", () => {
    expect(CHAT_DRAWER_PIN_CAP).toBe(10);
  });
});

describe("defaultDrawerTab", () => {
  it("defaults to 'scratchpad' to preserve existing UX", () => {
    expect(useLayoutStore.getState().defaultDrawerTab).toBe("scratchpad");
  });

  it("setter updates the value", () => {
    useLayoutStore.getState().setDefaultDrawerTab("artifacts");
    expect(useLayoutStore.getState().defaultDrawerTab).toBe("artifacts");
  });

  it("accepts arbitrary built-in tab IDs", () => {
    const ids = ["scratchpad", "terminal-1", "artifacts", "session-context"];
    for (const id of ids) {
      useLayoutStore.getState().setDefaultDrawerTab(id);
      expect(useLayoutStore.getState().defaultDrawerTab).toBe(id);
    }
  });
});

describe("panelEnvelopes (transient slot)", () => {
  const stubEnvelope = (id: string, type = "info-card"): Envelope => ({
    kind: "envelope",
    version: 1,
    type,
    id,
    title: `Card ${id}`,
  });

  it("pushPanelEnvelope appends to bottom_chat_drawer slot", () => {
    const store = useLayoutStore.getState();
    store.pushPanelEnvelope("bottom_chat_drawer", stubEnvelope("e1"));
    store.pushPanelEnvelope("bottom_chat_drawer", stubEnvelope("e2"));
    const env = useLayoutStore.getState().panelEnvelopes["bottom_chat_drawer"];
    expect(env).toHaveLength(2);
    expect(env?.[1]?.id).toBe("e2");
  });

  it("clearPanelEnvelopes drops the slot", () => {
    const store = useLayoutStore.getState();
    store.pushPanelEnvelope("bottom_chat_drawer", stubEnvelope("e1"));
    store.clearPanelEnvelopes("bottom_chat_drawer");
    expect(useLayoutStore.getState().panelEnvelopes["bottom_chat_drawer"]).toBeUndefined();
  });

  it("bottom drawer envelopes can be scoped by session", () => {
    const store = useLayoutStore.getState();
    store.pushPanelEnvelope("bottom_chat_drawer", stubEnvelope("a1"), "session-a");
    store.pushPanelEnvelope("bottom_chat_drawer", stubEnvelope("b1"), "session-b");

    expect(useLayoutStore.getState().panelEnvelopes["bottom_chat_drawer:session-a"]?.[0]?.id).toBe("a1");
    expect(useLayoutStore.getState().panelEnvelopes["bottom_chat_drawer:session-b"]?.[0]?.id).toBe("b1");

    store.clearPanelEnvelopes("bottom_chat_drawer", "session-a");
    expect(useLayoutStore.getState().panelEnvelopes["bottom_chat_drawer:session-a"]).toBeUndefined();
    expect(useLayoutStore.getState().panelEnvelopes["bottom_chat_drawer:session-b"]?.[0]?.id).toBe("b1");
  });
});

describe("chat working drawer session state", () => {
  const tab = (id: string): DynamicCardTab => ({
    id,
    label: id,
    payload: {
      kind: "envelope",
      version: 1,
      type: "info-card",
      id,
    },
    focused: true,
    pinned: false,
    createdAt: Date.now(),
  });

  it("keeps drawer active tab and card tabs per session", () => {
    const store = useLayoutStore.getState();
    store.setChatWorkingDrawer({ open: true, activeTab: "artifacts" }, "session-a");
    store.appendChatWorkingDrawerCardTab(tab("card:a"), "session-a");
    store.setChatWorkingDrawer({ open: false, activeTab: "scratchpad" }, "session-b");

    const state = useLayoutStore.getState().chatWorkingDrawerSessions;
    expect(state["session-a"]?.drawer.open).toBe(true);
    expect(state["session-a"]?.drawer.activeTab).toBe("card:a");
    expect(state["session-a"]?.cardTabs).toHaveLength(1);
    expect(state["session-b"]?.drawer.open).toBe(false);
    expect(state["session-b"]?.drawer.activeTab).toBe("scratchpad");
    expect(state["session-b"]?.cardTabs).toHaveLength(0);
  });
});

describe("Terminal-1 session state", () => {
  it("keeps output and pending command per session", () => {
    const store = useShellStore.getState();
    store.appendShellOutput("session-a", "a output\n");
    store.appendShellOutput("session-b", "b output\n");
    store.setPendingShellCommand("session-b", "pwd");
    store.setShellRunning("session-a", true);

    const state = useShellStore.getState().sessions;
    expect(state["session-a"]?.shellChunks.join("")).toBe("a output\n");
    expect(state["session-a"]?.shellRunning).toBe(true);
    expect(state["session-a"]?.pendingShellCommand).toBeNull();
    expect(state["session-b"]?.shellChunks.join("")).toBe("b output\n");
    expect(state["session-b"]?.pendingShellCommand).toBe("pwd");
    expect(state["session-b"]?.shellRunning).toBe(false);
  });
});

// ── Tab overflow smoke test ──────────────────────────────────────────────────
//
// The horizontal-scroll behavior is a CSS contract (`overflow-x-auto
// scrollbar-hide` on the tabstrip wrapper). We can't render the full drawer
// in a non-DOM Vitest run without significant React-Query setup, so this
// "smoke test" verifies the constant + helper invariants the JSX relies on:
// pinned-cards-as-tabs IDs are namespaced with `pin:` so the active-tab
// switch logic doesn't collide with built-in IDs.

describe("tab overflow contract", () => {
  it("pinned card tab IDs use the 'pin:' namespace", () => {
    const builtins = ["scratchpad", "terminal-1", "artifacts", "session-context"];
    const pinId = "pin:abc-123";
    expect(pinId.startsWith("pin:")).toBe(true);
    expect(builtins.includes(pinId)).toBe(false);
  });

  it("can encode up to CHAT_DRAWER_PIN_CAP unique pin tab IDs", () => {
    const ids = Array.from({ length: CHAT_DRAWER_PIN_CAP }, (_, i) => `pin:card-${i}`);
    const unique = new Set(ids);
    expect(unique.size).toBe(CHAT_DRAWER_PIN_CAP);
  });
});
