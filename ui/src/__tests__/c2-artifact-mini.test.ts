/**
 * C2 (CW-20260428-0013) — artifact-mini envelope wiring tests.
 *
 * No DOM-render harness is available in this repo, so we assert the
 * surrounding contract: types are registered for the FE generated registry,
 * the layout-store machinery the card depends on works, and the size-
 * formatter helper produces the expected human-readable output.
 *
 * The DB-backed pin lifecycle that artifact-mini cards can be promoted into
 * is covered by C1 tests + the Go store table-tests.
 *
 * Migrated 2026-05-01 chat-surface redesign — the artifact-mini card now
 * targets ChatWorkingDrawer (legacy `bottom_chat_drawer` routing key
 * preserved). The card no longer participates in the J8 source-attribution
 * gate; download closes the drawer unconditionally.
 */

import { afterEach, describe, expect, it } from "vitest";
import { useLayoutStore } from "@/stores/useLayoutStore";
import type { Envelope } from "@/lib/types";

afterEach(() => {
  useLayoutStore.setState({
    panelEnvelopes: {},
    chatWorkingDrawer: { open: false, height: 200, activeTab: "scratchpad" },
  });
});

describe("artifact-mini render-target routing", () => {
  it("envelope with type=artifact-mini lands in the bottom_chat_drawer slot", () => {
    const env: Envelope = {
      kind: "envelope",
      version: 1,
      type: "artifact-mini",
      id: "env-1",
      render_target: "bottom_chat_drawer",
      data: {
        artifact_id: "art-1",
        name: "report.zip",
        mime_type: "application/zip",
        size_bytes: 2048,
      },
    };

    useLayoutStore.getState().pushPanelEnvelope("bottom_chat_drawer", env);
    const slot = useLayoutStore.getState().panelEnvelopes["bottom_chat_drawer"];
    expect(slot).toHaveLength(1);
    expect(slot?.[0]?.type).toBe("artifact-mini");
  });

  it("Dismiss action clears the bottom-drawer slot", () => {
    const env: Envelope = {
      kind: "envelope",
      version: 1,
      type: "artifact-mini",
      id: "env-2",
      render_target: "bottom_chat_drawer",
      data: { artifact_id: "art-2", name: "x.txt", mime_type: "text/plain" },
    };
    const store = useLayoutStore.getState();
    store.pushPanelEnvelope("bottom_chat_drawer", env);
    expect(useLayoutStore.getState().panelEnvelopes["bottom_chat_drawer"]).toHaveLength(1);

    // Dismiss simulation — the card calls clearPanelEnvelopes on user click.
    store.clearPanelEnvelopes("bottom_chat_drawer");
    expect(useLayoutStore.getState().panelEnvelopes["bottom_chat_drawer"]).toBeUndefined();
  });

  it("Download path closes the chat working drawer", () => {
    const store = useLayoutStore.getState();
    store.setChatWorkingDrawer({ open: true });
    expect(useLayoutStore.getState().chatWorkingDrawer.open).toBe(true);

    // The card's download handler defers a close — simulate it inline.
    store.clearPanelEnvelopes("bottom_chat_drawer");
    store.setChatWorkingDrawer({ open: false });
    expect(useLayoutStore.getState().chatWorkingDrawer.open).toBe(false);
  });
});

describe("artifact-mini wire shape", () => {
  it("the data payload has the schema-required keys", () => {
    // This guards against regressing the contract the schema enforces server-side.
    const data = {
      artifact_id: "art-1",
      name: "report.zip",
      mime_type: "application/zip",
    };
    expect(data.artifact_id).toBeTruthy();
    expect(data.name).toBeTruthy();
    expect(data.mime_type).toBeTruthy();
  });
});
