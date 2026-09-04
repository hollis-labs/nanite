import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useWorkflowEvents } from "@/hooks/useWorkflows";

class FakeEventSource {
  static instances: FakeEventSource[] = [];

  onmessage: (() => void) | null = null;
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

  emit(type: string) {
    for (const listener of this.listeners.get(type) ?? []) {
      if (typeof listener === "function") {
        listener(new Event(type));
      } else {
        listener.handleEvent(new Event(type));
      }
    }
  }

  close() {
    this.closed = true;
  }
}

function WorkflowEventHarness() {
  useWorkflowEvents();
  return null;
}

function renderHarness(queryClient: QueryClient) {
  const Wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
  return render(<WorkflowEventHarness />, { wrapper: Wrapper });
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

describe("useWorkflowEvents", () => {
  it("invalidates workflow queries for named durable SSE events", async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue();

    const rendered = renderHarness(queryClient);
    const source = FakeEventSource.instances[0];
    expect(source?.url).toBe("/api/workflows/events");

    await act(async () => {
      source?.emit("step.completed");
    });

    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["workflow-runs"] });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["workflow-run"] });

    rendered.unmount();
    expect(source?.closed).toBe(true);
    expect([...(source?.listeners.values() ?? [])].every((listeners) => listeners.size === 0)).toBe(
      true,
    );
  });
});
