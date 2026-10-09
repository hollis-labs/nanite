import { conversationAPI } from "@/lib/conversation-api";

// Confirmation binds the exact active/queued turns returned by the host. A
// changed set is surfaced for another explicit command, never auto-confirmed.
export async function clearConversation(
  sessionId: string,
  args: string,
  confirm: (message: string) => boolean,
): Promise<boolean> {
  const flag = args.trim();
  if (flag && flag !== "--keep-handoff") throw new Error("Use /clear or /clear --keep-handoff");
  const options = { keep_handoff: flag === "--keep-handoff" };
  let result = await conversationAPI.clearSession(sessionId, options);
  if ("active_turn_ids" in result) {
    if (
      !confirm(
        `Cancel ${result.active_turn_ids.length} active or queued turn(s) and clear the working conversation? The transcript, system context, and pins will remain.`,
      )
    )
      return false;
    result = await conversationAPI.clearSession(sessionId, {
      ...options,
      confirm_cancel: true,
      expected_turn_ids: result.active_turn_ids,
    });
    if ("active_turn_ids" in result)
      throw new Error("Active turns changed. Run /clear again to confirm the current turns.");
  }
  return true;
}
