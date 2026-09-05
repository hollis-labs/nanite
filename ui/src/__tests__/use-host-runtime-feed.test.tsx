import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useHostRuntimeFeed } from "@/hooks/useHostRuntimeFeed";

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  readonly listeners = new Map<string, Set<EventListenerOrEventListenerObject>>();
  readonly url: string;
  onerror: ((event: Event) => unknown) | null = null;
  closed = false;

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

  emit(type: string, data: unknown) {
    const event = new MessageEvent(type, { data: JSON.stringify(data) });
    for (const listener of this.listeners.get(type) ?? []) {
      if (typeof listener === "function") listener(event);
      else listener.handleEvent(event);
    }
  }

  close() {
    this.closed = true;
  }
}

function Harness({ sessionID }: { sessionID: string }) {
  const state = useHostRuntimeFeed(sessionID);
  return <pre data-testid="state">{JSON.stringify(state)}</pre>;
}

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("useHostRuntimeFeed", () => {
  it("uses named durable events, tolerates reconnect, and closes only on session switch", () => {
    const rendered = render(<Harness sessionID="session-a" />);
    const first = FakeEventSource.instances[0];
    if (!first) throw new Error("runtime EventSource was not created");
    expect(first.url).toBe("/api/sessions/session-a/runtime-events");

    const ready = {
      schema_version: "host_runtime.v1",
      cursor: 12,
      session_id: "session-a",
      runtime_run_id: "run-a",
      runtime_generation: 1,
      source_event_id: "source-ready",
      source_sequence: 1,
      kind: "session.ready",
      occurred_at: "2026-09-05T12:00:00Z",
      source: { channel: "jsonrpc" },
      process: { provider: "codex" },
      payload: { state: "ready" },
      payload_visibility: "public_metadata",
    };
    act(() => first.emit("host_runtime.v1", ready));
    expect(screen.getByTestId("state").textContent).toContain('"lastCursor":12');
    act(() => first.emit("host_runtime.v1", ready));
    expect(screen.getByTestId("state").textContent?.match(/source-ready/g)).toHaveLength(2);

    act(() => first.onerror?.(new Event("error")));
    expect(first.closed).toBe(false);

    rendered.rerender(<Harness sessionID="session-b" />);
    expect(first.closed).toBe(true);
    expect(FakeEventSource.instances[1]?.url).toBe("/api/sessions/session-b/runtime-events");
    expect(screen.getByTestId("state").textContent).toContain('"lastCursor":0');
  });
});
