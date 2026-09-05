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
    act(() =>
      first.emit("host_runtime.head.v1", {
        schema_version: "host_runtime.head.v1",
        session_id: "session-a",
        latest_cursor: 12,
        pruned_through_cursor: 0,
        retention_dropped: 0,
        runtime_generation_floor: 1,
        current_runtime_run_id: "run-a",
      }),
    );
    act(() => first.emit("host_runtime.v1", ready));
    expect(screen.getByTestId("state").textContent).toContain('"lastCursor":12');
    act(() => first.emit("host_runtime.v1", ready));
    expect(screen.getByTestId("state").textContent?.match(/source-ready/g)).toHaveLength(2);

    // A reconnect gap installs the durable successor floor before replaying
    // retained rows. The only retained predecessor event cannot reclaim it.
    act(() =>
      first.emit("host_runtime.head.v1", {
        schema_version: "host_runtime.head.v1",
        session_id: "session-a",
        latest_cursor: 21,
        pruned_through_cursor: 19,
        retention_dropped: 19,
        runtime_generation_floor: 2,
        current_runtime_run_id: "run-b",
      }),
    );
    act(() =>
      first.emit("host_runtime.gap.v1", {
        schema_version: "host_runtime.gap.v1",
        session_id: "session-a",
        reason: "retention",
        requested_cursor: 12,
        oldest_available: 20,
        latest_cursor: 21,
        missing_cursor_span: 7,
        retention_dropped: 19,
        runtime_generation_floor: 2,
        current_runtime_run_id: "run-b",
      }),
    );
    act(() =>
      first.emit("host_runtime.v1", {
        ...ready,
        cursor: 20,
        runtime_run_id: "run-a",
        runtime_generation: 1,
        source_event_id: "late-predecessor",
        kind: "process.exited",
        payload: { disconnected: true },
      }),
    );
    expect(screen.getByTestId("state").textContent).toContain('"runtimeRunID":"run-b"');
    expect(screen.getByTestId("state").textContent).toContain('"status":"unknown"');
    act(() =>
      first.emit("host_runtime.v1", {
        ...ready,
        cursor: 21,
        runtime_run_id: "run-b",
        runtime_generation: 2,
        source_event_id: "successor-processing",
        kind: "session.processing",
      }),
    );
    expect(screen.getByTestId("state").textContent).toContain('"status":"processing"');

    // A cursor-ahead response may authoritatively restore an older database
    // generation. Its matching head+gap rewinds the committed cursor so the
    // lower-generation rows can rebuild state.
    act(() =>
      first.emit("host_runtime.head.v1", {
        schema_version: "host_runtime.head.v1",
        session_id: "session-a",
        latest_cursor: 10,
        pruned_through_cursor: 0,
        retention_dropped: 0,
        runtime_generation_floor: 1,
        current_runtime_run_id: "run-restored",
      }),
    );
    act(() =>
      first.emit("host_runtime.gap.v1", {
        schema_version: "host_runtime.gap.v1",
        session_id: "session-a",
        reason: "cursor_ahead",
        requested_cursor: 21,
        oldest_available: 1,
        latest_cursor: 10,
        missing_cursor_span: 11,
        retention_dropped: 0,
        runtime_generation_floor: 1,
        current_runtime_run_id: "run-restored",
      }),
    );
    act(() =>
      first.emit("host_runtime.v1", {
        ...ready,
        cursor: 1,
        runtime_run_id: "run-restored",
        runtime_generation: 1,
        source_event_id: "restored-ready",
      }),
    );
    expect(screen.getByTestId("state").textContent).toContain('"lastCursor":1');
    expect(screen.getByTestId("state").textContent).toContain('"runtimeRunID":"run-restored"');
    expect(screen.getByTestId("state").textContent).toContain('"status":"ready"');

    act(() => first.onerror?.(new Event("error")));
    expect(first.closed).toBe(false);

    rendered.rerender(<Harness sessionID="session-b" />);
    expect(first.closed).toBe(true);
    expect(FakeEventSource.instances[1]?.url).toBe("/api/sessions/session-b/runtime-events");
    expect(screen.getByTestId("state").textContent).toContain('"lastCursor":0');
  });
});
