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
  const newerRun = event.runtime_generation > state.runtimeGeneration;
  const adoptsRun = sameRun || newerRun;
  const changedRun = newerRun;
  const base: HostRuntimeFeedState = changedRun
    ? {
        ...initialHostRuntimeFeedState,
        runtimeRunID: event.runtime_run_id,
        runtimeGeneration: event.runtime_generation,
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
    if (!adoptsRun || event.runtime_run_id !== next.runtimeRunID) return next;
    const dropped = numericValue(event.payload.dropped_events);
    return {
      ...initialHostRuntimeFeedState,
      lastCursor: event.cursor,
      runtimeRunID: event.runtime_run_id,
      runtimeGeneration: event.runtime_generation,
      recentEvents,
      seenSourceEventIDs,
      gap: {
        schema_version: "host_runtime.gap.v1",
        session_id: event.session_id,
        reason: "ingestion",
        requested_cursor: Math.max(0, event.cursor - 1),
        oldest_available: event.cursor + 1,
        latest_cursor: event.cursor,
        missing_cursor_span: dropped,
        retention_dropped: 0,
      },
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

export function reduceHostRuntimeGap(
  state: HostRuntimeFeedState,
  gap: HostRuntimeFeedGap,
): HostRuntimeFeedState {
  if (gap.schema_version !== "host_runtime.gap.v1") return state;
  return {
    ...initialHostRuntimeFeedState,
    lastCursor: Math.max(0, gap.oldest_available - 1),
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
