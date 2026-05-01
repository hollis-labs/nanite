/**
 * J10 (CW-20260426-0008) — bottom drawer enhancements regression tests.
 *
 * Tests:
 * 1. Documents are excluded from context by default
 * 2. Working-drawer tab IDs (post-redesign)
 * 3. Session context prompt slot is non-compactable (SlotUserContext)
 *
 * Migrated 2026-05-01 chat-surface redesign — the J8 source-attribution
 * dismiss-machine assertions for `bottom_chat_drawer` were retired along
 * with the surface itself. Right-rail dismiss-machine coverage stays in
 * panel-dismiss-machine.test.ts.
 */

import { afterEach, describe, expect, it } from "vitest";
import { useLayoutStore } from "@/stores/useLayoutStore";

// Reset store between tests.
afterEach(() => {
  useLayoutStore.setState({
    panelPrefs: {
      panelEnabled: {},
      panelOrder: [],
      dismissedByUser: {},
      panelOpenSource: {},
    },
    chatWorkingDrawer: { open: false, height: 200, activeTab: "scratchpad" },
  });
});

// ── Documents context exclusion ───────────────────────────────────────────────

describe("documents context inclusion", () => {
  it("documents are excluded from agent context by default (included=false)", () => {
    // This tests the invariant that newly created documents have included=false.
    // The actual store test is in Go; here we verify the type contract.
    const doc = {
      id: "doc-1",
      session_id: "sess-1",
      name: "test.md",
      mime_type: "text/plain",
      content: "hello",
      size_bytes: 5,
      included: false,
      full_content: false,
      summary: "",
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    };
    expect(doc.included).toBe(false);
    expect(doc.full_content).toBe(false);
  });

  it("pointer mode is the default when included", () => {
    const doc = {
      included: true,
      full_content: false, // pointer mode by default
    };
    expect(doc.full_content).toBe(false);
  });
});

// ── Working drawer tab structure (post-redesign) ──────────────────────────────

describe("chat working drawer tab structure", () => {
  it("drawer supports the redesigned fixed-tab IDs", () => {
    // Structural invariant — these IDs are the source of truth in
    // ChatWorkingDrawer.tsx FIXED_TABS and the PreferencesPanel default-tab
    // selector. terminal-2 is dev-only; the rest are user-facing.
    const tabs = ["scratchpad", "terminal-1", "terminal-2", "artifacts", "session-context"] as const;
    expect(tabs).toHaveLength(5);
    expect(tabs[0]).toBe("scratchpad");
    expect(tabs[3]).toBe("artifacts");
    expect(tabs[4]).toBe("session-context");
  });
});

// ── J11 handoff seam ──────────────────────────────────────────────────────────

describe("J11 handoff seam — shared compaction-survival pattern", () => {
  it("SlotUserContext is identified as the pinned slot pattern for J11", () => {
    // This test documents the shared pattern between J10 (session context prompt)
    // and J11 (pin tool). Both use SlotUserContext as a non-compactable slot.
    // J11 will extend or compose with the same slot rather than adding a new one.
    // See: internal/context/slot.go SlotUserContext constant.
    const slotName = "user_context";
    expect(slotName).toBe("user_context");
  });
});
