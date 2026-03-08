import type { Session, SessionWithMessages, Message, Workspace, Agent, AgentProfile, AgentModeProfile, Bookmark, Artifact, Workflow, WorkflowResult, Provider, SessionAgent, SessionUsageSummary, GlobalUsageSummary, Skill, AgentSkill, PromptTemplate, AgentTemplate, ToolDefinition, ServerInfo, DiscoveryDiff, ToolSelection } from './types'

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

  createSession: async (data: { workspace_id: string; project_id?: string }): Promise<Session> => {
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
  listProviders: async (): Promise<Provider[]> => {
    const res = await fetch(`${API_BASE}/providers`)
    if (!res.ok) throw new Error(`Failed to list providers: ${res.status}`)
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

  // Agent Skills
  listAgentSkills: async (agentId: string): Promise<AgentSkill[]> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/skills`)
    if (!res.ok) throw new Error(`Failed to list agent skills: ${res.status}`)
    return res.json()
  },

  assignSkillToAgent: async (agentId: string, data: { skill_id: string; config_override?: Record<string, unknown> }): Promise<AgentSkill> => {
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

  // Agent Templates
  listAgentTemplates: async (agentId: string): Promise<AgentTemplate[]> => {
    const res = await fetch(`${API_BASE}/agents/${agentId}/prompt-templates`)
    if (!res.ok) throw new Error(`Failed to list agent templates: ${res.status}`)
    return res.json()
  },

  assignTemplateToAgent: async (agentId: string, data: { template_id: string }): Promise<AgentTemplate> => {
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
}
