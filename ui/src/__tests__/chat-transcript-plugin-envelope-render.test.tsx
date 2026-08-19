/**
 * CW-20260510-0078 / Phase 9 follow-up — render-block invariant for
 * plugin_envelope items in the chat transcript.
 *
 * Why this exists:
 *
 * The `plugin_envelope` SSE event is the standalone-card delivery path
 * (separate from `Message.envelope`, which appends to a chat message).
 * Five backend producers ride this path WITHOUT setting `render_target`
 * (per the audit table in the implementer report at tracking_root):
 *
 *   - chat_loop_budget_soft_warning.go:81  (devmode budget warning)
 *   - chat_loop_terminated.go:71           (abnormal-exit failures)
 *   - envelope_emit.go:66 (ApprovalEmitterImpl.Emit) — feeds
 *       subagent-spawn-approval and elicitation-prompt
 *   - recovery_envelope_sink.go:90         (Phase 9 recovery cards)
 *   - stream.go:679 (plugin subprocess Deliver)
 *
 * For these, `render_target` is unset/empty, so `ChatTranscript` MUST
 * render them inline. A drop of the `pluginEnvelopes.map(...)` block in
 * the transcript silently makes them all invisible (BLG-20260414-010 +
 * the Phase 9 wave-2 regression report).
 *
 * The same render block also implements the panel-routed skip branch:
 * when `render_target` is set and `render_target_blocked` is unset, the
 * envelope renders into the panel inbox, NOT inline. This test locks
 * both branches.
 *
 * The wrapper div carries `data-plugin-envelope-id` for stable
 * targeting; it is a synchronous DOM element regardless of whether the
 * lazy-loaded envelope component (`InfoCard` etc.) has resolved yet, so
 * the assertion does not race the React.lazy import.
 *
 * Local verification (per the brief):
 *   - With the `pluginEnvelopes.map(...)` block in ChatTranscript.tsx,
 *     `npx vitest run` passes.
 *   - With that block deleted, both `renders inline ...` cases fail
 *     because no `[data-plugin-envelope-id]` element appears in the
 *     transcript. The skip-branch case keeps passing (correctly — the
 *     deletion drops everything, so the skip is also satisfied).
 *   - Restoring the block returns to all-green.
 */

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// ---- Mock the API surface BEFORE importing the component under test ----
// useSettings + the bookmarks query both fire as soon as ChatTranscript
// mounts; without these stubs they hit `fetch` and litter the console.
vi.mock("@/lib/api", () => ({
  api: {
    getSettings: vi.fn().mockResolvedValue({
      recover_mode: false,
    }),
    listBookmarks: vi.fn().mockResolvedValue([]),
    toggleBookmark: vi.fn().mockResolvedValue({}),
    // ChatMessage transitively pulls in usePluginSlots → api.listUISlots.
    // Stub it to keep the React Query layer quiet in test runs.
    listUISlots: vi.fn().mockResolvedValue({}),
  },
}));

import { ChatTranscript } from "@/components/chat/ChatTranscript";
import type { Envelope, Message, PluginEnvelopeItem } from "@/lib/types";
import { useAppStore } from "@/stores/useAppStore";
import { useChatStore } from "@/stores/useChatStore";

const SESSION_ID = "sess-render-test";

// Non-empty messages list — ChatTranscript has an early-return empty
// state when `messages.length === 0 && !isStreaming`, which would short-
// circuit the pluginEnvelopes.map block we want to lock.
const STUB_MESSAGES: Message[] = [
  {
    id: "msg-1",
    session_id: SESSION_ID,
    agent_id: "agent-1",
    role: "user",
    content: "hi",
    envelope: null,
    metadata: "{}",
    created_at: new Date().toISOString(),
  },
];

function makeEnvelope(overrides: Partial<Envelope> = {}): Envelope {
  return {
    kind: "envelope",
    version: 1,
    type: "info-card",
    id: "env-1",
    data: { title: "Hello", body: "from a plugin envelope", variant: "info" },
    ...overrides,
  };
}

function makeItem(envelope: Envelope, id = "penv-1"): PluginEnvelopeItem {
  return {
    id,
    pluginId: "test-plugin",
    envelope,
    receivedAt: Date.now(),
  };
}

function renderTranscript(): ReturnType<typeof render> {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const Wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
  return render(
    <ChatTranscript messages={STUB_MESSAGES} isStreaming={false} streamingContent="" />,
    { wrapper: Wrapper },
  );
}

beforeEach(() => {
  // Activate the session so usePluginEnvelopes(activeSessionId) sees our
  // slice. ChatTranscript reads useAppStore.activeSessionId.
  useAppStore.setState({ activeSessionId: SESSION_ID });
  useChatStore.setState({
    sessions: new Map(),
    chatToast: null,
    pendingJump: null,
    scrollToMessageId: null,
    activeStreams: new Map(),
    pendingTools: new Map(),
    cliActiveSessions: new Map(),
  });
  useChatStore.getState().ensureSession(SESSION_ID);
});

afterEach(() => {
  cleanup();
  useAppStore.setState({ activeSessionId: null });
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

describe("ChatTranscript — plugin_envelope inline render", () => {
  it("renders inline when render_target is unset (the 5-producer casualty path)", () => {
    // Wire shape matches what `useChat`'s plugin_envelope SSE handler
    // produces: an Envelope with no render_target. This is what
    // chat_loop_terminated, recovery_envelope_sink, ApprovalEmitterImpl,
    // chat_loop_budget_soft_warning, and the plugin subprocess Deliver
    // path all emit today.
    useChatStore.getState().addPluginEnvelope(SESSION_ID, makeItem(makeEnvelope()));

    const { container } = renderTranscript();

    // Wrapper div is synchronous — it does NOT depend on the lazy
    // envelope-component import resolving. Its presence proves the
    // pluginEnvelopes.map(...) block in ChatTranscript executed and
    // produced an inline DOM node.
    const wrappers = container.querySelectorAll("[data-plugin-envelope-id]");
    expect(wrappers).toHaveLength(1);
    expect(wrappers[0]?.getAttribute("data-plugin-envelope-id")).toBe("penv-1");
    expect(wrappers[0]?.getAttribute("data-plugin-id")).toBe("test-plugin");
  });

  it("renders inline when render_target is blocked (trust-gate fallback)", () => {
    // A2 (CW-20260428-0008): when the backend rejects an explicit
    // render_target at the trust gate, it sets render_target_blocked
    // and the FE falls back to inline render. The condition in the
    // map is `render_target && !render_target_blocked` → false here,
    // so the envelope MUST render inline.
    useChatStore.getState().addPluginEnvelope(
      SESSION_ID,
      makeItem(
        makeEnvelope({
          render_target: "untrusted-panel",
          render_target_blocked: "untrusted_plugin_panel",
        }),
        "penv-blocked",
      ),
    );

    const { container } = renderTranscript();

    const wrappers = container.querySelectorAll("[data-plugin-envelope-id]");
    expect(wrappers).toHaveLength(1);
    expect(wrappers[0]?.getAttribute("data-plugin-envelope-id")).toBe("penv-blocked");
  });

  it("does NOT render inline when render_target is set and not blocked (panel route)", () => {
    // The skip branch: `chat_route_dispatch.go:200` and others set
    // render_target via DefaultRenderTarget. ChatTranscript must NOT
    // render those inline — the drawer/panel slot owns them.
    useChatStore
      .getState()
      .addPluginEnvelope(
        SESSION_ID,
        makeItem(makeEnvelope({ render_target: "bottom_chat_drawer" }), "penv-routed"),
      );

    const { container } = renderTranscript();

    const wrappers = container.querySelectorAll("[data-plugin-envelope-id]");
    expect(wrappers).toHaveLength(0);
  });

  it("does NOT render inline when the explicit lane class is content", () => {
    useChatStore.getState().addPluginEnvelope(
      SESSION_ID,
      makeItem(
        makeEnvelope({
          type: "report-card",
          display_class: "content",
          data: { title: "Metrics", metrics: [] },
        }),
        "penv-content",
      ),
    );

    const { container } = renderTranscript();

    const wrappers = container.querySelectorAll("[data-plugin-envelope-id]");
    expect(wrappers).toHaveLength(0);
  });

  it("renders one wrapper per envelope item (multiple unrouted envelopes accumulate)", () => {
    // Lock the loop semantics: every store entry without render_target
    // should produce exactly one inline wrapper, in arrival order.
    const store = useChatStore.getState();
    store.addPluginEnvelope(SESSION_ID, makeItem(makeEnvelope({ id: "env-A" }), "penv-A"));
    store.addPluginEnvelope(SESSION_ID, makeItem(makeEnvelope({ id: "env-B" }), "penv-B"));

    const { container } = renderTranscript();

    const wrappers = container.querySelectorAll("[data-plugin-envelope-id]");
    expect(wrappers).toHaveLength(2);
    expect(wrappers[0]?.getAttribute("data-plugin-envelope-id")).toBe("penv-A");
    expect(wrappers[1]?.getAttribute("data-plugin-envelope-id")).toBe("penv-B");
  });
});
