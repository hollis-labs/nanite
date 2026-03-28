import type { Session, SessionWithMessages, Message, Workspace, Agent, AgentProfile, AgentModeProfile, Bookmark, Artifact, Workflow, WorkflowResult, SessionAgent, SessionUsageSummary, GlobalUsageSummary, ContextBreakdown, Skill, PromptTemplate, ToolDefinition, ServerInfo, DiscoveryDiff, ToolSelection, MCPServerConfig, VolonSprint, VolonTask, VolonBacklogItem, PluginInfo, A2AMessage, UserSettings, ModelRecord, ProviderConfig, ProviderStatus, CLIDetectionResult, ExecutionMetrics, UtilityCallSummary } from './types'

const API_BASE = '/api'

export const api = {
  // Sessions
  listSessions: async (workspaceId?: string): Promise<Session[]> => {
    const params = workspaceId ? `?workspace_id=${workspaceId}` : ''
    const res = await fetch(`${API_BASE}/sessions${params}`)
    if (!res.ok) throw new Error(`Failed to list sessions: ${res.status}`)
    return res.json()
  },

  getSession: async (id: string): Promise<SessionWithMessages> => {
    const res = await fetch(`${API_BASE}/sessions/${id}`)
    if (!res.ok) throw new Error(`Failed to get session: ${res.status}`)
    return res.json()
  },

  createSession: async (data: { workspace_id: string; project_id?: string; provider?: string; model?: string; agent_id?: string }): Promise<Session> => {
    const res = await fetch(`${API_BASE}/sessions`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) throw new Error(`Failed to create session: ${res.status}`)
    return res.json()
  },

  updateSession: async (id: string, data: Partial<Session>): Promise<Session> => {
    const res = await fetch(`${API_BASE}/sessions/${id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) throw new Error(`Failed to update session: ${res.status}`)
    return res.json()
  },

  forkSession: async (id: string, data: { include_messages: boolean; provider?: string; model?: string }): Promise<Session> => {
    const res = await fetch(`${API_BASE}/sessions/${id}/fork`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) throw new Error(`Failed to fork session: ${res.status}`)
    return res.json()
  },

  deleteSession: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/sessions/${id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error(`Failed to delete session: ${res.status}`)
  },

  // Messages
  sendMessage: async (data: { session_id: string; content: string }): Promise<{ message_id: string; stream_url: string }> => {
    const res = await fetch(`${API_BASE}/messages`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) throw new Error(`Failed to send message: ${res.status}`)
    return res.json()
  },

  retryStream: async (sessionId: string): Promise<{ message_id: string; stream_url: string }> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/retry`, {
      method: 'POST',
    })
    if (!res.ok) throw new Error(`Failed to retry: ${res.status}`)
    return res.json()
  },

  getMessages: async (sessionId: string, limit = 50): Promise<Message[]> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/messages?limit=${limit}`)
    if (!res.ok) throw new Error(`Failed to get messages: ${res.status}`)
    return res.json()
  },

  // Workspaces
  listWorkspaces: async (): Promise<Workspace[]> => {
    const res = await fetch(`${API_BASE}/workspaces`)
    if (!res.ok) throw new Error(`Failed to list workspaces: ${res.status}`)
    return res.json()
  },

  // Agents
  listAgents: async (): Promise<Agent[]> => {
    const res = await fetch(`${API_BASE}/agents`)
    if (!res.ok) throw new Error(`Failed to list agents: ${res.status}`)
    return res.json()
  },

  // Agent Profiles (management)
  listAgentProfiles: async (): Promise<AgentProfile[]> => {
    const res = await fetch(`${API_BASE}/agents`)
    if (!res.ok) throw new Error(`Failed to list agent profiles: ${res.status}`)
    return res.json()
  },

  getAgentProfile: async (id: string): Promise<{ agent: AgentProfile; modes: AgentModeProfile[] }> => {
    const res = await fetch(`${API_BASE}/agents/${id}`)
    if (!res.ok) throw new Error(`Failed to get agent profile: ${res.status}`)
    return res.json()
  },

  createAgentProfile: async (data: Omit<AgentProfile, 'id' | 'created_at' | 'updated_at'>): Promise<AgentProfile> => {
    const res = await fetch(`${API_BASE}/agents`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) throw new Error(`Failed to create agent profile: ${res.status}`)
    return res.json()
  },

  updateAgentProfile: async (id: string, data: Partial<AgentProfile>): Promise<AgentProfile> => {
    const res = await fetch(`${API_BASE}/agents/${id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) throw new Error(`Failed to update agent profile: ${res.status}`)
    return res.json()
  },

  // Note: DELETE agent endpoint not implemented in backend yet
  // deleteAgentProfile: async (id: string): Promise<void> => {
  //   const res = await fetch(`${API_BASE}/agents/${id}`, { method: 'DELETE' })
  //   if (!res.ok) throw new Error(`Failed to delete agent profile: ${res.status}`)
  // },

  // Agent Modes
  listAgentModes: async (agentId: string): Promise<AgentModeProfile[]> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/modes`)
    if (!res.ok) throw new Error(`Failed to list agent modes: ${res.status}`)
    return res.json()
  },

  createAgentMode: async (agentId: string, data: Omit<AgentModeProfile, 'id' | 'agent_id'>): Promise<AgentModeProfile> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/modes`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) throw new Error(`Failed to create agent mode: ${res.status}`)
    return res.json()
  },

  // Note: DELETE mode endpoint not implemented in backend yet
  // deleteAgentMode: async (agentId: string, modeId: string): Promise<void> => {
  //   const res = await fetch(`${API_BASE}/agents/${agentId}/modes/${modeId}`, { method: 'DELETE' })
  //   if (!res.ok) throw new Error(`Failed to delete agent mode: ${res.status}`)
  // },

  // Mode
  switchMode: async (sessionId: string, mode: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/mode`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ mode }),
    })
    if (!res.ok) throw new Error(`Failed to switch mode: ${res.status}`)
  },

  // Pin/Unpin
  pinSession: async (id: string, pinned: boolean): Promise<Session> => {
    const res = await fetch(`${API_BASE}/sessions/${id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ is_pinned: pinned }),
    })
    if (!res.ok) throw new Error(`Failed to pin session: ${res.status}`)
    return res.json()
  },

  // Bookmarks
  listBookmarks: async (sessionId: string): Promise<Bookmark[]> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/bookmarks`)
    if (!res.ok) throw new Error(`Failed to list bookmarks: ${res.status}`)
    return res.json()
  },

  toggleBookmark: async (messageId: string, sessionId: string): Promise<void> => {
    await fetch(`${API_BASE}/messages/${messageId}/bookmark`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ session_id: sessionId }),
    })
  },

  // Artifacts
  listArtifacts: async (sessionId: string): Promise<Artifact[]> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/artifacts`)
    if (!res.ok) throw new Error(`Failed to list artifacts: ${res.status}`)
    return res.json()
  },

  // Compact
  compactSession: async (sessionId: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/compact`, {
      method: 'POST',
    })
    if (!res.ok) throw new Error(`Failed to compact session: ${res.status}`)
  },

  // Workflows
  listWorkflows: async (): Promise<Workflow[]> => {
    const res = await fetch(`${API_BASE}/workflows`)
    if (!res.ok) throw new Error(`Failed to list workflows: ${res.status}`)
    return res.json()
  },

  getWorkflow: async (name: string): Promise<Workflow> => {
    const res = await fetch(`${API_BASE}/workflows/${name}`)
    if (!res.ok) throw new Error(`Failed to get workflow: ${res.status}`)
    return res.json()
  },

  runWorkflow: async (name: string, inputs: Record<string, unknown>): Promise<WorkflowResult> => {
    const res = await fetch(`${API_BASE}/workflows/${name}/run`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(inputs),
    })
    if (!res.ok) throw new Error(`Failed to run workflow: ${res.status}`)
    return res.json()
  },

  // Providers
  listProviders: async (): Promise<ProviderConfig[]> => {
    const res = await fetch(`${API_BASE}/providers`)
    if (!res.ok) throw new Error(`Failed to list providers: ${res.status}`)
    return res.json()
  },
  listProviderStatuses: async (): Promise<ProviderStatus[]> => {
    const res = await fetch(`${API_BASE}/providers/status`)
    if (!res.ok) throw new Error(`Failed to list provider statuses: ${res.status}`)
    return res.json()
  },
  updateProvider: async (id: string, data: { is_enabled?: boolean; base_url?: string; settings?: string }): Promise<ProviderConfig> => {
    const res = await fetch(`${API_BASE}/providers/${id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) throw new Error(`Failed to update provider: ${res.status}`)
    return res.json()
  },
  setProviderAPIKey: async (id: string, apiKey: string): Promise<{ provider_id: string; has_key: boolean }> => {
    const res = await fetch(`${API_BASE}/providers/${id}/api-key`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ api_key: apiKey }),
    })
    if (!res.ok) throw new Error(`Failed to set API key: ${res.status}`)
    return res.json()
  },
  detectCLI: async (): Promise<CLIDetectionResult[]> => {
    const res = await fetch(`${API_BASE}/providers/detect-cli`)
    if (!res.ok) throw new Error(`Failed to detect CLI: ${res.status}`)
    return res.json()
  },

  // Models
  listModels: async (): Promise<ModelRecord[]> => {
    const res = await fetch(`${API_BASE}/models`)
    if (!res.ok) throw new Error(`Failed to list models: ${res.status}`)
    return res.json()
  },

  // Settings
  getSettings: async (): Promise<UserSettings> => {
    const res = await fetch(`${API_BASE}/settings`)
    if (!res.ok) throw new Error(`Failed to get settings: ${res.status}`)
    return res.json()
  },

  updateSettings: async (data: Partial<UserSettings>): Promise<UserSettings> => {
    const res = await fetch(`${API_BASE}/settings`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) throw new Error(`Failed to update settings: ${res.status}`)
    return res.json()
  },

  // Session Agents
  listSessionAgents: async (sessionId: string): Promise<SessionAgent[]> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/agents`)
    if (!res.ok) throw new Error(`Failed to list session agents: ${res.status}`)
    return res.json()
  },

  addSessionAgent: async (sessionId: string, agentId: string, role: 'primary' | 'participant'): Promise<SessionAgent> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/agents`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ agent_id: agentId, role }),
    })
    if (!res.ok) throw new Error(`Failed to add agent to session: ${res.status}`)
    return res.json()
  },

  removeSessionAgent: async (sessionId: string, agentId: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/agents/${agentId}`, {
      method: 'DELETE',
    })
    if (!res.ok) throw new Error(`Failed to remove agent from session: ${res.status}`)
  },

  // Token Usage
  getSessionUsage: async (sessionId: string): Promise<SessionUsageSummary> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/usage`)
    if (!res.ok) throw new Error(`Failed to get session usage: ${res.status}`)
    return res.json()
  },

  getUsageSummary: async (): Promise<GlobalUsageSummary> => {
    const res = await fetch(`${API_BASE}/usage/summary`)
    if (!res.ok) throw new Error(`Failed to get usage summary: ${res.status}`)
    return res.json()
  },

  getContextBreakdown: async (sessionId: string): Promise<ContextBreakdown> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/context-breakdown`)
    if (!res.ok) throw new Error(`Failed to get context breakdown: ${res.status}`)
    return res.json()
  },

  // Execution Metrics
  getSessionMetrics: async (sessionId: string): Promise<ExecutionMetrics[]> => {
    const res = await fetch(`${API_BASE}/sessions/${sessionId}/metrics`)
    if (!res.ok) throw new Error(`Failed to get session metrics: ${res.status}`)
    return res.json()
  },

  getRecentExecutions: async (limit = 50): Promise<ExecutionMetrics[]> => {
    const res = await fetch(`${API_BASE}/metrics/executions?limit=${limit}`)
    if (!res.ok) throw new Error(`Failed to get recent executions: ${res.status}`)
    return res.json()
  },

  getUtilityCallSummary: async (): Promise<UtilityCallSummary[]> => {
    const res = await fetch(`${API_BASE}/metrics/utility`)
    if (!res.ok) throw new Error(`Failed to get utility call summary: ${res.status}`)
    return res.json()
  },

  getUtilityCallLog: async (limit = 50): Promise<ExecutionMetrics[]> => {
    const res = await fetch(`${API_BASE}/metrics/utility/log?limit=${limit}`)
    if (!res.ok) throw new Error(`Failed to get utility call log: ${res.status}`)
    return res.json()
  },

  // Skills
  listSkills: async (): Promise<Skill[]> => {
    const res = await fetch(`${API_BASE}/skills`)
    if (!res.ok) throw new Error(`Failed to list skills: ${res.status}`)
    return res.json()
  },

  getSkill: async (id: string): Promise<Skill> => {
    const res = await fetch(`${API_BASE}/skills/${id}`)
    if (!res.ok) throw new Error(`Failed to get skill: ${res.status}`)
    return res.json()
  },

  createSkill: async (data: Omit<Skill, 'id' | 'created_at' | 'updated_at' | 'is_builtin'>): Promise<Skill> => {
    const res = await fetch(`${API_BASE}/skills`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) throw new Error(`Failed to create skill: ${res.status}`)
    return res.json()
  },

  updateSkill: async (id: string, data: Partial<Omit<Skill, 'id' | 'created_at' | 'updated_at' | 'is_builtin'>>): Promise<Skill> => {
    const res = await fetch(`${API_BASE}/skills/${id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) throw new Error(`Failed to update skill: ${res.status}`)
    return res.json()
  },

  deleteSkill: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/skills/${id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error(`Failed to delete skill: ${res.status}`)
  },

  // Agent Skills (returns Skill[], not a join-table type)
  listAgentSkills: async (agentId: string): Promise<Skill[]> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/skills`)
    if (!res.ok) throw new Error(`Failed to list agent skills: ${res.status}`)
    return res.json()
  },

  assignSkillToAgent: async (agentId: string, data: { skill_id: string; config?: Record<string, unknown> }): Promise<Skill> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/skills`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) throw new Error(`Failed to assign skill to agent: ${res.status}`)
    return res.json()
  },

  removeSkillFromAgent: async (agentId: string, skillId: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/skills/${skillId}`, {
      method: 'DELETE',
    })
    if (!res.ok) throw new Error(`Failed to remove skill from agent: ${res.status}`)
  },

  // Prompt Templates
  listPromptTemplates: async (): Promise<PromptTemplate[]> => {
    const res = await fetch(`${API_BASE}/prompt-templates`)
    if (!res.ok) throw new Error(`Failed to list prompt templates: ${res.status}`)
    return res.json()
  },

  getPromptTemplate: async (id: string): Promise<PromptTemplate> => {
    const res = await fetch(`${API_BASE}/prompt-templates/${id}`)
    if (!res.ok) throw new Error(`Failed to get prompt template: ${res.status}`)
    return res.json()
  },

  createPromptTemplate: async (data: Omit<PromptTemplate, 'id' | 'created_at' | 'updated_at' | 'is_builtin'>): Promise<PromptTemplate> => {
    const res = await fetch(`${API_BASE}/prompt-templates`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) throw new Error(`Failed to create prompt template: ${res.status}`)
    return res.json()
  },

  updatePromptTemplate: async (id: string, data: Partial<Omit<PromptTemplate, 'id' | 'created_at' | 'updated_at' | 'is_builtin'>>): Promise<PromptTemplate> => {
    const res = await fetch(`${API_BASE}/prompt-templates/${id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) throw new Error(`Failed to update prompt template: ${res.status}`)
    return res.json()
  },

  deletePromptTemplate: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/prompt-templates/${id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error(`Failed to delete prompt template: ${res.status}`)
  },

  // Agent Templates (returns PromptTemplate[], not a join-table type)
  listAgentTemplates: async (agentId: string): Promise<PromptTemplate[]> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/prompt-templates`)
    if (!res.ok) throw new Error(`Failed to list agent templates: ${res.status}`)
    return res.json()
  },

  assignTemplateToAgent: async (agentId: string, data: { template_id: string }): Promise<PromptTemplate> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/prompt-templates`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) throw new Error(`Failed to assign template to agent: ${res.status}`)
    return res.json()
  },

  removeTemplateFromAgent: async (agentId: string, templateId: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/prompt-templates/${templateId}`, {
      method: 'DELETE',
    })
    if (!res.ok) throw new Error(`Failed to remove template from agent: ${res.status}`)
  },

  // Volon Backlog
  createVolonBacklogItem: async (data: {
    title: string
    body: string
    priority: string
    tags?: string[]
    project_id?: string
  }): Promise<Record<string, unknown>> => {
    const res = await fetch(`${API_BASE}/volon/backlog`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: `Request failed: ${res.status}` }))
      throw new Error(err.error || `Failed to create backlog item: ${res.status}`)
    }
    return res.json()
  },

  // Tools & MCP
  fetchTools: async (): Promise<ToolDefinition[]> => {
    const res = await fetch(`${API_BASE}/tools`)
    if (!res.ok) throw new Error(`Failed to fetch tools: ${res.status}`)
    return res.json()
  },

  fetchToolServers: async (): Promise<ServerInfo[]> => {
    const res = await fetch(`${API_BASE}/tools/servers`)
    if (!res.ok) throw new Error(`Failed to fetch tool servers: ${res.status}`)
    return res.json()
  },

  refreshTools: async (): Promise<DiscoveryDiff> => {
    const res = await fetch(`${API_BASE}/tools/refresh`, {
      method: 'POST',
    })
    if (!res.ok) throw new Error(`Failed to refresh tools: ${res.status}`)
    return res.json()
  },

  selectTools: async (intent: string): Promise<ToolSelection[]> => {
    const res = await fetch(`${API_BASE}/tools/select`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ intent }),
    })
    if (!res.ok) throw new Error(`Failed to select tools: ${res.status}`)
    return res.json()
  },

  // MCP Servers (user-managed)
  listMCPServers: async (): Promise<MCPServerConfig[]> => {
    const res = await fetch(`${API_BASE}/mcp-servers`)
    if (!res.ok) throw new Error(`Failed to list MCP servers: ${res.status}`)
    return res.json()
  },

  addMCPServer: async (config: Omit<MCPServerConfig, 'id' | 'created_at' | 'updated_at'>): Promise<MCPServerConfig> => {
    const res = await fetch(`${API_BASE}/mcp-servers`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(config),
    })
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: `Request failed: ${res.status}` }))
      throw new Error(err.error || `Failed to add MCP server: ${res.status}`)
    }
    return res.json()
  },

  updateMCPServer: async (name: string, config: Partial<MCPServerConfig>): Promise<MCPServerConfig> => {
    const res = await fetch(`${API_BASE}/mcp-servers/${encodeURIComponent(name)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(config),
    })
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: `Request failed: ${res.status}` }))
      throw new Error(err.error || `Failed to update MCP server: ${res.status}`)
    }
    return res.json()
  },

  deleteMCPServer: async (name: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/mcp-servers/${encodeURIComponent(name)}`, {
      method: 'DELETE',
    })
    if (!res.ok) throw new Error(`Failed to delete MCP server: ${res.status}`)
  },

  // Volon (Sprint Planning)
  getVolonSprints: async (projectId?: string): Promise<{ items: VolonSprint[]; count: number }> => {
    const params = projectId ? `?project_id=${encodeURIComponent(projectId)}` : ''
    const res = await fetch(`${API_BASE}/volon/sprints${params}`)
    if (!res.ok) throw new Error(`Failed to list sprints: ${res.status}`)
    return res.json()
  },

  getVolonTasks: async (sprintId?: string, status?: string, projectId?: string): Promise<{ items: VolonTask[]; count: number }> => {
    const params = new URLSearchParams()
    if (sprintId) params.set('sprint_id', sprintId)
    if (status) params.set('status', status)
    if (projectId) params.set('project_id', projectId)
    const qs = params.toString()
    const res = await fetch(`${API_BASE}/volon/tasks${qs ? `?${qs}` : ''}`)
    if (!res.ok) throw new Error(`Failed to list tasks: ${res.status}`)
    return res.json()
  },

  getVolonBacklog: async (projectId?: string): Promise<{ items: VolonBacklogItem[]; count: number }> => {
    const params = projectId ? `?project_id=${encodeURIComponent(projectId)}` : ''
    const res = await fetch(`${API_BASE}/volon/backlog${params}`)
    if (!res.ok) throw new Error(`Failed to list backlog: ${res.status}`)
    return res.json()
  },

  transitionVolonTask: async (id: string, status: string): Promise<unknown> => {
    const res = await fetch(`${API_BASE}/volon/tasks/${encodeURIComponent(id)}/transition`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ status }),
    })
    if (!res.ok) throw new Error(`Failed to transition task: ${res.status}`)
    return res.json()
  },

  promoteBacklogItem: async (id: string, sprintId: string): Promise<unknown> => {
    const res = await fetch(`${API_BASE}/volon/backlog/${encodeURIComponent(id)}/promote`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sprint_id: sprintId }),
    })
    if (!res.ok) throw new Error(`Failed to promote backlog item: ${res.status}`)
    return res.json()
  },

  deleteVolonTask: async (id: string): Promise<unknown> => {
    const res = await fetch(`${API_BASE}/volon/tasks/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    })
    if (!res.ok) throw new Error(`Failed to delete task: ${res.status}`)
    return res.json()
  },

  // Plugins
  listPlugins: async (): Promise<PluginInfo[]> => {
    const res = await fetch(`${API_BASE}/plugins/managed`)
    if (!res.ok) throw new Error(`Failed to list plugins: ${res.status}`)
    return res.json()
  },

  installPlugin: async (name: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/plugins/install`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name }),
    })
    if (!res.ok) throw new Error(`Failed to install plugin: ${res.status}`)
  },

  uninstallPlugin: async (name: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/plugins/uninstall`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name }),
    })
    if (!res.ok) throw new Error(`Failed to uninstall plugin: ${res.status}`)
  },

  disablePlugin: async (name: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/plugins/disable`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name }),
    })
    if (!res.ok) throw new Error(`Failed to disable plugin: ${res.status}`)
  },

  enablePlugin: async (name: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/plugins/enable`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name }),
    })
    if (!res.ok) throw new Error(`Failed to enable plugin: ${res.status}`)
  },

  // --- A2A Messaging ---

  getA2AInbox: async (agentId: string, status?: string): Promise<A2AMessage[]> => {
    const params = new URLSearchParams({ agent_id: agentId })
    if (status) params.set('status', status)
    const res = await fetch(`${API_BASE}/a2a/inbox?${params}`)
    if (!res.ok) throw new Error(`Failed to get A2A inbox: ${res.status}`)
    return res.json()
  },

  getA2AThread: async (threadId: string): Promise<A2AMessage[]> => {
    const res = await fetch(`${API_BASE}/a2a/threads/${encodeURIComponent(threadId)}`)
    if (!res.ok) throw new Error(`Failed to get A2A thread: ${res.status}`)
    return res.json()
  },

  sendA2AMessage: async (data: {
    from_agent: string
    to_agent: string
    subject?: string
    body: string
    type?: string
    thread_id?: string
    reply_to?: string
    priority?: number
  }): Promise<A2AMessage> => {
    const res = await fetch(`${API_BASE}/a2a/messages`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) throw new Error(`Failed to send A2A message: ${res.status}`)
    return res.json()
  },

  ackA2AMessage: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/a2a/messages/${encodeURIComponent(id)}/ack`, {
      method: 'PUT',
    })
    if (!res.ok) throw new Error(`Failed to acknowledge A2A message: ${res.status}`)
  },

  resolveA2AMessage: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/a2a/messages/${encodeURIComponent(id)}/resolve`, {
      method: 'PUT',
    })
    if (!res.ok) throw new Error(`Failed to resolve A2A message: ${res.status}`)
  },

  getA2AUnreadCount: async (agentId: string): Promise<{ count: number }> => {
    const res = await fetch(`${API_BASE}/a2a/unread?agent_id=${encodeURIComponent(agentId)}`)
    if (!res.ok) throw new Error(`Failed to get A2A unread count: ${res.status}`)
    return res.json()
  },
}
