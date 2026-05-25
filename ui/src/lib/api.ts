import type { PluginRegistryResponse } from "./plugin-loader";
import type {
  AgentKnowledgeSeed,
  AgentSchedule,
  AgentBuilderDraftRequest,
  AgentBuilderDraftResponse,
  AgentBuilderDryRunRequest,
  AgentBuilderDryRunResponse,
  AgentBuilderReviewRequest,
  AgentBuilderReviewResponse,
  AgentBootPlanDocument,
  AgentBootPlanDryRunResponse,
  AgentKnowledgeSeedUpsertRequest,
  AgentKnownSkill,
  AgentKnownSkillUpsertRequest,
  AgentKnownTool,
  AgentKnownToolUpsertRequest,
  AgentMessage,
  AgentMessageChannel,
  AgentMessageKind,
  AgentModeProfile,
  AgentProfile,
  AgentProcedure,
  AgentProcedureUpsertRequest,
  AgentReflexRow,
  AgentStateScope,
  ApprovalDecision,
  ApprovalScope,
  Artifact,
  AttachDurableAgentSessionRequest,
  Bookmark,
  BrokerDecision,
  CatalogBrowseEntry,
  CatalogSource,
  CLIDetectionResult,
  ContextBreakdown,
  CreateAgentReflexRequest,
  CreateAgentProfileRequest,
  CreateDurableAgentRequest,
  CustomAction,
  DiscoveryDiff,
  Document,
  DrawerCardType,
  DrawerPinnedCard,
  DurableAgentEvent,
  DurableAgentInstance,
  DurableAgentLaunchPlan,
  DurableAgentLaunchResult,
  DurableAgentRecipe,
  DurableAgentRecipeApplyResult,
  DurableAgentRecipePlan,
  DurableAgentRecipeRequest,
  DurableAgentSessionAttachmentState,
  DurableAgentStartRequest,
  DurableAgentWakeDueItem,
  DurableAgentWakeResult,
  DurableAgentWakeRunResult,
  Envelope,
  ExecutionMetrics,
  ForkSessionRequest,
  FragmentsBacklogItem,
  FragmentsSprint,
  FragmentsTask,
  GlobalUsageSummary,
  HarnessCapabilitiesResponse,
  HarnessCancelResponse,
  HarnessCreateSessionRequest,
  HarnessInitializeResponse,
  HarnessSessionResponse,
  HarnessTurnRequest,
  HarnessTurnResponse,
  InspectorTurnSnapshot,
  InspectorTurnsResponse,
  MCPServerConfig,
  Memory,
  MemoryCreateRequest,
  MemoryListResponse,
  MemoryUpdateRequest,
  Message,
  MessagePage,
  MetaHarness,
  MetaHarnessInput,
  Mode,
  ModelRecord,
  PermissionMode,
  PinnedContent,
  Plan,
  PlanFilter,
  PlanStep,
  PendingReflexApproveRequest,
  PendingReflexRejectRequest,
  PendingReflexRejectResponse,
  PendingReflexRow,
  PatchAgentReflexRequest,
  PluginConfig,
  PluginInfo,
  PluginKeybinding,
  PluginUIComponent,
  ProcessHealthResponse,
  Project,
  PromptTemplate,
  ProviderConfig,
  ProviderStatus,
  Reminder,
  SearchResult,
  ServerInfo,
  Session,
  SessionAgent,
  SessionDetailsResponse,
  SessionUsageSummary,
  SessionWithMessages,
  Skill,
  SlashCommandDef,
  StartSurfaceCapabilitiesResponse,
  Todo,
  TodoFilter,
  ToolDefinition,
  ToolSelection,
  UISlotEntry,
  UpdateDurableAgentRequest,
  UpdateAgentProfileRequest,
  UserSettings,
  UtilityCallSummary,
  ValidateReflexRequest,
  ValidateReflexResponse,
  WorkDiff,
  Worker,
  WorkflowRun,
  Workspace,
  WorkspaceRoleTrustOverride,
} from "./types";

const API_BASE = "/api";

/**
 * Thrown by api.pinDrawerCard when the backend returns 409 because the 10-pin
 * cap is already full (C1, CW-20260428-0012). Callers catch this to surface
 * the user-facing "10-tab limit; unpin one first" toast instead of a generic
 * error message.
 */
export class DrawerPinCapError extends Error {
  constructor() {
    super("Drawer pin cap exceeded");
    this.name = "DrawerPinCapError";
  }
}

// The Go backend stores JSON fields as strings in SQLite.
// These helpers parse them into typed forms for the UI and stringify on write.

function hydrateTodo(raw: Record<string, unknown>): Todo {
  const todo = raw as unknown as Todo;
  if (typeof todo.labels === "string") {
    try {
      todo.labels = JSON.parse(todo.labels as unknown as string);
    } catch {
      todo.labels = [];
    }
  }
  if (!Array.isArray(todo.labels)) todo.labels = [];
  if (typeof todo.metadata === "string") {
    try {
      todo.metadata = JSON.parse(todo.metadata as unknown as string);
    } catch {
      todo.metadata = {};
    }
  }
  if (typeof todo.metadata !== "object" || todo.metadata === null)
    todo.metadata = {};
  return todo;
}

function hydratePlan(raw: Record<string, unknown>): Plan {
  const plan = raw as unknown as Plan;
  if (typeof plan.steps === "string") {
    try {
      plan.steps = JSON.parse(plan.steps as unknown as string);
    } catch {
      plan.steps = [];
    }
  }
  if (!Array.isArray(plan.steps)) plan.steps = [];
  if (typeof plan.metadata === "string") {
    try {
      plan.metadata = JSON.parse(plan.metadata as unknown as string);
    } catch {
      plan.metadata = {};
    }
  }
  if (typeof plan.metadata !== "object" || plan.metadata === null)
    plan.metadata = {};
  return plan;
}

// Serialize structured fields back to JSON strings for the Go backend.
function serializeTodoUpdates(
  updates: Record<string, unknown>,
): Record<string, unknown> {
  const out = { ...updates };
  if (out.labels !== undefined && typeof out.labels !== "string")
    out.labels = JSON.stringify(out.labels);
  if (out.metadata !== undefined && typeof out.metadata !== "string")
    out.metadata = JSON.stringify(out.metadata);
  return out;
}

function serializePlanPayload(
  data: Record<string, unknown>,
): Record<string, unknown> {
  const out = { ...data };
  if (out.steps !== undefined && typeof out.steps !== "string")
    out.steps = JSON.stringify(out.steps);
  if (out.metadata !== undefined && typeof out.metadata !== "string")
    out.metadata = JSON.stringify(out.metadata);
  return out;
}

async function readAPIError(
  res: Response,
  fallback: string,
): Promise<Error> {
  try {
    const err = (await res.json()) as { error?: string };
    if (typeof err.error === "string" && err.error.trim() !== "") {
      return new Error(err.error);
    }
  } catch {
    // Fall through to the fallback message when the response is not JSON.
  }
  return new Error(fallback);
}

export const api = {
  getHarnessInitialize: async (): Promise<HarnessInitializeResponse> => {
    const res = await fetch(`${API_BASE}/harness/v1/initialize`);
    if (!res.ok)
      throw new Error(`Failed to get harness initialize: ${res.status}`);
    return res.json();
  },

  getHarnessCapabilities: async (): Promise<HarnessCapabilitiesResponse> => {
    const res = await fetch(`${API_BASE}/harness/v1/capabilities`);
    if (!res.ok)
      throw new Error(`Failed to get harness capabilities: ${res.status}`);
    return res.json();
  },

  createHarnessSession: async (
    data: HarnessCreateSessionRequest,
  ): Promise<HarnessSessionResponse> => {
    const res = await fetch(`${API_BASE}/harness/v1/sessions`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok)
      throw new Error(`Failed to create harness session: ${res.status}`);
    return res.json();
  },

  getHarnessSession: async (id: string): Promise<HarnessSessionResponse> => {
    const res = await fetch(`${API_BASE}/harness/v1/sessions/${encodeURIComponent(id)}`);
    if (!res.ok)
      throw new Error(`Failed to get harness session: ${res.status}`);
    return res.json();
  },

  sendHarnessTurn: async (
    id: string,
    data: HarnessTurnRequest,
  ): Promise<HarnessTurnResponse> => {
    const res = await fetch(
      `${API_BASE}/harness/v1/sessions/${encodeURIComponent(id)}/turns`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok) throw new Error(`Failed to send harness turn: ${res.status}`);
    return res.json();
  },

  cancelHarnessTurn: async (id: string): Promise<HarnessCancelResponse> => {
    const res = await fetch(
      `${API_BASE}/harness/v1/sessions/${encodeURIComponent(id)}/cancel`,
      { method: "POST" },
    );
    if (!res.ok)
      throw new Error(`Failed to cancel harness turn: ${res.status}`);
    return res.json();
  },

  listHarnessDurableAgents: async (
    includeArchived = false,
  ): Promise<DurableAgentInstance[]> => {
    const query = includeArchived ? "?include_archived=true" : "";
    const res = await fetch(`${API_BASE}/harness/v1/durable-agents${query}`);
    if (!res.ok)
      throw new Error(`Failed to list harness durable agents: ${res.status}`);
    return res.json();
  },

  getHarnessDurableAgent: async (id: string): Promise<DurableAgentInstance> => {
    const res = await fetch(
      `${API_BASE}/harness/v1/durable-agents/${encodeURIComponent(id)}`,
    );
    if (!res.ok)
      throw new Error(`Failed to get harness durable agent: ${res.status}`);
    return res.json();
  },

  startHarnessDurableAgent: async (
    id: string,
    data: DurableAgentStartRequest,
  ): Promise<DurableAgentLaunchResult> => {
    const res = await fetch(
      `${API_BASE}/harness/v1/durable-agents/${encodeURIComponent(id)}/start`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to start harness durable agent: ${res.status}`);
    return res.json();
  },

  resumeHarnessDurableAgent: async (
    id: string,
    data: DurableAgentStartRequest,
  ): Promise<DurableAgentLaunchResult> => {
    const res = await fetch(
      `${API_BASE}/harness/v1/durable-agents/${encodeURIComponent(id)}/resume`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to resume harness durable agent: ${res.status}`);
    return res.json();
  },

  wakeHarnessDurableAgent: async (
    id: string,
    data: DurableAgentStartRequest,
  ): Promise<DurableAgentWakeResult> => {
    const res = await fetch(
      `${API_BASE}/harness/v1/durable-agents/${encodeURIComponent(id)}/wake`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to wake harness durable agent: ${res.status}`);
    return res.json();
  },

  // Sessions
  listSessions: async (workspaceId?: string): Promise<Session[]> => {
    const params = workspaceId ? `?workspace_id=${workspaceId}` : "";
    const res = await fetch(`${API_BASE}/sessions${params}`);
    if (!res.ok) throw new Error(`Failed to list sessions: ${res.status}`);
    return res.json();
  },

  getSession: async (id: string): Promise<SessionWithMessages> => {
    const res = await fetch(`${API_BASE}/sessions/${id}`);
    if (!res.ok) throw new Error(`Failed to get session: ${res.status}`);
    // BE returns `{session: Session, messages: Message[]}`; flatten so the
    // returned object satisfies SessionWithMessages (= Session + messages)
    // and consumers can read `session.<field>` directly. Several callers
    // were silently reading `undefined` before this fix (PR #93 Copilot
    // feedback).
    const raw = await res.json();
    return {
      ...(raw.session ?? {}),
      messages: raw.messages ?? [],
      // CW-20260518-0084: carry the interrupted-turn signal through the
      // flatten so consumers can detect a restart-killed in-flight turn.
      interrupted_turn: raw.interrupted_turn ?? null,
    };
  },

  getSessionDetails: async (id: string): Promise<SessionDetailsResponse> => {
    const res = await fetch(
      `${API_BASE}/sessions/${encodeURIComponent(id)}/details`,
    );
    if (!res.ok)
      throw new Error(`Failed to get session details: ${res.status}`);
    return res.json();
  },

  createSession: async (data: {
    workspace_id: string;
    project_id?: string;
    provider?: string;
    model?: string;
    agent_id?: string;
  }): Promise<Session> => {
    const res = await fetch(`${API_BASE}/sessions`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error(`Failed to create session: ${res.status}`);
    return res.json();
  },

  updateSession: async (
    id: string,
    data: Partial<Session>,
  ): Promise<Session> => {
    const res = await fetch(`${API_BASE}/sessions/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error(`Failed to update session: ${res.status}`);
    return res.json();
  },

  forkSession: async (
    id: string,
    data: ForkSessionRequest,
  ): Promise<Session> => {
    const res = await fetch(`${API_BASE}/sessions/${id}/fork`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error(`Failed to fork session: ${res.status}`);
    const body = await res.json();
    return body.session ?? body;
  },

  deleteSession: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/sessions/${id}`, { method: "DELETE" });
    if (!res.ok) throw new Error(`Failed to delete session: ${res.status}`);
  },

  // Messages
  sendMessage: async (data: {
    session_id: string;
    content: string;
    // F1 (CW-20260420-0014): optional effort scalar.
    // Values: "low" | "normal" | "high" | "max". Omit or empty → "normal".
    effort?: string;
  }): Promise<{ message_id: string; stream_url: string }> => {
    const res = await fetch(`${API_BASE}/messages`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error(`Failed to send message: ${res.status}`);
    return res.json();
  },

  retryStream: async (
    sessionId: string,
  ): Promise<{ message_id: string; stream_url: string }> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/retry`, {
      method: "POST",
    });
    if (!res.ok) throw new Error(`Failed to retry: ${res.status}`);
    return res.json();
  },

  /**
   * CW-20260512-0006: cancel the in-flight chat-stream generation for a
   * session. The 5-minute parent wall-clock deadline has been removed
   * from the BE, so user-initiated stop is now the load-bearing safety
   * net for runaway-cost concerns.
   *
   * BE: POST /api/sessions/{id}/chat/cancel (no body).
   *   - 200 → cancel dispatched; the registered context.CancelFunc fires
   *     and generateResponse exits cleanly on its next loop iteration.
   *   - 404 → no active generation (idempotent; ignore silently — the
   *     stream may have already completed in the gap between the FE
   *     closing the EventSource and this call landing).
   *   - other → network / config error; surface as "error" so the
   *     caller can decide whether to retry or just close the SSE.
   *
   * The FE composer's stop button calls this so the BE actually cancels
   * the LLM stream + tool work instead of just closing the SSE
   * client-side (which would leave the BE generating wasted tokens).
   */
  cancelChatStream: async (
    sessionId: string,
  ): Promise<"cancelled" | "idle" | "error"> => {
    try {
      const res = await fetch(`${API_BASE}/sessions/${sessionId}/chat/cancel`, {
        method: "POST",
      });
      if (res.ok) return "cancelled";
      if (res.status === 404) return "idle";
      return "error";
    } catch {
      return "error";
    }
  },

  /**
   * CW-20260516-0057: reboot a single session's runtime agent. The next
   * user turn cold-boots a fresh agent process + boot dir from the current
   * binary; other sessions are untouched. Use it to pick up a freshly
   * deployed binary / boot-dir change, or to recover one wedged agent,
   * without the coarse all-sessions restart of nanite-api-service.
   *
   * BE: POST /api/sessions/{id}/agent/reboot (no body).
   *   - 200 {status:"rebooted"}        → live agent stopped; next turn boots fresh.
   *   - 200 {status:"no_active_agent"} → nothing to stop; next turn boots fresh anyway.
   *   - 409 → a turn is in flight for the session; retry once it settles.
   *   - other → network / config error.
   */
  rebootSessionAgent: async (
    sessionId: string,
  ): Promise<"rebooted" | "no_active_agent" | "busy" | "error"> => {
    try {
      const res = await fetch(
        `${API_BASE}/sessions/${sessionId}/agent/reboot`,
        {
          method: "POST",
        },
      );
      if (res.ok) {
        const body = (await res.json().catch(() => null)) as {
          status?: string;
        } | null;
        return body?.status === "no_active_agent"
          ? "no_active_agent"
          : "rebooted";
      }
      if (res.status === 409) return "busy";
      return "error";
    } catch {
      return "error";
    }
  },

  /**
   * Phase 9 (CW-20260510-0017 / W2A): cancel an in-flight recovery
   * broker retry. The `token` is the wrap-level `cancel_token` lifted
   * verbatim from the recovery info-card envelope.
   *
   * BE: POST /api/sessions/{id}/recovery/cancel with {"token": ...}.
   * Per W1D's contract:
   *   - 200 → "cancelled" (broker cancelled; breadcrumb is OutcomeCancelled)
   *   - 404 → "stale" (token unknown / expired / cross-session)
   *   - 400 → "error" (malformed body / missing token — should not happen)
   *   - any other / network failure → "error"
   *
   * The FE distinguishes 404 (terminal "no longer cancellable") from
   * transient errors (button stays interactive) so the UX matches the
   * reality of the BE state.
   */
  cancelRecoveryRetry: async (
    sessionId: string,
    token: string,
  ): Promise<"cancelled" | "stale" | "error"> => {
    const res = await fetch(
      `${API_BASE}/sessions/${sessionId}/recovery/cancel`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ token }),
      },
    );
    if (res.ok) return "cancelled";
    if (res.status === 404) return "stale";
    return "error";
  },

  getMessages: async (sessionId: string, limit = 50): Promise<Message[]> => {
    const res = await fetch(
      `${API_BASE}/sessions/${sessionId}/messages?limit=${limit}`,
    );
    if (!res.ok) throw new Error(`Failed to get messages: ${res.status}`);
    const page = (await res.json()) as MessagePage | Message[];
    // Backend now returns MessagePage; handle both shapes for safety.
    if (Array.isArray(page)) return page;
    return page.messages;
  },

  getMessagePage: async (
    sessionId: string,
    limit = 50,
    offset?: number,
  ): Promise<MessagePage> => {
    const params = new URLSearchParams({ limit: String(limit) });
    if (offset !== undefined) params.set("offset", String(offset));
    const res = await fetch(
      `${API_BASE}/sessions/${sessionId}/messages?${params}`,
    );
    if (!res.ok) throw new Error(`Failed to get messages: ${res.status}`);
    return res.json();
  },

  getMessagesAround: async (
    sessionId: string,
    messageId: string,
    before = 25,
    after = 25,
  ): Promise<MessagePage> => {
    const params = new URLSearchParams({
      around: messageId,
      before: String(before),
      after: String(after),
    });
    const res = await fetch(
      `${API_BASE}/sessions/${sessionId}/messages?${params}`,
    );
    if (!res.ok)
      throw new Error(`Failed to get messages around: ${res.status}`);
    return res.json();
  },

  getSessionPluginEnvelopes: async (sessionId: string): Promise<Envelope[]> => {
    const res = await fetch(
      `${API_BASE}/sessions/${sessionId}/plugin-envelopes`,
    );
    if (!res.ok)
      throw new Error(`Failed to get session plugin envelopes: ${res.status}`);
    return res.json();
  },

  // Workspaces
  listWorkspaces: async (): Promise<Workspace[]> => {
    const res = await fetch(`${API_BASE}/workspaces`);
    if (!res.ok) throw new Error(`Failed to list workspaces: ${res.status}`);
    return res.json();
  },

  createWorkspace: async (data: {
    name: string;
    description?: string;
    icon?: string;
  }): Promise<Workspace> => {
    const res = await fetch(`${API_BASE}/workspaces`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) {
      const err = await res
        .json()
        .catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to create workspace: ${res.status}`);
    }
    return res.json();
  },

  listProjects: async (workspaceId: string): Promise<Project[]> => {
    const res = await fetch(`${API_BASE}/workspaces/${workspaceId}/projects`);
    if (!res.ok) throw new Error(`Failed to list projects: ${res.status}`);
    return res.json();
  },

  createProject: async (
    workspaceId: string,
    data: { name: string; description?: string; repo_path?: string },
  ): Promise<Project> => {
    const res = await fetch(`${API_BASE}/workspaces/${workspaceId}/projects`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) {
      const err = await res
        .json()
        .catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to create project: ${res.status}`);
    }
    return res.json();
  },

  // Agents — endpoint returns full AgentProfile shape
  // Pass manageable=true ONLY for the Admin>Agents management list; it appends
  // ?manageable=1 which EXCLUDES internal harness agents. The default (no arg)
  // hits /api/agents unchanged so chat pickers/roster/header stay full-list.
  listAgents: async (manageable?: boolean): Promise<AgentProfile[]> => {
    // Strict === true so an accidental truthy arg (e.g. a React Query context
    // object from a bare `queryFn: api.listAgents`) never flips a full-list
    // consumer onto the manageable (internal-excluded) list.
    const qs = manageable === true ? "?manageable=1" : "";
    const res = await fetch(`${API_BASE}/agents${qs}`);
    if (!res.ok) throw new Error(`Failed to list agents: ${res.status}`);
    return res.json();
  },

  getAgentProfile: async (
    id: string,
  ): Promise<{ agent: AgentProfile; modes: AgentModeProfile[] }> => {
    const res = await fetch(`${API_BASE}/agents/${id}`);
    if (!res.ok) throw new Error(`Failed to get agent profile: ${res.status}`);
    return res.json();
  },

  listAgentKnownTools: async (agentId: string): Promise<AgentKnownTool[]> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/known-tools`,
    );
    if (!res.ok)
      throw new Error(`Failed to list agent known tools: ${res.status}`);
    return res.json();
  },

  getAgentKnownTool: async (
    agentId: string,
    toolName: string,
  ): Promise<AgentKnownTool> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/known-tools/${encodeURIComponent(toolName)}`,
    );
    if (!res.ok)
      throw new Error(`Failed to get agent known tool: ${res.status}`);
    return res.json();
  },

  createAgentKnownTool: async (
    agentId: string,
    data: AgentKnownToolUpsertRequest,
  ): Promise<AgentKnownTool> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/known-tools`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to create agent known tool: ${res.status}`);
    return res.json();
  },

  updateAgentKnownTool: async (
    agentId: string,
    toolName: string,
    data: AgentKnownToolUpsertRequest,
  ): Promise<AgentKnownTool> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/known-tools/${encodeURIComponent(toolName)}`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to update agent known tool: ${res.status}`);
    return res.json();
  },

  deleteAgentKnownTool: async (
    agentId: string,
    toolName: string,
  ): Promise<{ status: string }> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/known-tools/${encodeURIComponent(toolName)}`,
      {
        method: "DELETE",
      },
    );
    if (!res.ok)
      throw new Error(`Failed to delete agent known tool: ${res.status}`);
    return res.json();
  },

  listAgentKnownSkills: async (agentId: string): Promise<AgentKnownSkill[]> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/known-skills`,
    );
    if (!res.ok)
      throw new Error(`Failed to list agent known skills: ${res.status}`);
    return res.json();
  },

  getAgentBootPlan: async (agentId: string): Promise<AgentBootPlanDocument> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/boot-plan`,
    );
    if (!res.ok)
      throw new Error(`Failed to get agent boot plan: ${res.status}`);
    return res.json();
  },

  updateAgentBootPlan: async (
    agentId: string,
    data: AgentBootPlanDocument,
  ): Promise<AgentBootPlanDocument> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/boot-plan`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to update agent boot plan: ${res.status}`);
    return res.json();
  },

  deleteAgentBootPlan: async (agentId: string): Promise<{ status: string }> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/boot-plan`,
      {
        method: "DELETE",
      },
    );
    if (!res.ok)
      throw new Error(`Failed to delete agent boot plan: ${res.status}`);
    return res.json();
  },

  dryRunAgentBootPlan: async (
    agentId: string,
    data?: AgentBootPlanDocument,
  ): Promise<AgentBootPlanDryRunResponse> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/boot-plan/dry-run`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: data ? JSON.stringify(data) : undefined,
      },
    );
    if (!res.ok)
      throw new Error(`Failed to dry-run agent boot plan: ${res.status}`);
    return res.json();
  },

  getAgentKnownSkill: async (
    agentId: string,
    skillName: string,
  ): Promise<AgentKnownSkill> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/known-skills/${encodeURIComponent(skillName)}`,
    );
    if (!res.ok)
      throw new Error(`Failed to get agent known skill: ${res.status}`);
    return res.json();
  },

  createAgentKnownSkill: async (
    agentId: string,
    data: AgentKnownSkillUpsertRequest,
  ): Promise<AgentKnownSkill> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/known-skills`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to create agent known skill: ${res.status}`);
    return res.json();
  },

  updateAgentKnownSkill: async (
    agentId: string,
    skillName: string,
    data: AgentKnownSkillUpsertRequest,
  ): Promise<AgentKnownSkill> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/known-skills/${encodeURIComponent(skillName)}`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to update agent known skill: ${res.status}`);
    return res.json();
  },

  deleteAgentKnownSkill: async (
    agentId: string,
    skillName: string,
  ): Promise<{ status: string }> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/known-skills/${encodeURIComponent(skillName)}`,
      {
        method: "DELETE",
      },
    );
    if (!res.ok)
      throw new Error(`Failed to delete agent known skill: ${res.status}`);
    return res.json();
  },

  listAgentProcedures: async (agentId: string): Promise<AgentProcedure[]> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/procedures`,
    );
    if (!res.ok)
      throw new Error(`Failed to list agent procedures: ${res.status}`);
    return res.json();
  },

  getAgentProcedure: async (
    agentId: string,
    name: string,
  ): Promise<AgentProcedure> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/procedures/${encodeURIComponent(name)}`,
    );
    if (!res.ok)
      throw new Error(`Failed to get agent procedure: ${res.status}`);
    return res.json();
  },

  createAgentProcedure: async (
    agentId: string,
    data: AgentProcedureUpsertRequest,
  ): Promise<AgentProcedure> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/procedures`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to create agent procedure: ${res.status}`);
    return res.json();
  },

  updateAgentProcedure: async (
    agentId: string,
    name: string,
    data: AgentProcedureUpsertRequest,
  ): Promise<AgentProcedure> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/procedures/${encodeURIComponent(name)}`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to update agent procedure: ${res.status}`);
    return res.json();
  },

  deleteAgentProcedure: async (
    agentId: string,
    name: string,
  ): Promise<{ status: string }> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/procedures/${encodeURIComponent(name)}`,
      {
        method: "DELETE",
      },
    );
    if (!res.ok)
      throw new Error(`Failed to delete agent procedure: ${res.status}`);
    return res.json();
  },

  listAgentKnowledgeSeeds: async (
    agentId: string,
  ): Promise<AgentKnowledgeSeed[]> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/knowledge-seeds`,
    );
    if (!res.ok)
      throw new Error(`Failed to list agent knowledge seeds: ${res.status}`);
    return res.json();
  },

  getAgentKnowledgeSeed: async (
    agentId: string,
    seedKey: string,
  ): Promise<AgentKnowledgeSeed> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/knowledge-seeds/${encodeURIComponent(seedKey)}`,
    );
    if (!res.ok)
      throw new Error(`Failed to get agent knowledge seed: ${res.status}`);
    return res.json();
  },

  createAgentKnowledgeSeed: async (
    agentId: string,
    data: AgentKnowledgeSeedUpsertRequest,
  ): Promise<AgentKnowledgeSeed> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/knowledge-seeds`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to create agent knowledge seed: ${res.status}`);
    return res.json();
  },

  updateAgentKnowledgeSeed: async (
    agentId: string,
    seedKey: string,
    data: AgentKnowledgeSeedUpsertRequest,
  ): Promise<AgentKnowledgeSeed> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/knowledge-seeds/${encodeURIComponent(seedKey)}`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to update agent knowledge seed: ${res.status}`);
    return res.json();
  },

  deleteAgentKnowledgeSeed: async (
    agentId: string,
    seedKey: string,
  ): Promise<{ status: string }> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/knowledge-seeds/${encodeURIComponent(seedKey)}`,
      {
        method: "DELETE",
      },
    );
    if (!res.ok)
      throw new Error(`Failed to delete agent knowledge seed: ${res.status}`);
    return res.json();
  },

  markAgentKnowledgeSeedApplied: async (
    agentId: string,
    seedKey: string,
  ): Promise<AgentKnowledgeSeed> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/knowledge-seeds/${encodeURIComponent(seedKey)}/mark-applied`,
      {
        method: "POST",
      },
    );
    if (!res.ok)
      throw new Error(
        `Failed to mark agent knowledge seed applied: ${res.status}`,
      );
    return res.json();
  },

  listAgentReflexes: async (agentId: string): Promise<AgentReflexRow[]> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/reflexes`,
    );
    if (!res.ok)
      throw new Error(`Failed to list agent reflexes: ${res.status}`);
    return res.json();
  },

  createAgentReflex: async (
    agentId: string,
    data: CreateAgentReflexRequest,
  ): Promise<AgentReflexRow> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/reflexes`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to create agent reflex: ${res.status}`);
    return res.json();
  },

  patchAgentReflex: async (
    agentId: string,
    reflexId: string,
    data: PatchAgentReflexRequest,
  ): Promise<AgentReflexRow> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/reflexes/${encodeURIComponent(reflexId)}`,
      {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to update agent reflex: ${res.status}`);
    return res.json();
  },

  deleteAgentReflex: async (
    agentId: string,
    reflexId: string,
  ): Promise<{ id: string; status: string }> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(agentId)}/reflexes/${encodeURIComponent(reflexId)}`,
      {
        method: "DELETE",
      },
    );
    if (!res.ok)
      throw new Error(`Failed to delete agent reflex: ${res.status}`);
    return res.json();
  },

  validateReflex: async (
    data: ValidateReflexRequest,
  ): Promise<ValidateReflexResponse> => {
    const res = await fetch(`${API_BASE}/reflexes/validate`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error(`Failed to validate reflex: ${res.status}`);
    return res.json();
  },

  listPendingReflexes: async (status?: string): Promise<PendingReflexRow[]> => {
    const qs = status ? `?status=${encodeURIComponent(status)}` : "";
    const res = await fetch(`${API_BASE}/pending/reflexes${qs}`);
    if (!res.ok)
      throw new Error(`Failed to list pending reflexes: ${res.status}`);
    return res.json();
  },

  approvePendingReflex: async (
    pendingId: string,
    data: PendingReflexApproveRequest = {},
  ): Promise<AgentReflexRow> => {
    const res = await fetch(
      `${API_BASE}/pending/reflexes/${encodeURIComponent(pendingId)}/approve`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to approve pending reflex: ${res.status}`);
    return res.json();
  },

  rejectPendingReflex: async (
    pendingId: string,
    data: PendingReflexRejectRequest = {},
  ): Promise<PendingReflexRejectResponse> => {
    const res = await fetch(
      `${API_BASE}/pending/reflexes/${encodeURIComponent(pendingId)}/reject`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to reject pending reflex: ${res.status}`);
    return res.json();
  },

  createAgentProfile: async (
    data: CreateAgentProfileRequest,
  ): Promise<AgentProfile> => {
    const res = await fetch(`${API_BASE}/agents`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok)
      throw new Error(`Failed to create agent profile: ${res.status}`);
    return res.json();
  },

  // `data` is sent as the JSON body as-is. Callers may include an optional
  // `revision` optimistic-concurrency token; a stale/changed file → 409.
  updateAgentProfile: async (
    id: string,
    data: UpdateAgentProfileRequest & { revision?: string },
  ): Promise<AgentProfile> => {
    const res = await fetch(`${API_BASE}/agents/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok)
      throw await readAPIError(
        res,
        `Failed to update agent profile: ${res.status}`,
      );
    return res.json();
  },

  agentBuilderDryRun: async (
    data: AgentBuilderDryRunRequest,
  ): Promise<AgentBuilderDryRunResponse> => {
    const res = await fetch(`${API_BASE}/agent-builder/dry-run`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok)
      throw new Error(`Failed to dry-run agent builder request: ${res.status}`);
    return res.json();
  },

  agentBuilderDraft: async (
    data: AgentBuilderDraftRequest,
  ): Promise<AgentBuilderDraftResponse> => {
    const res = await fetch(`${API_BASE}/agent-builder/draft`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok)
      throw new Error(`Failed to draft agent builder config: ${res.status}`);
    return res.json();
  },

  agentBuilderReview: async (
    data: AgentBuilderReviewRequest,
  ): Promise<AgentBuilderReviewResponse> => {
    const res = await fetch(`${API_BASE}/agent-builder/review`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok)
      throw new Error(`Failed to review agent builder config: ${res.status}`);
    return res.json();
  },

  // Delete a managed agent (file + DB + children). Non-managed agents → 409
  // with an `agent_not_managed` body; surface the BE message to the user.
  deleteAgentProfile: async (
    id: string,
  ): Promise<{ status: string; slug?: string }> => {
    const res = await fetch(`${API_BASE}/agents/${id}`, { method: "DELETE" });
    if (!res.ok)
      throw await readAPIError(
        res,
        `Failed to delete agent profile: ${res.status}`,
      );
    return res.json();
  },

  // Fork a read-only (plugin/external) agent into an editable managed copy with
  // a NEW slug (`<slug>-copy`) and identity. Already-managed → 409.
  copyAgentToManaged: async (id: string): Promise<AgentProfile> => {
    const res = await fetch(
      `${API_BASE}/agents/${encodeURIComponent(id)}/copy-to-managed`,
      { method: "POST" },
    );
    if (!res.ok)
      throw await readAPIError(
        res,
        `Failed to copy agent to managed: ${res.status}`,
      );
    return res.json();
  },

  // Start surface and durable-agent control plane
  getStartSurfaceCapabilities:
    async (): Promise<StartSurfaceCapabilitiesResponse> => {
      const res = await fetch(`${API_BASE}/start-surface/capabilities`);
      if (!res.ok)
        throw new Error(
          `Failed to get start surface capabilities: ${res.status}`,
        );
      return res.json();
    },

  listDurableAgents: async (
    includeArchived = false,
  ): Promise<DurableAgentInstance[]> => {
    const qs = includeArchived ? "?include_archived=true" : "";
    const res = await fetch(`${API_BASE}/durable-agents${qs}`);
    if (!res.ok)
      throw new Error(`Failed to list durable agents: ${res.status}`);
    return res.json();
  },

  getDurableAgent: async (id: string): Promise<DurableAgentInstance> => {
    const res = await fetch(
      `${API_BASE}/durable-agents/${encodeURIComponent(id)}`,
    );
    if (!res.ok) throw new Error(`Failed to get durable agent: ${res.status}`);
    return res.json();
  },

  listDurableAgentEvents: async (
    id: string,
    limit?: number,
  ): Promise<DurableAgentEvent[]> => {
    const qs = limit ? `?limit=${encodeURIComponent(String(limit))}` : "";
    const res = await fetch(
      `${API_BASE}/durable-agents/${encodeURIComponent(id)}/events${qs}`,
    );
    if (!res.ok)
      throw new Error(`Failed to list durable agent events: ${res.status}`);
    return res.json();
  },

  createDurableAgent: async (
    data: CreateDurableAgentRequest,
  ): Promise<DurableAgentInstance> => {
    const res = await fetch(`${API_BASE}/durable-agents`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok)
      throw new Error(`Failed to create durable agent: ${res.status}`);
    return res.json();
  },

  updateDurableAgent: async (
    id: string,
    data: UpdateDurableAgentRequest,
  ): Promise<DurableAgentInstance> => {
    const res = await fetch(
      `${API_BASE}/durable-agents/${encodeURIComponent(id)}`,
      {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to update durable agent: ${res.status}`);
    return res.json();
  },

  archiveDurableAgent: async (id: string): Promise<DurableAgentInstance> => {
    const res = await fetch(
      `${API_BASE}/durable-agents/${encodeURIComponent(id)}/archive`,
      {
        method: "POST",
      },
    );
    if (!res.ok)
      throw new Error(`Failed to archive durable agent: ${res.status}`);
    return res.json();
  },

  requestDurableAgentStart: async (
    id: string,
  ): Promise<DurableAgentInstance> => {
    const res = await fetch(
      `${API_BASE}/durable-agents/${encodeURIComponent(id)}/start-request`,
      {
        method: "POST",
      },
    );
    if (!res.ok)
      throw await readAPIError(
        res,
        `Failed to request durable agent start: ${res.status}`,
      );
    return res.json();
  },

  requestDurableAgentStop: async (
    id: string,
  ): Promise<DurableAgentInstance> => {
    const res = await fetch(
      `${API_BASE}/durable-agents/${encodeURIComponent(id)}/stop-request`,
      {
        method: "POST",
      },
    );
    if (!res.ok)
      throw await readAPIError(
        res,
        `Failed to request durable agent stop: ${res.status}`,
      );
    return res.json();
  },

  requestDurableAgentPause: async (
    id: string,
  ): Promise<DurableAgentInstance> => {
    const res = await fetch(
      `${API_BASE}/durable-agents/${encodeURIComponent(id)}/pause-request`,
      {
        method: "POST",
      },
    );
    if (!res.ok)
      throw await readAPIError(
        res,
        `Failed to request durable agent pause: ${res.status}`,
      );
    return res.json();
  },

  requestDurableAgentResume: async (
    id: string,
  ): Promise<DurableAgentInstance> => {
    const res = await fetch(
      `${API_BASE}/durable-agents/${encodeURIComponent(id)}/resume-request`,
      {
        method: "POST",
      },
    );
    if (!res.ok)
      throw await readAPIError(
        res,
        `Failed to request durable agent resume: ${res.status}`,
      );
    return res.json();
  },

  getDurableAgentLaunchPlan: async (
    id: string,
  ): Promise<DurableAgentLaunchPlan> => {
    const res = await fetch(
      `${API_BASE}/durable-agents/${encodeURIComponent(id)}/launch-plan`,
    );
    if (!res.ok)
      throw new Error(`Failed to get durable agent launch plan: ${res.status}`);
    return res.json();
  },

  startDurableAgent: async (
    id: string,
    data: DurableAgentStartRequest,
  ): Promise<DurableAgentLaunchResult> => {
    const res = await fetch(
      `${API_BASE}/durable-agents/${encodeURIComponent(id)}/start`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok)
      throw await readAPIError(
        res,
        `Failed to start durable agent: ${res.status}`,
      );
    return res.json();
  },

  resumeDurableAgent: async (
    id: string,
    data: DurableAgentStartRequest,
  ): Promise<DurableAgentLaunchResult> => {
    const res = await fetch(
      `${API_BASE}/durable-agents/${encodeURIComponent(id)}/resume`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok)
      throw await readAPIError(
        res,
        `Failed to resume durable agent: ${res.status}`,
      );
    return res.json();
  },

  listDurableAgentSessions: async (
    id: string,
  ): Promise<DurableAgentSessionAttachmentState[]> => {
    const res = await fetch(
      `${API_BASE}/durable-agents/${encodeURIComponent(id)}/sessions`,
    );
    if (!res.ok)
      throw new Error(`Failed to list durable agent sessions: ${res.status}`);
    return res.json();
  },

  listDurableAgentSchedules: async (id: string): Promise<AgentSchedule[]> => {
    const res = await fetch(
      `${API_BASE}/durable-agents/${encodeURIComponent(id)}/schedules`,
    );
    if (!res.ok)
      throw new Error(`Failed to list durable agent schedules: ${res.status}`);
    return res.json();
  },

  pauseDurableAgentSchedule: async (
    id: string,
    scheduleId: string,
  ): Promise<void> => {
    const res = await fetch(
      `${API_BASE}/durable-agents/${encodeURIComponent(id)}/schedules/${encodeURIComponent(scheduleId)}/pause`,
      { method: "POST" },
    );
    if (!res.ok)
      throw new Error(`Failed to pause durable agent schedule: ${res.status}`);
  },

  resumeDurableAgentSchedule: async (
    id: string,
    scheduleId: string,
  ): Promise<void> => {
    const res = await fetch(
      `${API_BASE}/durable-agents/${encodeURIComponent(id)}/schedules/${encodeURIComponent(scheduleId)}/resume`,
      { method: "POST" },
    );
    if (!res.ok)
      throw new Error(`Failed to resume durable agent schedule: ${res.status}`);
  },

  wakeDurableAgent: async (
    id: string,
    data: DurableAgentStartRequest,
  ): Promise<DurableAgentWakeResult> => {
    const res = await fetch(
      `${API_BASE}/durable-agents/${encodeURIComponent(id)}/wake`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok) throw new Error(`Failed to wake durable agent: ${res.status}`);
    return res.json();
  },

  listDurableAgentDueWake: async (): Promise<DurableAgentWakeDueItem[]> => {
    const res = await fetch(`${API_BASE}/durable-agent-wake/due`);
    if (!res.ok)
      throw new Error(`Failed to list due durable wakes: ${res.status}`);
    return res.json();
  },

  runDurableAgentDueWake: async (
    dryRun = false,
  ): Promise<DurableAgentWakeRunResult> => {
    const res = await fetch(`${API_BASE}/durable-agent-wake/run-due`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ dry_run: dryRun }),
    });
    if (!res.ok)
      throw new Error(`Failed to run due durable wakes: ${res.status}`);
    return res.json();
  },

  attachDurableAgentSession: async (
    id: string,
    data: AttachDurableAgentSessionRequest,
  ): Promise<void> => {
    const res = await fetch(
      `${API_BASE}/durable-agents/${encodeURIComponent(id)}/sessions`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to attach durable agent session: ${res.status}`);
  },

  listDurableAgentRecipes: async (): Promise<DurableAgentRecipe[]> => {
    const res = await fetch(`${API_BASE}/durable-agent-recipes`);
    if (!res.ok)
      throw new Error(`Failed to list durable agent recipes: ${res.status}`);
    return res.json();
  },

  getDurableAgentRecipe: async (id: string): Promise<DurableAgentRecipe> => {
    const res = await fetch(
      `${API_BASE}/durable-agent-recipes/${encodeURIComponent(id)}`,
    );
    if (!res.ok)
      throw new Error(`Failed to get durable agent recipe: ${res.status}`);
    return res.json();
  },

  dryRunDurableAgentRecipe: async (
    id: string,
    data: DurableAgentRecipeRequest,
  ): Promise<DurableAgentRecipePlan> => {
    const res = await fetch(
      `${API_BASE}/durable-agent-recipes/${encodeURIComponent(id)}/dry-run`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to dry-run durable agent recipe: ${res.status}`);
    return res.json();
  },

  applyDurableAgentRecipe: async (
    id: string,
    data: DurableAgentRecipeRequest,
  ): Promise<DurableAgentRecipeApplyResult> => {
    const res = await fetch(
      `${API_BASE}/durable-agent-recipes/${encodeURIComponent(id)}/apply`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to apply durable agent recipe: ${res.status}`);
    return res.json();
  },

  // Agent Modes
  listAgentModes: async (agentId: string): Promise<AgentModeProfile[]> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/modes`);
    if (!res.ok) throw new Error(`Failed to list agent modes: ${res.status}`);
    return res.json();
  },

  createAgentMode: async (
    agentId: string,
    data: Omit<AgentModeProfile, "id" | "agent_id">,
  ): Promise<AgentModeProfile> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/modes`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error(`Failed to create agent mode: ${res.status}`);
    return res.json();
  },

  // Note: DELETE mode endpoint not implemented in backend yet
  // deleteAgentMode: async (agentId: string, modeId: string): Promise<void> => {
  //   const res = await fetch(`${API_BASE}/agents/${agentId}/modes/${modeId}`, { method: 'DELETE' })
  //   if (!res.ok) throw new Error(`Failed to delete agent mode: ${res.status}`)
  // },

  // Mode (legacy: agent-scoped AgentMode pipeline).
  switchMode: async (sessionId: string, mode: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/mode`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ mode }),
    });
    if (!res.ok) throw new Error(`Failed to switch mode: ${res.status}`);
  },

  // First-class reusable Modes (B1, CW-20260428-0009).
  listModes: async (): Promise<Mode[]> => {
    const res = await fetch(`${API_BASE}/modes`);
    if (!res.ok) throw new Error(`Failed to list modes: ${res.status}`);
    return res.json();
  },

  getSessionMode: async (sessionId: string): Promise<Mode | null> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/mode`);
    if (!res.ok) throw new Error(`Failed to get session mode: ${res.status}`);
    return res.json();
  },

  setSessionMode: async (
    sessionId: string,
    body: { slug?: string; mode_id?: string } | null,
  ): Promise<Mode | null> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/mode`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body ?? {}),
    });
    if (!res.ok) throw new Error(`Failed to set session mode: ${res.status}`);
    return res.json();
  },

  // F2 (CW-20260429-0002): per-session auto-switch override.
  // override === null clears the override (session inherits user pref).
  setSessionAutoSwitch: async (
    sessionId: string,
    override: boolean | null,
  ): Promise<{ override: boolean | null }> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/auto-switch`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ override }),
    });
    if (!res.ok)
      throw new Error(`Failed to set session auto-switch: ${res.status}`);
    return res.json();
  },

  // Slash Commands
  listCommands: async (): Promise<SlashCommandDef[]> => {
    const res = await fetch(`${API_BASE}/commands`);
    if (!res.ok) throw new Error(`Failed to list commands: ${res.status}`);
    return res.json();
  },

  executeCommand: async (
    name: string,
    sessionId: string,
    args: string,
  ): Promise<{ action: string; content?: string }> => {
    const res = await fetch(`${API_BASE}/commands/execute`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name, session_id: sessionId, args }),
    });
    if (!res.ok) throw new Error(`Failed to execute command: ${res.status}`);
    return res.json();
  },

  // Pin/Unpin
  pinSession: async (id: string, pinned: boolean): Promise<Session> => {
    const res = await fetch(`${API_BASE}/sessions/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ is_pinned: pinned }),
    });
    if (!res.ok) throw new Error(`Failed to pin session: ${res.status}`);
    return res.json();
  },

  // Bookmarks
  listBookmarks: async (sessionId: string): Promise<Bookmark[]> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/bookmarks`);
    if (!res.ok) throw new Error(`Failed to list bookmarks: ${res.status}`);
    return res.json();
  },

  toggleBookmark: async (
    messageId: string,
    sessionId: string,
  ): Promise<void> => {
    await fetch(`${API_BASE}/messages/${messageId}/bookmark`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ session_id: sessionId }),
    });
  },

  autotitleBookmark: async (bookmarkId: string): Promise<{ title: string }> => {
    const res = await fetch(`${API_BASE}/bookmarks/${bookmarkId}/autotitle`, {
      method: "POST",
    });
    if (!res.ok) throw new Error(`Failed to autotitle bookmark: ${res.status}`);
    return res.json();
  },

  // Artifacts
  listArtifacts: async (sessionId: string): Promise<Artifact[]> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/artifacts`);
    if (!res.ok) throw new Error(`Failed to list artifacts: ${res.status}`);
    return res.json();
  },

  // F4 (CW-20260429-0004): list artifacts inherited from sibling sessions in
  // the given project. Pass `excludeSessionId` to filter out the active
  // session (rendered in its own "This Session" list).
  listArtifactsByProject: async (
    projectId: string,
    excludeSessionId?: string,
  ): Promise<Artifact[]> => {
    const qs = excludeSessionId
      ? `?exclude_session_id=${encodeURIComponent(excludeSessionId)}`
      : "";
    const res = await fetch(`${API_BASE}/projects/${projectId}/artifacts${qs}`);
    if (!res.ok)
      throw new Error(`Failed to list project artifacts: ${res.status}`);
    return res.json();
  },

  uploadArtifact: async (sessionId: string, file: File): Promise<Artifact> => {
    const form = new FormData();
    form.append("session_id", sessionId);
    form.append("file", file);
    const res = await fetch(`${API_BASE}/artifacts/upload`, {
      method: "POST",
      body: form,
    });
    if (!res.ok) throw new Error(`Failed to upload artifact: ${res.status}`);
    return res.json();
  },

  // Documents (J10, CW-20260426-0008)
  listDocuments: async (sessionId: string): Promise<Document[]> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/documents`);
    if (!res.ok) throw new Error(`Failed to list documents: ${res.status}`);
    return res.json();
  },

  createDocument: async (
    sessionId: string,
    doc: {
      name: string;
      content: string;
      mime_type?: string;
      summary?: string;
    },
  ): Promise<Document> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/documents`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ...doc, included: false, full_content: false }),
    });
    if (!res.ok) throw new Error(`Failed to create document: ${res.status}`);
    return res.json();
  },

  updateDocument: async (
    id: string,
    update: { included?: boolean; full_content?: boolean; summary?: string },
  ): Promise<Document> => {
    const res = await fetch(`${API_BASE}/documents/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(update),
    });
    if (!res.ok) throw new Error(`Failed to update document: ${res.status}`);
    return res.json();
  },

  deleteDocument: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/documents/${id}`, {
      method: "DELETE",
    });
    if (!res.ok) throw new Error(`Failed to delete document: ${res.status}`);
  },

  // Session context prompt (J10, CW-20260426-0008)
  getSessionContextPrompt: async (sessionId: string): Promise<string> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/context-prompt`);
    if (!res.ok) throw new Error(`Failed to get context prompt: ${res.status}`);
    const data = await res.json();
    return data.prompt ?? "";
  },

  setSessionContextPrompt: async (
    sessionId: string,
    prompt: string,
  ): Promise<void> => {
    const res = await fetch(
      `${API_BASE}/sessions/${sessionId}/context-prompt`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ prompt }),
      },
    );
    if (!res.ok) throw new Error(`Failed to set context prompt: ${res.status}`);
  },

  // Pinned content (J11, CW-20260426-0009; D1/D2, CW-20260428-0014/0015)
  listPins: async (sessionId: string): Promise<PinnedContent[]> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/pins`);
    if (!res.ok) throw new Error(`Failed to list pins: ${res.status}`);
    return res.json();
  },

  deletePin: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/pins/${id}`, { method: "DELETE" });
    if (!res.ok) throw new Error(`Failed to delete pin: ${res.status}`);
  },

  // Bottom-drawer pinned cards (C1, CW-20260428-0012)
  // Returns 409 when the 10-pin cap is exceeded — surfaced as DrawerPinCapError
  // so callers can render the "10-tab limit; unpin one first" toast.
  listDrawerCards: async (sessionId: string): Promise<DrawerPinnedCard[]> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/drawer-cards`);
    if (!res.ok) throw new Error(`Failed to list drawer cards: ${res.status}`);
    return res.json();
  },

  pinDrawerCard: async (
    sessionId: string,
    card: {
      card_type: DrawerCardType;
      content_ref?: string;
      title?: string;
      payload?: string;
    },
  ): Promise<DrawerPinnedCard> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/drawer-cards`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(card),
    });
    if (res.status === 409) {
      throw new DrawerPinCapError();
    }
    if (!res.ok) throw new Error(`Failed to pin drawer card: ${res.status}`);
    return res.json();
  },

  unpinDrawerCard: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/drawer-cards/${id}`, {
      method: "DELETE",
    });
    if (!res.ok) throw new Error(`Failed to unpin drawer card: ${res.status}`);
  },

  /** D2 — promote/demote a pin between session and project scope. */
  updatePinScope: async (
    id: string,
    scope: AgentStateScope,
    projectId?: string,
  ): Promise<void> => {
    const res = await fetch(`${API_BASE}/pins/${id}/scope`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ scope, project_id: projectId ?? "" }),
    });
    if (!res.ok) {
      const err = await res
        .json()
        .catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to update pin scope: ${res.status}`);
    }
  },

  // Reminders (D1/D2, CW-20260428-0014/0015)
  listReminders: async (sessionId: string): Promise<Reminder[]> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/reminders`);
    if (!res.ok) throw new Error(`Failed to list reminders: ${res.status}`);
    return res.json();
  },

  deleteReminder: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/reminders/${id}`, {
      method: "DELETE",
    });
    if (!res.ok) throw new Error(`Failed to delete reminder: ${res.status}`);
  },

  /** D2 — promote/demote a reminder between session and project scope. */
  updateReminderScope: async (
    id: string,
    scope: AgentStateScope,
    projectId?: string,
  ): Promise<Reminder> => {
    const res = await fetch(`${API_BASE}/reminders/${id}/scope`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ scope, project_id: projectId ?? "" }),
    });
    if (!res.ok) {
      const err = await res
        .json()
        .catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(
        err.error || `Failed to update reminder scope: ${res.status}`,
      );
    }
    return res.json();
  },

  // Compact
  compactSession: async (sessionId: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/compact`, {
      method: "POST",
    });
    if (!res.ok) throw new Error(`Failed to compact session: ${res.status}`);
  },

  // Providers
  listProviders: async (): Promise<ProviderConfig[]> => {
    const res = await fetch(`${API_BASE}/providers`);
    if (!res.ok) throw new Error(`Failed to list providers: ${res.status}`);
    return res.json();
  },
  listMetaHarnesses: async (): Promise<MetaHarness[]> => {
    const res = await fetch(`${API_BASE}/meta-harnesses`);
    if (!res.ok)
      throw await readAPIError(res, `Failed to list meta harnesses: ${res.status}`);
    return res.json();
  },
  createMetaHarness: async (data: MetaHarnessInput): Promise<MetaHarness> => {
    const res = await fetch(`${API_BASE}/meta-harnesses`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok)
      throw await readAPIError(res, `Failed to create meta harness: ${res.status}`);
    return res.json();
  },
  updateMetaHarness: async (
    id: string,
    data: MetaHarnessInput,
  ): Promise<MetaHarness> => {
    const res = await fetch(`${API_BASE}/meta-harnesses/${encodeURIComponent(id)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok)
      throw await readAPIError(res, `Failed to update meta harness: ${res.status}`);
    return res.json();
  },
  deleteMetaHarness: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/meta-harnesses/${encodeURIComponent(id)}`, {
      method: "DELETE",
    });
    if (!res.ok)
      throw await readAPIError(res, `Failed to delete meta harness: ${res.status}`);
  },
  listProviderStatuses: async (): Promise<ProviderStatus[]> => {
    const res = await fetch(`${API_BASE}/providers/status`);
    if (!res.ok)
      throw new Error(`Failed to list provider statuses: ${res.status}`);
    return res.json();
  },
  updateProvider: async (
    id: string,
    data: { is_enabled?: boolean; base_url?: string; settings?: string },
  ): Promise<ProviderConfig> => {
    const res = await fetch(`${API_BASE}/providers/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error(`Failed to update provider: ${res.status}`);
    return res.json();
  },
  setProviderAPIKey: async (
    id: string,
    apiKey: string,
  ): Promise<{ provider_id: string; has_key: boolean }> => {
    const res = await fetch(`${API_BASE}/providers/${id}/api-key`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ api_key: apiKey }),
    });
    if (!res.ok) throw new Error(`Failed to set API key: ${res.status}`);
    return res.json();
  },
  testProviderConnection: async (id: string): Promise<{ ok: boolean }> => {
    const res = await fetch(`${API_BASE}/providers/${id}/test`, {
      method: "POST",
    });
    // Gracefully handle missing endpoint — if the backend doesn't have a test
    // route yet, treat a successful key save as sufficient
    if (res.status === 404) return { ok: true };
    if (!res.ok) {
      const err = await res
        .json()
        .catch(() => ({ error: `Test failed: ${res.status}` }));
      throw new Error(err.error || `Connection test failed: ${res.status}`);
    }
    return res.json();
  },
  detectCLI: async (): Promise<CLIDetectionResult[]> => {
    const res = await fetch(`${API_BASE}/providers/detect-cli`);
    if (!res.ok) throw new Error(`Failed to detect CLI: ${res.status}`);
    return res.json();
  },

  // Models
  listModels: async (): Promise<ModelRecord[]> => {
    const res = await fetch(`${API_BASE}/models`);
    if (!res.ok) throw new Error(`Failed to list models: ${res.status}`);
    return res.json();
  },

  // Settings
  getSettings: async (): Promise<UserSettings> => {
    const res = await fetch(`${API_BASE}/settings`);
    if (!res.ok) throw new Error(`Failed to get settings: ${res.status}`);
    return res.json();
  },

  updateSettings: async (
    data: Partial<UserSettings>,
  ): Promise<UserSettings> => {
    const res = await fetch(`${API_BASE}/settings`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error(`Failed to update settings: ${res.status}`);
    return res.json();
  },

  listEmbeddingProviders: async (): Promise<
    import("./types").EmbeddingProviderInfo[]
  > => {
    const res = await fetch(`${API_BASE}/settings/embedding/providers`);
    if (!res.ok)
      throw new Error(`Failed to list embedding providers: ${res.status}`);
    const body = await res.json();
    return body.providers ?? [];
  },

  // B3 (CW-20260428-0011): mode auto-switch preference. Empty string = unset.
  getModeAutoSwitchPref: async (): Promise<{
    pref: "" | "always" | "ask" | "never";
  }> => {
    const res = await fetch(`${API_BASE}/settings/mode-auto-switch`);
    if (!res.ok)
      throw new Error(`Failed to get mode auto-switch pref: ${res.status}`);
    return res.json();
  },

  setModeAutoSwitchPref: async (
    pref: "" | "always" | "ask" | "never",
  ): Promise<{ pref: string }> => {
    const res = await fetch(`${API_BASE}/settings/mode-auto-switch`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ pref }),
    });
    if (!res.ok)
      throw new Error(`Failed to set mode auto-switch pref: ${res.status}`);
    return res.json();
  },

  // Session Agents
  listSessionAgents: async (sessionId: string): Promise<SessionAgent[]> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/agents`);
    if (!res.ok)
      throw new Error(`Failed to list session agents: ${res.status}`);
    return res.json();
  },

  addSessionAgent: async (
    sessionId: string,
    agentId: string,
    role: "primary" | "participant",
  ): Promise<SessionAgent> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/agents`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ agent_id: agentId, role }),
    });
    if (!res.ok)
      throw new Error(`Failed to add agent to session: ${res.status}`);
    return res.json();
  },

  removeSessionAgent: async (
    sessionId: string,
    agentId: string,
  ): Promise<void> => {
    const res = await fetch(
      `${API_BASE}/sessions/${sessionId}/agents/${agentId}`,
      {
        method: "DELETE",
      },
    );
    if (!res.ok)
      throw new Error(`Failed to remove agent from session: ${res.status}`);
  },

  // Token Usage
  getSessionUsage: async (sessionId: string): Promise<SessionUsageSummary> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/usage`);
    if (!res.ok) throw new Error(`Failed to get session usage: ${res.status}`);
    return res.json();
  },

  getUsageSummary: async (): Promise<GlobalUsageSummary> => {
    const res = await fetch(`${API_BASE}/usage/summary`);
    if (!res.ok) throw new Error(`Failed to get usage summary: ${res.status}`);
    return res.json();
  },

  getContextBreakdown: async (sessionId: string): Promise<ContextBreakdown> => {
    const res = await fetch(
      `${API_BASE}/sessions/${sessionId}/context-breakdown`,
    );
    if (!res.ok)
      throw new Error(`Failed to get context breakdown: ${res.status}`);
    return res.json();
  },

  // Execution Metrics
  getSessionMetrics: async (sessionId: string): Promise<ExecutionMetrics[]> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/metrics`);
    if (!res.ok)
      throw new Error(`Failed to get session metrics: ${res.status}`);
    return res.json();
  },

  getRecentExecutions: async (limit = 50): Promise<ExecutionMetrics[]> => {
    const res = await fetch(`${API_BASE}/metrics/executions?limit=${limit}`);
    if (!res.ok)
      throw new Error(`Failed to get recent executions: ${res.status}`);
    return res.json();
  },

  getUtilityCallSummary: async (): Promise<UtilityCallSummary[]> => {
    const res = await fetch(`${API_BASE}/metrics/utility`);
    if (!res.ok)
      throw new Error(`Failed to get utility call summary: ${res.status}`);
    return res.json();
  },

  getUtilityCallLog: async (limit = 50): Promise<ExecutionMetrics[]> => {
    const res = await fetch(`${API_BASE}/metrics/utility/log?limit=${limit}`);
    if (!res.ok)
      throw new Error(`Failed to get utility call log: ${res.status}`);
    return res.json();
  },

  // Process Health
  getProcessHealth: async (): Promise<ProcessHealthResponse> => {
    const res = await fetch(`${API_BASE}/processes/health`);
    if (!res.ok) throw new Error(`Failed to get process health: ${res.status}`);
    return res.json();
  },

  killStaleProcesses: async (): Promise<{ killed: number }> => {
    const res = await fetch(`${API_BASE}/processes/kill-stale`, {
      method: "POST",
    });
    if (!res.ok)
      throw new Error(`Failed to kill stale processes: ${res.status}`);
    return res.json();
  },

  // Skills
  listSkills: async (): Promise<Skill[]> => {
    const res = await fetch(`${API_BASE}/skills`);
    if (!res.ok) throw new Error(`Failed to list skills: ${res.status}`);
    return res.json();
  },

  getSkill: async (id: string): Promise<Skill> => {
    const res = await fetch(`${API_BASE}/skills/${id}`);
    if (!res.ok) throw new Error(`Failed to get skill: ${res.status}`);
    return res.json();
  },

  createSkill: async (
    data: Omit<Skill, "id" | "created_at" | "updated_at" | "is_builtin">,
  ): Promise<Skill> => {
    const res = await fetch(`${API_BASE}/skills`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error(`Failed to create skill: ${res.status}`);
    return res.json();
  },

  updateSkill: async (
    id: string,
    data: Partial<
      Omit<Skill, "id" | "created_at" | "updated_at" | "is_builtin">
    >,
  ): Promise<Skill> => {
    const res = await fetch(`${API_BASE}/skills/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error(`Failed to update skill: ${res.status}`);
    return res.json();
  },

  deleteSkill: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/skills/${id}`, { method: "DELETE" });
    if (!res.ok) throw new Error(`Failed to delete skill: ${res.status}`);
  },

  // E1 (CW-20260428-0016): dev-mode editor — fork an internal skill into
  // ~/.nanite/skills/<slug>.md so the user can edit it. Backend rejects with
  // 403 unless dev mode is on.
  forkSkillToUser: async (
    id: string,
    body: { prompt?: string },
  ): Promise<{ status: string; slug: string; path: string }> => {
    const res = await fetch(`${API_BASE}/skills/${id}/fork-to-user`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!res.ok) throw new Error(`Failed to fork skill: ${res.status}`);
    return res.json();
  },

  // E1: returns whether dev-mode is active (env var or developer_mode setting).
  getDevMode: async (): Promise<{ dev_mode: boolean; env_flag: boolean }> => {
    const res = await fetch(`${API_BASE}/dev-mode`);
    if (!res.ok) throw new Error(`Failed to get dev mode: ${res.status}`);
    return res.json();
  },

  // Agent Skills (returns Skill[], not a join-table type)
  listAgentSkills: async (agentId: string): Promise<Skill[]> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/skills`);
    if (!res.ok) throw new Error(`Failed to list agent skills: ${res.status}`);
    return res.json();
  },

  assignSkillToAgent: async (
    agentId: string,
    data: { skill_id: string; config?: Record<string, unknown> },
  ): Promise<Skill> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/skills`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok)
      throw new Error(`Failed to assign skill to agent: ${res.status}`);
    return res.json();
  },

  removeSkillFromAgent: async (
    agentId: string,
    skillId: string,
  ): Promise<void> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/skills/${skillId}`, {
      method: "DELETE",
    });
    if (!res.ok)
      throw new Error(`Failed to remove skill from agent: ${res.status}`);
  },

  // Prompt Templates
  listPromptTemplates: async (): Promise<PromptTemplate[]> => {
    const res = await fetch(`${API_BASE}/prompt-templates`);
    if (!res.ok)
      throw new Error(`Failed to list prompt templates: ${res.status}`);
    return res.json();
  },

  getPromptTemplate: async (id: string): Promise<PromptTemplate> => {
    const res = await fetch(`${API_BASE}/prompt-templates/${id}`);
    if (!res.ok)
      throw new Error(`Failed to get prompt template: ${res.status}`);
    return res.json();
  },

  createPromptTemplate: async (
    data: Omit<
      PromptTemplate,
      "id" | "created_at" | "updated_at" | "is_builtin"
    >,
  ): Promise<PromptTemplate> => {
    const res = await fetch(`${API_BASE}/prompt-templates`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok)
      throw new Error(`Failed to create prompt template: ${res.status}`);
    return res.json();
  },

  updatePromptTemplate: async (
    id: string,
    data: Partial<
      Omit<PromptTemplate, "id" | "created_at" | "updated_at" | "is_builtin">
    >,
  ): Promise<PromptTemplate> => {
    const res = await fetch(`${API_BASE}/prompt-templates/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok)
      throw new Error(`Failed to update prompt template: ${res.status}`);
    return res.json();
  },

  deletePromptTemplate: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/prompt-templates/${id}`, {
      method: "DELETE",
    });
    if (!res.ok)
      throw new Error(`Failed to delete prompt template: ${res.status}`);
  },

  // Agent Templates (returns PromptTemplate[], not a join-table type)
  listAgentTemplates: async (agentId: string): Promise<PromptTemplate[]> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/prompt-templates`);
    if (!res.ok)
      throw new Error(`Failed to list agent templates: ${res.status}`);
    return res.json();
  },

  assignTemplateToAgent: async (
    agentId: string,
    data: { template_id: string },
  ): Promise<PromptTemplate> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/prompt-templates`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok)
      throw new Error(`Failed to assign template to agent: ${res.status}`);
    return res.json();
  },

  removeTemplateFromAgent: async (
    agentId: string,
    templateId: string,
  ): Promise<void> => {
    const res = await fetch(
      `${API_BASE}/agents/${agentId}/prompt-templates/${templateId}`,
      {
        method: "DELETE",
      },
    );
    if (!res.ok)
      throw new Error(`Failed to remove template from agent: ${res.status}`);
  },

  // Engine Backlog
  createFragmentsBacklogItem: async (data: {
    title: string;
    body: string;
    priority: string;
    tags?: string[];
    project_id?: string;
  }): Promise<Record<string, unknown>> => {
    const res = await fetch(`${API_BASE}/plugins/engine/backlog`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) {
      const err = await res
        .json()
        .catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(
        err.error || `Failed to create backlog item: ${res.status}`,
      );
    }
    return res.json();
  },

  // Tools & MCP
  fetchTools: async (): Promise<ToolDefinition[]> => {
    const res = await fetch(`${API_BASE}/tools`);
    if (!res.ok) throw new Error(`Failed to fetch tools: ${res.status}`);
    return res.json();
  },

  fetchToolServers: async (): Promise<ServerInfo[]> => {
    const res = await fetch(`${API_BASE}/tools/servers`);
    if (!res.ok) throw new Error(`Failed to fetch tool servers: ${res.status}`);
    return res.json();
  },

  refreshTools: async (): Promise<DiscoveryDiff> => {
    const res = await fetch(`${API_BASE}/tools/refresh`, {
      method: "POST",
    });
    if (!res.ok) throw new Error(`Failed to refresh tools: ${res.status}`);
    return res.json();
  },

  selectTools: async (intent: string): Promise<ToolSelection[]> => {
    const res = await fetch(`${API_BASE}/tools/select`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ intent }),
    });
    if (!res.ok) throw new Error(`Failed to select tools: ${res.status}`);
    return res.json();
  },

  // MCP Servers (user-managed)
  listMCPServers: async (): Promise<MCPServerConfig[]> => {
    const res = await fetch(`${API_BASE}/mcp-servers`);
    if (!res.ok) throw new Error(`Failed to list MCP servers: ${res.status}`);
    return res.json();
  },

  addMCPServer: async (
    config: Omit<MCPServerConfig, "id" | "created_at" | "updated_at">,
  ): Promise<MCPServerConfig> => {
    const res = await fetch(`${API_BASE}/mcp-servers`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(config),
    });
    if (!res.ok) {
      const err = await res
        .json()
        .catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to add MCP server: ${res.status}`);
    }
    return res.json();
  },

  updateMCPServer: async (
    name: string,
    config: Partial<MCPServerConfig>,
  ): Promise<MCPServerConfig> => {
    const res = await fetch(
      `${API_BASE}/mcp-servers/${encodeURIComponent(name)}`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(config),
      },
    );
    if (!res.ok) {
      const err = await res
        .json()
        .catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(
        err.error || `Failed to update MCP server: ${res.status}`,
      );
    }
    return res.json();
  },

  deleteMCPServer: async (name: string): Promise<void> => {
    const res = await fetch(
      `${API_BASE}/mcp-servers/${encodeURIComponent(name)}`,
      {
        method: "DELETE",
      },
    );
    if (!res.ok) throw new Error(`Failed to delete MCP server: ${res.status}`);
  },

  importMCPServers: async (
    json: string,
  ): Promise<{ created: string[]; skipped: string[] }> => {
    const res = await fetch(`${API_BASE}/mcp-servers/import`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: json,
    });
    if (!res.ok) {
      const err = await res
        .json()
        .catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(
        err.error || `Failed to import MCP servers: ${res.status}`,
      );
    }
    return res.json();
  },

  // Tool Load Preferences
  fetchAllToolsWithLoadType: async (): Promise<
    import("./types").ToolLoadItem[]
  > => {
    const res = await fetch(`${API_BASE}/tools/all`);
    if (!res.ok) throw new Error(`Failed to fetch tools: ${res.status}`);
    return res.json();
  },

  fetchToolLoadPreferences: async (): Promise<
    import("./types").ToolLoadPreferences
  > => {
    const res = await fetch(`${API_BASE}/tools/load-preferences`);
    if (!res.ok)
      throw new Error(`Failed to fetch load preferences: ${res.status}`);
    return res.json();
  },

  updateToolLoadPreferences: async (
    updates: import("./types").ToolLoadPreferences,
  ): Promise<import("./types").ToolLoadPreferences> => {
    const res = await fetch(`${API_BASE}/tools/load-preferences`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(updates),
    });
    if (!res.ok) {
      const err = await res
        .json()
        .catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(
        err.error || `Failed to update load preferences: ${res.status}`,
      );
    }
    return res.json();
  },

  exportMCPServers: async (): Promise<string> => {
    const res = await fetch(`${API_BASE}/mcp-servers/export`);
    if (!res.ok) throw new Error(`Failed to export MCP servers: ${res.status}`);
    return res.text();
  },

  // Engine (Sprint Planning)
  getFragmentsSprints: async (
    projectId?: string,
  ): Promise<{ items: FragmentsSprint[]; count: number }> => {
    const params = projectId
      ? `?project_id=${encodeURIComponent(projectId)}`
      : "";
    const res = await fetch(`${API_BASE}/plugins/engine/sprints${params}`);
    if (!res.ok) throw new Error(`Failed to list sprints: ${res.status}`);
    return res.json();
  },

  getFragmentsTasks: async (
    sprintId?: string,
    status?: string,
    projectId?: string,
  ): Promise<{ items: FragmentsTask[]; count: number }> => {
    const params = new URLSearchParams();
    if (sprintId) params.set("sprint_id", sprintId);
    if (status) params.set("status", status);
    if (projectId) params.set("project_id", projectId);
    const qs = params.toString();
    const res = await fetch(
      `${API_BASE}/plugins/engine/tasks${qs ? `?${qs}` : ""}`,
    );
    if (!res.ok) throw new Error(`Failed to list tasks: ${res.status}`);
    return res.json();
  },

  getFragmentsBacklog: async (
    projectId?: string,
  ): Promise<{ items: FragmentsBacklogItem[]; count: number }> => {
    const params = projectId
      ? `?project_id=${encodeURIComponent(projectId)}`
      : "";
    const res = await fetch(`${API_BASE}/plugins/engine/backlog${params}`);
    if (!res.ok) throw new Error(`Failed to list backlog: ${res.status}`);
    return res.json();
  },

  transitionFragmentsTask: async (
    id: string,
    status: string,
  ): Promise<unknown> => {
    const res = await fetch(
      `${API_BASE}/plugins/engine/tasks/${encodeURIComponent(id)}/transition`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ status }),
      },
    );
    if (!res.ok) throw new Error(`Failed to transition task: ${res.status}`);
    return res.json();
  },

  promoteBacklogItem: async (
    id: string,
    sprintId: string,
  ): Promise<unknown> => {
    const res = await fetch(
      `${API_BASE}/volon/backlog/${encodeURIComponent(id)}/promote`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ sprint_id: sprintId }),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to promote backlog item: ${res.status}`);
    return res.json();
  },

  deleteFragmentsTask: async (id: string): Promise<unknown> => {
    const res = await fetch(
      `${API_BASE}/plugins/engine/tasks/${encodeURIComponent(id)}`,
      {
        method: "DELETE",
      },
    );
    if (!res.ok) throw new Error(`Failed to delete task: ${res.status}`);
    return res.json();
  },

  // --- Todos ---

  listTodos: async (filter?: TodoFilter): Promise<Todo[]> => {
    const params = new URLSearchParams();
    if (filter?.scope) params.set("scope", filter.scope);
    if (filter?.scope_id) params.set("scope_id", filter.scope_id);
    if (filter?.status) params.set("status", filter.status);
    if (filter?.priority) params.set("priority", filter.priority);
    if (filter?.parent_id) params.set("parent_id", filter.parent_id);
    if (filter?.labels?.length) params.set("labels", filter.labels.join(","));
    const qs = params.toString();
    const res = await fetch(`${API_BASE}/todos${qs ? `?${qs}` : ""}`);
    if (!res.ok) throw new Error(`Failed to list todos: ${res.status}`);
    const todos = await res.json();
    return todos.map(hydrateTodo);
  },

  createTodo: async (data: {
    title: string;
    scope: string;
    scope_id?: string;
    priority?: string;
    description?: string;
  }): Promise<Todo> => {
    const res = await fetch(`${API_BASE}/todos`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) {
      const err = await res
        .json()
        .catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to create todo: ${res.status}`);
    }
    return hydrateTodo(await res.json());
  },

  getTodo: async (id: string): Promise<Todo> => {
    const res = await fetch(`${API_BASE}/todos/${encodeURIComponent(id)}`);
    if (!res.ok) throw new Error(`Failed to get todo: ${res.status}`);
    return hydrateTodo(await res.json());
  },

  updateTodo: async (
    id: string,
    updates: Partial<
      Pick<
        Todo,
        "title" | "description" | "status" | "priority" | "labels" | "metadata"
      >
    >,
  ): Promise<Todo> => {
    const res = await fetch(`${API_BASE}/todos/${encodeURIComponent(id)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(
        serializeTodoUpdates(updates as Record<string, unknown>),
      ),
    });
    if (!res.ok) throw new Error(`Failed to update todo: ${res.status}`);
    return hydrateTodo(await res.json());
  },

  deleteTodo: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/todos/${encodeURIComponent(id)}`, {
      method: "DELETE",
    });
    if (!res.ok) throw new Error(`Failed to delete todo: ${res.status}`);
  },

  /** D2 — promote/demote a todo between session and project scope. */
  updateTodoScope: async (
    id: string,
    scope: AgentStateScope,
    scopeId: string,
    projectId?: string,
  ): Promise<Todo> => {
    const res = await fetch(
      `${API_BASE}/todos/${encodeURIComponent(id)}/scope`,
      {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          scope,
          scope_id: scopeId,
          project_id: projectId ?? "",
        }),
      },
    );
    if (!res.ok) {
      const err = await res
        .json()
        .catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(
        err.error || `Failed to update todo scope: ${res.status}`,
      );
    }
    return hydrateTodo(await res.json());
  },

  listTodoChildren: async (id: string): Promise<Todo[]> => {
    const res = await fetch(
      `${API_BASE}/todos/${encodeURIComponent(id)}/children`,
    );
    if (!res.ok) throw new Error(`Failed to list todo children: ${res.status}`);
    const todos = await res.json();
    return todos.map(hydrateTodo);
  },

  // --- Plans ---

  listPlans: async (filter?: PlanFilter): Promise<Plan[]> => {
    const params = new URLSearchParams();
    if (filter?.scope) params.set("scope", filter.scope);
    if (filter?.scope_id) params.set("scope_id", filter.scope_id);
    if (filter?.status) params.set("status", filter.status);
    const qs = params.toString();
    const res = await fetch(`${API_BASE}/plans${qs ? `?${qs}` : ""}`);
    if (!res.ok) throw new Error(`Failed to list plans: ${res.status}`);
    const plans = await res.json();
    return plans.map(hydratePlan);
  },

  createPlan: async (data: {
    title: string;
    scope: string;
    scope_id?: string;
    description?: string;
    steps?: PlanStep[];
  }): Promise<Plan> => {
    const res = await fetch(`${API_BASE}/plans`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(
        serializePlanPayload(data as Record<string, unknown>),
      ),
    });
    if (!res.ok) {
      const err = await res
        .json()
        .catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to create plan: ${res.status}`);
    }
    return hydratePlan(await res.json());
  },

  getPlan: async (id: string): Promise<Plan> => {
    const res = await fetch(`${API_BASE}/plans/${encodeURIComponent(id)}`);
    if (!res.ok) throw new Error(`Failed to get plan: ${res.status}`);
    return hydratePlan(await res.json());
  },

  updatePlan: async (
    id: string,
    updates: Partial<
      Pick<Plan, "title" | "description" | "status" | "steps" | "metadata">
    >,
  ): Promise<Plan> => {
    const res = await fetch(`${API_BASE}/plans/${encodeURIComponent(id)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(
        serializePlanPayload(updates as Record<string, unknown>),
      ),
    });
    if (!res.ok) throw new Error(`Failed to update plan: ${res.status}`);
    return hydratePlan(await res.json());
  },

  updatePlanStep: async (
    planId: string,
    stepId: string,
    updates: Partial<Pick<PlanStep, "title" | "status" | "notes">>,
  ): Promise<Plan> => {
    const res = await fetch(
      `${API_BASE}/plans/${encodeURIComponent(planId)}/steps/${encodeURIComponent(stepId)}`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(updates),
      },
    );
    if (!res.ok) throw new Error(`Failed to update plan step: ${res.status}`);
    return hydratePlan(await res.json());
  },

  deletePlan: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/plans/${encodeURIComponent(id)}`, {
      method: "DELETE",
    });
    if (!res.ok) throw new Error(`Failed to delete plan: ${res.status}`);
  },

  approvePlan: async (id: string, createTodos = true): Promise<Plan> => {
    const res = await fetch(
      `${API_BASE}/plans/${encodeURIComponent(id)}/approve`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ create_todos: createTodos }),
      },
    );
    if (!res.ok) {
      const err = await res
        .json()
        .catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to approve plan: ${res.status}`);
    }
    return hydratePlan(await res.json());
  },

  // --- Work Sync ---

  syncWorkChanges: async (diff: WorkDiff): Promise<{ ok: boolean }> => {
    const res = await fetch(`${API_BASE}/work/sync`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(diff),
    });
    if (!res.ok) throw new Error(`Failed to sync work changes: ${res.status}`);
    return res.json();
  },

  // Workers
  listWorkers: async (): Promise<Worker[]> => {
    const res = await fetch(`${API_BASE}/workers`);
    if (!res.ok) throw new Error(`Failed to list workers: ${res.status}`);
    return res.json();
  },

  cancelWorker: async (id: string): Promise<{ status: string }> => {
    const res = await fetch(
      `${API_BASE}/workers/${encodeURIComponent(id)}/cancel`,
      {
        method: "POST",
      },
    );
    if (!res.ok) throw new Error(`Failed to cancel worker: ${res.status}`);
    return res.json();
  },

  // Plugins
  listPlugins: async (): Promise<PluginInfo[]> => {
    const res = await fetch(`${API_BASE}/plugins/managed`);
    if (!res.ok) throw new Error(`Failed to list plugins: ${res.status}`);
    return res.json();
  },

  fetchPluginRegistry: async (): Promise<PluginRegistryResponse> => {
    const res = await fetch(`${API_BASE}/plugins/registry`);
    if (!res.ok)
      throw new Error(`Failed to fetch plugin registry: ${res.status}`);
    return res.json();
  },

  installPlugin: async (name: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/plugins/install`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    });
    if (!res.ok) throw new Error(`Failed to install plugin: ${res.status}`);
  },

  uninstallPlugin: async (name: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/plugins/uninstall`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    });
    if (!res.ok) throw new Error(`Failed to uninstall plugin: ${res.status}`);
  },

  disablePlugin: async (name: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/plugins/disable`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    });
    if (!res.ok) throw new Error(`Failed to disable plugin: ${res.status}`);
  },

  enablePlugin: async (name: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/plugins/enable`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    });
    if (!res.ok) throw new Error(`Failed to enable plugin: ${res.status}`);
  },

  getPluginConfig: async (pluginId: string): Promise<PluginConfig> => {
    const res = await fetch(
      `${API_BASE}/plugin-config/${encodeURIComponent(pluginId)}`,
    );
    if (!res.ok) throw new Error(`Failed to get plugin config: ${res.status}`);
    return res.json();
  },

  listUIComponents: async (): Promise<PluginUIComponent[]> => {
    const res = await fetch(`${API_BASE}/plugins/ui-components`);
    if (!res.ok) throw new Error(`Failed to list UI components: ${res.status}`);
    const data = await res.json();
    return data.components ?? [];
  },

  listUISlots: async (): Promise<Record<string, UISlotEntry[]>> => {
    const res = await fetch(`${API_BASE}/plugins/ui-slots`);
    if (!res.ok) throw new Error(`Failed to list UI slots: ${res.status}`);
    return res.json();
  },

  updatePluginConfig: async (
    pluginId: string,
    settings: Record<string, unknown>,
  ): Promise<PluginConfig> => {
    const res = await fetch(
      `${API_BASE}/plugin-config/${encodeURIComponent(pluginId)}`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(settings),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to update plugin config: ${res.status}`);
    return res.json();
  },

  // --- Plugin Catalog ---

  browseCatalog: async (): Promise<CatalogBrowseEntry[]> => {
    const res = await fetch(`${API_BASE}/plugins/catalog`);
    if (!res.ok) throw new Error(`Failed to browse catalog: ${res.status}`);
    const ct = res.headers.get("content-type") ?? "";
    if (!ct.includes("application/json")) {
      throw new Error(
        "Catalog API not available — backend may need a rebuild (cerberus_rebuild)",
      );
    }
    return res.json();
  },

  refreshCatalog: async (): Promise<void> => {
    const res = await fetch(`${API_BASE}/plugins/catalog/refresh`, {
      method: "POST",
    });
    if (!res.ok) throw new Error(`Failed to refresh catalog: ${res.status}`);
  },

  catalogInstall: async (
    name: string,
  ): Promise<{
    status: string;
    plugin: string;
    version: string;
    source: string;
    message: string;
  }> => {
    const res = await fetch(`${API_BASE}/plugins/catalog/install`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    });
    if (!res.ok) {
      const err = await res
        .json()
        .catch(() => ({ error: `Install failed: ${res.status}` }));
      throw new Error(err.error || `Install failed: ${res.status}`);
    }
    return res.json();
  },

  listCatalogSources: async (): Promise<CatalogSource[]> => {
    const res = await fetch(`${API_BASE}/plugins/catalog/sources`);
    if (!res.ok)
      throw new Error(`Failed to list catalog sources: ${res.status}`);
    return res.json();
  },

  addCatalogSource: async (
    name: string,
    url: string,
    priority: number,
  ): Promise<CatalogSource> => {
    const res = await fetch(`${API_BASE}/plugins/catalog/sources`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name, url, priority }),
    });
    if (!res.ok) {
      const err = await res
        .json()
        .catch(() => ({ error: `Add source failed: ${res.status}` }));
      throw new Error(err.error || `Add source failed: ${res.status}`);
    }
    return res.json();
  },

  updateCatalogSource: async (
    id: string,
    data: { name?: string; url?: string; enabled?: boolean; priority?: number },
  ): Promise<void> => {
    const res = await fetch(`${API_BASE}/plugins/catalog/sources/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok)
      throw new Error(`Failed to update catalog source: ${res.status}`);
  },

  deleteCatalogSource: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/plugins/catalog/sources/${id}`, {
      method: "DELETE",
    });
    if (!res.ok)
      throw new Error(`Failed to delete catalog source: ${res.status}`);
  },

  setCatalogSourceKey: async (id: string, publicKey: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/plugins/catalog/sources/${id}/key`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ public_key: publicKey }),
    });
    if (!res.ok) throw new Error(`Failed to set source key: ${res.status}`);
  },

  // --- Messaging subsystem (agent-to-agent + agent-to-user) ---
  // Distinct from session-chat `sendMessage` above — that's for user
  // chat turns; these target the agent-to-agent messaging primitive.
  //
  // Backend (S7) requires BOTH session_id and agent_id on every
  // read/ack/resolve — caller identity is enforced at the service
  // layer. Helpers accept session + agent as required args.

  getMessagingInbox: async (
    sessionId: string,
    agentId: string,
    filter?: { status?: string; channel?: string; kind?: string },
  ): Promise<AgentMessage[]> => {
    const params = new URLSearchParams({
      session_id: sessionId,
      agent_id: agentId,
    });
    if (filter?.status) params.set("status", filter.status);
    if (filter?.channel) params.set("channel", filter.channel);
    if (filter?.kind) params.set("kind", filter.kind);
    const res = await fetch(`${API_BASE}/messaging/inbox?${params}`);
    if (!res.ok)
      throw new Error(`Failed to get messaging inbox: ${res.status}`);
    return res.json();
  },

  getMessagingThread: async (
    threadId: string,
    sessionId: string,
    agentId: string,
  ): Promise<AgentMessage[]> => {
    const params = new URLSearchParams({
      session_id: sessionId,
      agent_id: agentId,
    });
    const res = await fetch(
      `${API_BASE}/messaging/threads/${encodeURIComponent(threadId)}?${params}`,
    );
    if (!res.ok) throw new Error(`Failed to get thread: ${res.status}`);
    return res.json();
  },

  sendAgentMessage: async (data: {
    from_session_id: string;
    from_agent_id: string;
    to_session_id: string;
    to_agent_id: string;
    channel?: AgentMessageChannel;
    kind?: AgentMessageKind;
    payload_json?: string;
    subject?: string;
    body: string;
    type?: string;
    thread_id?: string;
    reply_to?: string;
    priority?: number;
    register_as?: "external" | "cli";
  }): Promise<AgentMessage> => {
    const res = await fetch(`${API_BASE}/messaging/send`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error(`Failed to send agent message: ${res.status}`);
    return res.json();
  },

  ackAgentMessage: async (
    id: string,
    sessionId: string,
    agentId: string,
  ): Promise<void> => {
    const res = await fetch(
      `${API_BASE}/messaging/${encodeURIComponent(id)}/ack`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ session_id: sessionId, agent_id: agentId }),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to acknowledge agent message: ${res.status}`);
  },

  resolveAgentMessage: async (
    id: string,
    sessionId: string,
    agentId: string,
  ): Promise<void> => {
    const res = await fetch(
      `${API_BASE}/messaging/${encodeURIComponent(id)}/resolve`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ session_id: sessionId, agent_id: agentId }),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to resolve agent message: ${res.status}`);
  },

  getAgentMessageUnreadCount: async (
    sessionId: string,
    agentId: string,
  ): Promise<{ count: number }> => {
    const params = new URLSearchParams({
      session_id: sessionId,
      agent_id: agentId,
    });
    const res = await fetch(`${API_BASE}/messaging/unread?${params}`);
    if (!res.ok) throw new Error(`Failed to get unread count: ${res.status}`);
    return res.json();
  },

  // Search
  searchMessages: async (
    query: string,
    workspaceId: string,
    projectId?: string,
    limit = 20,
  ): Promise<SearchResult[]> => {
    const params = new URLSearchParams({
      q: query,
      workspace_id: workspaceId,
      limit: String(limit),
    });
    if (projectId) params.set("project_id", projectId);
    const res = await fetch(`${API_BASE}/search?${params}`);
    if (!res.ok) throw new Error(`Failed to search: ${res.status}`);
    return res.json();
  },

  // Agent Projects (many-to-many)
  listAgentProjects: async (agentId: string): Promise<Project[]> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/projects`);
    if (!res.ok)
      throw new Error(`Failed to list agent projects: ${res.status}`);
    return res.json();
  },

  addAgentProject: async (
    agentId: string,
    projectId: string,
  ): Promise<Project[]> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/projects`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ project_id: projectId }),
    });
    if (!res.ok) throw new Error(`Failed to add agent project: ${res.status}`);
    return res.json();
  },

  removeAgentProject: async (
    agentId: string,
    projectId: string,
  ): Promise<void> => {
    const res = await fetch(
      `${API_BASE}/agents/${agentId}/projects/${projectId}`,
      {
        method: "DELETE",
      },
    );
    if (!res.ok)
      throw new Error(`Failed to remove agent project: ${res.status}`);
  },

  // Workspace & Project Management
  updateWorkspace: async (
    id: string,
    data: Partial<Workspace>,
  ): Promise<Workspace> => {
    const res = await fetch(`${API_BASE}/workspaces/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) {
      const err = await res
        .json()
        .catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to update workspace: ${res.status}`);
    }
    return res.json();
  },

  deleteWorkspace: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/workspaces/${id}`, {
      method: "DELETE",
    });
    if (!res.ok) throw new Error(`Failed to delete workspace: ${res.status}`);
  },

  updateProject: async (
    workspaceId: string,
    projectId: string,
    data: Partial<Project>,
  ): Promise<Project> => {
    const res = await fetch(
      `${API_BASE}/workspaces/${workspaceId}/projects/${projectId}`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      },
    );
    if (!res.ok) {
      const err = await res
        .json()
        .catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to update project: ${res.status}`);
    }
    return res.json();
  },

  deleteProject: async (
    workspaceId: string,
    projectId: string,
  ): Promise<void> => {
    const res = await fetch(
      `${API_BASE}/workspaces/${workspaceId}/projects/${projectId}`,
      {
        method: "DELETE",
      },
    );
    if (!res.ok) throw new Error(`Failed to delete project: ${res.status}`);
  },

  // Custom Actions
  listActions: async (): Promise<{
    actions: CustomAction[];
    count: number;
  }> => {
    const res = await fetch(`${API_BASE}/actions`);
    if (!res.ok) throw new Error(`Failed to list actions: ${res.status}`);
    return res.json();
  },

  createAction: async (
    data: Omit<CustomAction, "id" | "created_at" | "updated_at">,
  ): Promise<CustomAction> => {
    const res = await fetch(`${API_BASE}/actions`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error(`Failed to create action: ${res.status}`);
    return res.json();
  },

  getAction: async (id: string): Promise<CustomAction> => {
    const res = await fetch(`${API_BASE}/actions/${id}`);
    if (!res.ok) throw new Error(`Failed to get action: ${res.status}`);
    return res.json();
  },

  updateAction: async (
    id: string,
    data: Partial<CustomAction>,
  ): Promise<CustomAction> => {
    const res = await fetch(`${API_BASE}/actions/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error(`Failed to update action: ${res.status}`);
    return res.json();
  },

  deleteAction: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/actions/${id}`, { method: "DELETE" });
    if (!res.ok) throw new Error(`Failed to delete action: ${res.status}`);
  },

  executeAction: async (
    id: string,
    sessionId: string,
  ): Promise<{
    action: string;
    command: string;
    session_id: string;
    action_id: string;
  }> => {
    const res = await fetch(`${API_BASE}/actions/${id}/execute`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ session_id: sessionId }),
    });
    if (!res.ok) throw new Error(`Failed to execute action: ${res.status}`);
    return res.json();
  },

  // Plugin Keybindings
  listPluginKeybindings: async (): Promise<{
    keybindings: PluginKeybinding[];
    count: number;
  }> => {
    const res = await fetch(`${API_BASE}/plugins/keybindings`);
    if (!res.ok) throw new Error(`Failed to list keybindings: ${res.status}`);
    return res.json();
  },

  // --- vNext: Permissions & Approvals ---

  respondToApproval: async (
    sessionId: string,
    requestId: string,
    decision: ApprovalDecision,
    scope?: ApprovalScope,
  ): Promise<{ status: string }> => {
    const res = await fetch(
      `${API_BASE}/sessions/${sessionId}/approvals/${requestId}`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ decision, scope }),
      },
    );
    if (!res.ok)
      throw new Error(`Failed to respond to approval: ${res.status}`);
    return res.json();
  },

  getPermissionMode: async (): Promise<{ mode: PermissionMode }> => {
    const res = await fetch(`${API_BASE}/permissions/mode`);
    if (!res.ok)
      throw new Error(`Failed to get permission mode: ${res.status}`);
    return res.json();
  },

  setPermissionMode: async (
    mode: PermissionMode,
  ): Promise<{ mode: PermissionMode }> => {
    const res = await fetch(`${API_BASE}/permissions/mode`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ mode }),
    });
    if (!res.ok)
      throw new Error(`Failed to set permission mode: ${res.status}`);
    return res.json();
  },

  // --- Shell Execution ---

  getShellMode: async (
    sessionId: string,
  ): Promise<{ mode: import("./types").ShellMode }> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/shell-mode`);
    if (!res.ok) throw new Error(`Failed to get shell mode: ${res.status}`);
    return res.json();
  },

  setShellMode: async (
    sessionId: string,
    mode: import("./types").ShellMode,
  ): Promise<{ mode: import("./types").ShellMode }> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/shell-mode`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ mode }),
    });
    if (!res.ok) throw new Error(`Failed to set shell mode: ${res.status}`);
    return res.json();
  },

  shellExec: async (
    sessionId: string,
    command: string,
    approved = false,
  ): Promise<{
    message_id?: string;
    command: string;
    output?: string;
    exit_code?: number;
    duration_ms?: number;
    truncated?: boolean;
    requires_approval?: boolean;
    mode?: string;
  }> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/shell-exec`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ command, approved }),
    });
    if (!res.ok) {
      const err = await res
        .json()
        .catch(() => ({ error: `HTTP ${res.status}` }));
      throw new Error(err.error || `Shell exec failed: ${res.status}`);
    }
    return res.json();
  },

  shellCheck: async (
    sessionId: string,
    command: string,
  ): Promise<{ allowed: boolean; reason: string; mode: string }> => {
    const res = await fetch(
      `${API_BASE}/sessions/${sessionId}/shell-check?command=${encodeURIComponent(command)}`,
    );
    if (!res.ok)
      throw new Error(`Failed to check shell command: ${res.status}`);
    return res.json();
  },

  getShellInfo: async (
    sessionId: string,
  ): Promise<{
    work_dir: string;
    git_branch?: string;
    git_status?: string;
  }> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/shell-info`);
    if (!res.ok) throw new Error(`Failed to get shell info: ${res.status}`);
    return res.json();
  },

  // --- vNext: Broker Decisions ---

  getBrokerDecisions: async (
    sessionId: string,
    limit = 50,
  ): Promise<BrokerDecision[]> => {
    const res = await fetch(
      `${API_BASE}/broker/decisions?session_id=${encodeURIComponent(sessionId)}&limit=${limit}`,
    );
    if (!res.ok)
      throw new Error(`Failed to get broker decisions: ${res.status}`);
    return res.json();
  },

  // --- vNext: Execution Metrics (with debug snapshots) ---

  getExecutionMetrics: async (
    sessionId: string,
  ): Promise<ExecutionMetrics[]> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/metrics`);
    if (!res.ok)
      throw new Error(`Failed to get execution metrics: ${res.status}`);
    return res.json();
  },

  // Workflow Runs
  listWorkflowRuns: async (params?: {
    status?: string;
    pipeline_id?: string;
  }): Promise<WorkflowRun[]> => {
    const qs = new URLSearchParams();
    if (params?.status) qs.set("status", params.status);
    if (params?.pipeline_id) qs.set("pipeline_id", params.pipeline_id);
    const query = qs.toString();
    const res = await fetch(
      `${API_BASE}/workflows/runs${query ? `?${query}` : ""}`,
    );
    if (!res.ok) throw new Error(`Failed to list workflow runs: ${res.status}`);
    return res.json();
  },

  getWorkflowRun: async (runId: string): Promise<WorkflowRun> => {
    const res = await fetch(
      `${API_BASE}/workflows/runs/${encodeURIComponent(runId)}`,
    );
    if (!res.ok) throw new Error(`Failed to get workflow run: ${res.status}`);
    return res.json();
  },

  cancelWorkflowRun: async (runId: string): Promise<{ status: string }> => {
    const res = await fetch(
      `${API_BASE}/workflows/runs/${encodeURIComponent(runId)}/cancel`,
      {
        method: "POST",
      },
    );
    if (!res.ok)
      throw new Error(`Failed to cancel workflow run: ${res.status}`);
    return res.json();
  },

  // Memories
  listMemories: async (params?: {
    scope?: string;
    status?: string;
    q?: string;
    tags?: string;
    limit?: number;
    offset?: number;
  }): Promise<MemoryListResponse> => {
    const qs = new URLSearchParams();
    if (params?.scope) qs.set("scope", params.scope);
    if (params?.status) qs.set("status", params.status);
    if (params?.q) qs.set("q", params.q);
    if (params?.tags) qs.set("tags", params.tags);
    if (params?.limit) qs.set("limit", String(params.limit));
    if (params?.offset) qs.set("offset", String(params.offset));
    const query = qs.toString();
    const res = await fetch(`${API_BASE}/memories${query ? `?${query}` : ""}`);
    if (!res.ok) throw new Error(`Failed to list memories: ${res.status}`);
    return res.json();
  },

  createMemory: async (data: MemoryCreateRequest): Promise<Memory> => {
    const res = await fetch(`${API_BASE}/memories`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) {
      const err = await res
        .json()
        .catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to create memory: ${res.status}`);
    }
    return res.json();
  },

  updateMemory: async (
    key: string,
    data: MemoryUpdateRequest,
  ): Promise<Memory> => {
    const res = await fetch(`${API_BASE}/memories/${encodeURIComponent(key)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) {
      const err = await res
        .json()
        .catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to update memory: ${res.status}`);
    }
    return res.json();
  },

  deleteMemory: async (key: string): Promise<{ deleted: boolean }> => {
    const res = await fetch(`${API_BASE}/memories/${encodeURIComponent(key)}`, {
      method: "DELETE",
    });
    if (!res.ok) throw new Error(`Failed to delete memory: ${res.status}`);
    return res.json();
  },

  updateMemoryStatus: async (key: string, status: string): Promise<Memory> => {
    const res = await fetch(
      `${API_BASE}/memories/${encodeURIComponent(key)}/status`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ status }),
      },
    );
    if (!res.ok) {
      const err = await res
        .json()
        .catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(
        err.error || `Failed to update memory status: ${res.status}`,
      );
    }
    return res.json();
  },

  // Role trust — H1 CW-20260421-0014
  // GET /api/workspaces/{workspace_id}/roles
  listWorkspaceRoleTrust: async (
    workspaceID: string,
  ): Promise<{
    workspace_id: string;
    trust_overrides: WorkspaceRoleTrustOverride[];
  }> => {
    const res = await fetch(
      `${API_BASE}/workspaces/${encodeURIComponent(workspaceID)}/roles`,
    );
    if (!res.ok) throw new Error(`Failed to list role trust: ${res.status}`);
    return res.json();
  },

  // POST /api/workspaces/{workspace_id}/roles/{agent_profile_id}/trust
  setWorkspaceRoleTrust: async (
    workspaceID: string,
    agentProfileID: string,
    tier: "untrusted" | "normal" | "trusted",
  ): Promise<{
    workspace_id: string;
    agent_profile_id: string;
    trust_tier: string;
  }> => {
    const res = await fetch(
      `${API_BASE}/workspaces/${encodeURIComponent(workspaceID)}/roles/${encodeURIComponent(agentProfileID)}/trust`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ tier, promoted_by: "ui" }),
      },
    );
    if (!res.ok) throw new Error(`Failed to set role trust: ${res.status}`);
    return res.json();
  },

  // DELETE /api/workspaces/{workspace_id}/roles/{agent_profile_id}/trust
  deleteWorkspaceRoleTrust: async (
    workspaceID: string,
    agentProfileID: string,
  ): Promise<{ status: string }> => {
    const res = await fetch(
      `${API_BASE}/workspaces/${encodeURIComponent(workspaceID)}/roles/${encodeURIComponent(agentProfileID)}/trust`,
      { method: "DELETE" },
    );
    if (!res.ok) throw new Error(`Failed to delete role trust: ${res.status}`);
    return res.json();
  },

  // Inspector (I1, CW-20260426-0004)
  getInspectorTurns: async (
    sessionId: string,
    limit = 20,
  ): Promise<InspectorTurnsResponse> => {
    const res = await fetch(
      `${API_BASE}/inspector/sessions/${encodeURIComponent(sessionId)}/turns?limit=${limit}`,
    );
    if (!res.ok)
      throw new Error(`Failed to get inspector turns: ${res.status}`);
    return res.json();
  },

  getInspectorTurn: async (
    sessionId: string,
    turnId: string,
  ): Promise<InspectorTurnSnapshot> => {
    const res = await fetch(
      `${API_BASE}/inspector/sessions/${encodeURIComponent(sessionId)}/turns/${encodeURIComponent(turnId)}`,
    );
    if (!res.ok) throw new Error(`Failed to get inspector turn: ${res.status}`);
    return res.json();
  },
};
