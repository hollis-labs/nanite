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
import type { Envelope, Message } from "@/lib/types";
import { useChatStore } from "@/stores/useChatStore";

const SESSION_ID = "sess-stalled-stream";
const ASSISTANT_ID = "msg-assistant";

class FakeEventSource {
  static instances: FakeEventSource[] = [];

  listeners = new Map<string, Array<(event: MessageEvent) => void>>();
  onerror: ((this: EventSource, ev: Event) => unknown) | null = null;
  closed = false;
  readonly url: string;

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: EventListenerOrEventListenerObject) {
    const fn =
      typeof listener === "function"
        ? (listener as (event: MessageEvent) => void)
        : ((event: MessageEvent) => listener.handleEvent(event));
    const bucket = this.listeners.get(type) ?? [];
    bucket.push(fn);
    this.listeners.set(type, bucket);
  }

  removeEventListener(type: string, listener: EventListenerOrEventListenerObject) {
    const current = this.listeners.get(type) ?? [];
    const fn =
      typeof listener === "function"
        ? (listener as (event: MessageEvent) => void)
        : ((event: MessageEvent) => listener.handleEvent(event));
    this.listeners.set(
      type,
      current.filter((item) => item !== fn),
    );
  }

  close() {
    this.closed = true;
  }
}

let latestHook: ReturnType<typeof useChat> | null = null;

function HookHarness({ sessionId }: { sessionId: string }) {
  latestHook = useChat(sessionId);
  return null;
}

function renderHarness() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const Wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
  return render(<HookHarness sessionId={SESSION_ID} />, { wrapper: Wrapper });
}

async function flushAsync() {
  await act(async () => {
    await Promise.resolve();
  });
}

beforeEach(() => {
  vi.useFakeTimers();
  FakeEventSource.instances = [];
  latestHook = null;
  vi.stubGlobal("EventSource", FakeEventSource);
  useChatStore.setState({
    sessions: new Map(),
    chatToast: null,
    pendingJump: null,
    scrollToMessageId: null,
    autoSwitchSessionOverrides: {},
    activeStreams: new Map(),
    pendingTools: new Map(),
    cliActiveSessions: new Map(),
  });

  mockSendMessage.mockResolvedValue({ message_id: ASSISTANT_ID, stream_url: `/api/stream/${ASSISTANT_ID}` });
  mockGetMessagesAround.mockResolvedValue({ messages: [], total: 0, has_more: false });
  mockCancelChatStream.mockResolvedValue(undefined);
  // Default: backend reports no interrupted turn.
  mockGetSession.mockResolvedValue({ id: SESSION_ID, messages: [], interrupted_turn: null });
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.useRealTimers();
  vi.clearAllMocks();
});

describe("useChat stalled-stream reconciliation", () => {
  it("finalizes a quiet stream from persisted backend state and rehydrates plugin envelopes", async () => {
    const assistantMessage: Message = {
      id: ASSISTANT_ID,
      session_id: SESSION_ID,
      agent_id: "agent-1",
      role: "assistant",
      content: "done from backend",
      envelope: null,
      metadata: "{}",
      created_at: "2026-05-16T12:00:01Z",
    };
    const lateEnvelope: Envelope = {
      kind: "envelope",
      version: 1,
      type: "info-card",
      id: "env-late",
      data: { title: "Late card", body: "arrived via reconcile" },
    };

    mockGetMessagePage
      .mockResolvedValueOnce({ messages: [], total: 0, has_more: false })
      .mockResolvedValueOnce({ messages: [assistantMessage], total: 2, has_more: false });
    mockGetSessionPluginEnvelopes
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([lateEnvelope]);

    renderHarness();
    await flushAsync();

    await act(async () => {
      await latestHook?.sendMessage("hello");
    });

    expect(useChatStore.getState().sessions.get(SESSION_ID)?.isStreaming).toBe(true);
    expect(FakeEventSource.instances).toHaveLength(1);
    expect(FakeEventSource.instances[0]?.closed).toBe(false);

    await act(async () => {
      vi.advanceTimersByTime(5000);
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(useChatStore.getState().sessions.get(SESSION_ID)?.isStreaming).toBe(false);
    expect(FakeEventSource.instances[0]?.closed).toBe(true);
    expect(latestHook?.messages.map((msg) => msg.id)).toContain(ASSISTANT_ID);
    expect(useChatStore.getState().sessions.get(SESSION_ID)?.pluginEnvelopes).toHaveLength(1);
    expect(mockGetSessionPluginEnvelopes).toHaveBeenCalledTimes(2);
  });

  // CW-20260518-0084 — a service restart kills the in-flight turn's agent.
  // The assistant message never lands, so the stalled-stream reconcile finds
  // nothing to finalize. Instead of spinning forever, the reconcile probes
  // the session GET, sees `interrupted_turn`, and raises the indicator.
  it("surfaces interruptedTurn when a quiet stream never produces an assistant message and the backend reports a restart-killed turn", async () => {
    // The reconcile probe (getMessagePage) keeps returning no assistant message.
    mockGetMessagePage.mockResolvedValue({ messages: [], total: 1, has_more: false });
    mockGetSessionPluginEnvelopes.mockResolvedValue([]);
    // Backend session GET reports the interrupted turn.
    mockGetSession.mockResolvedValue({
      id: SESSION_ID,
      messages: [],
      interrupted_turn: {
        interrupted: true,
        reason: "service_restart",
        last_message_id: "msg-user-1",
        last_activity_at: "2026-05-18T12:00:00Z",
      },
    });

    renderHarness();
    await flushAsync();

    await act(async () => {
      await latestHook?.sendMessage("hello");
    });

    expect(useChatStore.getState().sessions.get(SESSION_ID)?.isStreaming).toBe(true);

    await act(async () => {
      vi.advanceTimersByTime(5000);
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
    });

    const slice = useChatStore.getState().sessions.get(SESSION_ID);
    expect(slice?.interruptedTurn).toBe(true);
    expect(slice?.isStreaming).toBe(false);
    expect(FakeEventSource.instances[0]?.closed).toBe(true);
  });
});
