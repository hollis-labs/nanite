import { describe, expect, it } from "vitest";
import {
  type HostRuntimeFeedEvent,
  initialHostRuntimeFeedState,
  reduceHostRuntimeEvent,
  reduceHostRuntimeGap,
} from "@/lib/host-runtime-feed";

function event(
  cursor: number,
  kind: string,
  payload: Record<string, unknown> = {},
  run = "run-a",
  generation = run === "run-a" ? 1 : 2,
): HostRuntimeFeedEvent {
  return {
    schema_version: "host_runtime.v1",
    cursor,
    session_id: "session-a",
    runtime_run_id: run,
    runtime_generation: generation,
    source_event_id: `${run}-source-${cursor}`,
    source_sequence: cursor + 40,
    kind,
    occurred_at: `2026-09-05T12:00:${String(cursor).padStart(2, "0")}Z`,
    source: { channel: "jsonrpc", confidence: "exact" },
    process: { provider: "claude", runtime: "jsonrpc-stdio" },
    payload,
    payload_visibility: "public_metadata",
  };
}

describe("host runtime reducer", () => {
  it("is idempotent, accepts result-before-start, and preserves terminal tool state", () => {
    let state = reduceHostRuntimeEvent(initialHostRuntimeFeedState, event(1, "session.ready"));
    state = reduceHostRuntimeEvent(
      state,
      event(2, "agent.tool_result", { tool_id: "tool-1", stage: "update" }),
    );
    expect(state.tools).toEqual([
      expect.objectContaining({ id: "tool-1", name: "Tool", stage: "update" }),
    ]);

    state = reduceHostRuntimeEvent(
      state,
      event(3, "agent.tool_use", { tool_id: "tool-1", name: "read_file", stage: "started" }),
    );
    expect(state.tools[0]).toEqual(expect.objectContaining({ name: "read_file", stage: "update" }));

    state = reduceHostRuntimeEvent(
      state,
      event(4, "agent.tool_result", { tool_id: "tool-1", stage: "completed" }),
    );
    const terminal = state;
    state = reduceHostRuntimeEvent(
      state,
      event(5, "agent.tool_result", { tool_id: "tool-1", stage: "update" }),
    );
    expect(state.tools[0]?.stage).toBe("completed");

    // Replayed cursors and repeated source IDs do not duplicate tool rows.
    expect(reduceHostRuntimeEvent(state, event(4, "turn.failed"))).toBe(state);
    const repeatedID = terminal.recentEvents.at(-1)?.source_event_id;
    expect(repeatedID).toBeTruthy();
    const repeatedSource = {
      ...event(6, "turn.failed"),
      source_event_id: repeatedID ?? "missing-source-id",
    };
    state = reduceHostRuntimeEvent(state, repeatedSource);
    expect(state.tools).toHaveLength(1);
    expect(state.lastCursor).toBe(6);
  });

  it("uses monotonic generations and ignores all delayed predecessor lifecycle events", () => {
    let state = reduceHostRuntimeEvent(initialHostRuntimeFeedState, event(1, "session.ready"));
    state = reduceHostRuntimeEvent(state, event(2, "session.processing"));
    state = reduceHostRuntimeEvent(
      state,
      event(3, "turn.completed", {
        terminal: false,
        usage: { input_tokens: 13, output_tokens: 8 },
      }),
    );
    expect(state.status).toBe("processing");
    expect(state.usage).toEqual({ input_tokens: 13, output_tokens: 8 });

    state = reduceHostRuntimeEvent(state, {
      ...event(4, "session.ready", {}, "run-b", 2),
      source_sequence: 1,
    });
    state = reduceHostRuntimeEvent(state, event(5, "session.processing", {}, "run-b", 2));

    state = reduceHostRuntimeEvent(state, event(6, "process.started", {}, "run-a", 1));
    state = reduceHostRuntimeEvent(state, event(7, "session.ready", {}, "run-a", 1));
    expect(state.runtimeRunID).toBe("run-b");
    expect(state.runtimeGeneration).toBe(2);
    expect(state.status).toBe("processing");

    const staleExit = {
      ...event(8, "process.exited", { disconnected: true }, "run-a", 1),
      process: { provider: "claude", provider_session_id: "old-provider-session" },
    };
    state = reduceHostRuntimeEvent(state, staleExit);
    expect(state.runtimeRunID).toBe("run-b");
    expect(state.status).toBe("processing");
    expect(state.providerSessionID).toBeUndefined();

    state = reduceHostRuntimeEvent(
      state,
      event(9, "host_runtime.ingest_gap", { dropped_events: 2 }, "run-a", 1),
    );
    expect(state.runtimeRunID).toBe("run-b");
    expect(state.status).toBe("processing");
    expect(state.gap?.reason).toBe("ingestion");

    const current = {
      ...event(10, "session.heartbeat", {}, "run-b", 2),
      process: { provider: "claude", provider_session_id: "new-provider-session" },
    };
    state = reduceHostRuntimeEvent(state, current);
    expect(state.providerSessionID).toBe("new-provider-session");
  });

  it("marks replay gaps incomplete and rebuilds from subsequent retained events", () => {
    let state = reduceHostRuntimeEvent(initialHostRuntimeFeedState, event(1, "session.ready"));
    state = reduceHostRuntimeGap(state, {
      schema_version: "host_runtime.gap.v1",
      session_id: "session-a",
      reason: "retention",
      requested_cursor: 1,
      oldest_available: 9,
      latest_cursor: 12,
      missing_cursor_span: 7,
      retention_dropped: 8,
      runtime_generation_floor: 2,
      current_runtime_run_id: "run-b",
    });
    expect(state.lastCursor).toBe(8);
    expect(state.status).toBe("unknown");
    expect(state.gap?.reason).toBe("retention");
    expect(state.runtimeGeneration).toBe(2);
    expect(state.runtimeRunID).toBe("run-b");

    state = reduceHostRuntimeEvent(state, event(9, "process.started", {}, "run-a", 1));
    expect(state.status).toBe("unknown");
    expect(state.runtimeRunID).toBe("run-b");

    state = reduceHostRuntimeEvent(state, event(10, "session.processing", {}, "run-b", 2));
    expect(state.status).toBe("processing");
    expect(state.runtimeRunID).toBe("run-b");
  });

  it("resets derived state at a durable ingestion-gap boundary", () => {
    let state = reduceHostRuntimeEvent(initialHostRuntimeFeedState, event(1, "session.ready"));
    state = reduceHostRuntimeEvent(
      state,
      event(2, "agent.tool_use", { tool_id: "tool-a", name: "shell", stage: "started" }),
    );
    state = reduceHostRuntimeEvent(
      state,
      event(3, "host_runtime.ingest_gap", {
        dropped_events: 4,
        first_source_sequence: 3,
        last_source_sequence: 6,
      }),
    );
    expect(state.tools).toEqual([]);
    expect(state.status).toBe("unknown");
    expect(state.gap).toEqual(
      expect.objectContaining({ reason: "ingestion", missing_cursor_span: 4 }),
    );
  });
});
