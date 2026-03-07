import type { Session, SessionWithMessages, Message, Workspace, Agent, Bookmark, Artifact, Workflow, WorkflowResult, Provider, SessionAgent } from './types'

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
}
