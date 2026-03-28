import { useState, useCallback, useMemo } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Plus,
  Edit,
  Save,
  User,
  Settings,
  Loader2,
  ChevronLeft,
  Code2,
  Wrench,
  X,
  FileText,
  Eye,
  Copy,
  Hash,
} from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { StatusDot } from '@/components/agents/StatusDot'
import { SourceBadge } from '@/components/agents/SourceBadge'
import { TagPills } from '@/components/agents/TagPills'
import { api } from '@/lib/api'
import type { AgentProfile, AgentModeProfile } from '@/lib/types'
import { useModels } from '@/hooks/useSettings'

interface AgentProfileManagerProps {}

export function AgentProfileManager({}: AgentProfileManagerProps) {
  const [selectedAgent, setSelectedAgent] = useState<string | null>(null)
  const [showCreateForm, setShowCreateForm] = useState(false)
  const [editingAgent, setEditingAgent] = useState<AgentProfile | null>(null)
  // const [showDeleteConfirm, setShowDeleteConfirm] = useState<string | null>(null) // DELETE not implemented in backend
  const [showModeForm, setShowModeForm] = useState(false)
  const [showDisabled, setShowDisabled] = useState(false)
  const [sourceFilter, setSourceFilter] = useState<string>('all')
  const [showSkillPicker, setShowSkillPicker] = useState(false)
  const [showTemplatePicker, setShowTemplatePicker] = useState(false)
  const queryClient = useQueryClient()
  const { data: modelRecords } = useModels()
  const modelOptions = (modelRecords ?? []).map((m) => ({ id: m.model_id, label: m.display_name }))

  const { data: agents = [], isLoading } = useQuery({
    queryKey: ['agent-profiles'],
    queryFn: api.listAgentProfiles,
  })

  const { data: agentDetail } = useQuery({
    queryKey: ['agent-detail', selectedAgent],
    queryFn: () => api.getAgentProfile(selectedAgent!),
    enabled: !!selectedAgent,
  })

  const { data: allSkills = [] } = useQuery({
    queryKey: ['skills'],
    queryFn: api.listSkills,
    enabled: !!selectedAgent,
  })

  const { data: agentSkills = [] } = useQuery({
    queryKey: ['agent-skills', selectedAgent],
    queryFn: () => api.listAgentSkills(selectedAgent!),
    enabled: !!selectedAgent,
  })

  const { data: allTemplates = [] } = useQuery({
    queryKey: ['prompt-templates'],
    queryFn: api.listPromptTemplates,
    enabled: !!selectedAgent,
  })

  const { data: agentTemplates = [] } = useQuery({
    queryKey: ['agent-templates', selectedAgent],
    queryFn: () => api.listAgentTemplates(selectedAgent!),
    enabled: !!selectedAgent,
  })

  const createMutation = useMutation({
    mutationFn: api.createAgentProfile,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['agent-profiles'] })
      setShowCreateForm(false)
      setEditingAgent(null)
    },
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: string; data: Partial<AgentProfile> }) =>
      api.updateAgentProfile(id, data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['agent-profiles'] })
      void queryClient.invalidateQueries({ queryKey: ['agent-detail', selectedAgent] })
      setEditingAgent(null)
    },
  })

  // const deleteMutation = useMutation({  // DELETE not implemented in backend
  //   mutationFn: api.deleteAgentProfile,
  //   onSuccess: () => {
  //     void queryClient.invalidateQueries({ queryKey: ['agent-profiles'] })
  //     setSelectedAgent(null)
  //     setShowDeleteConfirm(null)
  //   },
  // })

  const createModeMutation = useMutation({
    mutationFn: ({ agentId, data }: { agentId: string; data: Omit<AgentModeProfile, 'id' | 'agent_id'> }) =>
      api.createAgentMode(agentId, data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['agent-detail', selectedAgent] })
      setShowModeForm(false)
    },
  })

  const assignSkillMutation = useMutation({
    mutationFn: ({ agentId, skillId }: { agentId: string; skillId: string }) =>
      api.assignSkillToAgent(agentId, { skill_id: skillId }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['agent-skills', selectedAgent] })
      setShowSkillPicker(false)
    },
  })

  const removeSkillMutation = useMutation({
    mutationFn: ({ agentId, skillId }: { agentId: string; skillId: string }) =>
      api.removeSkillFromAgent(agentId, skillId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['agent-skills', selectedAgent] })
    },
  })

  const assignTemplateMutation = useMutation({
    mutationFn: ({ agentId, templateId }: { agentId: string; templateId: string }) =>
      api.assignTemplateToAgent(agentId, { template_id: templateId }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['agent-templates', selectedAgent] })
      setShowTemplatePicker(false)
    },
  })

  const removeTemplateMutation = useMutation({
    mutationFn: ({ agentId, templateId }: { agentId: string; templateId: string }) =>
      api.removeTemplateFromAgent(agentId, templateId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['agent-templates', selectedAgent] })
    },
  })

  // const deleteModeMutation = useMutation({  // DELETE not implemented in backend
  //   mutationFn: ({ agentId, modeId }: { agentId: string; modeId: string }) =>
  //     api.deleteAgentMode(agentId, modeId),
  //   onSuccess: () => {
  //     void queryClient.invalidateQueries({ queryKey: ['agent-detail', selectedAgent] })
  //   },
  // })

  const handleCreateAgent = useCallback((formData: FormData) => {
    // Parse tools from multi-line to JSON array
    const toolsRaw = (formData.get('tools') as string || '').trim()
    const toolsArr = toolsRaw ? toolsRaw.split('\n').map((l) => l.trim()).filter(Boolean) : []
    // Parse directories from multi-line to JSON array
    const dirsRaw = (formData.get('directories') as string || '').trim()
    const dirsArr = dirsRaw ? dirsRaw.split('\n').map((l) => l.trim()).filter(Boolean) : []
    // Parse tags from comma-separated to JSON array
    const tagsRaw = (formData.get('tags') as string || '').trim()
    const tagsArr = tagsRaw ? tagsRaw.split(',').map((t) => t.trim()).filter(Boolean) : []
    // Parse constraints from individual fields
    const constraints: Record<string, number> = {}
    const maxIter = formData.get('max_iterations') as string
    const maxTime = formData.get('max_time_seconds') as string
    const retryBudget = formData.get('retry_budget') as string
    if (maxIter) constraints.max_iterations = Number(maxIter)
    if (maxTime) constraints.max_time_seconds = Number(maxTime)
    if (retryBudget) constraints.retry_budget = Number(retryBudget)

    const data = {
      name: formData.get('name') as string,
      slug: formData.get('slug') as string,
      avatar: formData.get('avatar') as string,
      description: formData.get('description') as string,
      system_prompt: formData.get('system_prompt') as string,
      default_model: formData.get('default_model') as string,
      can_execute: formData.get('can_execute') === 'on',
      mcp_servers: formData.get('mcp_servers') as string || '[]',
      tool_permissions: formData.get('tool_permissions') as string || '{}',
      modes: '',
      default_mode: 'default',
      settings: '{}',
      tools: JSON.stringify(toolsArr),
      directories: JSON.stringify(dirsArr),
      constraints: JSON.stringify(constraints),
      tags: JSON.stringify(tagsArr),
      status: 'active',
      source: 'api',
      source_ref: '',
    }
    createMutation.mutate(data)
  }, [createMutation])

  const handleUpdateAgent = useCallback((formData: FormData) => {
    if (!editingAgent) return
    // Parse tools from multi-line to JSON array
    const toolsRaw = (formData.get('tools') as string || '').trim()
    const toolsArr = toolsRaw ? toolsRaw.split('\n').map((l) => l.trim()).filter(Boolean) : []
    // Parse directories from multi-line to JSON array
    const dirsRaw = (formData.get('directories') as string || '').trim()
    const dirsArr = dirsRaw ? dirsRaw.split('\n').map((l) => l.trim()).filter(Boolean) : []
    // Parse tags from comma-separated to JSON array
    const tagsRaw = (formData.get('tags') as string || '').trim()
    const tagsArr = tagsRaw ? tagsRaw.split(',').map((t) => t.trim()).filter(Boolean) : []
    // Parse constraints from individual fields
    const constraints: Record<string, number> = {}
    const maxIter = formData.get('max_iterations') as string
    const maxTime = formData.get('max_time_seconds') as string
    const retryBudget = formData.get('retry_budget') as string
    if (maxIter) constraints.max_iterations = Number(maxIter)
    if (maxTime) constraints.max_time_seconds = Number(maxTime)
    if (retryBudget) constraints.retry_budget = Number(retryBudget)

    const data = {
      name: formData.get('name') as string,
      slug: formData.get('slug') as string,
      avatar: formData.get('avatar') as string,
      description: formData.get('description') as string,
      system_prompt: formData.get('system_prompt') as string,
      default_model: formData.get('default_model') as string,
      can_execute: formData.get('can_execute') === 'on',
      mcp_servers: formData.get('mcp_servers') as string,
      tool_permissions: formData.get('tool_permissions') as string,
      status: formData.get('status') as string || 'active',
      tools: JSON.stringify(toolsArr),
      directories: JSON.stringify(dirsArr),
      tags: JSON.stringify(tagsArr),
      constraints: JSON.stringify(constraints),
    }
    updateMutation.mutate({ id: editingAgent.id, data })
  }, [editingAgent, updateMutation])

  const handleCreateMode = useCallback((formData: FormData) => {
    if (!selectedAgent) return
    const data = {
      name: formData.get('name') as string,
      slug: formData.get('slug') as string,
      prompt_addendum: formData.get('prompt_addendum') as string,
      tool_overrides: formData.get('tool_overrides') as string || '{}',
      settings: formData.get('settings') as string || '{}',
    }
    createModeMutation.mutate({ agentId: selectedAgent, data })
  }, [selectedAgent, createModeMutation])

  const handleAssignSkill = useCallback((skillId: string) => {
    if (!selectedAgent) return
    assignSkillMutation.mutate({ agentId: selectedAgent, skillId })
  }, [selectedAgent, assignSkillMutation])

  const handleRemoveSkill = useCallback((skillId: string) => {
    if (!selectedAgent) return
    removeSkillMutation.mutate({ agentId: selectedAgent, skillId })
  }, [selectedAgent, removeSkillMutation])

  const handleAssignTemplate = useCallback((templateId: string) => {
    if (!selectedAgent) return
    assignTemplateMutation.mutate({ agentId: selectedAgent, templateId })
  }, [selectedAgent, assignTemplateMutation])

  const handleRemoveTemplate = useCallback((templateId: string) => {
    if (!selectedAgent) return
    removeTemplateMutation.mutate({ agentId: selectedAgent, templateId })
  }, [selectedAgent, removeTemplateMutation])

  // Get available skills (not yet assigned to this agent)
  const availableSkills = allSkills.filter(skill =>
    !agentSkills.some(s => s.id === skill.id)
  )

  // Get available templates (not yet assigned to this agent)
  const availableTemplates = allTemplates.filter(template =>
    !agentTemplates.some(t => t.id === template.id)
  )

  // Helper functions for templates
  const getScopeBadgeColor = (scope: string) => {
    switch (scope) {
      case 'system': return 'bg-blue-500'
      case 'mode': return 'bg-green-500'
      case 'skill': return 'bg-yellow-500'
      case 'context': return 'bg-purple-500'
      default: return 'bg-gray-500'
    }
  }

  const filteredAgents = useMemo(() => {
    return agents.filter((a) => {
      if (!showDisabled && a.status === 'disabled') return false
      if (sourceFilter !== 'all' && a.source !== sourceFilter) return false
      return true
    })
  }, [agents, showDisabled, sourceFilter])

  const sourceCounts = useMemo(() => {
    const counts: Record<string, number> = {}
    for (const a of agents) {
      const s = a.source || 'unknown'
      counts[s] = (counts[s] || 0) + 1
    }
    return counts
  }, [agents])

  // List View
  if (!selectedAgent && !showCreateForm) {
    return (
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <h2 className="text-xl font-semibold text-zinc-100">Agent Profiles</h2>
          <Button
            onClick={() => setShowCreateForm(true)}
            className="gap-2"
          >
            <Plus className="w-4 h-4" />
            Create Agent
          </Button>
        </div>

        {/* Filters */}
        <div className="flex items-center gap-3 flex-wrap">
          <div className="flex items-center gap-1.5">
            {['all', ...Object.keys(sourceCounts)].map((s) => (
              <button
                key={s}
                onClick={() => setSourceFilter(s)}
                className={`px-2.5 py-1 rounded-md text-xs font-medium transition-colors ${
                  sourceFilter === s
                    ? 'bg-indigo-600 text-white'
                    : 'bg-zinc-800 text-zinc-400 hover:text-zinc-200 hover:bg-zinc-700'
                }`}
              >
                {s === 'all' ? 'All' : s}
                {s !== 'all' && (
                  <span className="ml-1 text-[10px] opacity-70">{sourceCounts[s]}</span>
                )}
              </button>
            ))}
          </div>
          <label className="flex items-center gap-1.5 text-xs text-zinc-400 ml-auto cursor-pointer select-none">
            <input
              type="checkbox"
              checked={showDisabled}
              onChange={(e) => setShowDisabled(e.target.checked)}
              className="rounded border-zinc-600 bg-zinc-800 text-indigo-500 focus:ring-indigo-500 focus:ring-offset-zinc-900"
            />
            Show disabled
          </label>
        </div>

        {isLoading ? (
          <div className="flex items-center justify-center py-8">
            <Loader2 className="w-6 h-6 animate-spin text-zinc-400" />
          </div>
        ) : filteredAgents.length === 0 ? (
          <div className="text-center py-8 text-zinc-500">
            {agents.length === 0
              ? 'No agent profiles found. Create your first agent to get started.'
              : 'No agents match the current filters.'}
          </div>
        ) : (
          <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
            {filteredAgents.map((agent) => (
              <div
                key={agent.id}
                className={`bg-zinc-800 rounded-lg p-4 border transition-colors cursor-pointer ${
                  agent.status === 'disabled'
                    ? 'border-zinc-700/50 opacity-60 hover:opacity-80'
                    : 'border-zinc-700 hover:border-zinc-600'
                }`}
                onClick={() => setSelectedAgent(agent.id)}
              >
                <div className="flex items-start gap-3">
                  <div className="relative w-10 h-10 rounded-full bg-zinc-700 flex items-center justify-center shrink-0">
                    {agent.avatar ? (
                      <span className="text-lg">{agent.avatar}</span>
                    ) : (
                      <User className="w-5 h-5 text-zinc-400" />
                    )}
                    <span className="absolute -bottom-0.5 -right-0.5 border-2 border-zinc-800 rounded-full">
                      <StatusDot status={agent.status || 'active'} size="md" />
                    </span>
                  </div>
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <h3 className="font-medium text-zinc-100 truncate">{agent.name}</h3>
                      <SourceBadge source={agent.source} className="bg-zinc-700" />
                    </div>
                    <div className="flex items-center gap-2">
                      <p className="text-sm text-zinc-400 truncate">{agent.slug}</p>
                      {agent.version > 0 && (
                        <span className="text-[10px] text-zinc-600">v{agent.version}</span>
                      )}
                    </div>
                    {agent.description && (
                      <p className="text-xs text-zinc-500 mt-1 line-clamp-2">{agent.description}</p>
                    )}
                    <TagPills tags={agent.tags} max={4} className="mt-1.5" />
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    )
  }

  // Create Form
  if (showCreateForm) {
    return (
      <div className="space-y-4">
        <div className="flex items-center gap-3">
          <Button
            variant="ghost"
            size="icon"
            onClick={() => setShowCreateForm(false)}
          >
            <ChevronLeft className="w-4 h-4" />
          </Button>
          <h2 className="text-xl font-semibold text-zinc-100">Create Agent Profile</h2>
        </div>

        <form
          onSubmit={(e) => {
            e.preventDefault()
            handleCreateAgent(new FormData(e.currentTarget))
          }}
          className="space-y-6 max-w-2xl"
        >
          <div className="grid gap-4 md:grid-cols-2">
            <div>
              <label className="block text-sm font-medium text-zinc-300 mb-2">Name</label>
              <input
                name="name"
                type="text"
                required
                className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500"
                placeholder="Agent name"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-zinc-300 mb-2">Slug</label>
              <input
                name="slug"
                type="text"
                required
                className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500"
                placeholder="agent-slug"
              />
            </div>
          </div>

          <div className="grid gap-4 md:grid-cols-2">
            <div>
              <label className="block text-sm font-medium text-zinc-300 mb-2">Avatar (emoji)</label>
              <input
                name="avatar"
                type="text"
                className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500"
                placeholder="🤖"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-zinc-300 mb-2">Default Model</label>
              <select
                name="default_model"
                className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500"
              >
                {modelOptions.map((model) => (
                  <option key={model.id} value={model.id}>
                    {model.label}
                  </option>
                ))}
              </select>
            </div>
          </div>

          <div>
            <label className="block text-sm font-medium text-zinc-300 mb-2">Description</label>
            <input
              name="description"
              type="text"
              className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500"
              placeholder="Brief description of the agent"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-zinc-300 mb-2">System Prompt</label>
            <textarea
              name="system_prompt"
              required
              rows={8}
              className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500 font-mono text-sm"
              placeholder="Enter the system prompt for this agent..."
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-zinc-300 mb-2">MCP Servers (JSON)</label>
            <textarea
              name="mcp_servers"
              rows={3}
              className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500 font-mono text-sm"
              placeholder='[]'
              defaultValue="[]"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-zinc-300 mb-2">Tool Permissions (JSON)</label>
            <textarea
              name="tool_permissions"
              rows={3}
              className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500 font-mono text-sm"
              placeholder='{"allow": ["*"], "deny": []}'
              defaultValue="{}"
            />
          </div>

          <div>
            <label className="flex items-center gap-2">
              <input
                name="can_execute"
                type="checkbox"
                className="rounded border-zinc-600 bg-zinc-800 text-indigo-500 focus:ring-indigo-500 focus:ring-offset-zinc-900"
              />
              <span className="text-sm font-medium text-zinc-300">Can Execute Tools</span>
            </label>
          </div>

          {/* v2 fields */}
          <div>
            <label className="block text-sm font-medium text-zinc-300 mb-2">Tags</label>
            <input
              name="tags"
              type="text"
              className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500 text-sm"
              placeholder="backend, go, infra (comma-separated)"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-zinc-300 mb-2">Tools Allowlist</label>
            <textarea
              name="tools"
              rows={3}
              className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500 font-mono text-sm"
              placeholder={"mcp__engine__*\nmcp__cortex__*\n(one glob pattern per line, empty = all tools)"}
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-zinc-300 mb-2">Directories</label>
            <textarea
              name="directories"
              rows={2}
              className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500 font-mono text-sm"
              placeholder={"internal/api/\nui/src/\n(one path per line)"}
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-zinc-300 mb-2">Constraints</label>
            <div className="grid gap-4 md:grid-cols-3">
              <div>
                <label className="block text-xs text-zinc-500 mb-1">Max Iterations</label>
                <input name="max_iterations" type="number" min="0" className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500 text-sm" placeholder="10" />
              </div>
              <div>
                <label className="block text-xs text-zinc-500 mb-1">Max Time (seconds)</label>
                <input name="max_time_seconds" type="number" min="0" className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500 text-sm" placeholder="300" />
              </div>
              <div>
                <label className="block text-xs text-zinc-500 mb-1">Retry Budget</label>
                <input name="retry_budget" type="number" min="0" className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500 text-sm" placeholder="3" />
              </div>
            </div>
          </div>

          <div className="flex gap-2">
            <Button
              type="submit"
              disabled={createMutation.isPending}
              className="gap-2"
            >
              {createMutation.isPending ? (
                <Loader2 className="w-4 h-4 animate-spin" />
              ) : (
                <Save className="w-4 h-4" />
              )}
              Create Agent
            </Button>
            <Button
              type="button"
              variant="ghost"
              onClick={() => setShowCreateForm(false)}
            >
              Cancel
            </Button>
          </div>
        </form>
      </div>
    )
  }

  // Detail/Edit View
  if (selectedAgent && agentDetail) {
    const { agent, modes } = agentDetail
    const isEditing = editingAgent?.id === agent.id

    if (isEditing) {
      return (
        <div className="space-y-4">
          <div className="flex items-center gap-3">
            <Button
              variant="ghost"
              size="icon"
              onClick={() => setEditingAgent(null)}
            >
              <ChevronLeft className="w-4 h-4" />
            </Button>
            <h2 className="text-xl font-semibold text-zinc-100">Edit {agent.name}</h2>
          </div>

          <form
            onSubmit={(e) => {
              e.preventDefault()
              handleUpdateAgent(new FormData(e.currentTarget))
            }}
            className="space-y-6 max-w-2xl"
          >
            <div className="grid gap-4 md:grid-cols-2">
              <div>
                <label className="block text-sm font-medium text-zinc-300 mb-2">Name</label>
                <input
                  name="name"
                  type="text"
                  required
                  defaultValue={agent.name}
                  className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500"
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-zinc-300 mb-2">Slug</label>
                <input
                  name="slug"
                  type="text"
                  required
                  defaultValue={agent.slug}
                  className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500"
                />
              </div>
            </div>

            <div className="grid gap-4 md:grid-cols-2">
              <div>
                <label className="block text-sm font-medium text-zinc-300 mb-2">Avatar (emoji)</label>
                <input
                  name="avatar"
                  type="text"
                  defaultValue={agent.avatar}
                  className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500"
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-zinc-300 mb-2">Default Model</label>
                <select
                  name="default_model"
                  defaultValue={agent.default_model}
                  className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500"
                >
                  {modelOptions.map((model) => (
                    <option key={model.id} value={model.id}>
                      {model.label}
                    </option>
                  ))}
                </select>
              </div>
            </div>

            <div>
              <label className="block text-sm font-medium text-zinc-300 mb-2">Description</label>
              <input
                name="description"
                type="text"
                defaultValue={agent.description}
                className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500"
              />
            </div>

            <div>
              <label className="block text-sm font-medium text-zinc-300 mb-2">System Prompt</label>
              <textarea
                name="system_prompt"
                required
                rows={8}
                defaultValue={agent.system_prompt}
                className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500 font-mono text-sm"
              />
            </div>

            <div>
              <label className="block text-sm font-medium text-zinc-300 mb-2">MCP Servers (JSON)</label>
              <textarea
                name="mcp_servers"
                rows={3}
                defaultValue={agent.mcp_servers}
                className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500 font-mono text-sm"
              />
            </div>

            <div>
              <label className="block text-sm font-medium text-zinc-300 mb-2">Tool Permissions (JSON)</label>
              <textarea
                name="tool_permissions"
                rows={3}
                defaultValue={agent.tool_permissions}
                className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500 font-mono text-sm"
              />
            </div>

            <div>
              <label className="flex items-center gap-2">
                <input
                  name="can_execute"
                  type="checkbox"
                  defaultChecked={agent.can_execute}
                  className="rounded border-zinc-600 bg-zinc-800 text-indigo-500 focus:ring-indigo-500 focus:ring-offset-zinc-900"
                />
                <span className="text-sm font-medium text-zinc-300">Can Execute Tools</span>
              </label>
            </div>

            {/* v2 fields */}
            <div>
              <label className="block text-sm font-medium text-zinc-300 mb-2">Status</label>
              <select
                name="status"
                defaultValue={agent.status || 'active'}
                className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500 text-sm"
              >
                <option value="active">Active</option>
                <option value="disabled">Disabled</option>
              </select>
              {agent.source === 'agentrc' && (
                <p className="text-xs text-amber-500 mt-1">This agent is managed by agentrc sync. Status may be overwritten on next sync.</p>
              )}
            </div>

            <div>
              <label className="block text-sm font-medium text-zinc-300 mb-2">Tags</label>
              <input
                name="tags"
                type="text"
                defaultValue={(() => { try { return JSON.parse(agent.tags || '[]').join(', ') } catch { return '' } })()}
                className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500 text-sm"
                placeholder="backend, go, infra (comma-separated)"
              />
            </div>

            <div>
              <label className="block text-sm font-medium text-zinc-300 mb-2">Tools Allowlist</label>
              <textarea
                name="tools"
                rows={3}
                defaultValue={(() => { try { return JSON.parse(agent.tools || '[]').join('\n') } catch { return '' } })()}
                className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500 font-mono text-sm"
                placeholder={"mcp__engine__*\nmcp__cortex__*\n(one glob pattern per line, empty = all tools)"}
              />
            </div>

            <div>
              <label className="block text-sm font-medium text-zinc-300 mb-2">Directories</label>
              <textarea
                name="directories"
                rows={2}
                defaultValue={(() => { try { return JSON.parse(agent.directories || '[]').join('\n') } catch { return '' } })()}
                className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500 font-mono text-sm"
                placeholder={"internal/api/\nui/src/\n(one path per line)"}
              />
            </div>

            <div>
              <label className="block text-sm font-medium text-zinc-300 mb-2">Constraints</label>
              {(() => {
                let c: Record<string, number> = {}
                try { c = JSON.parse(agent.constraints || '{}') } catch { /* ignore */ }
                return (
                  <div className="grid gap-4 md:grid-cols-3">
                    <div>
                      <label className="block text-xs text-zinc-500 mb-1">Max Iterations</label>
                      <input name="max_iterations" type="number" min="0" defaultValue={c.max_iterations || ''} className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500 text-sm" placeholder="10" />
                    </div>
                    <div>
                      <label className="block text-xs text-zinc-500 mb-1">Max Time (seconds)</label>
                      <input name="max_time_seconds" type="number" min="0" defaultValue={c.max_time_seconds || ''} className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500 text-sm" placeholder="300" />
                    </div>
                    <div>
                      <label className="block text-xs text-zinc-500 mb-1">Retry Budget</label>
                      <input name="retry_budget" type="number" min="0" defaultValue={c.retry_budget || ''} className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500 text-sm" placeholder="3" />
                    </div>
                  </div>
                )
              })()}
            </div>

            {/* Read-only metadata */}
            <div className="border-t border-zinc-700 pt-4">
              <label className="block text-sm font-medium text-zinc-400 mb-2">Metadata</label>
              <div className="grid gap-3 md:grid-cols-2 text-sm">
                <div className="flex items-center gap-2">
                  <span className="text-zinc-500">Source:</span>
                  <span className="text-zinc-300">{agent.source || 'unknown'}</span>
                </div>
                {agent.source_ref && (
                  <div className="flex items-center gap-2 min-w-0">
                    <span className="text-zinc-500 shrink-0">Source Ref:</span>
                    <span className="text-zinc-300 truncate font-mono text-xs">{agent.source_ref}</span>
                  </div>
                )}
                <div className="flex items-center gap-2">
                  <span className="text-zinc-500">Version:</span>
                  <span className="text-zinc-300">v{agent.version || 0}</span>
                </div>
                {agent.agent_hash && (
                  <div className="flex items-center gap-2 min-w-0">
                    <span className="text-zinc-500 shrink-0">Hash:</span>
                    <span className="text-zinc-300 font-mono text-xs">{agent.agent_hash.slice(0, 12)}</span>
                    <button
                      type="button"
                      onClick={() => navigator.clipboard.writeText(agent.agent_hash)}
                      className="p-0.5 rounded text-zinc-600 hover:text-zinc-300 hover:bg-zinc-700 transition-colors"
                      title="Copy full hash"
                    >
                      <Copy className="w-3 h-3" />
                    </button>
                  </div>
                )}
              </div>
              {agent.source === 'agentrc' && (
                <p className="text-xs text-zinc-500 mt-2">This agent is managed by agentrc sync ({agent.source_ref}). Source and source_ref cannot be changed.</p>
              )}
            </div>

            <div className="flex gap-2">
              <Button
                type="submit"
                disabled={updateMutation.isPending}
                className="gap-2"
              >
                {updateMutation.isPending ? (
                  <Loader2 className="w-4 h-4 animate-spin" />
                ) : (
                  <Save className="w-4 h-4" />
                )}
                Save Changes
              </Button>
              <Button
                type="button"
                variant="ghost"
                onClick={() => setEditingAgent(null)}
              >
                Cancel
              </Button>
            </div>
          </form>
        </div>
      )
    }

    return (
      <div className="space-y-6">
        <div className="flex items-center gap-3">
          <Button
            variant="ghost"
            size="icon"
            onClick={() => setSelectedAgent(null)}
          >
            <ChevronLeft className="w-4 h-4" />
          </Button>
          <div className="flex items-center gap-3 flex-1">
            <div className="w-10 h-10 rounded-full bg-zinc-700 flex items-center justify-center shrink-0">
              {agent.avatar ? (
                <span className="text-lg">{agent.avatar}</span>
              ) : (
                <User className="w-5 h-5 text-zinc-400" />
              )}
            </div>
            <div>
              <h2 className="text-xl font-semibold text-zinc-100">{agent.name}</h2>
              <p className="text-sm text-zinc-400">{agent.slug}</p>
            </div>
          </div>
          <div className="flex gap-2">
            <Button
              onClick={() => setEditingAgent(agent)}
              variant="ghost"
              size="icon"
            >
              <Edit className="w-4 h-4" />
            </Button>
            {/* Delete functionality not implemented in backend yet */}
            {/* <Button
              onClick={() => setShowDeleteConfirm(agent.id)}
              variant="ghost"
              size="icon"
              className="text-red-400 hover:text-red-300"
            >
              <Trash2 className="w-4 h-4" />
            </Button> */}
          </div>
        </div>

        <div className="grid gap-6 lg:grid-cols-2">
          {/* Agent Details */}
          <div className="space-y-4">
            <h3 className="text-lg font-medium text-zinc-200 flex items-center gap-2">
              <Settings className="w-5 h-5" />
              Agent Details
            </h3>

            <div className="space-y-3 bg-zinc-800 rounded-lg p-4">
              <div className="flex items-center gap-3 flex-wrap">
                <span className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-xs font-medium ${
                  agent.status === 'disabled' ? 'bg-zinc-700 text-zinc-400' : 'bg-emerald-500/15 text-emerald-400'
                }`}>
                  <StatusDot status={agent.status || 'active'} />
                  {agent.status || 'active'}
                </span>
                <SourceBadge source={agent.source} className="bg-zinc-700" />
                {agent.version > 0 && (
                  <span className="text-xs text-zinc-500">v{agent.version}</span>
                )}
                {agent.agent_hash && (
                  <span className="flex items-center gap-1 text-xs text-zinc-600 font-mono">
                    <Hash className="w-3 h-3" />
                    {agent.agent_hash.slice(0, 12)}
                    <button
                      onClick={() => navigator.clipboard.writeText(agent.agent_hash)}
                      className="p-0.5 rounded text-zinc-600 hover:text-zinc-300 hover:bg-zinc-700 transition-colors"
                      title="Copy full hash"
                    >
                      <Copy className="w-3 h-3" />
                    </button>
                  </span>
                )}
              </div>

              {agent.source_ref && (
                <div>
                  <label className="text-sm font-medium text-zinc-400">Source Ref</label>
                  <p className="text-xs text-zinc-300 font-mono">{agent.source_ref}</p>
                </div>
              )}

              <div>
                <label className="text-sm font-medium text-zinc-400">Description</label>
                <p className="text-zinc-200">{agent.description || 'No description'}</p>
              </div>

              <div>
                <label className="text-sm font-medium text-zinc-400">Default Model</label>
                <p className="text-zinc-200">{agent.default_model}</p>
              </div>

              <div>
                <label className="text-sm font-medium text-zinc-400">Can Execute</label>
                <p className="text-zinc-200">{agent.can_execute ? 'Yes' : 'No'}</p>
              </div>

              {(() => {
                try {
                  const tags: string[] = JSON.parse(agent.tags || '[]')
                  return tags.length > 0 ? (
                    <div>
                      <label className="text-sm font-medium text-zinc-400">Tags</label>
                      <TagPills tags={agent.tags} max={10} className="mt-1" />
                    </div>
                  ) : null
                } catch { return null }
              })()}

              {(() => {
                try {
                  const tools: string[] = JSON.parse(agent.tools || '[]')
                  return tools.length > 0 ? (
                    <div>
                      <label className="text-sm font-medium text-zinc-400">Tools Allowlist</label>
                      <div className="flex gap-1 flex-wrap mt-1">
                        {tools.map((t) => (
                          <span key={t} className="text-xs px-2 py-0.5 rounded bg-zinc-700 text-zinc-300 font-mono">{t}</span>
                        ))}
                      </div>
                    </div>
                  ) : null
                } catch { return null }
              })()}

              {(() => {
                try {
                  const dirs: string[] = JSON.parse(agent.directories || '[]')
                  return dirs.length > 0 ? (
                    <div>
                      <label className="text-sm font-medium text-zinc-400">Directories</label>
                      <div className="flex gap-1 flex-wrap mt-1">
                        {dirs.map((d) => (
                          <span key={d} className="text-xs px-2 py-0.5 rounded bg-zinc-700 text-zinc-300 font-mono">{d}</span>
                        ))}
                      </div>
                    </div>
                  ) : null
                } catch { return null }
              })()}

              {(() => {
                try {
                  const c = JSON.parse(agent.constraints || '{}')
                  const entries = Object.entries(c).filter(([, v]) => v !== null && v !== undefined)
                  return entries.length > 0 ? (
                    <div>
                      <label className="text-sm font-medium text-zinc-400">Constraints</label>
                      <div className="flex gap-3 mt-1">
                        {entries.map(([k, v]) => (
                          <span key={k} className="text-xs text-zinc-300">
                            <span className="text-zinc-500">{k.replace(/_/g, ' ')}:</span> {String(v)}
                          </span>
                        ))}
                      </div>
                    </div>
                  ) : null
                } catch { return null }
              })()}

              <div>
                <label className="text-sm font-medium text-zinc-400">System Prompt</label>
                <pre className="text-xs text-zinc-300 bg-zinc-900 rounded p-2 mt-1 overflow-x-auto max-h-32 overflow-y-auto font-mono">
                  {agent.system_prompt}
                </pre>
              </div>
            </div>
          </div>

          {/* Modes */}
          <div className="space-y-4">
            <div className="flex items-center justify-between">
              <h3 className="text-lg font-medium text-zinc-200 flex items-center gap-2">
                <Code2 className="w-5 h-5" />
                Modes ({modes.length})
              </h3>
              <Button
                onClick={() => setShowModeForm(true)}
                size="sm"
                variant="secondary"
                className="gap-2"
              >
                <Plus className="w-4 h-4" />
                Add Mode
              </Button>
            </div>

            <div className="space-y-2">
              {modes.length === 0 ? (
                <p className="text-zinc-500 text-center py-4">No modes configured</p>
              ) : (
                modes.map((mode) => (
                  <div
                    key={mode.id}
                    className="bg-zinc-800 rounded-lg p-3 flex items-center justify-between"
                  >
                    <div>
                      <h4 className="font-medium text-zinc-200">{mode.name}</h4>
                      <p className="text-sm text-zinc-400">{mode.slug}</p>
                      {mode.prompt_addendum && (
                        <p className="text-xs text-zinc-500 mt-1 line-clamp-1">
                          {mode.prompt_addendum}
                        </p>
                      )}
                    </div>
                    {/* Delete functionality not implemented in backend yet */}
                    {/* <Button
                      onClick={() =>
                        deleteModeMutation.mutate({ agentId: agent.id, modeId: mode.id })
                      }
                      variant="ghost"
                      size="icon"
                      className="text-red-400 hover:text-red-300"
                      disabled={deleteModeMutation.isPending}
                    >
                      <Trash2 className="w-4 h-4" />
                    </Button> */}
                  </div>
                ))
              )}
            </div>

            {/* Add Mode Form */}
            {showModeForm && (
              <div className="bg-zinc-800 rounded-lg p-4">
                <form
                  onSubmit={(e) => {
                    e.preventDefault()
                    handleCreateMode(new FormData(e.currentTarget))
                  }}
                  className="space-y-4"
                >
                  <div>
                    <label className="block text-sm font-medium text-zinc-300 mb-1">Mode Name</label>
                    <input
                      name="name"
                      type="text"
                      required
                      className="w-full px-3 py-2 bg-zinc-700 border border-zinc-600 rounded text-zinc-100 text-sm focus:outline-none focus:border-indigo-500"
                      placeholder="Mode name"
                    />
                  </div>

                  <div>
                    <label className="block text-sm font-medium text-zinc-300 mb-1">Slug</label>
                    <input
                      name="slug"
                      type="text"
                      required
                      className="w-full px-3 py-2 bg-zinc-700 border border-zinc-600 rounded text-zinc-100 text-sm focus:outline-none focus:border-indigo-500"
                      placeholder="mode-slug"
                    />
                  </div>

                  <div>
                    <label className="block text-sm font-medium text-zinc-300 mb-1">Prompt Addendum</label>
                    <textarea
                      name="prompt_addendum"
                      required
                      rows={3}
                      className="w-full px-3 py-2 bg-zinc-700 border border-zinc-600 rounded text-zinc-100 text-sm focus:outline-none focus:border-indigo-500 font-mono"
                      placeholder="Additional instructions for this mode..."
                    />
                  </div>

                  <div>
                    <label className="block text-sm font-medium text-zinc-300 mb-1">Tool Overrides (JSON)</label>
                    <textarea
                      name="tool_overrides"
                      rows={2}
                      className="w-full px-3 py-2 bg-zinc-700 border border-zinc-600 rounded text-zinc-100 text-sm focus:outline-none focus:border-indigo-500 font-mono"
                      placeholder="{}"
                      defaultValue="{}"
                    />
                  </div>

                  <div className="flex gap-2">
                    <Button
                      type="submit"
                      size="sm"
                      disabled={createModeMutation.isPending}
                    >
                      {createModeMutation.isPending ? (
                        <Loader2 className="w-4 h-4 animate-spin" />
                      ) : (
                        'Add Mode'
                      )}
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      onClick={() => setShowModeForm(false)}
                    >
                      Cancel
                    </Button>
                  </div>
                </form>
              </div>
            )}
          </div>
        </div>

        {/* Skills */}
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <h3 className="text-lg font-medium text-zinc-200 flex items-center gap-2">
              <Wrench className="w-5 h-5" />
              Skills ({agentSkills.length})
            </h3>
            <Button
              onClick={() => setShowSkillPicker(true)}
              size="sm"
              variant="secondary"
              className="gap-2"
              disabled={availableSkills.length === 0}
            >
              <Plus className="w-4 h-4" />
              Add Skill
            </Button>
          </div>

          <div className="space-y-2">
            {agentSkills.length === 0 ? (
              <p className="text-zinc-500 text-center py-4">No skills assigned</p>
            ) : (
              agentSkills.map((skill) => (
                <div
                  key={skill.id}
                  className="bg-zinc-800 rounded-lg p-3 flex items-center justify-between"
                >
                  <div>
                    <h4 className="font-medium text-zinc-200">{skill.name}</h4>
                    <div className="flex items-center gap-2 mt-1">
                      <span className="text-xs bg-zinc-700 text-zinc-300 px-2 py-1 rounded">
                        {skill.category}
                      </span>
                      <span className="text-xs text-zinc-400">{skill.slug}</span>
                    </div>
                  </div>
                  <Button
                    onClick={() => handleRemoveSkill(skill.id)}
                    variant="ghost"
                    size="icon"
                    className="text-red-400 hover:text-red-300"
                    disabled={removeSkillMutation.isPending}
                  >
                    <X className="w-4 h-4" />
                  </Button>
                </div>
              ))
            )}
          </div>

          {/* Skill Picker Modal */}
          {showSkillPicker && (
            <div className="bg-zinc-800 rounded-lg p-4">
              <div className="flex items-center justify-between mb-3">
                <h4 className="font-medium text-zinc-200">Available Skills</h4>
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => setShowSkillPicker(false)}
                >
                  <X className="w-4 h-4" />
                </Button>
              </div>

              {availableSkills.length === 0 ? (
                <p className="text-zinc-500 text-center py-4">All skills are already assigned</p>
              ) : (
                <div className="space-y-2 max-h-64 overflow-y-auto">
                  {availableSkills.map((skill) => (
                    <div
                      key={skill.id}
                      className="flex items-center justify-between p-2 bg-zinc-700 rounded"
                    >
                      <div>
                        <div className="font-medium text-zinc-200 text-sm">{skill.name}</div>
                        <div className="flex items-center gap-2 mt-1">
                          <span className="text-xs bg-zinc-600 text-zinc-300 px-2 py-1 rounded">
                            {skill.category}
                          </span>
                          <span className="text-xs text-zinc-400">
                            {JSON.parse(skill.tool_bindings || '[]').length} tool{JSON.parse(skill.tool_bindings || '[]').length !== 1 ? 's' : ''}
                          </span>
                          {skill.is_builtin && (
                            <span className="w-2 h-2 rounded-full bg-blue-500 inline-block" title="Built-in skill" />
                          )}
                        </div>
                      </div>
                      <Button
                        size="sm"
                        onClick={() => handleAssignSkill(skill.id)}
                        disabled={assignSkillMutation.isPending}
                      >
                        {assignSkillMutation.isPending ? (
                          <Loader2 className="w-3 h-3 animate-spin" />
                        ) : (
                          'Assign'
                        )}
                      </Button>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
        </div>

        {/* Prompt Templates */}
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <h3 className="text-lg font-medium text-zinc-200 flex items-center gap-2">
              <FileText className="w-5 h-5" />
              Prompt Templates ({agentTemplates.length})
            </h3>
            <Button
              onClick={() => setShowTemplatePicker(true)}
              size="sm"
              variant="secondary"
              className="gap-2"
              disabled={availableTemplates.length === 0}
            >
              <Plus className="w-4 h-4" />
              Assign Template
            </Button>
          </div>

          <div className="space-y-2">
            {agentTemplates.length === 0 ? (
              <p className="text-zinc-500 text-center py-4">No prompt templates assigned</p>
            ) : (
              // Sort by priority (descending)
              [...agentTemplates]
                .sort((a, b) => b.priority - a.priority)
                .map((tmpl) => (
                  <div
                    key={tmpl.id}
                    className="bg-zinc-800 rounded-lg p-3 flex items-center justify-between"
                  >
                    <div>
                      <h4 className="font-medium text-zinc-200">{tmpl.name}</h4>
                      <div className="flex items-center gap-2 mt-1">
                        <div className={`w-3 h-3 rounded-full ${getScopeBadgeColor(tmpl.scope)}`} />
                        <span className="text-xs text-zinc-300 capitalize">
                          {tmpl.scope}
                        </span>
                        <span className="text-xs text-zinc-400">{tmpl.slug}</span>
                        <span className="text-xs bg-zinc-700 text-zinc-300 px-2 py-1 rounded">
                          Priority: {tmpl.priority}
                        </span>
                      </div>
                    </div>
                    <Button
                      onClick={() => handleRemoveTemplate(tmpl.id)}
                      variant="ghost"
                      size="icon"
                      className="text-red-400 hover:text-red-300"
                      disabled={removeTemplateMutation.isPending}
                    >
                      <X className="w-4 h-4" />
                    </Button>
                  </div>
                ))
            )}
          </div>

          {/* Template Picker Modal */}
          {showTemplatePicker && (
            <div className="bg-zinc-800 rounded-lg p-4">
              <div className="flex items-center justify-between mb-3">
                <h4 className="font-medium text-zinc-200">Available Templates</h4>
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => setShowTemplatePicker(false)}
                >
                  <X className="w-4 h-4" />
                </Button>
              </div>

              {availableTemplates.length === 0 ? (
                <p className="text-zinc-500 text-center py-4">All templates are already assigned</p>
              ) : (
                <div className="space-y-2 max-h-64 overflow-y-auto">
                  {availableTemplates
                    .sort((a, b) => b.priority - a.priority)
                    .map((template) => (
                      <div
                        key={template.id}
                        className="flex items-center justify-between p-2 bg-zinc-700 rounded"
                      >
                        <div>
                          <div className="font-medium text-zinc-200 text-sm flex items-center gap-2">
                            {template.name}
                            {template.is_builtin && (
                              <span className="w-2 h-2 rounded-full bg-blue-500 inline-block" title="Built-in template" />
                            )}
                          </div>
                          <div className="flex items-center gap-2 mt-1">
                            <div className={`w-2 h-2 rounded-full ${getScopeBadgeColor(template.scope)}`} />
                            <span className="text-xs text-zinc-300 capitalize">
                              {template.scope}
                            </span>
                            <span className="text-xs text-zinc-400">
                              Priority: {template.priority}
                            </span>
                            <span className="text-xs text-zinc-500">
                              {JSON.parse(template.variables || '[]').length} var{JSON.parse(template.variables || '[]').length !== 1 ? 's' : ''}
                            </span>
                          </div>
                        </div>
                        <Button
                          size="sm"
                          onClick={() => handleAssignTemplate(template.id)}
                          disabled={assignTemplateMutation.isPending}
                        >
                          {assignTemplateMutation.isPending ? (
                            <Loader2 className="w-3 h-3 animate-spin" />
                          ) : (
                            'Assign'
                          )}
                        </Button>
                      </div>
                    ))}
                </div>
              )}
            </div>
          )}

          {/* Composed Preview */}
          {agentTemplates.length > 0 && (
            <div className="bg-zinc-800 rounded-lg p-4">
              <div className="flex items-center justify-between mb-3">
                <h4 className="font-medium text-zinc-200 flex items-center gap-2">
                  <Eye className="w-4 h-4" />
                  Composed Template Preview
                </h4>
                <span className="text-xs text-zinc-400">
                  {agentTemplates.length} template{agentTemplates.length !== 1 ? 's' : ''} • Ordered by priority
                </span>
              </div>
              <div className="text-xs text-zinc-300 bg-zinc-900 rounded p-3 overflow-x-auto max-h-64 overflow-y-auto font-mono">
                <div className="space-y-4">
                  {[...agentTemplates]
                    .sort((a, b) => b.priority - a.priority)
                    .map((tmpl) => (
                      <div key={tmpl.id} className="border-l-2 border-zinc-600 pl-3">
                        <div className="text-zinc-400 mb-1">
                          # {tmpl.name} ({tmpl.scope}, priority: {tmpl.priority})
                        </div>
                        <div className="text-zinc-300">
                          [Template body would be rendered here with variable substitution]
                        </div>
                      </div>
                    ))}
                </div>
              </div>
              <p className="text-xs text-zinc-500 mt-2">
                This is a simplified preview. The actual composition would include variable substitution and proper template assembly.
              </p>
            </div>
          )}
        </div>

        {/* Advanced Configuration */}
        <div className="space-y-4">
          <h3 className="text-lg font-medium text-zinc-200 flex items-center gap-2">
            <Wrench className="w-5 h-5" />
            Advanced Configuration
          </h3>

          <div className="grid gap-4 md:grid-cols-2">
            <div>
              <label className="block text-sm font-medium text-zinc-400 mb-2">MCP Servers</label>
              <pre className="text-xs text-zinc-300 bg-zinc-800 rounded p-3 overflow-x-auto max-h-32 overflow-y-auto font-mono">
                {agent.mcp_servers}
              </pre>
            </div>

            <div>
              <label className="block text-sm font-medium text-zinc-400 mb-2">Tool Permissions</label>
              <pre className="text-xs text-zinc-300 bg-zinc-800 rounded p-3 overflow-x-auto max-h-32 overflow-y-auto font-mono">
                {agent.tool_permissions}
              </pre>
            </div>
          </div>
        </div>

        {/* Delete functionality not implemented in backend yet */}
        {/* {showDeleteConfirm === agent.id && (
          <div className="fixed inset-0 z-50 flex items-center justify-center">
            <div className="absolute inset-0 bg-black/60" onClick={() => setShowDeleteConfirm(null)} />
            <div className="relative bg-zinc-900 border border-zinc-700 rounded-xl p-6 max-w-md w-full mx-4">
              <h3 className="text-lg font-semibold text-zinc-100 mb-2">Delete Agent</h3>
              <p className="text-zinc-400 mb-4">
                Are you sure you want to delete "{agent.name}"? This action cannot be undone.
              </p>
              <div className="flex gap-2 justify-end">
                <Button
                  variant="ghost"
                  onClick={() => setShowDeleteConfirm(null)}
                >
                  Cancel
                </Button>
                <Button
                  variant="destructive"
                  onClick={() => deleteMutation.mutate(agent.id)}
                  disabled={deleteMutation.isPending}
                  className="gap-2"
                >
                  {deleteMutation.isPending ? (
                    <Loader2 className="w-4 h-4 animate-spin" />
                  ) : (
                    <Trash2 className="w-4 h-4" />
                  )}
                  Delete
                </Button>
              </div>
            </div>
          </div>
        )} */}
      </div>
    )
  }

  return null
}