import { Play, Check, X, RotateCcw } from 'lucide-react'
import { useState } from 'react'
import type { SessionTaskStatus } from '@/lib/types'
import { api } from '@/lib/api'

interface SessionTaskData {
  task_id: string
  title: string
  status: SessionTaskStatus
  description?: string
}

interface SessionTaskCardProps {
  data: SessionTaskData
  onSendMessage?: (text: string) => void
}

const STATUS_DOT: Record<SessionTaskStatus, string> = {
  pending: 'bg-zinc-400',
  in_progress: 'bg-blue-400 animate-pulse',
  completed: 'bg-success',
  failed: 'bg-red-400',
  cancelled: 'bg-zinc-500',
}

const STATUS_LABEL: Record<SessionTaskStatus, string> = {
  pending: 'Pending',
  in_progress: 'In Progress',
  completed: 'Done',
  failed: 'Failed',
  cancelled: 'Cancelled',
}

export function SessionTaskCard({ data }: SessionTaskCardProps) {
  const [status, setStatus] = useState<SessionTaskStatus>(data.status)
  const [transitioning, setTransitioning] = useState(false)

  const handleTransition = async (newStatus: SessionTaskStatus) => {
    setTransitioning(true)
    try {
      await api.transitionSessionTask(data.task_id, newStatus)
      setStatus(newStatus)
    } catch {
      // transition failed — keep current status
    } finally {
      setTransitioning(false)
    }
  }

  return (
    <div className="rounded-sm border border-border-subtle bg-bg-elevated/60 overflow-hidden my-2">
      <div className="flex items-center gap-2.5 px-3 py-2">
        <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${STATUS_DOT[status]}`} />
        <div className="flex-1 min-w-0">
          <span className={`text-sm text-fg ${status === 'completed' ? 'line-through text-fg-muted' : ''}`}>
            {data.title}
          </span>
          <span className="text-[11px] text-fg-muted ml-2">{STATUS_LABEL[status]}</span>
        </div>
        {!transitioning && (
          <div className="flex items-center gap-0.5 shrink-0">
            {status === 'pending' && (
              <button
                onClick={() => handleTransition('in_progress')}
                className="p-1 rounded text-fg-faint hover:text-blue-400 transition-colors"
                title="Start"
              >
                <Play className="w-3 h-3" />
              </button>
            )}
            {status === 'in_progress' && (
              <button
                onClick={() => handleTransition('completed')}
                className="p-1 rounded text-fg-faint hover:text-success transition-colors"
                title="Complete"
              >
                <Check className="w-3 h-3" />
              </button>
            )}
            {(status === 'pending' || status === 'in_progress') && (
              <button
                onClick={() => handleTransition('cancelled')}
                className="p-1 rounded text-fg-faint hover:text-red-400 transition-colors"
                title="Cancel"
              >
                <X className="w-3 h-3" />
              </button>
            )}
            {status === 'failed' && (
              <button
                onClick={() => handleTransition('pending')}
                className="p-1 rounded text-fg-faint hover:text-fg-secondary transition-colors"
                title="Retry"
              >
                <RotateCcw className="w-3 h-3" />
              </button>
            )}
          </div>
        )}
      </div>
      {data.description && (
        <div className="border-t border-border/50 px-3 py-1.5 bg-bg-elevated/40">
          <p className="text-[11px] text-fg-muted">{data.description}</p>
        </div>
      )}
    </div>
  )
}
