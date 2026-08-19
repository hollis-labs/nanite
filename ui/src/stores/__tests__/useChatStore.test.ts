/**
 * G-FE-SINGLETON: cross-session isolation tests for useChatStore.
 *
 * Repros the user-reported bleed bug at the store level: writing to session A
 * must not change any field on session B's slice. Pre-refactor, every key in
 * the table below was a global field and would change for both sessions on a
 * single setX call. Post-refactor, each is in `Map<sessionID, slice>` and the
 * tests below assert the isolation.
 */

import { afterEach, describe, expect, it } from "vitest";
import {
  EMPTY_CHAT_SESSION_STATE,
  MAX_RETAINED_SESSIONS,
  emptyChatSessionState,
} from "@/stores/chatSessionState";
import { useChatStore } from "@/stores/useChatStore";
import type { ChatError, ToolCall } from "@/lib/types";

const SESSION_A = "sess-A";
const SESSION_B = "sess-B";

afterEach(() => {
  useChatStore.setState({
    sessions: new Map(),
    chatToast: null,
    pendingJump: null,
    scrollToMessageId: null,
    activeStreams: new Map(),
    pendingTools: new Map(),
    cliActiveSessions: new Map(),
  });
});

function err(id: string): ChatError {
  return {
    id,
    code: "internal_error",
    message: `error ${id}`,
    timestamp: new Date().toISOString(),
  };
}

function toolCall(id: string, tool = "read_file"): ToolCall {
  return { id, tool, status: "running" };
}

describe("useChatStore lifecycle", () => {
  it("ensureSession creates an empty slice with default dials", () => {
    useChatStore.getState().ensureSession(SESSION_A);
    const slice = useChatStore.getState().sessions.get(SESSION_A);
    expect(slice).toBeDefined();
    expect(slice?.activeModel).toBe(EMPTY_CHAT_SESSION_STATE.activeModel);
    expect(slice?.activeEffort).toBe(EMPTY_CHAT_SESSION_STATE.activeEffort);
    expect(slice?.isStreaming).toBe(false);
  });

  it("ensureSession is idempotent (does not overwrite an existing slice)", () => {
    const store = useChatStore.getState();
    store.ensureSession(SESSION_A);
    store.setActiveModel(SESSION_A, "claude-opus-4-7");
    store.ensureSession(SESSION_A);
    const slice = useChatStore.getState().sessions.get(SESSION_A);
    expect(slice?.activeModel).toBe("claude-opus-4-7");
  });

  it("removeSession drops the slice", () => {
    const store = useChatStore.getState();
    store.ensureSession(SESSION_A);
    expect(useChatStore.getState().sessions.has(SESSION_A)).toBe(true);
    store.removeSession(SESSION_A);
    expect(useChatStore.getState().sessions.has(SESSION_A)).toBe(false);
  });

  it("clearStreaming resets only streaming/status fields, preserving other slice state", () => {
    const store = useChatStore.getState();
    store.ensureSession(SESSION_A);
    store.setStreaming(SESSION_A, true);
    store.appendStreamFinal(SESSION_A, "hello");
    store.appendStreamThinking(SESSION_A, "thinking…");
    store.setStatusMessage(SESSION_A, "retrying");
    store.addChatError(SESSION_A, err("e1"));
    store.addToolCall(SESSION_A, toolCall("t1"));

    store.clearStreaming(SESSION_A);

    const slice = useChatStore.getState().sessions.get(SESSION_A);
    expect(slice?.isStreaming).toBe(false);
    expect(slice?.streamingContent).toBe("");
    expect(slice?.streamingFinal).toBe("");
    expect(slice?.streamingThinking).toBe("");
    expect(slice?.statusMessage).toBe(null);
    // Errors + tool calls survive — they are not streaming state.
    expect(slice?.chatErrors).toHaveLength(1);
    expect(slice?.toolCalls).toHaveLength(1);
  });
});

describe("useChatStore LRU eviction", () => {
  it("evicts the least-recently-active slice when the cap is exceeded", () => {
    const store = useChatStore.getState();
    // Pre-fill MAX_RETAINED_SESSIONS slices with monotonically increasing
    // lastActivityAt so the eviction order is deterministic.
    const seeded = new Map<string, ReturnType<typeof emptyChatSessionState>>();
    for (let i = 0; i < MAX_RETAINED_SESSIONS; i++) {
      seeded.set(`s${i}`, { ...emptyChatSessionState(), lastActivityAt: i + 1 });
    }
    useChatStore.setState({ sessions: seeded });

    // Adding the (cap+1)th session must evict s0 (oldest lastActivityAt).
    store.ensureSession("s-new");
    const sessions = useChatStore.getState().sessions;
    expect(sessions.size).toBe(MAX_RETAINED_SESSIONS);
    expect(sessions.has("s0")).toBe(false);
    expect(sessions.has("s-new")).toBe(true);
  });

  it("does not evict on writer mutations (only ensureSession enforces the cap)", () => {
    const store = useChatStore.getState();
    // Overflow the cap deliberately via writers — the cap is "soft" until
    // an explicit ensureSession call enforces it. Documents the design
    // decision that mid-flow writes never evict an actively-mounted slice.
    const seeded = new Map<string, ReturnType<typeof emptyChatSessionState>>();
    for (let i = 0; i < MAX_RETAINED_SESSIONS + 5; i++) {
      seeded.set(`s${i}`, { ...emptyChatSessionState(), lastActivityAt: i + 1 });
    }
    useChatStore.setState({ sessions: seeded });
    store.setStreaming("s-extra", true);
    expect(useChatStore.getState().sessions.size).toBe(MAX_RETAINED_SESSIONS + 6);
  });
});

describe("useChatStore cross-session isolation (G-FE-SINGLETON repro)", () => {
  it("setStreaming on A does not flip B's isStreaming", () => {
    const store = useChatStore.getState();
    store.ensureSession(SESSION_A);
    store.ensureSession(SESSION_B);
    store.setStreaming(SESSION_A, true);
    expect(useChatStore.getState().sessions.get(SESSION_A)?.isStreaming).toBe(true);
    expect(useChatStore.getState().sessions.get(SESSION_B)?.isStreaming).toBe(false);
  });

  it("streaming text appended to A does not appear on B", () => {
    const store = useChatStore.getState();
    store.appendStreamFinal(SESSION_A, "hello ");
    store.appendStreamFinal(SESSION_A, "world");
    store.appendStreamNarration(SESSION_A, "narration");
    store.appendStreamThinking(SESSION_A, "thinking");

    const a = useChatStore.getState().sessions.get(SESSION_A);
    const b = useChatStore.getState().sessions.get(SESSION_B) ?? EMPTY_CHAT_SESSION_STATE;
    expect(a?.streamingFinal).toBe("hello world");
    expect(a?.streamingContent).toBe("hello world");
    expect(a?.streamingNarration).toBe("narration");
    expect(a?.streamingThinking).toBe("thinking");
    expect(b.streamingFinal).toBe("");
    expect(b.streamingContent).toBe("");
    expect(b.streamingNarration).toBe("");
    expect(b.streamingThinking).toBe("");
  });

  it("circuit-open / takeover banners on A do not flip B", () => {
    const store = useChatStore.getState();
    store.setCircuitOpen(SESSION_A, true);
    store.setSessionTakeover(SESSION_A, true);

    const b = useChatStore.getState().sessions.get(SESSION_B) ?? EMPTY_CHAT_SESSION_STATE;
    expect(b.circuitOpen).toBe(false);
    expect(b.sessionTakeover).toBe(false);
  });

  it("chat errors added to A do not appear on B", () => {
    const store = useChatStore.getState();
    store.addChatError(SESSION_A, err("e1"));
    store.addChatError(SESSION_A, err("e2"));
    expect(useChatStore.getState().sessions.get(SESSION_A)?.chatErrors).toHaveLength(2);
    const b = useChatStore.getState().sessions.get(SESSION_B);
    expect(b?.chatErrors ?? []).toHaveLength(0);
  });

  it("dismissChatError marks only the named error in the named session", () => {
    const store = useChatStore.getState();
    store.addChatError(SESSION_A, err("e1"));
    store.addChatError(SESSION_A, err("e2"));
    store.addChatError(SESSION_B, err("e1"));

    store.dismissChatError(SESSION_A, "e1");

    const a = useChatStore.getState().sessions.get(SESSION_A);
    const b = useChatStore.getState().sessions.get(SESSION_B);
    expect(a?.chatErrors.find((e) => e.id === "e1")?.dismissed).toBe(true);
    expect(a?.chatErrors.find((e) => e.id === "e2")?.dismissed).toBeUndefined();
    expect(b?.chatErrors.find((e) => e.id === "e1")?.dismissed).toBeUndefined();
  });

  it("tool calls added to A do not appear on B", () => {
    const store = useChatStore.getState();
    store.addToolCall(SESSION_A, toolCall("t1"));
    store.addToolCall(SESSION_A, toolCall("t2"));
    expect(useChatStore.getState().sessions.get(SESSION_A)?.toolCalls).toHaveLength(2);
    const b = useChatStore.getState().sessions.get(SESSION_B);
    expect(b?.toolCalls ?? []).toHaveLength(0);
  });

  it("addToolCall upserts by id within a slice (CW-20260419-0014 defense-in-depth)", () => {
    const store = useChatStore.getState();
    store.addToolCall(SESSION_A, { id: "t1", tool: "read_file", status: "running" });
    store.addToolCall(SESSION_A, { id: "t1", tool: "read_file", status: "running", detail: "/etc/hosts" });
    const calls = useChatStore.getState().sessions.get(SESSION_A)?.toolCalls;
    expect(calls).toHaveLength(1);
    expect(calls?.[0]?.detail).toBe("/etc/hosts");
  });

  it("dial settings (model/effort) are independent per session", () => {
    const store = useChatStore.getState();
    store.setActiveModel(SESSION_A, "claude-opus-4-7");
    store.setActiveEffort(SESSION_A, "high");

    store.setActiveModel(SESSION_B, "claude-haiku-4-5");
    store.setActiveEffort(SESSION_B, "low");

    const a = useChatStore.getState().sessions.get(SESSION_A);
    const b = useChatStore.getState().sessions.get(SESSION_B);
    expect(a?.activeModel).toBe("claude-opus-4-7");
    expect(a?.activeEffort).toBe("high");
    expect(b?.activeModel).toBe("claude-haiku-4-5");
    expect(b?.activeEffort).toBe("low");
  });

  it("composer drafts are independent per session", () => {
    const store = useChatStore.getState();
    store.setComposerDraft(SESSION_A, "draft for A");
    store.setComposerDraft(SESSION_B, "draft for B");

    expect(useChatStore.getState().sessions.get(SESSION_A)?.composerDraft).toBe("draft for A");
    expect(useChatStore.getState().sessions.get(SESSION_B)?.composerDraft).toBe("draft for B");

    store.clearComposerDraft(SESSION_A);

    expect(useChatStore.getState().sessions.get(SESSION_A)?.composerDraft).toBe("");
    expect(useChatStore.getState().sessions.get(SESSION_B)?.composerDraft).toBe("draft for B");
  });

  it("repros the original symptom: switching mid-stream from A to B leaves B clean", () => {
    // 1. User sends in session A. The hook fires the same sequence the SSE
    //    handlers fire today.
    const store = useChatStore.getState();
    store.ensureSession(SESSION_A);
    store.ensureSession(SESSION_B);
    store.setStreaming(SESSION_A, true);
    store.appendStreamFinal(SESSION_A, "Working on it…");
    store.setStatusMessage(SESSION_A, "retrying");
    store.addToolCall(SESSION_A, toolCall("t1", "dev_bash"));

    // 2. The user switches to session B mid-stream. Pre-refactor, B's view
    //    inherits A's globals; post-refactor, B's slice is untouched.
    const b = useChatStore.getState().sessions.get(SESSION_B);
    expect(b?.isStreaming).toBe(false);
    expect(b?.streamingFinal).toBe("");
    expect(b?.streamingContent).toBe("");
    expect(b?.statusMessage).toBe(null);
    expect(b?.toolCalls).toHaveLength(0);

    // 3. Stream completes on A; B is still untouched.
    store.clearStreaming(SESSION_A);
    const bAfter = useChatStore.getState().sessions.get(SESSION_B);
    expect(bAfter?.isStreaming).toBe(false);
    expect(bAfter?.toolCalls).toHaveLength(0);
  });
});

describe("useChatStore retention pruning", () => {
  it("pruneToolCallRetention clears stale tool-call buffers but preserves slice", () => {
    const store = useChatStore.getState();
    const oldSlice = {
      ...emptyChatSessionState(),
      toolCalls: [toolCall("old")],
      toolCallsLastActivity: Date.now() - 60 * 60 * 1000, // 1h ago
    };
    const freshSlice = {
      ...emptyChatSessionState(),
      toolCalls: [toolCall("fresh")],
      toolCallsLastActivity: Date.now(),
    };
    useChatStore.setState({
      sessions: new Map([
        ["old", oldSlice],
        ["fresh", freshSlice],
      ]),
    });

    store.pruneToolCallRetention(15);

    const sessions = useChatStore.getState().sessions;
    expect(sessions.has("old")).toBe(true); // slice itself preserved
    expect(sessions.get("old")?.toolCalls).toHaveLength(0);
    expect(sessions.get("fresh")?.toolCalls).toHaveLength(1);
  });

  it("pruneToolCallRetention with negative retention is a no-op", () => {
    const store = useChatStore.getState();
    store.addToolCall(SESSION_A, toolCall("t1"));
    store.pruneToolCallRetention(-1);
    expect(useChatStore.getState().sessions.get(SESSION_A)?.toolCalls).toHaveLength(1);
  });
});
