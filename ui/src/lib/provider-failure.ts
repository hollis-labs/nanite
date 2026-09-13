import type { ChatError, Message } from "@/lib/types";

export function messageProviderFailure(message: Message): { error: ChatError; partial: boolean } | null {
  if (message.role !== "assistant") return null;
  try {
    const meta = JSON.parse(message.metadata || "{}");
    const failure = meta.provider_error;
    if (!failure || typeof failure.message !== "string" ||
      !["provider_error", "rate_limit"].includes(failure.code)) return null;
    return {
      error: { ...failure, id: message.id, timestamp: failure.timestamp || message.created_at },
      partial: meta.partial_output === true,
    };
  } catch {
    return null;
  }
}
