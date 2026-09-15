import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useUnifiedEvents } from "@/hooks/useUnifiedEvents";
import { useChatStore } from "@/stores/useChatStore";

class FakeEventSource {
  static instances: FakeEventSource[] = [];

  onmessage: ((e: MessageEvent) => void) | null = null;
  onerror: (() => void) | null = null;
  readonly listeners = new Map<string, Set<EventListenerOrEventListenerObject>>();
  closed = false;
  readonly url: string;

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: EventListenerOrEventListenerObject) {
    const listeners = this.listeners.get(type) ?? new Set();
    listeners.add(listener);
    this.listeners.set(type, listeners);
  }

  removeEventListener(type: string, listener: EventListenerOrEventListenerObject) {
    this.listeners.get(type)?.delete(listener);
  }

  emit(type: string, data: unknown = {}) {
    const event = new MessageEvent(type, {
      data: typeof data === "string" ? data : JSON.stringify(data),
    });
    for (const listener of this.listeners.get(type) ?? []) {
      if (typeof listener === "function") {
        listener(event);
      } else {
        listener.handleEvent(event);
      }
    }
  }

  close() {
    this.closed = true;
  }
}

function UnifiedEventsHarness() {
  useUnifiedEvents();
  return null;
}

function renderHarness(queryClient: QueryClient) {
  const Wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
  return render(<UnifiedEventsHarness />, { wrapper: Wrapper });
}

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("useUnifiedEvents", () => {
  it("connects to /api/events and handles presence and plugin lifecycle events", async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue();

    const rendered = renderHarness(queryClient);
    const source = FakeEventSource.instances[0];
    expect(source?.url).toBe("/api/events");

    // 1. Presence event (stream_start)
    await act(async () => {
      source?.emit("presence", {
        type: "stream_start",
        session_id: "sess-123",
        agent_id: "agent-a",
        timestamp: "2026-09-15T00:00:00Z",
      });
    });
    expect(useChatStore.getState().activeStreams.has("sess-123")).toBe(true);

    // 2. Presence event (work_changed) -> invalidates todos, plans, workflow-runs
    await act(async () => {
      source?.emit("presence", {
        type: "work_changed",
        session_id: "sess-123",
        timestamp: "2026-09-15T00:00:01Z",
      });
    });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["todos"] });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["plans"] });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["workflow-runs"] });

    // 3. Plugin event -> invalidates plugin registries
    await act(async () => {
      source?.emit("plugin", {
        type: "plugin.installed",
        plugin_id: "sample-plugin",
      });
    });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["plugins", "registry"] });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["plugins-managed"] });

    // 4. Clean unmount
    rendered.unmount();
    expect(source?.closed).toBe(true);
    expect([...(source?.listeners.values() ?? [])].every((listeners) => listeners.size === 0)).toBe(
      true,
    );
  });
});
