import { useState, useCallback } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import type { Project } from '@/lib/types'

interface CreateProjectModalProps {
  onClose: () => void
  onCreated: (project: Project) => void
}

export function CreateProjectModal({ onClose, onCreated }: CreateProjectModalProps) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const queryClient = useQueryClient()

  const createMutation = useMutation({
    mutationFn: () => api.createProject({
      name: name.trim(),
      description: description.trim() || undefined,
    }),
    onSuccess: (project) => {
      void queryClient.invalidateQueries({ queryKey: ['projects'] })
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
          <DialogDescription className="sr-only">Create a new project</DialogDescription>
        </DialogHeader>

        {/* Form */}
        <div className="px-5 py-4 space-y-4">
          <div className="space-y-1.5">
            <label className="font-mono text-[11px] font-semibold uppercase tracking-[0.04em] text-fg-muted">Name</label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Project name"
              autoFocus
              className="w-full rounded-[7px] border border-border-subtle bg-surface px-3 py-[7px] text-[13px] text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-primary"
              onKeyDown={(e) => { if (e.key === 'Enter') handleSubmit() }}
              disabled={createMutation.isPending}
            />
          </div>

          <div className="space-y-1.5">
            <label className="font-mono text-[11px] font-semibold uppercase tracking-[0.04em] text-fg-muted">Description <span className="text-fg-faint font-normal normal-case tracking-normal">(optional)</span></label>
            <textarea
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="What is this project about?"
              rows={3}
              className="w-full rounded-[7px] border border-border-subtle bg-surface px-3 py-[7px] text-[13px] text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-primary resize-none"
              disabled={createMutation.isPending}
            />
          </div>

          {createMutation.isError && (
            <p className="text-xs text-danger">
              {createMutation.error instanceof Error ? createMutation.error.message : 'Failed to create project'}
            </p>
          )}
        </div>

        <DialogFooter>
          <Button variant="outline" size="sm" onClick={onClose} disabled={createMutation.isPending}>
            Cancel
          </Button>
          <Button
            size="sm"
            onClick={handleSubmit}
            disabled={!name.trim() || createMutation.isPending}
            className="bg-primary hover:bg-primary-hover text-white"
          >
            {createMutation.isPending ? 'Creating...' : 'Create Project'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
