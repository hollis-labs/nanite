import { Skull, Activity } from 'lucide-react'
import { useProcessHealth, useKillStaleProcesses } from '@/hooks/useObservability'
import type { ProcessHealthEntry } from '@/lib/types'

function formatDuration(ns: number): string {
  const ms = ns / 1_000_000
  if (ms < 1000) return `${Math.round(ms)}ms`
  const s = ms / 1000
  if (s < 60) return `${s.toFixed(1)}s`
  const m = s / 60
  if (m < 60) return `${m.toFixed(1)}m`
  return `${(m / 60).toFixed(1)}h`
}

function ProcessRow({ proc }: { proc: ProcessHealthEntry }) {
  return (
    <tr className="border-b border-border/30 hover:bg-surface/20 transition-colors">
      <td className="py-1.5 pr-3 font-mono text-fg-secondary">{proc.pid}</td>
      <td className="py-1.5 pr-3 font-mono text-fg-secondary text-xs">{proc.session_id.slice(0, 8)}</td>
      <td className="py-1.5 pr-3 text-right font-mono tabular-nums text-info">{formatDuration(proc.uptime)}</td>
      <td className="py-1.5 pr-3 text-right font-mono tabular-nums text-fg-secondary">{formatDuration(proc.idle_duration)}</td>
      <td className="py-1.5 text-center">
        {proc.is_stale ? (
          <span className="text-[10px] px-1.5 py-0.5 rounded bg-warning/20 text-warning font-medium">stale</span>
        ) : (
          <span className="text-[10px] px-1.5 py-0.5 rounded bg-success/20 text-success font-medium">healthy</span>
        )}
      </td>
    </tr>
  )
}

export function ProcessHealthPanel() {
  const { data, isLoading } = useProcessHealth()
  const killStale = useKillStaleProcesses()

  const processes = data?.processes ?? []
  const staleCount = processes.filter((p) => p.is_stale).length

  if (isLoading) {
    return <p className="text-xs text-fg-faint italic py-2">Loading process health...</p>
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2 text-xs text-fg-secondary">
          <Activity className="w-3.5 h-3.5" />
          <span>
            {processes.length} active process{processes.length !== 1 && 'es'}
            {staleCount > 0 && (
              <span className="text-warning ml-1">({staleCount} stale)</span>
            )}
          </span>
        </div>
        {staleCount > 0 && (
          <button
            type="button"
            onClick={() => killStale.mutate()}
            disabled={killStale.isPending}
            className="flex items-center gap-1.5 text-[10px] px-2 py-1 rounded bg-danger/10 text-danger hover:bg-danger/20 transition-colors disabled:opacity-50"
          >
            <Skull className="w-3 h-3" />
            {killStale.isPending ? 'Killing...' : 'Kill Stale'}
          </button>
        )}
      </div>

      {killStale.isSuccess && (
        <p className="text-[10px] text-success">
          Killed {killStale.data.killed} stale process{killStale.data.killed !== 1 && 'es'}
        </p>
      )}

      {processes.length === 0 ? (
        <p className="text-xs text-fg-faint italic py-2">No active CLI processes</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead>
              <tr className="text-[10px] uppercase tracking-wider text-fg-muted border-b border-border">
                <th className="text-left py-2 pr-3 font-medium">PID</th>
                <th className="text-left py-2 pr-3 font-medium">Session</th>
                <th className="text-right py-2 pr-3 font-medium">Uptime</th>
                <th className="text-right py-2 pr-3 font-medium">Idle</th>
                <th className="text-center py-2 font-medium">Status</th>
              </tr>
            </thead>
            <tbody>
              {processes.map((proc) => (
                <ProcessRow key={proc.pid} proc={proc} />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
