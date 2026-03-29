import { useState, useCallback } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Plus,
  Edit,
  Save,
  Loader2,
  ChevronLeft,
  Wrench,
  Code2,
  Settings,
  Filter,
  Trash2,
} from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { api } from '@/lib/api'
import type { Skill, ToolBinding } from '@/lib/types'

function parseToolBindings(s: string): ToolBinding[] {
  try { return JSON.parse(s) } catch { return [] }
}

function parseInputSchema(s: string): Record<string, unknown> | null {
  try { const v = JSON.parse(s); return v && typeof v === 'object' ? v : null } catch { return null }
}

interface SkillsBrowserProps {}

const SKILL_CATEGORIES = [
  'general',
  'development',
  'communication',
  'analysis',
  'automation',
  'integration',
  'productivity',
  'other',
] as const

export function SkillsBrowser({}: SkillsBrowserProps) {
  const [selectedSkill, setSelectedSkill] = useState<string | null>(null)
  const [showCreateForm, setShowCreateForm] = useState(false)
  const [editingSkill, setEditingSkill] = useState<Skill | null>(null)
  const [categoryFilter, setCategoryFilter] = useState<string>('all')
  const [showDeleteConfirm, setShowDeleteConfirm] = useState<string | null>(null)
  const queryClient = useQueryClient()

  const { data: skills = [], isLoading } = useQuery({
    queryKey: ['skills'],
    queryFn: api.listSkills,
  })

  const { data: skillDetail } = useQuery({
    queryKey: ['skill-detail', selectedSkill],
    queryFn: () => api.getSkill(selectedSkill!),
    enabled: !!selectedSkill,
  })

  const createMutation = useMutation({
    mutationFn: api.createSkill,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['skills'] })
      setShowCreateForm(false)
      setEditingSkill(null)
    },
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: string; data: Partial<Skill> }) =>
      api.updateSkill(id, data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['skills'] })
      void queryClient.invalidateQueries({ queryKey: ['skill-detail', selectedSkill] })
      setEditingSkill(null)
    },
  })

  const deleteMutation = useMutation({
    mutationFn: api.deleteSkill,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['skills'] })
      setSelectedSkill(null)
      setShowDeleteConfirm(null)
    },
  })

  const handleCreateSkill = useCallback((formData: FormData) => {
    const data = {
      name: formData.get('name') as string,
      slug: formData.get('slug') as string,
      category: formData.get('category') as string,
      description: formData.get('description') as string,
      tool_bindings: formData.get('tool_bindings') as string || '[]',
      input_schema: formData.get('input_schema') as string || '{}',
      settings: '{}',
    }
    createMutation.mutate(data)
  }, [createMutation])

  const handleUpdateSkill = useCallback((formData: FormData) => {
    if (!editingSkill) return
    const data = {
      name: formData.get('name') as string,
      slug: formData.get('slug') as string,
      category: formData.get('category') as string,
      description: formData.get('description') as string,
      tool_bindings: formData.get('tool_bindings') as string || '[]',
      input_schema: formData.get('input_schema') as string || '{}',
    }
    updateMutation.mutate({ id: editingSkill.id, data })
  }, [editingSkill, updateMutation])

  // Filter skills based on category
  const filteredSkills = skills.filter(skill =>
    categoryFilter === 'all' || skill.category === categoryFilter
  )

  // List View
  if (!selectedSkill && !showCreateForm) {
    return (
      <div className="space-y-4">
        {/* Toolbar */}
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-2">
            <Filter className="w-3.5 h-3.5 text-fg-muted" />
            <select
              value={categoryFilter}
              onChange={(e) => setCategoryFilter(e.target.value)}
              className="appearance-none px-3 pr-8 py-1.5 bg-surface/50 border border-border rounded-lg text-fg text-xs focus:outline-none focus:ring-1 focus:ring-accent cursor-pointer"
            >
              <option value="all">All Categories</option>
              {SKILL_CATEGORIES.map(category => (
                <option key={category} value={category}>
                  {category.charAt(0).toUpperCase() + category.slice(1)}
                </option>
              ))}
            </select>
          </div>
          <div className="flex-1" />
          <Button
            size="sm"
            onClick={() => setShowCreateForm(true)}
            className="gap-1.5 bg-accent hover:bg-accent-hover text-white"
          >
            <Plus className="w-3.5 h-3.5" />
            Create Skill
          </Button>
        </div>

        {isLoading ? (
          <div className="flex items-center justify-center py-8">
            <Loader2 className="w-6 h-6 animate-spin text-fg-secondary" />
          </div>
        ) : filteredSkills.length === 0 ? (
          <div className="text-center py-8 text-fg-muted">
            {skills.length === 0 ?
              'No skills found. Create your first skill to get started.' :
              `No skills found in "${categoryFilter}" category.`
            }
          </div>
        ) : (
          <div className="grid gap-3 grid-cols-2">
            {filteredSkills.map((skill) => {
              const toolCount = parseToolBindings(skill.tool_bindings).length
              return (
                <div
                  key={skill.id}
                  className="rounded-xl border border-border-subtle bg-white dark:bg-bg-elevated/60 shadow-sm overflow-hidden transition-all cursor-pointer hover:shadow-md"
                  onClick={() => setSelectedSkill(skill.id)}
                >
                  {/* Header */}
                  <div className="flex items-center gap-2.5 px-3.5 py-3">
                    <span className="inline-flex items-center justify-center w-9 h-9 rounded-lg bg-zinc-700 text-zinc-300 shrink-0">
                      <Wrench className="w-4 h-4" />
                    </span>
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-semibold text-fg truncate">{skill.name}</span>
                        {skill.is_builtin && <span className="w-1.5 h-1.5 rounded-full bg-success shrink-0" />}
                      </div>
                      <span className="text-[11px] text-fg-muted font-mono truncate block">{skill.slug}</span>
                    </div>
                  </div>

                  {/* Detail footer */}
                  <div className="border-t border-border/50 px-3.5 py-2 bg-bg-elevated/40">
                    <div className="flex items-center gap-2">
                      <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-bg-elevated border border-border-subtle text-fg-muted leading-none">
                        {skill.category}
                      </span>
                      {toolCount > 0 && (
                        <span className="text-[11px] text-fg-muted">
                          {toolCount} tool{toolCount !== 1 ? 's' : ''}
                        </span>
                      )}
                    </div>
                    {skill.description && (
                      <p className="text-[11px] text-fg-muted line-clamp-2 mt-1">{skill.description}</p>
                    )}
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
          <h2 className="text-xl font-semibold text-fg">Create Skill</h2>
        </div>

        <form
          onSubmit={(e) => {
            e.preventDefault()
            handleCreateSkill(new FormData(e.currentTarget))
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
                placeholder="Skill name"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-fg-secondary mb-2">Slug</label>
              <input
                name="slug"
                type="text"
                required
                className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
                placeholder="skill-slug"
              />
            </div>
          </div>

          <div>
            <label className="block text-sm font-medium text-fg-secondary mb-2">Category</label>
            <select
              name="category"
              required
              className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
            >
              {SKILL_CATEGORIES.map(category => (
                <option key={category} value={category}>
                  {category.charAt(0).toUpperCase() + category.slice(1)}
                </option>
              ))}
            </select>
          </div>

          <div>
            <label className="block text-sm font-medium text-fg-secondary mb-2">Description</label>
            <textarea
              name="description"
              rows={3}
              className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
              placeholder="Brief description of what this skill does..."
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-fg-secondary mb-2">Tool Bindings (JSON)</label>
            <textarea
              name="tool_bindings"
              rows={5}
              className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent font-mono text-sm"
              placeholder='[{"server": "filesystem", "tool": "read_file"}]'
              defaultValue="[]"
            />
            <p className="text-xs text-fg-muted mt-1">
              Array of objects with "server" and "tool" properties
            </p>
          </div>

          <div>
            <label className="block text-sm font-medium text-fg-secondary mb-2">Input Schema (JSON, optional)</label>
            <textarea
              name="input_schema"
              rows={5}
              className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent font-mono text-sm"
              placeholder='{"properties": {"query": {"type": "string", "description": "Search query"}}}'
            />
            <p className="text-xs text-fg-muted mt-1">
              JSON schema describing the skill's input parameters
            </p>
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
              Create Skill
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
  if (selectedSkill && skillDetail) {
    const skill = skillDetail
    const isEditing = editingSkill?.id === skill.id

    if (isEditing) {
      return (
        <div className="space-y-4">
          <div className="flex items-center gap-3">
            <Button
              variant="ghost"
              size="icon"
              onClick={() => setEditingSkill(null)}
            >
              <ChevronLeft className="w-4 h-4" />
            </Button>
            <h2 className="text-xl font-semibold text-fg">Edit {skill.name}</h2>
          </div>

          <form
            onSubmit={(e) => {
              e.preventDefault()
              handleUpdateSkill(new FormData(e.currentTarget))
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
                  defaultValue={skill.name}
                  className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-fg-secondary mb-2">Slug</label>
                <input
                  name="slug"
                  type="text"
                  required
                  defaultValue={skill.slug}
                  className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
                />
              </div>
            </div>

            <div>
              <label className="block text-sm font-medium text-fg-secondary mb-2">Category</label>
              <select
                name="category"
                required
                defaultValue={skill.category}
                className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
              >
                {SKILL_CATEGORIES.map(category => (
                  <option key={category} value={category}>
                    {category.charAt(0).toUpperCase() + category.slice(1)}
                  </option>
                ))}
              </select>
            </div>

            <div>
              <label className="block text-sm font-medium text-fg-secondary mb-2">Description</label>
              <textarea
                name="description"
                rows={3}
                defaultValue={skill.description}
                className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
              />
            </div>

            <div>
              <label className="block text-sm font-medium text-fg-secondary mb-2">Tool Bindings (JSON)</label>
              <textarea
                name="tool_bindings"
                rows={5}
                defaultValue={skill.tool_bindings}
                className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent font-mono text-sm"
              />
            </div>

            <div>
              <label className="block text-sm font-medium text-fg-secondary mb-2">Input Schema (JSON, optional)</label>
              <textarea
                name="input_schema"
                rows={5}
                defaultValue={skill.input_schema || '{}'}
                className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent font-mono text-sm"
              />
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
                onClick={() => setEditingSkill(null)}
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
            onClick={() => setSelectedSkill(null)}
          >
            <ChevronLeft className="w-4 h-4" />
          </Button>
          <div className="flex items-center gap-3 flex-1">
            <div>
              <h2 className="text-xl font-semibold text-fg flex items-center gap-2">
                {skill.name}
                {skill.is_builtin && (
                  <span className="w-2 h-2 rounded-full bg-blue-500 inline-block" title="Built-in skill" />
                )}
              </h2>
              <p className="text-sm text-fg-secondary">{skill.slug}</p>
            </div>
          </div>
          <div className="flex gap-2">
            {!skill.is_builtin && (
              <>
                <Button
                  onClick={() => setEditingSkill(skill)}
                  variant="ghost"
                  size="icon"
                >
                  <Edit className="w-4 h-4" />
                </Button>
                <Button
                  onClick={() => setShowDeleteConfirm(skill.id)}
                  variant="ghost"
                  size="icon"
                  className="text-red-400 hover:text-red-300"
                >
                  <Trash2 className="w-4 h-4" />
                </Button>
              </>
            )}
          </div>
        </div>

        <div className="grid gap-6 lg:grid-cols-2">
          {/* Skill Details */}
          <div className="space-y-4">
            <h3 className="text-lg font-medium text-fg flex items-center gap-2">
              <Settings className="w-5 h-5" />
              Skill Details
            </h3>

            <div className="space-y-3 bg-white dark:bg-bg-elevated/60 rounded-xl border border-border-subtle shadow-sm p-4">
              <div>
                <label className="text-sm font-medium text-fg-secondary">Category</label>
                <p className="text-fg capitalize">{skill.category}</p>
              </div>

              <div>
                <label className="text-sm font-medium text-fg-secondary">Description</label>
                <p className="text-fg">{skill.description || 'No description'}</p>
              </div>

              <div>
                <label className="text-sm font-medium text-fg-secondary">Type</label>
                <p className="text-fg">{skill.is_builtin ? 'Built-in' : 'Custom'}</p>
              </div>
            </div>
          </div>

          {/* Tool Bindings */}
          <div className="space-y-4">
            <h3 className="text-lg font-medium text-fg flex items-center gap-2">
              <Wrench className="w-5 h-5" />
              Tool Bindings ({parseToolBindings(skill.tool_bindings).length})
            </h3>

            <div className="space-y-2">
              {parseToolBindings(skill.tool_bindings).length === 0 ? (
                <p className="text-fg-muted text-center py-4">No tool bindings configured</p>
              ) : (
                parseToolBindings(skill.tool_bindings).map((binding, index) => (
                  <div
                    key={index}
                    className="bg-white dark:bg-bg-elevated/60 rounded-xl border border-border-subtle shadow-sm p-3 flex items-center gap-3"
                  >
                    <Code2 className="w-4 h-4 text-fg-secondary shrink-0" />
                    <div className="flex-1">
                      <div className="text-sm font-medium text-fg">
                        {binding.server}/{binding.tool}
                      </div>
                      <div className="text-xs text-fg-muted">
                        Server: {binding.server} • Tool: {binding.tool}
                      </div>
                    </div>
                  </div>
                ))
              )}
            </div>
          </div>
        </div>

        {/* Input Schema */}
        {parseInputSchema(skill.input_schema) && (
          <div className="space-y-4">
            <h3 className="text-lg font-medium text-fg flex items-center gap-2">
              <Code2 className="w-5 h-5" />
              Input Schema
            </h3>

            <div className="bg-white dark:bg-bg-elevated/60 rounded-xl border border-border-subtle shadow-sm p-4">
              <pre className="text-xs text-fg-secondary overflow-x-auto font-mono">
                {skill.input_schema}
              </pre>
            </div>
          </div>
        )}

        {/* Delete Confirmation Modal */}
        {showDeleteConfirm === skill.id && (
          <div className="fixed inset-0 z-50 flex items-center justify-center">
            <div className="absolute inset-0 bg-black/60" onClick={() => setShowDeleteConfirm(null)} />
            <div className="relative bg-bg-elevated border border-border-subtle rounded-xl p-6 max-w-md w-full mx-4">
              <h3 className="text-lg font-semibold text-fg mb-2">Delete Skill</h3>
              <p className="text-fg-secondary mb-4">
                Are you sure you want to delete "{skill.name}"? This action cannot be undone.
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
                  onClick={() => deleteMutation.mutate(skill.id)}
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
        )}
      </div>
    )
  }

  return null
}