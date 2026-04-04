import type { SessionTaskStatus } from '@/lib/types'
import { cn } from '@/lib/utils'

const STATUS_CONFIG: Record<SessionTaskStatus, { dot: string; label: string }> = {
  pending: { dot: 'bg-zinc-400', label: 'Pending' },
  in_progress: { dot: 'bg-blue-400 animate-pulse', label: 'In Progress' },
  completed: { dot: 'bg-success', label: 'Done' },
  failed: { dot: 'bg-red-400', label: 'Failed' },
  cancelled: { dot: 'bg-zinc-500', label: 'Cancelled' },
}

interface TaskStatusBadgeProps {
  status: SessionTaskStatus
  className?: string
}

export function TaskStatusBadge({ status, className }: TaskStatusBadgeProps) {
  const config = STATUS_CONFIG[status]
  return (
    <span className={cn('inline-flex items-center gap-1.5', className)}>
      <span className={cn('w-1.5 h-1.5 rounded-full shrink-0', config.dot)} />
      <span className="text-[11px] text-fg-muted">{config.label}</span>
    </span>
  )
}
