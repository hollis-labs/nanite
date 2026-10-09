import { afterEach, describe, expect, it, vi } from "vitest";
import { clearConversation } from "@/lib/clearConversation";
import { conversationAPI } from "@/lib/conversation-api";

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("clear working conversation", () => {
  it("drops handoff without confirmation when idle", async () => {
    const clear = vi
      .spyOn(conversationAPI, "clearSession")
      .mockResolvedValue({ message_id: "boundary", keep_handoff: false });
    const confirm = vi.fn();
    expect(await clearConversation("session", "", confirm)).toBe(true);
    expect(clear).toHaveBeenCalledWith("session", { keep_handoff: false });
    expect(confirm).not.toHaveBeenCalled();
  });
  it("does not cancel when confirmation is declined", async () => {
    const clear = vi
      .spyOn(conversationAPI, "clearSession")
      .mockResolvedValue({ active_turn_ids: ["turn"] });
    expect(await clearConversation("session", "", () => false)).toBe(false);
    expect(clear).toHaveBeenCalledTimes(1);
  });
  it("binds confirmed exact IDs and keep-handoff to the retry", async () => {
    const clear = vi
      .spyOn(conversationAPI, "clearSession")
      .mockResolvedValueOnce({ active_turn_ids: ["active", "queued"] })
      .mockResolvedValueOnce({ message_id: "boundary", keep_handoff: true });
    expect(await clearConversation("session", "--keep-handoff", () => true)).toBe(true);
    expect(clear).toHaveBeenLastCalledWith("session", {
      keep_handoff: true,
      confirm_cancel: true,
      expected_turn_ids: ["active", "queued"],
    });
  });
  it("requires a new command if active turns change", async () => {
    const clear = vi
      .spyOn(conversationAPI, "clearSession")
      .mockResolvedValueOnce({ active_turn_ids: ["old"] })
      .mockResolvedValueOnce({ active_turn_ids: ["new"] });
    await expect(clearConversation("session", "", () => true)).rejects.toThrow(
      "Active turns changed",
    );
    expect(clear).toHaveBeenCalledTimes(2);
  });
  it("refuses unsupported command flags before requests", async () => {
    const clear = vi.spyOn(conversationAPI, "clearSession");
    await expect(clearConversation("session", "--undo", () => true)).rejects.toThrow("Use /clear");
    expect(clear).not.toHaveBeenCalled();
  });
  it("uses the real HTTP wire for typed confirmation then confirmed cancellation", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ code: "clear_confirmation_required", active_turn_ids: ["turn"] }),
          { status: 409 },
        ),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ message_id: "marker", keep_handoff: false })),
      );
    vi.stubGlobal("fetch", fetch);
    expect(await clearConversation("session/encoded", "", () => true)).toBe(true);
    expect(fetch).toHaveBeenLastCalledWith("/api/sessions/session%2Fencoded/clear", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        keep_handoff: false,
        confirm_cancel: true,
        expected_turn_ids: ["turn"],
      }),
    });
  });
  it("reports a refused reset without claiming success", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("failed", { status: 500 })));
    await expect(clearConversation("session", "", () => true)).rejects.toThrow(
      "Failed to clear session",
    );
  });
});
