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

  addEventListener() {}
  removeEventListener() {}

  close() {
    this.closed = true;
  }
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

  mockSendMessage.mockResolvedValue({ message_id: ASSISTANT_ID, stream_url: `/api/stream/${ASSISTANT_ID}` });
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
