import { useState, useEffect, useCallback } from 'react'
import { X } from 'lucide-react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { Project } from '@/lib/types'

interface CreateProjectModalProps {
  workspaceId: string
  onClose: () => void
  onCreated: (project: Project) => void
}

export function CreateProjectModal({ workspaceId, onClose, onCreated }: CreateProjectModalProps) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const queryClient = useQueryClient()

  const createMutation = useMutation({
    mutationFn: () => api.createProject(workspaceId, {
      name: name.trim(),
      description: description.trim() || undefined,
    }),
    onSuccess: (project) => {
      void queryClient.invalidateQueries({ queryKey: ['projects', workspaceId] })
      onCreated(project)
    },
  })

  const handleSubmit = useCallback(() => {
    if (!name.trim() || createMutation.isPending) return
    createMutation.mutate()
  }, [name, createMutation])

  // Close on Escape
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [onClose])

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <div className="relative w-full max-w-md mx-4 bg-white dark:bg-bg-elevated border border-border-subtle rounded-xl shadow-2xl">
        {/* Header */}
        <div className="flex items-center justify-between px-5 h-12 border-b border-border">
          <h2 className="text-sm font-semibold text-fg">New Project</h2>
          <button
            onClick={onClose}
            className="p-1 rounded text-fg-muted hover:text-fg-secondary hover:bg-surface transition-colors"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        {/* Form */}
        <div className="px-5 py-4 space-y-4">
          <div className="space-y-1.5">
            <label className="text-xs font-medium text-fg-secondary">Name</label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Project name"
              autoFocus
              className="w-full rounded-lg border border-border-subtle bg-bg-elevated px-3 py-2 text-sm text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-accent"
              onKeyDown={(e) => { if (e.key === 'Enter') handleSubmit() }}
              disabled={createMutation.isPending}
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-xs font-medium text-fg-secondary">Description <span className="text-fg-faint font-normal">(optional)</span></label>
            <textarea
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="What is this project about?"
              rows={3}
              className="w-full rounded-lg border border-border-subtle bg-bg-elevated px-3 py-2 text-sm text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-accent resize-none"
              disabled={createMutation.isPending}
            />
          </div>

          {createMutation.isError && (
            <p className="text-xs text-red-400">
              {createMutation.error instanceof Error ? createMutation.error.message : 'Failed to create project'}
            </p>
          )}
        </div>

        {/* Footer */}
        <div className="flex justify-end gap-2 px-5 py-3 border-t border-border">
          <button
            onClick={onClose}
            disabled={createMutation.isPending}
            className="px-3 py-1.5 text-xs font-medium text-fg-secondary hover:text-fg rounded-md hover:bg-surface transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={handleSubmit}
            disabled={!name.trim() || createMutation.isPending}
            className="px-4 py-1.5 text-xs font-medium text-white bg-accent hover:bg-accent-hover rounded-md transition-colors disabled:opacity-50"
          >
            {createMutation.isPending ? 'Creating...' : 'Create Project'}
          </button>
        </div>
      </div>
    </div>
  )
}
