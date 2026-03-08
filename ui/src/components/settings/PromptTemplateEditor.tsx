import { useState, useCallback, useMemo } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Plus,
  Edit,
  Save,
  Trash2,
  Code2,
  ChevronLeft,
  Loader2,
  Eye,
  AlertCircle,
} from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { api } from '@/lib/api'
import type { PromptTemplate, TemplateVariable } from '@/lib/types'

function parseVariables(s: string): TemplateVariable[] {
  try { return JSON.parse(s) } catch { return [] }
}

interface PromptTemplateEditorProps {}

export function PromptTemplateEditor({}: PromptTemplateEditorProps) {
  const [selectedTemplate, setSelectedTemplate] = useState<string | null>(null)
  const [showCreateForm, setShowCreateForm] = useState(false)
  const [editingTemplate, setEditingTemplate] = useState<PromptTemplate | null>(null)
  const [showDeleteConfirm, setShowDeleteConfirm] = useState<string | null>(null)
  const [previewVariables, setPreviewVariables] = useState<Record<string, any>>({})
  const [showPreview, setShowPreview] = useState(false)
  const queryClient = useQueryClient()

  const { data: templates = [], isLoading } = useQuery({
    queryKey: ['prompt-templates'],
    queryFn: api.listPromptTemplates,
  })

  const { data: templateDetail } = useQuery({
    queryKey: ['prompt-template', selectedTemplate],
    queryFn: () => api.getPromptTemplate(selectedTemplate!),
    enabled: !!selectedTemplate,
  })

  const createMutation = useMutation({
    mutationFn: api.createPromptTemplate,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['prompt-templates'] })
      setShowCreateForm(false)
      setEditingTemplate(null)
    },
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: string; data: Partial<PromptTemplate> }) =>
      api.updatePromptTemplate(id, data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['prompt-templates'] })
      void queryClient.invalidateQueries({ queryKey: ['prompt-template', selectedTemplate] })
      setEditingTemplate(null)
    },
  })

  const deleteMutation = useMutation({
    mutationFn: api.deletePromptTemplate,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['prompt-templates'] })
      setSelectedTemplate(null)
      setShowDeleteConfirm(null)
    },
  })

  const handleCreateTemplate = useCallback((formData: FormData) => {
    const data = {
      name: formData.get('name') as string,
      slug: formData.get('slug') as string,
      scope: formData.get('scope') as 'system' | 'mode' | 'skill' | 'context',
      template: formData.get('template') as string,
      variables: formData.get('variables') as string || '[]',
      priority: parseInt(formData.get('priority') as string) || 0,
    }
    createMutation.mutate(data)
  }, [createMutation])

  const handleUpdateTemplate = useCallback((formData: FormData) => {
    if (!editingTemplate) return
    const data = {
      name: formData.get('name') as string,
      slug: formData.get('slug') as string,
      scope: formData.get('scope') as 'system' | 'mode' | 'skill' | 'context',
      template: formData.get('template') as string,
      variables: formData.get('variables') as string || '[]',
      priority: parseInt(formData.get('priority') as string) || 0,
    }
    updateMutation.mutate({ id: editingTemplate.id, data })
  }, [editingTemplate, updateMutation])

  const renderTemplatePreview = useMemo(() => {
    if (!templateDetail) return ''

    let preview = templateDetail.template

    // Replace variables with sample values
    parseVariables(templateDetail.variables).forEach(variable => {
      const placeholder = `{{${variable.name}}}`
      let value = previewVariables[variable.name] || variable.default || `[${variable.name}]`

      if (variable.type === 'boolean') {
        value = value === true || value === 'true' ? 'true' : 'false'
      }

      preview = preview.replace(new RegExp(placeholder.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'g'), String(value))
    })

    return preview
  }, [templateDetail, previewVariables])

  const getScopeBadgeColor = (scope: string) => {
    switch (scope) {
      case 'system': return 'bg-blue-500'
      case 'mode': return 'bg-green-500'
      case 'skill': return 'bg-yellow-500'
      case 'context': return 'bg-purple-500'
      default: return 'bg-gray-500'
    }
  }

  // Sorted templates by priority then scope
  const sortedTemplates = useMemo(() => {
    return [...templates].sort((a, b) => {
      if (a.priority !== b.priority) return b.priority - a.priority
      return a.scope.localeCompare(b.scope)
    })
  }, [templates])

  // List View
  if (!selectedTemplate && !showCreateForm) {
    return (
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <h2 className="text-xl font-semibold text-zinc-100">Prompt Templates</h2>
          <Button
            onClick={() => setShowCreateForm(true)}
            className="gap-2"
          >
            <Plus className="w-4 h-4" />
            Create Template
          </Button>
        </div>

        {isLoading ? (
          <div className="flex items-center justify-center py-8">
            <Loader2 className="w-6 h-6 animate-spin text-zinc-400" />
          </div>
        ) : sortedTemplates.length === 0 ? (
          <div className="text-center py-8 text-zinc-500">
            No prompt templates found. Create your first template to get started.
          </div>
        ) : (
          <div className="space-y-2">
            {sortedTemplates.map((template) => (
              <div
                key={template.id}
                className="bg-zinc-800 rounded-lg p-4 border border-zinc-700 hover:border-zinc-600 transition-colors cursor-pointer"
                onClick={() => setSelectedTemplate(template.id)}
              >
                <div className="flex items-center justify-between">
                  <div className="flex-1">
                    <div className="flex items-center gap-3 mb-2">
                      <h3 className="font-medium text-zinc-100">{template.name}</h3>
                      <div className={`w-3 h-3 rounded-full ${getScopeBadgeColor(template.scope)}`} />
                      <span className="text-xs text-zinc-400 capitalize">{template.scope}</span>
                      {template.is_builtin && (
                        <span className="w-2 h-2 rounded-full bg-blue-500 inline-block" title="Built-in template" />
                      )}
                    </div>
                    <div className="flex items-center gap-4">
                      <p className="text-sm text-zinc-400">{template.slug}</p>
                      <span className="text-xs text-zinc-500">Priority: {template.priority}</span>
                      <span className="text-xs text-zinc-500">
                        {parseVariables(template.variables).length} variable{parseVariables(template.variables).length !== 1 ? 's' : ''}
                      </span>
                    </div>
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
          <h2 className="text-xl font-semibold text-zinc-100">Create Prompt Template</h2>
        </div>

        <form
          onSubmit={(e) => {
            e.preventDefault()
            handleCreateTemplate(new FormData(e.currentTarget))
          }}
          className="space-y-6 max-w-4xl"
        >
          <div className="grid gap-4 md:grid-cols-2">
            <div>
              <label className="block text-sm font-medium text-zinc-300 mb-2">Name</label>
              <input
                name="name"
                type="text"
                required
                className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500"
                placeholder="Template name"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-zinc-300 mb-2">Slug</label>
              <input
                name="slug"
                type="text"
                required
                className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500"
                placeholder="template-slug"
              />
            </div>
          </div>

          <div className="grid gap-4 md:grid-cols-2">
            <div>
              <label className="block text-sm font-medium text-zinc-300 mb-2">Scope</label>
              <select
                name="scope"
                required
                className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500"
              >
                <option value="system">System</option>
                <option value="mode">Mode</option>
                <option value="skill">Skill</option>
                <option value="context">Context</option>
              </select>
            </div>
            <div>
              <label className="block text-sm font-medium text-zinc-300 mb-2">Priority</label>
              <input
                name="priority"
                type="number"
                min="0"
                defaultValue="100"
                className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500"
                placeholder="100"
              />
            </div>
          </div>

          <div>
            <label className="block text-sm font-medium text-zinc-300 mb-2">Template Body</label>
            <textarea
              name="template"
              required
              rows={12}
              className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500 font-mono text-sm"
              placeholder="Enter the template content... Use {{variable_name}} for variables."
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-zinc-300 mb-2">
              Variables Definition (JSON)
              <span className="text-xs text-zinc-500 ml-2">
                Array of {`{name, type, required, default, description}`}
              </span>
            </label>
            <textarea
              name="variables"
              rows={6}
              className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500 font-mono text-sm"
              placeholder={`[
  {
    "name": "user_name",
    "type": "text",
    "required": true,
    "description": "The user's display name"
  }
]`}
              defaultValue="[]"
            />
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
              Create Template
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
  if (selectedTemplate && templateDetail) {
    const template = templateDetail
    const isEditing = editingTemplate?.id === template.id

    if (isEditing) {
      return (
        <div className="space-y-4">
          <div className="flex items-center gap-3">
            <Button
              variant="ghost"
              size="icon"
              onClick={() => setEditingTemplate(null)}
            >
              <ChevronLeft className="w-4 h-4" />
            </Button>
            <h2 className="text-xl font-semibold text-zinc-100">Edit {template.name}</h2>
          </div>

          <form
            onSubmit={(e) => {
              e.preventDefault()
              handleUpdateTemplate(new FormData(e.currentTarget))
            }}
            className="space-y-6 max-w-4xl"
          >
            <div className="grid gap-4 md:grid-cols-2">
              <div>
                <label className="block text-sm font-medium text-zinc-300 mb-2">Name</label>
                <input
                  name="name"
                  type="text"
                  required
                  defaultValue={template.name}
                  className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500"
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-zinc-300 mb-2">Slug</label>
                <input
                  name="slug"
                  type="text"
                  required
                  defaultValue={template.slug}
                  className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500"
                />
              </div>
            </div>

            <div className="grid gap-4 md:grid-cols-2">
              <div>
                <label className="block text-sm font-medium text-zinc-300 mb-2">Scope</label>
                <select
                  name="scope"
                  required
                  defaultValue={template.scope}
                  className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500"
                >
                  <option value="system">System</option>
                  <option value="mode">Mode</option>
                  <option value="skill">Skill</option>
                  <option value="context">Context</option>
                </select>
              </div>
              <div>
                <label className="block text-sm font-medium text-zinc-300 mb-2">Priority</label>
                <input
                  name="priority"
                  type="number"
                  min="0"
                  defaultValue={template.priority}
                  className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500"
                />
              </div>
            </div>

            <div>
              <label className="block text-sm font-medium text-zinc-300 mb-2">Template Body</label>
              <textarea
                name="template"
                required
                rows={12}
                defaultValue={template.template}
                className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500 font-mono text-sm"
              />
            </div>

            <div>
              <label className="block text-sm font-medium text-zinc-300 mb-2">
                Variables Definition (JSON)
              </label>
              <textarea
                name="variables"
                rows={6}
                defaultValue={template.variables}
                className="w-full px-3 py-2 bg-zinc-800 border border-zinc-600 rounded text-zinc-100 focus:outline-none focus:border-indigo-500 font-mono text-sm"
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
                onClick={() => setEditingTemplate(null)}
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
            onClick={() => setSelectedTemplate(null)}
          >
            <ChevronLeft className="w-4 h-4" />
          </Button>
          <div className="flex items-center gap-3 flex-1">
            <div className={`w-3 h-3 rounded-full ${getScopeBadgeColor(template.scope)}`} />
            <div>
              <h2 className="text-xl font-semibold text-zinc-100 flex items-center gap-2">
                {template.name}
                {template.is_builtin && (
                  <span className="w-2 h-2 rounded-full bg-blue-500 inline-block" title="Built-in template" />
                )}
              </h2>
              <p className="text-sm text-zinc-400">{template.slug}</p>
            </div>
          </div>
          <div className="flex gap-2">
            <Button
              onClick={() => setShowPreview(!showPreview)}
              variant="ghost"
              size="icon"
            >
              <Eye className="w-4 h-4" />
            </Button>
            <Button
              onClick={() => setEditingTemplate(template)}
              variant="ghost"
              size="icon"
            >
              <Edit className="w-4 h-4" />
            </Button>
            {!template.is_builtin && (
              <Button
                onClick={() => setShowDeleteConfirm(template.id)}
                variant="ghost"
                size="icon"
                className="text-red-400 hover:text-red-300"
              >
                <Trash2 className="w-4 h-4" />
              </Button>
            )}
          </div>
        </div>

        <div className="grid gap-6 lg:grid-cols-2">
          {/* Template Details */}
          <div className="space-y-4">
            <h3 className="text-lg font-medium text-zinc-200 flex items-center gap-2">
              <Code2 className="w-5 h-5" />
              Template Details
            </h3>

            <div className="space-y-3 bg-zinc-800 rounded-lg p-4">
              <div>
                <label className="text-sm font-medium text-zinc-400">Scope</label>
                <p className="text-zinc-200 capitalize">{template.scope}</p>
              </div>

              <div>
                <label className="text-sm font-medium text-zinc-400">Priority</label>
                <p className="text-zinc-200">{template.priority}</p>
              </div>

              <div>
                <label className="text-sm font-medium text-zinc-400">Variables ({parseVariables(template.variables).length})</label>
                {parseVariables(template.variables).length === 0 ? (
                  <p className="text-zinc-500 text-sm">No variables defined</p>
                ) : (
                  <div className="space-y-2 mt-1">
                    {parseVariables(template.variables).map((variable, index) => (
                      <div key={index} className="bg-zinc-900 rounded p-2 text-xs">
                        <div className="flex items-center gap-2 mb-1">
                          <span className="font-mono text-zinc-300">{variable.name}</span>
                          <span className="text-zinc-500">({variable.type})</span>
                          {variable.required && (
                            <span className="text-red-400 text-[10px]">*</span>
                          )}
                        </div>
                        {variable.description && (
                          <p className="text-zinc-500">{variable.description}</p>
                        )}
                        {variable.default !== undefined && (
                          <p className="text-zinc-600">Default: {String(variable.default)}</p>
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </div>

              <div>
                <label className="text-sm font-medium text-zinc-400">Template Body</label>
                <pre className="text-xs text-zinc-300 bg-zinc-900 rounded p-2 mt-1 overflow-x-auto max-h-40 overflow-y-auto font-mono whitespace-pre-wrap">
                  {template.template}
                </pre>
              </div>
            </div>
          </div>

          {/* Live Preview */}
          <div className="space-y-4">
            <div className="flex items-center justify-between">
              <h3 className="text-lg font-medium text-zinc-200 flex items-center gap-2">
                <Eye className="w-5 h-5" />
                Live Preview
              </h3>
              <Button
                onClick={() => setShowPreview(!showPreview)}
                size="sm"
                variant={showPreview ? "default" : "secondary"}
              >
                {showPreview ? 'Hide' : 'Show'} Preview
              </Button>
            </div>

            {showPreview && (
              <div className="space-y-3">
                {/* Variable Inputs */}
                {parseVariables(template.variables).length > 0 && (
                  <div className="bg-zinc-800 rounded-lg p-4">
                    <h4 className="font-medium text-zinc-200 mb-3">Sample Values</h4>
                    <div className="space-y-3">
                      {parseVariables(template.variables).map((variable) => (
                        <div key={variable.name}>
                          <label className="block text-sm text-zinc-400 mb-1">
                            {variable.name}
                            {variable.required && <span className="text-red-400 ml-1">*</span>}
                          </label>
                          {variable.type === 'textarea' ? (
                            <textarea
                              rows={2}
                              value={previewVariables[variable.name] || variable.default || ''}
                              onChange={(e) => setPreviewVariables(prev => ({
                                ...prev,
                                [variable.name]: e.target.value
                              }))}
                              className="w-full px-2 py-1 bg-zinc-700 border border-zinc-600 rounded text-zinc-100 text-sm focus:outline-none focus:border-indigo-500"
                              placeholder={variable.description || `Enter ${variable.name}`}
                            />
                          ) : variable.type === 'boolean' ? (
                            <select
                              value={previewVariables[variable.name] || variable.default || 'false'}
                              onChange={(e) => setPreviewVariables(prev => ({
                                ...prev,
                                [variable.name]: e.target.value === 'true'
                              }))}
                              className="w-full px-2 py-1 bg-zinc-700 border border-zinc-600 rounded text-zinc-100 text-sm focus:outline-none focus:border-indigo-500"
                            >
                              <option value="true">true</option>
                              <option value="false">false</option>
                            </select>
                          ) : variable.type === 'number' ? (
                            <input
                              type="number"
                              value={previewVariables[variable.name] || variable.default || ''}
                              onChange={(e) => setPreviewVariables(prev => ({
                                ...prev,
                                [variable.name]: e.target.value
                              }))}
                              className="w-full px-2 py-1 bg-zinc-700 border border-zinc-600 rounded text-zinc-100 text-sm focus:outline-none focus:border-indigo-500"
                              placeholder={variable.description || `Enter ${variable.name}`}
                            />
                          ) : (
                            <input
                              type="text"
                              value={previewVariables[variable.name] || variable.default || ''}
                              onChange={(e) => setPreviewVariables(prev => ({
                                ...prev,
                                [variable.name]: e.target.value
                              }))}
                              className="w-full px-2 py-1 bg-zinc-700 border border-zinc-600 rounded text-zinc-100 text-sm focus:outline-none focus:border-indigo-500"
                              placeholder={variable.description || `Enter ${variable.name}`}
                            />
                          )}
                        </div>
                      ))}
                    </div>
                  </div>
                )}

                {/* Rendered Preview */}
                <div className="bg-zinc-800 rounded-lg p-4">
                  <h4 className="font-medium text-zinc-200 mb-3">Rendered Template</h4>
                  <pre className="text-xs text-zinc-300 bg-zinc-900 rounded p-3 overflow-x-auto max-h-80 overflow-y-auto font-mono whitespace-pre-wrap">
                    {renderTemplatePreview}
                  </pre>
                </div>
              </div>
            )}
          </div>
        </div>

        {/* Delete Confirmation Modal */}
        {showDeleteConfirm === template.id && (
          <div className="fixed inset-0 z-50 flex items-center justify-center">
            <div className="absolute inset-0 bg-black/60" onClick={() => setShowDeleteConfirm(null)} />
            <div className="relative bg-zinc-900 border border-zinc-700 rounded-xl p-6 max-w-md w-full mx-4">
              <div className="flex items-start gap-3">
                <AlertCircle className="w-6 h-6 text-red-400 shrink-0 mt-0.5" />
                <div>
                  <h3 className="text-lg font-semibold text-zinc-100 mb-2">Delete Template</h3>
                  <p className="text-zinc-400 mb-4">
                    Are you sure you want to delete "{template.name}"? This action cannot be undone and will remove the template from all agents.
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
                      onClick={() => deleteMutation.mutate(template.id)}
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
            </div>
          </div>
        )}
      </div>
    )
  }

  return null
}