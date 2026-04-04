import { Cpu, X } from 'lucide-react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Tooltip } from '@/components/ui/tooltip'
import { Widget } from './Widget'
import { api } from '@/lib/api'
import type { Worker } from '@/lib/types'

const STATUS_DOT: Record<string, string> = {
  spawning: 'bg-warning animate-pulse',
  running: 'bg-info',
  completed: 'bg-success',
  failed: 'bg-danger',
  cancelled: 'bg-fg-muted',
}

export function WorkerStatusWidget() {
  const queryClient = useQueryClient()

  const { data: workers = [] } = useQuery({
    queryKey: ['workers'],
    queryFn: api.listWorkers,
    refetchInterval: 5000,
  })

  const cancelMutation = useMutation({
    mutationFn: (id: string) => api.cancelWorker(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workers'] })
    },
  })

  const active = workers.filter(
    (w: Worker) => w.status === 'spawning' || w.status === 'running',
  )

  return (
    <Widget id="worker-status" title="Workers" icon={Cpu}>
      {active.length === 0 ? (
        <p className="text-[11px] text-fg-muted">No active workers</p>
      ) : (
        <div className="space-y-1.5">
          {active.map((worker) => (
            <div
              key={worker.id}
              className="flex items-center gap-2 px-2 py-1.5 rounded-md bg-surface/30"
            >
              <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${STATUS_DOT[worker.status]}`} />
              <div className="flex-1 min-w-0">
                <span className="text-xs text-fg truncate block">{worker.agent_id}</span>
                <span className="text-[10px] text-fg-muted">
                  {worker.type === 'full' ? 'Full' : 'Light'} worker
                </span>
              </div>
              <Tooltip content="Cancel worker" side="left">
                <button
                  onClick={() => cancelMutation.mutate(worker.id)}
                  disabled={cancelMutation.isPending}
                  className="p-1 rounded text-fg-faint hover:text-danger hover:bg-surface transition-colors"
                >
                  <X className="w-3 h-3" />
                </button>
              </Tooltip>
            </div>
          ))}
        </div>
      )}
    </Widget>
  )
}
