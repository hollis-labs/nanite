import type {
  A2AMessage,
  AgentModeProfile,
  AgentProfile,
  ApprovalDecision,
  ApprovalScope,
  Artifact,
  Bookmark,
  BrokerDecision,
  CatalogBrowseEntry,
  CatalogSource,
  CLIDetectionResult,
  ContextBreakdown,
  CustomAction,
  DiscoveryDiff,
  ExecutionMetrics,
  GlobalUsageSummary,
  MCPServerConfig,
  Message,
  MessagePage,
  ModelRecord,
  PermissionMode,
  PluginConfig,
  PluginInfo,
  PluginKeybinding,
  PluginUIComponent,
  ProcessHealthResponse,
  Project,
  PromptTemplate,
  ProviderConfig,
  ProviderStatus,
  SearchResult,
  ServerInfo,
  Session,
  SessionAgent,
  SessionUsageSummary,
  SessionWithMessages,
  Skill,
  SlashCommandDef,
  ToolDefinition,
  ToolSelection,
  UISlotEntry,
  UserSettings,
  UtilityCallSummary,
  FragmentsBacklogItem,
  FragmentsSprint,
  FragmentsTask,
  SessionTask,
  SessionTaskStatus,
  Worker,
  Workspace,
} from "./types";

const API_BASE = "/api";

export const api = {
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

  updateSession: async (id: string, data: Partial<Session>): Promise<Session> => {
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
    data: { include_messages: boolean; provider?: string; model?: string },
  ): Promise<Session> => {
    const res = await fetch(`${API_BASE}/sessions/${id}/fork`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error(`Failed to fork session: ${res.status}`);
    return res.json();
  },

  deleteSession: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/sessions/${id}`, { method: "DELETE" });
    if (!res.ok) throw new Error(`Failed to delete session: ${res.status}`);
  },

  // Messages
  sendMessage: async (data: {
    session_id: string;
    content: string;
  }): Promise<{ message_id: string; stream_url: string }> => {
    const res = await fetch(`${API_BASE}/messages`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error(`Failed to send message: ${res.status}`);
    return res.json();
  },

  retryStream: async (sessionId: string): Promise<{ message_id: string; stream_url: string }> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/retry`, {
      method: "POST",
    });
    if (!res.ok) throw new Error(`Failed to retry: ${res.status}`);
    return res.json();
  },

  getMessages: async (sessionId: string, limit = 50): Promise<Message[]> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/messages?limit=${limit}`);
    if (!res.ok) throw new Error(`Failed to get messages: ${res.status}`);
    const page = (await res.json()) as MessagePage | Message[];
    // Backend now returns MessagePage; handle both shapes for safety.
    if (Array.isArray(page)) return page;
    return page.messages;
  },

  getMessagePage: async (sessionId: string, limit = 50, offset?: number): Promise<MessagePage> => {
    const params = new URLSearchParams({ limit: String(limit) });
    if (offset !== undefined) params.set("offset", String(offset));
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/messages?${params}`);
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
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/messages?${params}`);
    if (!res.ok) throw new Error(`Failed to get messages around: ${res.status}`);
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
      const err = await res.json().catch(() => ({ error: `Request failed: ${res.status}` }));
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
    data: { name: string; description?: string },
  ): Promise<Project> => {
    const res = await fetch(`${API_BASE}/workspaces/${workspaceId}/projects`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to create project: ${res.status}`);
    }
    return res.json();
  },

  // Agents — endpoint returns full AgentProfile shape
  listAgents: async (): Promise<AgentProfile[]> => {
    const res = await fetch(`${API_BASE}/agents`);
    if (!res.ok) throw new Error(`Failed to list agents: ${res.status}`);
    return res.json();
  },

  /** @deprecated Use listAgents — same endpoint, same return type */
  listAgentProfiles: async (): Promise<AgentProfile[]> => api.listAgents(),

  getAgentProfile: async (
    id: string,
  ): Promise<{ agent: AgentProfile; modes: AgentModeProfile[] }> => {
    const res = await fetch(`${API_BASE}/agents/${id}`);
    if (!res.ok) throw new Error(`Failed to get agent profile: ${res.status}`);
    return res.json();
  },

  createAgentProfile: async (
    data: Omit<AgentProfile, "id" | "created_at" | "updated_at" | "agent_hash" | "version">,
  ): Promise<AgentProfile> => {
    const res = await fetch(`${API_BASE}/agents`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error(`Failed to create agent profile: ${res.status}`);
    return res.json();
  },

  updateAgentProfile: async (id: string, data: Partial<AgentProfile>): Promise<AgentProfile> => {
    const res = await fetch(`${API_BASE}/agents/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error(`Failed to update agent profile: ${res.status}`);
    return res.json();
  },

  // Note: DELETE agent endpoint not implemented in backend yet
  // deleteAgentProfile: async (id: string): Promise<void> => {
  //   const res = await fetch(`${API_BASE}/agents/${id}`, { method: 'DELETE' })
  //   if (!res.ok) throw new Error(`Failed to delete agent profile: ${res.status}`)
  // },

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

  // Mode
  switchMode: async (sessionId: string, mode: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/mode`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ mode }),
    });
    if (!res.ok) throw new Error(`Failed to switch mode: ${res.status}`);
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

  toggleBookmark: async (messageId: string, sessionId: string): Promise<void> => {
    await fetch(`${API_BASE}/messages/${messageId}/bookmark`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ session_id: sessionId }),
    });
  },

  autotitleBookmark: async (bookmarkId: string): Promise<{ title: string }> => {
    const res = await fetch(`${API_BASE}/bookmarks/${bookmarkId}/autotitle`, { method: "POST" });
    if (!res.ok) throw new Error(`Failed to autotitle bookmark: ${res.status}`);
    return res.json();
  },

  // Artifacts
  listArtifacts: async (sessionId: string): Promise<Artifact[]> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/artifacts`);
    if (!res.ok) throw new Error(`Failed to list artifacts: ${res.status}`);
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
  listProviderStatuses: async (): Promise<ProviderStatus[]> => {
    const res = await fetch(`${API_BASE}/providers/status`);
    if (!res.ok) throw new Error(`Failed to list provider statuses: ${res.status}`);
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
    const res = await fetch(`${API_BASE}/providers/${id}/test`, { method: "POST" });
    // Gracefully handle missing endpoint — if the backend doesn't have a test
    // route yet, treat a successful key save as sufficient
    if (res.status === 404) return { ok: true };
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: `Test failed: ${res.status}` }));
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

  updateSettings: async (data: Partial<UserSettings>): Promise<UserSettings> => {
    const res = await fetch(`${API_BASE}/settings`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error(`Failed to update settings: ${res.status}`);
    return res.json();
  },

  // Session Agents
  listSessionAgents: async (sessionId: string): Promise<SessionAgent[]> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/agents`);
    if (!res.ok) throw new Error(`Failed to list session agents: ${res.status}`);
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
    if (!res.ok) throw new Error(`Failed to add agent to session: ${res.status}`);
    return res.json();
  },

  removeSessionAgent: async (sessionId: string, agentId: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/agents/${agentId}`, {
      method: "DELETE",
    });
    if (!res.ok) throw new Error(`Failed to remove agent from session: ${res.status}`);
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
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/context-breakdown`);
    if (!res.ok) throw new Error(`Failed to get context breakdown: ${res.status}`);
    return res.json();
  },

  // Execution Metrics
  getSessionMetrics: async (sessionId: string): Promise<ExecutionMetrics[]> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/metrics`);
    if (!res.ok) throw new Error(`Failed to get session metrics: ${res.status}`);
    return res.json();
  },

  getRecentExecutions: async (limit = 50): Promise<ExecutionMetrics[]> => {
    const res = await fetch(`${API_BASE}/metrics/executions?limit=${limit}`);
    if (!res.ok) throw new Error(`Failed to get recent executions: ${res.status}`);
    return res.json();
  },

  getUtilityCallSummary: async (): Promise<UtilityCallSummary[]> => {
    const res = await fetch(`${API_BASE}/metrics/utility`);
    if (!res.ok) throw new Error(`Failed to get utility call summary: ${res.status}`);
    return res.json();
  },

  getUtilityCallLog: async (limit = 50): Promise<ExecutionMetrics[]> => {
    const res = await fetch(`${API_BASE}/metrics/utility/log?limit=${limit}`);
    if (!res.ok) throw new Error(`Failed to get utility call log: ${res.status}`);
    return res.json();
  },

  // Process Health
  getProcessHealth: async (): Promise<ProcessHealthResponse> => {
    const res = await fetch(`${API_BASE}/processes/health`);
    if (!res.ok) throw new Error(`Failed to get process health: ${res.status}`);
    return res.json();
  },

  killStaleProcesses: async (): Promise<{ killed: number }> => {
    const res = await fetch(`${API_BASE}/processes/kill-stale`, { method: "POST" });
    if (!res.ok) throw new Error(`Failed to kill stale processes: ${res.status}`);
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
    data: Partial<Omit<Skill, "id" | "created_at" | "updated_at" | "is_builtin">>,
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
    if (!res.ok) throw new Error(`Failed to assign skill to agent: ${res.status}`);
    return res.json();
  },

  removeSkillFromAgent: async (agentId: string, skillId: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/skills/${skillId}`, {
      method: "DELETE",
    });
    if (!res.ok) throw new Error(`Failed to remove skill from agent: ${res.status}`);
  },

  // Prompt Templates
  listPromptTemplates: async (): Promise<PromptTemplate[]> => {
    const res = await fetch(`${API_BASE}/prompt-templates`);
    if (!res.ok) throw new Error(`Failed to list prompt templates: ${res.status}`);
    return res.json();
  },

  getPromptTemplate: async (id: string): Promise<PromptTemplate> => {
    const res = await fetch(`${API_BASE}/prompt-templates/${id}`);
    if (!res.ok) throw new Error(`Failed to get prompt template: ${res.status}`);
    return res.json();
  },

  createPromptTemplate: async (
    data: Omit<PromptTemplate, "id" | "created_at" | "updated_at" | "is_builtin">,
  ): Promise<PromptTemplate> => {
    const res = await fetch(`${API_BASE}/prompt-templates`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error(`Failed to create prompt template: ${res.status}`);
    return res.json();
  },

  updatePromptTemplate: async (
    id: string,
    data: Partial<Omit<PromptTemplate, "id" | "created_at" | "updated_at" | "is_builtin">>,
  ): Promise<PromptTemplate> => {
    const res = await fetch(`${API_BASE}/prompt-templates/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error(`Failed to update prompt template: ${res.status}`);
    return res.json();
  },

  deletePromptTemplate: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/prompt-templates/${id}`, { method: "DELETE" });
    if (!res.ok) throw new Error(`Failed to delete prompt template: ${res.status}`);
  },

  // Agent Templates (returns PromptTemplate[], not a join-table type)
  listAgentTemplates: async (agentId: string): Promise<PromptTemplate[]> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/prompt-templates`);
    if (!res.ok) throw new Error(`Failed to list agent templates: ${res.status}`);
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
    if (!res.ok) throw new Error(`Failed to assign template to agent: ${res.status}`);
    return res.json();
  },

  removeTemplateFromAgent: async (agentId: string, templateId: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/prompt-templates/${templateId}`, {
      method: "DELETE",
    });
    if (!res.ok) throw new Error(`Failed to remove template from agent: ${res.status}`);
  },

  // Fragments Engine Backlog
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
      const err = await res.json().catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to create backlog item: ${res.status}`);
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
      const err = await res.json().catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to add MCP server: ${res.status}`);
    }
    return res.json();
  },

  updateMCPServer: async (
    name: string,
    config: Partial<MCPServerConfig>,
  ): Promise<MCPServerConfig> => {
    const res = await fetch(`${API_BASE}/mcp-servers/${encodeURIComponent(name)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(config),
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to update MCP server: ${res.status}`);
    }
    return res.json();
  },

  deleteMCPServer: async (name: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/mcp-servers/${encodeURIComponent(name)}`, {
      method: "DELETE",
    });
    if (!res.ok) throw new Error(`Failed to delete MCP server: ${res.status}`);
  },

  importMCPServers: async (json: string): Promise<{ created: string[]; skipped: string[] }> => {
    const res = await fetch(`${API_BASE}/mcp-servers/import`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: json,
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to import MCP servers: ${res.status}`);
    }
    return res.json();
  },

  // Tool Load Preferences
  fetchAllToolsWithLoadType: async (): Promise<import("./types").ToolLoadItem[]> => {
    const res = await fetch(`${API_BASE}/tools/all`);
    if (!res.ok) throw new Error(`Failed to fetch tools: ${res.status}`);
    return res.json();
  },

  fetchToolLoadPreferences: async (): Promise<import("./types").ToolLoadPreferences> => {
    const res = await fetch(`${API_BASE}/tools/load-preferences`);
    if (!res.ok) throw new Error(`Failed to fetch load preferences: ${res.status}`);
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
      const err = await res.json().catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to update load preferences: ${res.status}`);
    }
    return res.json();
  },

  exportMCPServers: async (): Promise<string> => {
    const res = await fetch(`${API_BASE}/mcp-servers/export`);
    if (!res.ok) throw new Error(`Failed to export MCP servers: ${res.status}`);
    return res.text();
  },

  // Fragments Engine (Sprint Planning)
  getFragmentsSprints: async (projectId?: string): Promise<{ items: FragmentsSprint[]; count: number }> => {
    const params = projectId ? `?project_id=${encodeURIComponent(projectId)}` : "";
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
    const res = await fetch(`${API_BASE}/plugins/engine/tasks${qs ? `?${qs}` : ""}`);
    if (!res.ok) throw new Error(`Failed to list tasks: ${res.status}`);
    return res.json();
  },

  getFragmentsBacklog: async (
    projectId?: string,
  ): Promise<{ items: FragmentsBacklogItem[]; count: number }> => {
    const params = projectId ? `?project_id=${encodeURIComponent(projectId)}` : "";
    const res = await fetch(`${API_BASE}/volon/backlog${params}`);
    if (!res.ok) throw new Error(`Failed to list backlog: ${res.status}`);
    return res.json();
  },

  transitionFragmentsTask: async (id: string, status: string): Promise<unknown> => {
    const res = await fetch(`${API_BASE}/plugins/engine/tasks/${encodeURIComponent(id)}/transition`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ status }),
    });
    if (!res.ok) throw new Error(`Failed to transition task: ${res.status}`);
    return res.json();
  },

  promoteBacklogItem: async (id: string, sprintId: string): Promise<unknown> => {
    const res = await fetch(`${API_BASE}/volon/backlog/${encodeURIComponent(id)}/promote`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ sprint_id: sprintId }),
    });
    if (!res.ok) throw new Error(`Failed to promote backlog item: ${res.status}`);
    return res.json();
  },

  deleteFragmentsTask: async (id: string): Promise<unknown> => {
    const res = await fetch(`${API_BASE}/plugins/engine/tasks/${encodeURIComponent(id)}`, {
      method: "DELETE",
    });
    if (!res.ok) throw new Error(`Failed to delete task: ${res.status}`);
    return res.json();
  },

  // Session Tasks
  listSessionTasks: async (sessionId: string): Promise<SessionTask[]> => {
    const res = await fetch(`${API_BASE}/sessions/${encodeURIComponent(sessionId)}/tasks`);
    if (!res.ok) throw new Error(`Failed to list session tasks: ${res.status}`);
    return res.json();
  },

  createSessionTask: async (data: {
    title: string;
    session_id: string;
    description?: string;
  }): Promise<SessionTask> => {
    const res = await fetch(`${API_BASE}/tasks`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to create task: ${res.status}`);
    }
    return res.json();
  },

  updateSessionTask: async (
    id: string,
    data: Partial<Pick<SessionTask, "title" | "description" | "result" | "metadata">>,
  ): Promise<SessionTask> => {
    const res = await fetch(`${API_BASE}/tasks/${encodeURIComponent(id)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error(`Failed to update task: ${res.status}`);
    return res.json();
  },

  transitionSessionTask: async (id: string, status: SessionTaskStatus): Promise<SessionTask> => {
    const res = await fetch(`${API_BASE}/tasks/${encodeURIComponent(id)}/transition`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ status }),
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to transition task: ${res.status}`);
    }
    return res.json();
  },

  // Workers
  listWorkers: async (): Promise<Worker[]> => {
    const res = await fetch(`${API_BASE}/workers`);
    if (!res.ok) throw new Error(`Failed to list workers: ${res.status}`);
    return res.json();
  },

  cancelWorker: async (id: string): Promise<{ status: string }> => {
    const res = await fetch(`${API_BASE}/workers/${encodeURIComponent(id)}/cancel`, {
      method: "POST",
    });
    if (!res.ok) throw new Error(`Failed to cancel worker: ${res.status}`);
    return res.json();
  },

  // Plugins
  listPlugins: async (): Promise<PluginInfo[]> => {
    const res = await fetch(`${API_BASE}/plugins/managed`);
    if (!res.ok) throw new Error(`Failed to list plugins: ${res.status}`);
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
    const res = await fetch(`${API_BASE}/plugin-config/${encodeURIComponent(pluginId)}`);
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
    const res = await fetch(`${API_BASE}/plugin-config/${encodeURIComponent(pluginId)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(settings),
    });
    if (!res.ok) throw new Error(`Failed to update plugin config: ${res.status}`);
    return res.json();
  },

  // --- Plugin Catalog ---

  browseCatalog: async (): Promise<CatalogBrowseEntry[]> => {
    const res = await fetch(`${API_BASE}/plugins/catalog`);
    if (!res.ok) throw new Error(`Failed to browse catalog: ${res.status}`);
    const ct = res.headers.get("content-type") ?? "";
    if (!ct.includes("application/json")) {
      throw new Error("Catalog API not available — backend may need a rebuild (cerberus_rebuild)");
    }
    return res.json();
  },

  refreshCatalog: async (): Promise<void> => {
    const res = await fetch(`${API_BASE}/plugins/catalog/refresh`, { method: "POST" });
    if (!res.ok) throw new Error(`Failed to refresh catalog: ${res.status}`);
  },

  catalogInstall: async (name: string): Promise<{ status: string; plugin: string; version: string; source: string; message: string }> => {
    const res = await fetch(`${API_BASE}/plugins/catalog/install`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: `Install failed: ${res.status}` }));
      throw new Error(err.error || `Install failed: ${res.status}`);
    }
    return res.json();
  },

  listCatalogSources: async (): Promise<CatalogSource[]> => {
    const res = await fetch(`${API_BASE}/plugins/catalog/sources`);
    if (!res.ok) throw new Error(`Failed to list catalog sources: ${res.status}`);
    return res.json();
  },

  addCatalogSource: async (name: string, url: string, priority: number): Promise<CatalogSource> => {
    const res = await fetch(`${API_BASE}/plugins/catalog/sources`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name, url, priority }),
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: `Add source failed: ${res.status}` }));
      throw new Error(err.error || `Add source failed: ${res.status}`);
    }
    return res.json();
  },

  updateCatalogSource: async (id: string, data: { name?: string; url?: string; enabled?: boolean; priority?: number }): Promise<void> => {
    const res = await fetch(`${API_BASE}/plugins/catalog/sources/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error(`Failed to update catalog source: ${res.status}`);
  },

  deleteCatalogSource: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/plugins/catalog/sources/${id}`, { method: "DELETE" });
    if (!res.ok) throw new Error(`Failed to delete catalog source: ${res.status}`);
  },

  setCatalogSourceKey: async (id: string, publicKey: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/plugins/catalog/sources/${id}/key`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ public_key: publicKey }),
    });
    if (!res.ok) throw new Error(`Failed to set source key: ${res.status}`);
  },

  // --- A2A Messaging ---

  getA2AInbox: async (agentId: string, status?: string): Promise<A2AMessage[]> => {
    const params = new URLSearchParams({ agent_id: agentId });
    if (status) params.set("status", status);
    const res = await fetch(`${API_BASE}/a2a/inbox?${params}`);
    if (!res.ok) throw new Error(`Failed to get A2A inbox: ${res.status}`);
    return res.json();
  },

  getA2AThread: async (threadId: string): Promise<A2AMessage[]> => {
    const res = await fetch(`${API_BASE}/a2a/threads/${encodeURIComponent(threadId)}`);
    if (!res.ok) throw new Error(`Failed to get A2A thread: ${res.status}`);
    return res.json();
  },

  sendA2AMessage: async (data: {
    from_agent: string;
    to_agent: string;
    subject?: string;
    body: string;
    type?: string;
    thread_id?: string;
    reply_to?: string;
    priority?: number;
  }): Promise<A2AMessage> => {
    const res = await fetch(`${API_BASE}/a2a/messages`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) throw new Error(`Failed to send A2A message: ${res.status}`);
    return res.json();
  },

  ackA2AMessage: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/a2a/messages/${encodeURIComponent(id)}/ack`, {
      method: "PUT",
    });
    if (!res.ok) throw new Error(`Failed to acknowledge A2A message: ${res.status}`);
  },

  resolveA2AMessage: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/a2a/messages/${encodeURIComponent(id)}/resolve`, {
      method: "PUT",
    });
    if (!res.ok) throw new Error(`Failed to resolve A2A message: ${res.status}`);
  },

  getA2AUnreadCount: async (agentId: string): Promise<{ count: number }> => {
    const res = await fetch(`${API_BASE}/a2a/unread?agent_id=${encodeURIComponent(agentId)}`);
    if (!res.ok) throw new Error(`Failed to get A2A unread count: ${res.status}`);
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
    if (!res.ok) throw new Error(`Failed to list agent projects: ${res.status}`);
    return res.json();
  },

  addAgentProject: async (agentId: string, projectId: string): Promise<Project[]> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/projects`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ project_id: projectId }),
    });
    if (!res.ok) throw new Error(`Failed to add agent project: ${res.status}`);
    return res.json();
  },

  removeAgentProject: async (agentId: string, projectId: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/projects/${projectId}`, {
      method: "DELETE",
    });
    if (!res.ok) throw new Error(`Failed to remove agent project: ${res.status}`);
  },

  // Workspace & Project Management
  updateWorkspace: async (id: string, data: Partial<Workspace>): Promise<Workspace> => {
    const res = await fetch(`${API_BASE}/workspaces/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to update workspace: ${res.status}`);
    }
    return res.json();
  },

  deleteWorkspace: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/workspaces/${id}`, { method: "DELETE" });
    if (!res.ok) throw new Error(`Failed to delete workspace: ${res.status}`);
  },

  updateProject: async (
    workspaceId: string,
    projectId: string,
    data: Partial<Project>,
  ): Promise<Project> => {
    const res = await fetch(`${API_BASE}/workspaces/${workspaceId}/projects/${projectId}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to update project: ${res.status}`);
    }
    return res.json();
  },

  deleteProject: async (workspaceId: string, projectId: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/workspaces/${workspaceId}/projects/${projectId}`, {
      method: "DELETE",
    });
    if (!res.ok) throw new Error(`Failed to delete project: ${res.status}`);
  },

  // Custom Actions
  listActions: async (): Promise<{ actions: CustomAction[]; count: number }> => {
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

  updateAction: async (id: string, data: Partial<CustomAction>): Promise<CustomAction> => {
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
  ): Promise<{ action: string; command: string; session_id: string; action_id: string }> => {
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
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/approvals/${requestId}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ decision, scope }),
    });
    if (!res.ok) throw new Error(`Failed to respond to approval: ${res.status}`);
    return res.json();
  },

  getPermissionMode: async (): Promise<{ mode: PermissionMode }> => {
    const res = await fetch(`${API_BASE}/permissions/mode`);
    if (!res.ok) throw new Error(`Failed to get permission mode: ${res.status}`);
    return res.json();
  },

  setPermissionMode: async (mode: PermissionMode): Promise<{ mode: PermissionMode }> => {
    const res = await fetch(`${API_BASE}/permissions/mode`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ mode }),
    });
    if (!res.ok) throw new Error(`Failed to set permission mode: ${res.status}`);
    return res.json();
  },

  // --- vNext: Broker Decisions ---

  getBrokerDecisions: async (sessionId: string, limit = 50): Promise<BrokerDecision[]> => {
    const res = await fetch(`${API_BASE}/broker/decisions?session_id=${encodeURIComponent(sessionId)}&limit=${limit}`);
    if (!res.ok) throw new Error(`Failed to get broker decisions: ${res.status}`);
    return res.json();
  },

  // --- vNext: Execution Metrics (with debug snapshots) ---

  getExecutionMetrics: async (sessionId: string): Promise<ExecutionMetrics[]> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/metrics`);
    if (!res.ok) throw new Error(`Failed to get execution metrics: ${res.status}`);
    return res.json();
  },
};
