import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/api", () => ({
  api: {
    getSettings: vi.fn().mockResolvedValue({ recover_mode: false }),
    listBookmarks: vi.fn().mockResolvedValue([]),
    toggleBookmark: vi.fn().mockResolvedValue({}),
    listUISlots: vi.fn().mockResolvedValue({}),
  },
}));

import { ChatTranscript } from "@/components/chat/ChatTranscript";
import type { Message } from "@/lib/types";
import { useAppStore } from "@/stores/useAppStore";
import { useChatStore } from "@/stores/useChatStore";

const SESSION_ID = "sess-child-work-test";

const STUB_MESSAGES: Message[] = [
  {
    id: "msg-user-1",
    session_id: SESSION_ID,
    agent_id: "agent-1",
    role: "user",
    content: "Review inbox items",
    envelope: null,
    metadata: "{}",
    created_at: new Date().toISOString(),
  },
];

function renderTranscript(props: Partial<Parameters<typeof ChatTranscript>[0]> = {}): ReturnType<typeof render> {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const Wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
  return render(
    <ChatTranscript
      messages={STUB_MESSAGES}
      isStreaming={true}
      streamingContent="Looking into the items..."
      sessionId={SESSION_ID}
      {...props}
    />,
    { wrapper: Wrapper },
  );
}

describe("ChatTranscript — child work visibility and intermediate output (CW-20260912-0148)", () => {
  beforeEach(() => {
    useAppStore.setState({ activeSessionId: SESSION_ID });
    useChatStore.getState().ensureSession(SESSION_ID);
  });

  afterEach(() => {
    cleanup();
    useChatStore.setState({ sessions: new Map() });
  });

  it("renders active subagent running card with detail when subagent_spawn is in flight", () => {
    useChatStore.getState().setStreaming(SESSION_ID, true);
    useChatStore.getState().appendStreamFinal(SESSION_ID, "I will review the inbox items now.");
    useChatStore.getState().addToolCall(SESSION_ID, {
      id: "tc-subagent-1",
      tool: "subagent_spawn",
      status: "running",
      detail: "reviewer: inspect inbox items",
    });

    renderTranscript();

    // Intermediate text is visible
    expect(screen.getByText("I will review the inbox items now.")).toBeDefined();

    // In-progress header is visible
    expect(screen.getByText("running subagent…")).toBeDefined();

    // Subagent active work strip is visible with detail
    expect(screen.getByText("Subagent")).toBeDefined();
    expect(screen.getByText("subagent_spawn")).toBeDefined();
    expect(screen.getByText("reviewer: inspect inbox items")).toBeDefined();
  });

  it("renders tool running card with tool name and detail when regular tool is in flight", () => {
    useChatStore.getState().setStreaming(SESSION_ID, true);
    useChatStore.getState().appendStreamFinal(SESSION_ID, "Checking files.");
    useChatStore.getState().addToolCall(SESSION_ID, {
      id: "tc-read-1",
      tool: "dev_read",
      status: "running",
      detail: "src/main.go",
    });

    renderTranscript();

    expect(screen.getByText("Checking files.")).toBeDefined();
    expect(screen.getByText("running tools…")).toBeDefined();
    expect(screen.getByText("Tool")).toBeDefined();
    expect(screen.getByText("dev_read")).toBeDefined();
    expect(screen.getByText("src/main.go")).toBeDefined();
  });

  it("keeps thinking indicator visible below intermediate output when no tool is running", () => {
    useChatStore.getState().setStreaming(SESSION_ID, true);
    useChatStore.getState().appendStreamFinal(SESSION_ID, "Working on your request.");

    renderTranscript();

    // Header says generating…
    expect(screen.getByText("generating…")).toBeDefined();

    // Content is visible
    expect(screen.getByText("Working on your request.")).toBeDefined();

    // Thinking indicator is present
    expect(screen.getByText("Thinking")).toBeDefined();
  });

  it("does not render running indicators when isStreaming is false", () => {
    useChatStore.getState().setStreaming(SESSION_ID, false);

    renderTranscript({ isStreaming: false });

    expect(screen.queryByText("running subagent…")).toBeNull();
    expect(screen.queryByText("generating…")).toBeNull();
    expect(screen.queryByText("Thinking")).toBeNull();
  });
});
