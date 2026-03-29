import { useState, useCallback } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
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

  return (
    <Dialog open={true} onOpenChange={() => onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader className="px-5 pt-5">
          <DialogTitle className="text-sm">New Project</DialogTitle>
          <DialogDescription className="sr-only">Create a new project in this workspace</DialogDescription>
        </DialogHeader>

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

        <DialogFooter className="px-5 py-3 border-t border-border">
          <Button variant="outline" size="sm" onClick={onClose} disabled={createMutation.isPending}>
            Cancel
          </Button>
          <Button
            size="sm"
            onClick={handleSubmit}
            disabled={!name.trim() || createMutation.isPending}
            className="bg-accent hover:bg-accent-hover text-white"
          >
            {createMutation.isPending ? 'Creating...' : 'Create Project'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
