import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const {
  mockSendMessage,
  mockGetMessagePage,
  mockGetSessionPluginEnvelopes,
  mockGetMessagesAround,
  mockCancelChatStream,
  mockGetSession,
} = vi.hoisted(() => ({
  mockSendMessage: vi.fn(),
  mockGetMessagePage: vi.fn(),
  mockGetSessionPluginEnvelopes: vi.fn(),
  mockGetMessagesAround: vi.fn(),
  mockCancelChatStream: vi.fn(),
  mockGetSession: vi.fn(),
}));

vi.hoisted(() => {
  const storage = new Map<string, string>();
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: {
      getItem: (key: string) => storage.get(key) ?? null,
      setItem: (key: string, value: string) => {
        storage.set(key, value);
      },
      removeItem: (key: string) => {
        storage.delete(key);
      },
      clear: () => {
        storage.clear();
      },
    },
  });
});

vi.mock("@/lib/api", () => ({
  api: {
    sendMessage: mockSendMessage,
    getMessagePage: mockGetMessagePage,
    getSessionPluginEnvelopes: mockGetSessionPluginEnvelopes,
    getMessagesAround: mockGetMessagesAround,
    cancelChatStream: mockCancelChatStream,
    getSession: mockGetSession,
  },
}));

import { useChat } from "@/hooks/useChat";
import { useChatStore } from "@/stores/useChatStore";

const SESSION_A = "sess-a";
const SESSION_B = "sess-b";
const ASSISTANT_ID = "msg-stream";

class FakeEventSource {
  static instances: FakeEventSource[] = [];

  closed = false;
  readonly url: string;

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }

  listeners = new Map<string, (event: MessageEvent) => void>();
  onerror: (() => void) | null = null;
  addEventListener(type: string, listener: (event: MessageEvent) => void) {
    this.listeners.set(type, listener);
  }
  emit(type: string, data: unknown) {
    this.listeners.get(type)?.({ data: JSON.stringify(data) } as MessageEvent);
  }
  removeEventListener() {}

  close() {
    this.closed = true;
  }
}

function lastStream() {
  const stream = FakeEventSource.instances.at(-1);
  if (!stream) throw new Error("No stream was opened");
  return stream;
}

let latestHook: ReturnType<typeof useChat> | null = null;

function HookHarness({ sessionId }: { sessionId: string }) {
  latestHook = useChat(sessionId);
  return null;
}

function renderHarness(sessionId: string) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const Wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
  return render(<HookHarness sessionId={sessionId} />, { wrapper: Wrapper });
}

async function flushAsync() {
  await act(async () => {
    await Promise.resolve();
  });
}

beforeEach(() => {
  FakeEventSource.instances = [];
  latestHook = null;
  vi.stubGlobal("EventSource", FakeEventSource);
  useChatStore.setState({
    sessions: new Map(),
    chatToast: null,
    pendingJump: null,
    scrollToMessageId: null,
    activeStreams: new Map(),
    pendingTools: new Map(),
    cliActiveSessions: new Map(),
  });

  mockSendMessage.mockResolvedValue({
    message_id: ASSISTANT_ID,
    stream_url: `/api/stream/${ASSISTANT_ID}`,
  });
  mockGetMessagePage.mockResolvedValue({ messages: [], total: 0, has_more: false });
  mockGetSessionPluginEnvelopes.mockResolvedValue([]);
  mockGetMessagesAround.mockResolvedValue({ messages: [], total: 0, has_more: false });
  mockCancelChatStream.mockResolvedValue(undefined);
  mockGetSession.mockImplementation(async (sessionId: string) => ({
    id: sessionId,
    messages: [],
    interrupted_turn: null,
  }));
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

describe("useChat session switching", () => {
  it("closes the prior SSE stream when the active session changes", async () => {
    const view = renderHarness(SESSION_A);
    await flushAsync();

    await act(async () => {
      await latestHook?.sendMessage("hello");
    });

    expect(FakeEventSource.instances).toHaveLength(1);
    expect(FakeEventSource.instances[0]?.closed).toBe(false);

    view.rerender(<HookHarness sessionId={SESSION_B} />);
    await flushAsync();

    expect(FakeEventSource.instances[0]?.closed).toBe(true);
  });
});

describe("stream recovery", () => {
  it("reattaches after leaving mid-turn, retaining content and skipping duplicate events", async () => {
    const view = renderHarness(SESSION_A);
    await flushAsync();
    await act(async () => {
      await latestHook?.sendMessage("hello");
    });
    const first = lastStream();
    act(() => first.emit("delta", { event_id: 1, content: "Hello " }));
    view.rerender(<HookHarness sessionId={SESSION_B} />);
    await flushAsync();
    expect(first.closed).toBe(true);
    mockGetSession.mockImplementation(async (id: string) => ({
      id,
      messages: [],
      active_message_id: id === SESSION_A ? ASSISTANT_ID : null,
    }));
    view.rerender(<HookHarness sessionId={SESSION_A} />);
    await flushAsync();
    const resumed = lastStream();
    expect(resumed.url).toBe(`/api/stream/${ASSISTANT_ID}?from=1`);
    act(() => {
      resumed.emit("delta", { event_id: 1, content: "Hello " });
      resumed.emit("delta", { event_id: 2, content: "again" });
      resumed.emit("tool_call", { event_id: 3, tool_id: "tool-a", tool: "lookup" });
      resumed.emit("tool_result", { event_id: 4, tool_id: "tool-a", summary: "found" });
    });
    expect(latestHook?.streamingContent).toBe("Hello again");
    expect(useChatStore.getState().sessions.get(SESSION_A)?.toolCalls).toEqual([
      expect.objectContaining({ id: "tool-a", status: "done", summary: "found" }),
    ]);
  });

  it("discovers an in-flight turn on a fresh mount and finalizes from the persisted full answer", async () => {
    mockGetSession.mockResolvedValue({ active_message_id: ASSISTANT_ID, messages: [] });
    renderHarness(SESSION_A);
    await flushAsync();
    const stream = lastStream();
    expect(stream.url).toBe(`/api/stream/${ASSISTANT_ID}`);
    const complete = {
      id: ASSISTANT_ID,
      session_id: SESSION_A,
      role: "assistant",
      content: "Full answer including the evicted prefix",
      created_at: "2026-09-12T01:00:00Z",
    };
    mockGetMessagePage.mockResolvedValue({ messages: [complete], total: 1, has_more: false });
    await act(async () => {
      stream.emit("delta", { event_id: 300, content: "only the tail" });
      stream.emit("stream_end", { event_id: 301 });
    });
    expect(latestHook?.messages).toEqual([complete]);
    expect(latestHook?.isStreaming).toBe(false);
  });

  it("keeps native transport errors reconnectable and stops on an explicit takeover", async () => {
    mockGetSession.mockResolvedValue({ active_message_id: ASSISTANT_ID, messages: [] });
    renderHarness(SESSION_A);
    await flushAsync();
    const stream = lastStream();
    await act(async () => {
      stream.listeners.get("error")?.({} as MessageEvent);
      stream.onerror?.();
    });
    expect(stream.closed).toBe(false);
    expect(latestHook?.isStreaming).toBe(true);
    act(() => stream.emit("session_takeover", {}));
    expect(stream.closed).toBe(true);
    expect(latestHook?.sessionTakeover).toBe(true);
    expect(latestHook?.isStreaming).toBe(false);
  });

  it("ignores a delayed mount response after a switch to another session", async () => {
    let resolvePage!: (page: unknown) => void;
    mockGetMessagePage.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolvePage = resolve;
        }),
    );
    mockGetSession.mockImplementation(async (id: string) => ({ messages: [], active_message_id: id === SESSION_A ? ASSISTANT_ID : null }));
    const view = renderHarness(SESSION_A);
    view.rerender(<HookHarness sessionId={SESSION_B} />);
    await flushAsync();
    await act(async () =>
      resolvePage({ messages: [{ id: "old", session_id: SESSION_A }], total: 1, has_more: false }),
    );
    expect(latestHook?.messages).toEqual([]);
    expect(latestHook?.messagesReady).toBe(true);
    expect(FakeEventSource.instances).toEqual([]);
  });
});
