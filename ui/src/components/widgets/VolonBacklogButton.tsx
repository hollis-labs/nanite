import { useState, useCallback } from 'react'
import { Bug, Send, Check, X, ChevronDown } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { useAppStore } from '@/stores/useAppStore'
import { api } from '@/lib/api'
import { useQuery } from '@tanstack/react-query'
import { cn } from '@/lib/utils'

type Priority = 'A' | 'B' | 'C'
type FormStatus = 'idle' | 'submitting' | 'success' | 'error'

interface VolonBacklogButtonProps {
  /** Extra context to include in the body (e.g. from context inspector). */
  contextBody?: string
  /** Override the default project ID. */
  projectId?: string
  className?: string
}

export function VolonBacklogButton({ contextBody, projectId, className }: VolonBacklogButtonProps) {
  const activeSessionId = useAppStore((s) => s.activeSessionId)

  const { data: session } = useQuery({
    queryKey: ['session', activeSessionId],
    queryFn: () => api.getSession(activeSessionId!),
    enabled: !!activeSessionId,
  })

  const [isOpen, setIsOpen] = useState(false)
  const [title, setTitle] = useState('')
  const [priority, setPriority] = useState<Priority>('B')
  const [notes, setNotes] = useState('')
  const [status, setStatus] = useState<FormStatus>('idle')
  const [errorMsg, setErrorMsg] = useState('')

  const sessionName = session?.custom_name || session?.title || session?.short_code || 'unknown'

  const handleOpen = useCallback(() => {
    setIsOpen(true)
    setTitle(`Context budget issue \u2014 ${sessionName}`)
    setNotes(contextBody ?? '')
    setStatus('idle')
    setErrorMsg('')
  }, [sessionName, contextBody])

  const handleClose = useCallback(() => {
    setIsOpen(false)
    setTitle('')
    setNotes('')
    setPriority('B')
    setStatus('idle')
    setErrorMsg('')
  }, [])

  const handleSubmit = useCallback(async () => {
    if (!title.trim()) return

    setStatus('submitting')
    setErrorMsg('')

    try {
      await api.createVolonBacklogItem({
        title: title.trim(),
        body: notes.trim(),
        priority,
        tags: ['mentat-chat', 'context-inspector'],
        project_id: projectId || 'mentat-chat',
      })
      setStatus('success')
      setTimeout(() => {
        handleClose()
      }, 1500)
    } catch (err) {
      setStatus('error')
      setErrorMsg(err instanceof Error ? err.message : 'Failed to create backlog item')
    }
  }, [title, notes, priority, projectId, handleClose])

  if (!isOpen) {
    return (
      <Button
        variant="secondary"
        size="sm"
        onClick={handleOpen}
        className={cn('gap-1.5', className)}
      >
        <Bug className="w-3.5 h-3.5" />
        <span>Create Backlog Item</span>
      </Button>
    )
  }

  return (
    <div className={cn('rounded-lg border border-zinc-700 bg-zinc-900 p-3 space-y-3', className)}>
      <div className="flex items-center justify-between">
        <h4 className="text-xs font-medium text-zinc-300 uppercase tracking-wider">
          New Volon Backlog Item
        </h4>
        <button
          onClick={handleClose}
          className="text-zinc-500 hover:text-zinc-300 transition-colors"
        >
          <X className="w-3.5 h-3.5" />
        </button>
      </div>

      {/* Title */}
      <div className="space-y-1">
        <label className="text-xs text-zinc-500">Title</label>
        <input
          type="text"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          className="w-full rounded-md border border-zinc-700 bg-zinc-800 px-2.5 py-1.5 text-sm text-zinc-200 placeholder-zinc-600 focus:outline-none focus:ring-1 focus:ring-zinc-500"
          placeholder="Backlog item title"
          disabled={status === 'submitting'}
        />
      </div>

      {/* Priority */}
      <div className="space-y-1">
        <label className="text-xs text-zinc-500">Priority</label>
        <div className="relative">
          <select
            value={priority}
            onChange={(e) => setPriority(e.target.value as Priority)}
            className="w-full appearance-none rounded-md border border-zinc-700 bg-zinc-800 px-2.5 py-1.5 pr-8 text-sm text-zinc-200 focus:outline-none focus:ring-1 focus:ring-zinc-500"
            disabled={status === 'submitting'}
          >
            <option value="A">A - High</option>
            <option value="B">B - Medium</option>
            <option value="C">C - Low</option>
          </select>
          <ChevronDown className="absolute right-2 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-zinc-500 pointer-events-none" />
        </div>
      </div>

      {/* Notes */}
      <div className="space-y-1">
        <label className="text-xs text-zinc-500">Notes (optional)</label>
        <textarea
          value={notes}
          onChange={(e) => setNotes(e.target.value)}
          rows={3}
          className="w-full rounded-md border border-zinc-700 bg-zinc-800 px-2.5 py-1.5 text-sm text-zinc-200 placeholder-zinc-600 focus:outline-none focus:ring-1 focus:ring-zinc-500 resize-none"
          placeholder="Additional context..."
          disabled={status === 'submitting'}
        />
      </div>

      {/* Status feedback */}
      {status === 'error' && (
        <p className="text-xs text-red-400">{errorMsg}</p>
      )}
      {status === 'success' && (
        <p className="flex items-center gap-1 text-xs text-green-400">
          <Check className="w-3.5 h-3.5" />
          Backlog item created
        </p>
      )}

      {/* Actions */}
      <div className="flex justify-end gap-2">
        <Button
          variant="ghost"
          size="sm"
          onClick={handleClose}
          disabled={status === 'submitting'}
        >
          Cancel
        </Button>
        <Button
          variant="secondary"
          size="sm"
          onClick={handleSubmit}
          disabled={status === 'submitting' || !title.trim()}
          className="gap-1.5"
        >
          {status === 'submitting' ? (
            <>Submitting...</>
          ) : (
            <>
              <Send className="w-3.5 h-3.5" />
              Create
            </>
          )}
        </Button>
      </div>
    </div>
  )
}
