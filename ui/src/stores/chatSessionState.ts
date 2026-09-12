import type { TranscriptPosition } from "@/lib/transcript-position";
import type {
  ChatError,
  PendingApproval,
  PluginEnvelopeItem,
  ToolCall,
  ToolWarning,
} from "@/lib/types";

/**
 * G-FE-SINGLETON: per-session slice for chat state.
 *
 * One slice per `sessionID` lives inside `useChatStore.sessions`. Replaces the
 * previous singleton fields (`isStreaming`, `streamingContent`, `chatErrors`,
 * `activeMode/Model/Effort`, etc.) that bled across concurrent sessions.
 */
export interface ChatSessionState {
  // Streaming
  isStreaming: boolean;
  streamingContent: string;
  streamingNarration: string;
  streamingFinal: string;
  streamingThinking: string;
  streamMessageId: string | null;
  streamCursor: number;

  // Status / banners
  statusMessage: string | null;
  circuitOpen: boolean;
  sessionTakeover: boolean;
  textOnlyMode: boolean;
  /**
   * CW-20260518-0084: true when an in-flight turn's backend agent is gone —
   * a service restart (deploy/reload) killed the turn mid-generation. The
   * backend reports this via the `interrupted_turn` field on the session GET
   * response; the FE also raises it locally when a stalled stream fails to
   * reconcile against any persisted assistant message. Cleared on the next
   * user-message turn (send-to-resume).
   */
  interruptedTurn: boolean;

  // Errors / approvals / warnings
  chatErrors: ChatError[];
  pendingApprovals: PendingApproval[];
  toolWarnings: ToolWarning[];

  // Per-session dials
  activeModel: string;
  activeEffort: string;

  // Composer
  composerDraft: string;
  transcriptPosition: TranscriptPosition | null;

  // Already session-keyed; folded into the slice for shape consistency
  toolCalls: ToolCall[];
  pluginEnvelopes: PluginEnvelopeItem[];
  toolCallsLastActivity: number;
  pluginEnvelopesLastActivity: number;

  // LRU bookkeeping
  lastActivityAt: number;
}

export const DEFAULT_ACTIVE_MODEL = "claude-sonnet-4-20250514";
export const DEFAULT_ACTIVE_EFFORT = "normal";

export function emptyChatSessionState(now: number = Date.now()): ChatSessionState {
  return {
    isStreaming: false,
    streamingContent: "",
    streamingNarration: "",
    streamingFinal: "",
    streamingThinking: "",
    streamMessageId: null,
    streamCursor: 0,
    statusMessage: null,
    circuitOpen: false,
    sessionTakeover: false,
    textOnlyMode: false,
    interruptedTurn: false,
    chatErrors: [],
    pendingApprovals: [],
    toolWarnings: [],
    activeModel: DEFAULT_ACTIVE_MODEL,
    activeEffort: DEFAULT_ACTIVE_EFFORT,
    composerDraft: "",
    transcriptPosition: null,
    toolCalls: [],
    pluginEnvelopes: [],
    toolCallsLastActivity: now,
    pluginEnvelopesLastActivity: now,
    lastActivityAt: now,
  };
}

/**
 * Frozen empty slice returned by selectors when the queried session is not
 * present in the map. Lets components consume `useActiveChatSession()` etc.
 * without null checks. Reference-stable to avoid unnecessary re-renders.
 */
export const EMPTY_CHAT_SESSION_STATE: Readonly<ChatSessionState> = Object.freeze({
  ...emptyChatSessionState(0),
});

/**
 * Soft cap on retained per-session slices. Beyond this we LRU-evict on
 * `ensureSession` so a long-running browser tab does not accumulate state for
 * every session it has ever attached. Chat history is loaded from the backend
 * on demand, so eviction does not lose user data — it only clears in-memory
 * UI state for the evicted session.
 */
export const MAX_RETAINED_SESSIONS = 20;
