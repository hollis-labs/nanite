export interface HostRuntimeFeedEvent {
  schema_version: "host_runtime.v1";
  cursor: number;
  session_id: string;
  runtime_run_id: string;
  runtime_generation: number;
  source_event_id: string;
  source_sequence: number;
  kind: string;
  occurred_at: string;
  turn_id?: string;
  parent_id?: string;
  source: { channel: string; confidence?: string };
  process?: {
    provider?: string;
    runtime?: string;
    provider_session_id?: string;
  };
  payload: Record<string, unknown>;
  payload_truncated?: boolean;
  payload_visibility: string;
}

export interface HostRuntimeFeedGap {
  schema_version: "host_runtime.gap.v1";
  session_id: string;
  reason: "retention" | "cursor_ahead" | string;
  requested_cursor: number;
  oldest_available: number;
  latest_cursor: number;
  missing_cursor_span: number;
  retention_dropped: number;
  runtime_generation_floor: number;
  current_runtime_run_id?: string;
}

export interface HostRuntimeFeedHead {
  schema_version: "host_runtime.head.v1";
  session_id: string;
  latest_cursor: number;
  pruned_through_cursor: number;
  retention_dropped: number;
  runtime_generation_floor: number;
  current_runtime_run_id?: string;
}

export type HostRuntimeToolStage = "started" | "update" | "completed" | "failed";

export interface HostRuntimeTool {
  id: string;
  name: string;
  stage: HostRuntimeToolStage;
  status?: string;
  lastCursor: number;
}

export interface HostRuntimeFeedState {
  lastCursor: number;
  runtimeRunID: string;
  runtimeGeneration: number;
  status:
    | "unknown"
    | "starting"
    | "ready"
    | "processing"
    | "idle"
    | "failed"
    | "exited"
    | "disconnected";
  interrupt: "none" | "requested" | "acknowledged";
  provider?: string;
  runtime?: string;
  providerSessionID?: string;
  usage: Record<string, number>;
  tools: HostRuntimeTool[];
  recentEvents: HostRuntimeFeedEvent[];
  seenSourceEventIDs: string[];
  head?: HostRuntimeFeedHead;
  gap?: HostRuntimeFeedGap;
}

export const initialHostRuntimeFeedState: HostRuntimeFeedState = {
  lastCursor: 0,
  runtimeRunID: "",
  runtimeGeneration: 0,
  status: "unknown",
  interrupt: "none",
  usage: {},
  tools: [],
  recentEvents: [],
  seenSourceEventIDs: [],
};

const terminalToolStages = new Set<HostRuntimeToolStage>(["completed", "failed"]);

export function reduceHostRuntimeEvent(
  state: HostRuntimeFeedState,
  event: HostRuntimeFeedEvent,
): HostRuntimeFeedState {
  if (event.schema_version !== "host_runtime.v1" || event.cursor <= state.lastCursor) {
    return state;
  }
  if (!Number.isSafeInteger(event.runtime_generation) || event.runtime_generation < 1) {
    return { ...state, lastCursor: event.cursor };
  }
  const sourceIdentity = `${event.runtime_generation}:${event.runtime_run_id}:${event.source_event_id}`;
  if (state.seenSourceEventIDs.includes(sourceIdentity)) {
    return { ...state, lastCursor: event.cursor };
  }
  const sameRun =
    event.runtime_generation === state.runtimeGeneration &&
    event.runtime_run_id === state.runtimeRunID;
  const matchesUnboundFloor =
    event.runtime_generation === state.runtimeGeneration && !state.runtimeRunID;
  const newerRun = event.runtime_generation > state.runtimeGeneration;
  const adoptsRun = sameRun || matchesUnboundFloor || newerRun;
  const changedRun = matchesUnboundFloor || newerRun;
  const base: HostRuntimeFeedState = changedRun
    ? {
        ...initialHostRuntimeFeedState,
        runtimeRunID: event.runtime_run_id,
        runtimeGeneration: event.runtime_generation,
        head: state.head,
        gap: state.gap,
      }
    : state;
  const recentEvents = [...base.recentEvents, event].slice(-100);
  const seenSourceEventIDs = [...base.seenSourceEventIDs, sourceIdentity].slice(-256);
  let next: HostRuntimeFeedState = {
    ...base,
    lastCursor: event.cursor,
    recentEvents,
    seenSourceEventIDs,
  };

  if (event.kind === "host_runtime.ingest_gap") {
    const dropped = numericValue(event.payload.dropped_events);
    const gap: HostRuntimeFeedGap = {
      schema_version: "host_runtime.gap.v1",
      session_id: event.session_id,
      reason: "ingestion",
      requested_cursor: Math.max(0, event.cursor - 1),
      oldest_available: event.cursor + 1,
      latest_cursor: event.cursor,
      missing_cursor_span: dropped,
      retention_dropped: 0,
      runtime_generation_floor: Math.max(state.runtimeGeneration, event.runtime_generation),
      current_runtime_run_id: adoptsRun ? event.runtime_run_id : state.runtimeRunID,
    };
    if (!adoptsRun || event.runtime_run_id !== next.runtimeRunID) {
      return { ...next, gap };
    }
    return {
      ...initialHostRuntimeFeedState,
      lastCursor: event.cursor,
      runtimeRunID: event.runtime_run_id,
      runtimeGeneration: event.runtime_generation,
      recentEvents,
      seenSourceEventIDs,
      head: state.head,
      gap,
    };
  }

  // A delayed predecessor terminal must remain visible in the timeline but
  // cannot mark a newer replacement run offline.
  if (!adoptsRun || event.runtime_run_id !== next.runtimeRunID) {
    return next;
  }

  if (event.process?.provider) next = { ...next, provider: event.process.provider };
  if (event.process?.runtime) next = { ...next, runtime: event.process.runtime };
  if (event.process?.provider_session_id) {
    next = { ...next, providerSessionID: event.process.provider_session_id };
  }

  switch (event.kind) {
    case "process.started":
      next = { ...next, status: "starting" };
      break;
    case "session.ready":
      next = { ...next, status: "ready" };
      break;
    case "session.processing":
    case "turn.started":
      next = { ...next, status: "processing", interrupt: "none" };
      break;
    case "session.idle":
      next = { ...next, status: "idle" };
      break;
    case "turn.completed": {
      const usage = numericRecord(event.payload.usage);
      if (Object.keys(usage).length > 0) next = { ...next, usage: { ...next.usage, ...usage } };
      if (event.payload.terminal === true) next = { ...next, status: "idle" };
      break;
    }
    case "turn.failed":
      next = { ...next, status: "failed" };
      break;
    case "process.exited":
      next = {
        ...next,
        status: event.payload.disconnected === true ? "disconnected" : "exited",
      };
      break;
    case "interrupt.requested":
      next = { ...next, interrupt: "requested" };
      break;
    case "interrupt.acknowledged":
      next = { ...next, interrupt: "acknowledged" };
      break;
    case "agent.tool_use":
    case "agent.tool_result":
      next = { ...next, tools: reduceRuntimeTool(next.tools, event) };
      break;
  }
  return next;
}

export function reduceHostRuntimeHead(
  state: HostRuntimeFeedState,
  head: HostRuntimeFeedHead,
): HostRuntimeFeedState {
  if (
    head.schema_version !== "host_runtime.head.v1" ||
    !Number.isSafeInteger(head.latest_cursor) ||
    head.latest_cursor < 0 ||
    !Number.isSafeInteger(head.pruned_through_cursor) ||
    head.pruned_through_cursor < 0 ||
    !Number.isSafeInteger(head.retention_dropped) ||
    head.retention_dropped < 0 ||
    !Number.isSafeInteger(head.runtime_generation_floor) ||
    head.runtime_generation_floor < 0
  ) {
    return state;
  }
  const newerOwner = head.runtime_generation_floor > state.runtimeGeneration;
  const bindsCurrentFloor =
    head.runtime_generation_floor === state.runtimeGeneration &&
    !state.runtimeRunID &&
    !!head.current_runtime_run_id;
  if (!newerOwner && !bindsCurrentFloor) {
    // Keep the latest ordered snapshot even when a restored lower-generation
    // head must wait for its matching cursor_ahead gap to authorize rewind.
    return { ...state, head };
  }
  return {
    ...initialHostRuntimeFeedState,
    lastCursor: state.lastCursor,
    runtimeRunID: head.current_runtime_run_id ?? "",
    runtimeGeneration: head.runtime_generation_floor,
    head,
    gap: state.gap,
  };
}

export function reduceHostRuntimeGap(
  state: HostRuntimeFeedState,
  gap: HostRuntimeFeedGap,
): HostRuntimeFeedState {
  if (
    gap.schema_version !== "host_runtime.gap.v1" ||
    (gap.reason !== "retention" && gap.reason !== "cursor_ahead") ||
    !Number.isSafeInteger(gap.requested_cursor) ||
    gap.requested_cursor < 0 ||
    !Number.isSafeInteger(gap.oldest_available) ||
    gap.oldest_available < 1 ||
    !Number.isSafeInteger(gap.latest_cursor) ||
    gap.latest_cursor < 0 ||
    !Number.isSafeInteger(gap.retention_dropped) ||
    gap.retention_dropped < 0 ||
    !Number.isSafeInteger(gap.runtime_generation_floor) ||
    gap.runtime_generation_floor < 0
  ) {
    return state;
  }
  const gapFloor = gap.runtime_generation_floor;
  if (gap.requested_cursor !== state.lastCursor) return state;
  if (
    state.head &&
    (state.head.latest_cursor !== gap.latest_cursor ||
      state.head.pruned_through_cursor + 1 !== gap.oldest_available ||
      state.head.retention_dropped !== gap.retention_dropped ||
      state.head.runtime_generation_floor !== gapFloor ||
      (state.head.current_runtime_run_id ?? "") !== (gap.current_runtime_run_id ?? ""))
  ) {
    return state;
  }
  if (gap.reason !== "cursor_ahead" && gapFloor < state.runtimeGeneration) return state;
  return {
    ...initialHostRuntimeFeedState,
    lastCursor: Math.max(0, gap.oldest_available - 1),
    runtimeRunID: gap.current_runtime_run_id ?? "",
    runtimeGeneration: gapFloor,
    head: state.head,
    gap,
  };
}

function reduceRuntimeTool(
  tools: HostRuntimeTool[],
  event: HostRuntimeFeedEvent,
): HostRuntimeTool[] {
  const toolID = stringValue(event.payload.tool_id) || `event:${event.source_event_id}`;
  const index = tools.findIndex((tool) => tool.id === toolID);
  const previous = index >= 0 ? tools[index] : undefined;
  const requestedStage = toolStage(event.payload.stage);
  const stage =
    previous &&
    ((terminalToolStages.has(previous.stage) && !terminalToolStages.has(requestedStage)) ||
      toolStageRank(previous.stage) > toolStageRank(requestedStage))
      ? previous.stage
      : requestedStage;
  const tool: HostRuntimeTool = {
    id: toolID,
    name: stringValue(event.payload.name) || previous?.name || "Tool",
    stage,
    status: stringValue(event.payload.status) || previous?.status,
    lastCursor: event.cursor,
  };
  if (index < 0) return [...tools, tool].slice(-50);
  const next = [...tools];
  next[index] = tool;
  return next;
}

function toolStage(value: unknown): HostRuntimeToolStage {
  return value === "started" || value === "completed" || value === "failed" ? value : "update";
}

function toolStageRank(stage: HostRuntimeToolStage): number {
  if (terminalToolStages.has(stage)) return 2;
  return stage === "update" ? 1 : 0;
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function numericRecord(value: unknown): Record<string, number> {
  if (!value || typeof value !== "object" || Array.isArray(value)) return {};
  const result: Record<string, number> = {};
  for (const [key, child] of Object.entries(value)) {
    if (typeof child === "number" && Number.isFinite(child)) result[key] = child;
  }
  return result;
}

function numericValue(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}
