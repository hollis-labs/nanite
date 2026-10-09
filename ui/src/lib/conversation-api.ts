export const conversationAPI = {
  clearSession: async (
    sessionId: string,
    options: {
      keep_handoff?: boolean;
      confirm_cancel?: boolean;
      expected_turn_ids?: string[];
    } = {},
  ): Promise<{ message_id: string; keep_handoff: boolean } | { active_turn_ids: string[] }> => {
    const res = await fetch(`/api/sessions/${encodeURIComponent(sessionId)}/clear`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(options),
    });
    if (res.status === 409) {
      const body = await res.json();
      if (body.code === "clear_confirmation_required" && Array.isArray(body.active_turn_ids)) {
        return { active_turn_ids: body.active_turn_ids };
      }
    }
    if (!res.ok) throw new Error(`Failed to clear session: ${res.status}`);
    return res.json();
  },
};
