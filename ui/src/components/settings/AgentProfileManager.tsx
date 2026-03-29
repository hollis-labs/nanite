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
      case 'context': return 'bg-accent'
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
          <h2 className="text-xl font-semibold text-fg">Agent Profiles</h2>
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
                    ? 'bg-accent text-white'
                    : 'bg-surface text-fg-secondary hover:text-fg hover:bg-surface-hover'
                }`}
              >
                {s === 'all' ? 'All' : s}
                {s !== 'all' && (
                  <span className="ml-1 text-[10px] opacity-70">{sourceCounts[s]}</span>
                )}
              </button>
            ))}
          </div>
          <label className="flex items-center gap-1.5 text-xs text-fg-secondary ml-auto cursor-pointer select-none">
            <input
              type="checkbox"
              checked={showDisabled}
              onChange={(e) => setShowDisabled(e.target.checked)}
              className="rounded border-border-subtle bg-surface text-accent focus:ring-accent focus:ring-offset-bg-elevated"
            />
            Show disabled
          </label>
        </div>

        {isLoading ? (
          <div className="flex items-center justify-center py-8">
            <Loader2 className="w-6 h-6 animate-spin text-fg-secondary" />
          </div>
        ) : filteredAgents.length === 0 ? (
          <div className="text-center py-8 text-fg-muted">
            {agents.length === 0
              ? 'No agent profiles found. Create your first agent to get started.'
              : 'No agents match the current filters.'}
          </div>
        ) : (
          <div className="grid gap-3 grid-cols-2">
            {filteredAgents.map((agent) => {
              const isActive = agent.status !== 'disabled'
              return (
                <div
                  key={agent.id}
                  className={`rounded-xl border shadow-sm overflow-hidden transition-all cursor-pointer ${
                    isActive
                      ? 'border-border-subtle bg-white dark:bg-bg-elevated/60 hover:shadow-md'
                      : 'border-border bg-white dark:bg-bg/30 opacity-45'
                  }`}
                  onClick={() => setSelectedAgent(agent.id)}
                >
                  {/* Header: Icon · Name · Status dot */}
                  <div className="flex items-center gap-2.5 px-3.5 py-3">
                    <span className={`inline-flex items-center justify-center w-9 h-9 rounded-lg text-sm shrink-0 ${
                      isActive
                        ? 'bg-zinc-700 text-zinc-300'
                        : 'bg-zinc-300 text-zinc-500'
                    }`}>
                      {agent.avatar || <User className="w-4 h-4" />}
                    </span>
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2">
                        <span className={`text-sm font-semibold truncate ${isActive ? 'text-fg' : 'text-fg-muted'}`}>
                          {agent.name}
                        </span>
                        {isActive && <StatusDot status={agent.status || 'active'} />}
                      </div>
                      <div className="flex items-center gap-1.5 mt-0.5">
                        <span className="text-[11px] text-fg-muted font-mono truncate">{agent.slug}</span>
                        <SourceBadge source={agent.source} />
                      </div>
                    </div>
                  </div>

                  {/* Detail footer */}
                  <div className="border-t border-border/50 px-3.5 py-2 bg-bg-elevated/40 flex flex-col gap-1.5">
                    {agent.description && (
                      <p className="text-[11px] text-fg-muted line-clamp-2">{agent.description}</p>
                    )}
                    <TagPills tags={agent.tags} max={4} />
                  </div>
                </div>
              )
            })}
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
          <h2 className="text-xl font-semibold text-fg">Create Agent Profile</h2>
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
              <label className="block text-sm font-medium text-fg-secondary mb-2">Name</label>
              <input
                name="name"
                type="text"
                required
                className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
                placeholder="Agent name"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-fg-secondary mb-2">Slug</label>
              <input
                name="slug"
                type="text"
                required
                className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
                placeholder="agent-slug"
              />
            </div>
          </div>

          <div className="grid gap-4 md:grid-cols-2">
            <div>
              <label className="block text-sm font-medium text-fg-secondary mb-2">Avatar (emoji)</label>
              <input
                name="avatar"
                type="text"
                className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
                placeholder="🤖"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-fg-secondary mb-2">Default Model</label>
              <select
                name="default_model"
                className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
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
            <label className="block text-sm font-medium text-fg-secondary mb-2">Description</label>
            <input
              name="description"
              type="text"
              className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
              placeholder="Brief description of the agent"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-fg-secondary mb-2">System Prompt</label>
            <textarea
              name="system_prompt"
              required
              rows={8}
              className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent font-mono text-sm"
              placeholder="Enter the system prompt for this agent..."
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-fg-secondary mb-2">MCP Servers (JSON)</label>
            <textarea
              name="mcp_servers"
              rows={3}
              className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent font-mono text-sm"
              placeholder='[]'
              defaultValue="[]"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-fg-secondary mb-2">Tool Permissions (JSON)</label>
            <textarea
              name="tool_permissions"
              rows={3}
              className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent font-mono text-sm"
              placeholder='{"allow": ["*"], "deny": []}'
              defaultValue="{}"
            />
          </div>

          <div>
            <label className="flex items-center gap-2">
              <input
                name="can_execute"
                type="checkbox"
                className="rounded border-border-subtle bg-surface text-accent focus:ring-accent focus:ring-offset-bg-elevated"
              />
              <span className="text-sm font-medium text-fg-secondary">Can Execute Tools</span>
            </label>
          </div>

          {/* v2 fields */}
          <div>
            <label className="block text-sm font-medium text-fg-secondary mb-2">Tags</label>
            <input
              name="tags"
              type="text"
              className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent text-sm"
              placeholder="backend, go, infra (comma-separated)"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-fg-secondary mb-2">Tools Allowlist</label>
            <textarea
              name="tools"
              rows={3}
              className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent font-mono text-sm"
              placeholder={"mcp__engine__*\nmcp__cortex__*\n(one glob pattern per line, empty = all tools)"}
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-fg-secondary mb-2">Directories</label>
            <textarea
              name="directories"
              rows={2}
              className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent font-mono text-sm"
              placeholder={"internal/api/\nui/src/\n(one path per line)"}
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-fg-secondary mb-2">Constraints</label>
            <div className="grid gap-4 md:grid-cols-3">
              <div>
                <label className="block text-xs text-fg-muted mb-1">Max Iterations</label>
                <input name="max_iterations" type="number" min="0" className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent text-sm" placeholder="10" />
              </div>
              <div>
                <label className="block text-xs text-fg-muted mb-1">Max Time (seconds)</label>
                <input name="max_time_seconds" type="number" min="0" className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent text-sm" placeholder="300" />
              </div>
              <div>
                <label className="block text-xs text-fg-muted mb-1">Retry Budget</label>
                <input name="retry_budget" type="number" min="0" className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent text-sm" placeholder="3" />
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
            <h2 className="text-xl font-semibold text-fg">Edit {agent.name}</h2>
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
                <label className="block text-sm font-medium text-fg-secondary mb-2">Name</label>
                <input
                  name="name"
                  type="text"
                  required
                  defaultValue={agent.name}
                  className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-fg-secondary mb-2">Slug</label>
                <input
                  name="slug"
                  type="text"
                  required
                  defaultValue={agent.slug}
                  className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
                />
              </div>
            </div>

            <div className="grid gap-4 md:grid-cols-2">
              <div>
                <label className="block text-sm font-medium text-fg-secondary mb-2">Avatar (emoji)</label>
                <input
                  name="avatar"
                  type="text"
                  defaultValue={agent.avatar}
                  className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-fg-secondary mb-2">Default Model</label>
                <select
                  name="default_model"
                  defaultValue={agent.default_model}
                  className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
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
              <label className="block text-sm font-medium text-fg-secondary mb-2">Description</label>
              <input
                name="description"
                type="text"
                defaultValue={agent.description}
                className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
              />
            </div>

            <div>
              <label className="block text-sm font-medium text-fg-secondary mb-2">System Prompt</label>
              <textarea
                name="system_prompt"
                required
                rows={8}
                defaultValue={agent.system_prompt}
                className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent font-mono text-sm"
              />
            </div>

            <div>
              <label className="block text-sm font-medium text-fg-secondary mb-2">MCP Servers (JSON)</label>
              <textarea
                name="mcp_servers"
                rows={3}
                defaultValue={agent.mcp_servers}
                className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent font-mono text-sm"
              />
            </div>

            <div>
              <label className="block text-sm font-medium text-fg-secondary mb-2">Tool Permissions (JSON)</label>
              <textarea
                name="tool_permissions"
                rows={3}
                defaultValue={agent.tool_permissions}
                className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent font-mono text-sm"
              />
            </div>

            <div>
              <label className="flex items-center gap-2">
                <input
                  name="can_execute"
                  type="checkbox"
                  defaultChecked={agent.can_execute}
                  className="rounded border-border-subtle bg-surface text-accent focus:ring-accent focus:ring-offset-bg-elevated"
                />
                <span className="text-sm font-medium text-fg-secondary">Can Execute Tools</span>
              </label>
            </div>

            {/* v2 fields */}
            <div>
              <label className="block text-sm font-medium text-fg-secondary mb-2">Status</label>
              <select
                name="status"
                defaultValue={agent.status || 'active'}
                className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent text-sm"
              >
                <option value="active">Active</option>
                <option value="disabled">Disabled</option>
              </select>
              {agent.source === 'agentrc' && (
                <p className="text-xs text-amber-500 mt-1">This agent is managed by agentrc sync. Status may be overwritten on next sync.</p>
              )}
            </div>

            <div>
              <label className="block text-sm font-medium text-fg-secondary mb-2">Tags</label>
              <input
                name="tags"
                type="text"
                defaultValue={(() => { try { return JSON.parse(agent.tags || '[]').join(', ') } catch { return '' } })()}
                className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent text-sm"
                placeholder="backend, go, infra (comma-separated)"
              />
            </div>

            <div>
              <label className="block text-sm font-medium text-fg-secondary mb-2">Tools Allowlist</label>
              <textarea
                name="tools"
                rows={3}
                defaultValue={(() => { try { return JSON.parse(agent.tools || '[]').join('\n') } catch { return '' } })()}
                className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent font-mono text-sm"
                placeholder={"mcp__engine__*\nmcp__cortex__*\n(one glob pattern per line, empty = all tools)"}
              />
            </div>

            <div>
              <label className="block text-sm font-medium text-fg-secondary mb-2">Directories</label>
              <textarea
                name="directories"
                rows={2}
                defaultValue={(() => { try { return JSON.parse(agent.directories || '[]').join('\n') } catch { return '' } })()}
                className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent font-mono text-sm"
                placeholder={"internal/api/\nui/src/\n(one path per line)"}
              />
            </div>

            <div>
              <label className="block text-sm font-medium text-fg-secondary mb-2">Constraints</label>
              {(() => {
                let c: Record<string, number> = {}
                try { c = JSON.parse(agent.constraints || '{}') } catch { /* ignore */ }
                return (
                  <div className="grid gap-4 md:grid-cols-3">
                    <div>
                      <label className="block text-xs text-fg-muted mb-1">Max Iterations</label>
                      <input name="max_iterations" type="number" min="0" defaultValue={c.max_iterations || ''} className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent text-sm" placeholder="10" />
                    </div>
                    <div>
                      <label className="block text-xs text-fg-muted mb-1">Max Time (seconds)</label>
                      <input name="max_time_seconds" type="number" min="0" defaultValue={c.max_time_seconds || ''} className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent text-sm" placeholder="300" />
                    </div>
                    <div>
                      <label className="block text-xs text-fg-muted mb-1">Retry Budget</label>
                      <input name="retry_budget" type="number" min="0" defaultValue={c.retry_budget || ''} className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent text-sm" placeholder="3" />
                    </div>
                  </div>
                )
              })()}
            </div>

            {/* Read-only metadata */}
            <div className="border-t border-border-subtle pt-4">
              <label className="block text-sm font-medium text-fg-secondary mb-2">Metadata</label>
              <div className="grid gap-3 md:grid-cols-2 text-sm">
                <div className="flex items-center gap-2">
                  <span className="text-fg-muted">Source:</span>
                  <span className="text-fg-secondary">{agent.source || 'unknown'}</span>
                </div>
                {agent.source_ref && (
                  <div className="flex items-center gap-2 min-w-0">
                    <span className="text-fg-muted shrink-0">Source Ref:</span>
                    <span className="text-fg-secondary truncate font-mono text-xs">{agent.source_ref}</span>
                  </div>
                )}
                <div className="flex items-center gap-2">
                  <span className="text-fg-muted">Version:</span>
                  <span className="text-fg-secondary">v{agent.version || 0}</span>
                </div>
                {agent.agent_hash && (
                  <div className="flex items-center gap-2 min-w-0">
                    <span className="text-fg-muted shrink-0">Hash:</span>
                    <span className="text-fg-secondary font-mono text-xs">{agent.agent_hash.slice(0, 12)}</span>
                    <button
                      type="button"
                      onClick={() => navigator.clipboard.writeText(agent.agent_hash)}
                      className="p-0.5 rounded text-fg-faint hover:text-fg-secondary hover:bg-surface-hover transition-colors"
                      title="Copy full hash"
                    >
                      <Copy className="w-3 h-3" />
                    </button>
                  </div>
                )}
              </div>
              {agent.source === 'agentrc' && (
                <p className="text-xs text-fg-muted mt-2">This agent is managed by agentrc sync ({agent.source_ref}). Source and source_ref cannot be changed.</p>
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
            <div className="w-10 h-10 rounded-full bg-surface-hover flex items-center justify-center shrink-0">
              {agent.avatar ? (
                <span className="text-lg">{agent.avatar}</span>
              ) : (
                <User className="w-5 h-5 text-fg-secondary" />
              )}
            </div>
            <div>
              <h2 className="text-xl font-semibold text-fg">{agent.name}</h2>
              <p className="text-sm text-fg-secondary">{agent.slug}</p>
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
            <h3 className="text-lg font-medium text-fg flex items-center gap-2">
              <Settings className="w-5 h-5" />
              Agent Details
            </h3>

            <div className="space-y-3 bg-white dark:bg-bg-elevated/60 rounded-xl border border-border-subtle shadow-sm p-4">
              <div className="flex items-center gap-3 flex-wrap">
                <span className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-xs font-medium ${
                  agent.status === 'disabled' ? 'bg-surface-hover text-fg-secondary' : 'bg-emerald-500/15 text-emerald-400'
                }`}>
                  <StatusDot status={agent.status || 'active'} />
                  {agent.status || 'active'}
                </span>
                <SourceBadge source={agent.source} className="bg-surface-hover" />
                {agent.version > 0 && (
                  <span className="text-xs text-fg-muted">v{agent.version}</span>
                )}
                {agent.agent_hash && (
                  <span className="flex items-center gap-1 text-xs text-fg-faint font-mono">
                    <Hash className="w-3 h-3" />
                    {agent.agent_hash.slice(0, 12)}
                    <button
                      onClick={() => navigator.clipboard.writeText(agent.agent_hash)}
                      className="p-0.5 rounded text-fg-faint hover:text-fg-secondary hover:bg-surface-hover transition-colors"
                      title="Copy full hash"
                    >
                      <Copy className="w-3 h-3" />
                    </button>
                  </span>
                )}
              </div>

              {agent.source_ref && (
                <div>
                  <label className="text-sm font-medium text-fg-secondary">Source Ref</label>
                  <p className="text-xs text-fg-secondary font-mono">{agent.source_ref}</p>
                </div>
              )}

              <div>
                <label className="text-sm font-medium text-fg-secondary">Description</label>
                <p className="text-fg">{agent.description || 'No description'}</p>
              </div>

              <div>
                <label className="text-sm font-medium text-fg-secondary">Default Model</label>
                <p className="text-fg">{agent.default_model}</p>
              </div>

              <div>
                <label className="text-sm font-medium text-fg-secondary">Can Execute</label>
                <p className="text-fg">{agent.can_execute ? 'Yes' : 'No'}</p>
              </div>

              {(() => {
                try {
                  const tags: string[] = JSON.parse(agent.tags || '[]')
                  return tags.length > 0 ? (
                    <div>
                      <label className="text-sm font-medium text-fg-secondary">Tags</label>
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
                      <label className="text-sm font-medium text-fg-secondary">Tools Allowlist</label>
                      <div className="flex gap-1 flex-wrap mt-1">
                        {tools.map((t) => (
                          <span key={t} className="text-xs px-2 py-0.5 rounded bg-surface-hover text-fg-secondary font-mono">{t}</span>
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
                      <label className="text-sm font-medium text-fg-secondary">Directories</label>
                      <div className="flex gap-1 flex-wrap mt-1">
                        {dirs.map((d) => (
                          <span key={d} className="text-xs px-2 py-0.5 rounded bg-surface-hover text-fg-secondary font-mono">{d}</span>
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
                      <label className="text-sm font-medium text-fg-secondary">Constraints</label>
                      <div className="flex gap-3 mt-1">
                        {entries.map(([k, v]) => (
                          <span key={k} className="text-xs text-fg-secondary">
                            <span className="text-fg-muted">{k.replace(/_/g, ' ')}:</span> {String(v)}
                          </span>
                        ))}
                      </div>
                    </div>
                  ) : null
                } catch { return null }
              })()}

              <div>
                <label className="text-sm font-medium text-fg-secondary">System Prompt</label>
                <pre className="text-xs text-fg-secondary bg-bg-elevated rounded p-2 mt-1 overflow-x-auto max-h-32 overflow-y-auto font-mono">
                  {agent.system_prompt}
                </pre>
              </div>
            </div>
          </div>

          {/* Modes */}
          <div className="space-y-4">
            <div className="flex items-center justify-between">
              <h3 className="text-lg font-medium text-fg flex items-center gap-2">
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
                <p className="text-fg-muted text-center py-4">No modes configured</p>
              ) : (
                modes.map((mode) => (
                  <div
                    key={mode.id}
                    className="bg-white dark:bg-bg-elevated/60 rounded-xl border border-border-subtle shadow-sm p-3 flex items-center justify-between"
                  >
                    <div>
                      <h4 className="font-medium text-fg">{mode.name}</h4>
                      <p className="text-sm text-fg-secondary">{mode.slug}</p>
                      {mode.prompt_addendum && (
                        <p className="text-xs text-fg-muted mt-1 line-clamp-1">
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
              <div className="bg-white dark:bg-bg-elevated/60 rounded-xl border border-border-subtle shadow-sm p-4">
                <form
                  onSubmit={(e) => {
                    e.preventDefault()
                    handleCreateMode(new FormData(e.currentTarget))
                  }}
                  className="space-y-4"
                >
                  <div>
                    <label className="block text-sm font-medium text-fg-secondary mb-1">Mode Name</label>
                    <input
                      name="name"
                      type="text"
                      required
                      className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg text-sm focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
                      placeholder="Mode name"
                    />
                  </div>

                  <div>
                    <label className="block text-sm font-medium text-fg-secondary mb-1">Slug</label>
                    <input
                      name="slug"
                      type="text"
                      required
                      className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg text-sm focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
                      placeholder="mode-slug"
                    />
                  </div>

                  <div>
                    <label className="block text-sm font-medium text-fg-secondary mb-1">Prompt Addendum</label>
                    <textarea
                      name="prompt_addendum"
                      required
                      rows={3}
                      className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg text-sm focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent font-mono"
                      placeholder="Additional instructions for this mode..."
                    />
                  </div>

                  <div>
                    <label className="block text-sm font-medium text-fg-secondary mb-1">Tool Overrides (JSON)</label>
                    <textarea
                      name="tool_overrides"
                      rows={2}
                      className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg text-sm focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent font-mono"
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
            <h3 className="text-lg font-medium text-fg flex items-center gap-2">
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
              <p className="text-fg-muted text-center py-4">No skills assigned</p>
            ) : (
              agentSkills.map((skill) => (
                <div
                  key={skill.id}
                  className="bg-white dark:bg-bg-elevated/60 rounded-xl border border-border-subtle shadow-sm p-3 flex items-center justify-between"
                >
                  <div>
                    <h4 className="font-medium text-fg">{skill.name}</h4>
                    <div className="flex items-center gap-2 mt-1">
                      <span className="text-xs bg-surface-hover text-fg-secondary px-2 py-1 rounded">
                        {skill.category}
                      </span>
                      <span className="text-xs text-fg-secondary">{skill.slug}</span>
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
            <div className="bg-white dark:bg-bg-elevated/60 rounded-xl border border-border-subtle shadow-sm p-4">
              <div className="flex items-center justify-between mb-3">
                <h4 className="font-medium text-fg">Available Skills</h4>
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => setShowSkillPicker(false)}
                >
                  <X className="w-4 h-4" />
                </Button>
              </div>

              {availableSkills.length === 0 ? (
                <p className="text-fg-muted text-center py-4">All skills are already assigned</p>
              ) : (
                <div className="space-y-2 max-h-64 overflow-y-auto">
                  {availableSkills.map((skill) => (
                    <div
                      key={skill.id}
                      className="flex items-center justify-between p-2 bg-surface-hover rounded"
                    >
                      <div>
                        <div className="font-medium text-fg text-sm">{skill.name}</div>
                        <div className="flex items-center gap-2 mt-1">
                          <span className="text-xs bg-surface-hover text-fg-secondary px-2 py-1 rounded">
                            {skill.category}
                          </span>
                          <span className="text-xs text-fg-secondary">
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
            <h3 className="text-lg font-medium text-fg flex items-center gap-2">
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
              <p className="text-fg-muted text-center py-4">No prompt templates assigned</p>
            ) : (
              // Sort by priority (descending)
              [...agentTemplates]
                .sort((a, b) => b.priority - a.priority)
                .map((tmpl) => (
                  <div
                    key={tmpl.id}
                    className="bg-white dark:bg-bg-elevated/60 rounded-xl border border-border-subtle shadow-sm p-3 flex items-center justify-between"
                  >
                    <div>
                      <h4 className="font-medium text-fg">{tmpl.name}</h4>
                      <div className="flex items-center gap-2 mt-1">
                        <div className={`w-3 h-3 rounded-full ${getScopeBadgeColor(tmpl.scope)}`} />
                        <span className="text-xs text-fg-secondary capitalize">
                          {tmpl.scope}
                        </span>
                        <span className="text-xs text-fg-secondary">{tmpl.slug}</span>
                        <span className="text-xs bg-surface-hover text-fg-secondary px-2 py-1 rounded">
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
            <div className="bg-white dark:bg-bg-elevated/60 rounded-xl border border-border-subtle shadow-sm p-4">
              <div className="flex items-center justify-between mb-3">
                <h4 className="font-medium text-fg">Available Templates</h4>
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => setShowTemplatePicker(false)}
                >
                  <X className="w-4 h-4" />
                </Button>
              </div>

              {availableTemplates.length === 0 ? (
                <p className="text-fg-muted text-center py-4">All templates are already assigned</p>
              ) : (
                <div className="space-y-2 max-h-64 overflow-y-auto">
                  {availableTemplates
                    .sort((a, b) => b.priority - a.priority)
                    .map((template) => (
                      <div
                        key={template.id}
                        className="flex items-center justify-between p-2 bg-surface-hover rounded"
                      >
                        <div>
                          <div className="font-medium text-fg text-sm flex items-center gap-2">
                            {template.name}
                            {template.is_builtin && (
                              <span className="w-2 h-2 rounded-full bg-blue-500 inline-block" title="Built-in template" />
                            )}
                          </div>
                          <div className="flex items-center gap-2 mt-1">
                            <div className={`w-2 h-2 rounded-full ${getScopeBadgeColor(template.scope)}`} />
                            <span className="text-xs text-fg-secondary capitalize">
                              {template.scope}
                            </span>
                            <span className="text-xs text-fg-secondary">
                              Priority: {template.priority}
                            </span>
                            <span className="text-xs text-fg-muted">
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
            <div className="bg-white dark:bg-bg-elevated/60 rounded-xl border border-border-subtle shadow-sm p-4">
              <div className="flex items-center justify-between mb-3">
                <h4 className="font-medium text-fg flex items-center gap-2">
                  <Eye className="w-4 h-4" />
                  Composed Template Preview
                </h4>
                <span className="text-xs text-fg-secondary">
                  {agentTemplates.length} template{agentTemplates.length !== 1 ? 's' : ''} • Ordered by priority
                </span>
              </div>
              <div className="text-xs text-fg-secondary bg-bg-elevated rounded p-3 overflow-x-auto max-h-64 overflow-y-auto font-mono">
                <div className="space-y-4">
                  {[...agentTemplates]
                    .sort((a, b) => b.priority - a.priority)
                    .map((tmpl) => (
                      <div key={tmpl.id} className="border-l-2 border-border-subtle pl-3">
                        <div className="text-fg-secondary mb-1">
                          # {tmpl.name} ({tmpl.scope}, priority: {tmpl.priority})
                        </div>
                        <div className="text-fg-secondary">
                          [Template body would be rendered here with variable substitution]
                        </div>
                      </div>
                    ))}
                </div>
              </div>
              <p className="text-xs text-fg-muted mt-2">
                This is a simplified preview. The actual composition would include variable substitution and proper template assembly.
              </p>
            </div>
          )}
        </div>

        {/* Advanced Configuration */}
        <div className="space-y-4">
          <h3 className="text-lg font-medium text-fg flex items-center gap-2">
            <Wrench className="w-5 h-5" />
            Advanced Configuration
          </h3>

          <div className="grid gap-4 md:grid-cols-2">
            <div>
              <label className="block text-sm font-medium text-fg-secondary mb-2">MCP Servers</label>
              <pre className="text-xs text-fg-secondary bg-surface rounded p-3 overflow-x-auto max-h-32 overflow-y-auto font-mono">
                {agent.mcp_servers}
              </pre>
            </div>

            <div>
              <label className="block text-sm font-medium text-fg-secondary mb-2">Tool Permissions</label>
              <pre className="text-xs text-fg-secondary bg-surface rounded p-3 overflow-x-auto max-h-32 overflow-y-auto font-mono">
                {agent.tool_permissions}
              </pre>
            </div>
          </div>
        </div>

        {/* Delete functionality not implemented in backend yet */}
        {/* {showDeleteConfirm === agent.id && (
          <div className="fixed inset-0 z-50 flex items-center justify-center">
            <div className="absolute inset-0 bg-black/60" onClick={() => setShowDeleteConfirm(null)} />
            <div className="relative bg-bg-elevated border border-border-subtle rounded-xl p-6 max-w-md w-full mx-4">
              <h3 className="text-lg font-semibold text-fg mb-2">Delete Agent</h3>
              <p className="text-fg-secondary mb-4">
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