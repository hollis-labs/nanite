import { CheckCircle2, Loader2, X } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { useAppStore } from "@/stores/useAppStore";
import { useChatStore } from "@/stores/useChatStore";

/**
 * Phase 9 (CW-20260510-0017 / W2A) — recovery broker cancel-retry affordance.
 *
 * Rendered inside an info-card envelope footer when the wrap-level
 * `cancel_token` is set. POSTs the token verbatim to
 * `POST /api/sessions/{sessionID}/recovery/cancel`. The BE broker
 * validates session-binding and returns:
 *   - 200 → retry canceled; render a terminal "canceled" state.
 *   - 404 → token unknown / expired / cross-session → terminal "no
 *     longer cancellable" state (transitive failure, not actionable).
 *   - 400 / network → transient toast; button stays interactive so the
 *     user can retry.
 *
 * The session id binding is read from `useAppStore.activeSessionId`,
 * matching the SSE stream the recovery envelope rode in on. We do NOT
 * thread it as a prop because the recovery sink is recovery-only and
 * the session-context-from-store pattern matches the rest of the chat
 * surface (ChatTranscript, ToolWarningBanner, etc.).
 */

export interface RecoveryCancelButtonProps {
  /** Opaque hex cancel token from the envelope wrap. */
  token: string;
}

type CancelState = "idle" | "pending" | "canceled" | "stale";

export function RecoveryCancelButton({ token }: RecoveryCancelButtonProps) {
  const [state, setState] = useState<CancelState>("idle");
  const sessionId = useAppStore((s) => s.activeSessionId);
  const showChatToast = useChatStore((s) => s.showChatToast);

  const handleClick = async () => {
    if (state !== "idle") return;
    if (!sessionId) {
      showChatToast("Cannot cancel retry: no active session.", "info");
      return;
    }
    setState("pending");
    try {
      const result = await api.cancelRecoveryRetry(sessionId, token);
      if (result === "canceled") {
        setState("canceled");
      } else if (result === "stale") {
        setState("stale");
      } else {
        // Transient failure — keep button interactive.
        setState("idle");
        showChatToast("Could not cancel retry. Try again.", "info");
      }
    } catch {
      setState("idle");
      showChatToast("Network error canceling retry.", "info");
    }
  };

  if (state === "canceled") {
    return (
      <span
        data-recovery-cancel="canceled"
        className="inline-flex items-center gap-1.5 font-mono text-[11px] text-fg-muted"
      >
        <CheckCircle2 className="h-3.5 w-3.5 text-success" />
        Retry canceled
      </span>
    );
  }

  if (state === "stale") {
    return (
      <span
        data-recovery-cancel="stale"
        className="inline-flex items-center gap-1.5 font-mono text-[11px] text-fg-muted"
      >
        Retry no longer cancellable
      </span>
    );
  }

  return (
    <Button
      data-recovery-cancel="action"
      type="button"
      size="sm"
      variant="outline"
      disabled={state === "pending"}
      onClick={handleClick}
    >
      {state === "pending" ? (
        <Loader2 className="h-3.5 w-3.5 animate-spin" />
      ) : (
        <X className="h-3.5 w-3.5" />
      )}
      Cancel retry
    </Button>
  );
}
