import { useQuery } from '@tanstack/react-query'
import { Columns3 } from 'lucide-react'
import { api } from '@/lib/api'
import { DebugPanel } from './DebugPanel'

const SLOT_NAMES = [
  'System',
  'Memory',
  'Agent',
  'Rules',
  'Tools',
  'Session',
  'Context',
  'Conversation',
] as const

interface SlotData {
  name: string
  tokens: number
  cached: boolean
}

interface SlotInspectorPanelProps {
  sessionId: string
}

export function SlotInspectorPanel({ sessionId }: SlotInspectorPanelProps) {
  const { data: metrics = [], isLoading } = useQuery({
    queryKey: ['execution-metrics', sessionId],
    queryFn: () => api.getExecutionMetrics(sessionId),
    enabled: !!sessionId,
    staleTime: 10_000,
  })

  // Take the most recent metric to show current slot allocation
  const latest = metrics[metrics.length - 1]

  // Parse debug_snapshots to extract slot data if available
  let slots: SlotData[] = []
  let totalBudget = 0
  let totalUsed = 0

  if (latest?.debug_snapshots) {
    try {
      const snapshots = JSON.parse(latest.debug_snapshots) as Array<Record<string, unknown>>
      // Look for slot allocation data in the most recent snapshot
      const lastSnapshot = snapshots[snapshots.length - 1]
      if (lastSnapshot?.slots && Array.isArray(lastSnapshot.slots)) {
        slots = (lastSnapshot.slots as SlotData[]).map((s) => ({
          name: s.name,
          tokens: s.tokens ?? 0,
          cached: s.cached ?? false,
        }))
      }
      if (typeof lastSnapshot?.total_budget === 'number') {
        totalBudget = lastSnapshot.total_budget
      }
    } catch { /* no slot data in snapshots */ }
  }

  // Fallback: show basic context info from the metric itself
  if (slots.length === 0 && latest) {
    totalUsed = latest.context_tokens ?? 0
    // Create a simplified view from available data
    slots = SLOT_NAMES.map((name) => ({
      name,
      tokens: 0,
      cached: false,
    }))
  } else {
    totalUsed = slots.reduce((sum, s) => sum + s.tokens, 0)
  }

  const remaining = totalBudget > 0 ? totalBudget - totalUsed : 0
  const usagePercent = totalBudget > 0 ? Math.round((totalUsed / totalBudget) * 100) : 0

  return (
    <DebugPanel title="Context Slots" icon={Columns3}>
      {isLoading ? (
        <p className="text-[11px] text-fg-muted">Loading...</p>
      ) : !latest ? (
        <p className="text-[11px] text-fg-faint">No execution data yet. Send a message to populate.</p>
      ) : (
        <div className="space-y-2.5">
          {/* Budget bar */}
          {totalBudget > 0 && (
            <div>
              <div className="flex items-center justify-between text-[10px] text-fg-muted mb-1">
                <span>{formatTokens(totalUsed)} / {formatTokens(totalBudget)} tokens</span>
                <span>{usagePercent}% used</span>
              </div>
              <div className="h-1.5 rounded-full bg-surface overflow-hidden">
                <div
                  className={`h-full rounded-full transition-all ${
                    usagePercent > 90 ? 'bg-accent' : 'bg-toggle-on'
                  }`}
                  style={{ width: `${Math.min(100, usagePercent)}%` }}
                />
              </div>
            </div>
          )}

          {/* Slot table */}
          <table className="w-full text-[11px]">
            <thead>
              <tr className="border-b border-border/30">
                <th className="text-left py-1 text-[10px] uppercase tracking-wider text-fg-muted font-medium">Slot</th>
                <th className="text-right py-1 text-[10px] uppercase tracking-wider text-fg-muted font-medium">Tokens</th>
                <th className="text-right py-1 text-[10px] uppercase tracking-wider text-fg-muted font-medium">Cache</th>
              </tr>
            </thead>
            <tbody>
              {slots
                .filter((s) => s.tokens > 0)
                .map((slot) => (
                  <tr key={slot.name} className="border-b border-border/20 hover:bg-surface/20 transition-colors">
                    <td className="py-1 text-fg-secondary">{slot.name}</td>
                    <td className="py-1 text-right font-mono tabular-nums text-fg-muted">{formatTokens(slot.tokens)}</td>
                    <td className="py-1 text-right">
                      {slot.cached ? (
                        <span className="text-[10px] text-success">hit</span>
                      ) : (
                        <span className="text-[10px] text-fg-faint">miss</span>
                      )}
                    </td>
                  </tr>
                ))}
            </tbody>
          </table>

          {/* Context info fallback when no slot data */}
          {slots.every((s) => s.tokens === 0) && latest.context_tokens > 0 && (
            <div className="text-[10px] text-fg-muted space-y-1">
              <div className="flex justify-between">
                <span>Context tokens</span>
                <span className="font-mono tabular-nums">{formatTokens(latest.context_tokens)}</span>
              </div>
              <div className="flex justify-between">
                <span>Context messages</span>
                <span className="font-mono tabular-nums">{latest.context_messages}</span>
              </div>
              <div className="flex justify-between">
                <span>Cache creation</span>
                <span className="font-mono tabular-nums">{formatTokens(latest.cache_creation_tokens)}</span>
              </div>
              <div className="flex justify-between">
                <span>Cache read</span>
                <span className="font-mono tabular-nums">{formatTokens(latest.cache_read_tokens)}</span>
              </div>
              <p className="text-fg-faint pt-1">
                Slot-level data not yet available. Enable in a future release.
              </p>
            </div>
          )}

          {remaining > 0 && (
            <div className="text-[10px] text-fg-faint">
              {formatTokens(remaining)} tokens remaining
            </div>
          )}
        </div>
      )}
    </DebugPanel>
  )
}

function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return String(n)
}
