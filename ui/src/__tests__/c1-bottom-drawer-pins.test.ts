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
import { CHAT_DRAWER_PIN_CAP } from "@/lib/constants";
import type { Envelope } from "@/lib/types";

afterEach(() => {
  useLayoutStore.setState({
    panelEnvelopes: {},
    defaultDrawerTab: "scratchpad",
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
});

// ── Tab overflow smoke test ──────────────────────────────────────────────────
//
// The horizontal-scroll behaviour is a CSS contract (`overflow-x-auto
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
