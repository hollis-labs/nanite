import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { GitBranch, ChevronDown, ChevronRight } from 'lucide-react'
import { api } from '@/lib/api'
import type { BrokerDecision } from '@/lib/types'
import { DebugPanel } from './DebugPanel'

const LAYER_LABELS: Record<string, { label: string; color: string }> = {
  '1': { label: 'Explicit', color: 'text-success' },
  '2': { label: 'Rule', color: 'text-fg-secondary' },
  '3': { label: 'Classifier', color: 'text-fg-muted' },
  explicit: { label: 'Explicit', color: 'text-success' },
  rule: { label: 'Rule', color: 'text-fg-secondary' },
  classifier: { label: 'Classifier', color: 'text-fg-muted' },
}

interface BrokerDecisionsPanelProps {
  sessionId: string
}

export function BrokerDecisionsContent({ sessionId }: BrokerDecisionsPanelProps) {
  const { data: rawDecisions, isLoading } = useQuery({
    queryKey: ['broker-decisions', sessionId],
    queryFn: () => api.getBrokerDecisions(sessionId),
    enabled: !!sessionId,
    staleTime: 10_000,
    refetchInterval: 15_000,
  })
  const decisions = rawDecisions ?? []

  if (isLoading) return <p className="text-[11px] text-fg-muted">Loading...</p>
  if (decisions.length === 0) return <p className="text-[11px] text-fg-faint">No broker decisions recorded for this session.</p>

  return (
    <div className="space-y-1">
      {decisions.map((d) => (
        <DecisionRow key={d.id} decision={d} />
      ))}
    </div>
  )
}

export function BrokerDecisionsPanel({ sessionId }: BrokerDecisionsPanelProps) {
  return (
    <DebugPanel title="Broker Decisions" icon={GitBranch}>
      <BrokerDecisionsContent sessionId={sessionId} />
    </DebugPanel>
  )
}

function DecisionRow({ decision }: { decision: BrokerDecision }) {
  const [expanded, setExpanded] = useState(false)
  const layer = LAYER_LABELS[decision.layer_reached] ?? { label: decision.layer_reached, color: 'text-fg-muted' }

  let signals: Record<string, unknown> | null = null
  if (decision.signals) {
    try {
      signals = JSON.parse(decision.signals) as Record<string, unknown>
    } catch { /* ignore */ }
  }

  const timeAgo = formatTimeAgo(decision.created_at)

  return (
    <div className="rounded-md border border-border/30 overflow-hidden">
      <button
        onClick={() => setExpanded((o) => !o)}
        className="w-full flex items-center gap-2 px-2.5 py-1.5 text-left hover:bg-surface/20 transition-colors"
      >
        {expanded ? (
          <ChevronDown className="w-3 h-3 text-fg-faint shrink-0" />
        ) : (
          <ChevronRight className="w-3 h-3 text-fg-faint shrink-0" />
        )}
        <span className={`text-[10px] font-medium px-1.5 py-0.5 rounded ${layer.color} bg-surface/40`}>
          {layer.label}
        </span>
        <span className="text-[11px] text-fg-secondary truncate flex-1">
          {decision.intent || 'No intent recorded'}
        </span>
        <span className="text-[10px] text-fg-faint tabular-nums shrink-0">{timeAgo}</span>
        <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-bg-elevated border border-border-subtle text-fg-muted leading-none shrink-0">
          {decision.selected_tools?.length ?? 0} tools
        </span>
      </button>

      {expanded && (
        <div className="px-2.5 py-2 border-t border-border/20 bg-surface/10 space-y-2">
          {/* Selected tools */}
          {decision.selected_tools && decision.selected_tools.length > 0 && (
            <div>
              <div className="text-[10px] text-fg-muted uppercase tracking-wider mb-1">Selected Tools</div>
              <div className="flex flex-wrap gap-1">
                {decision.selected_tools.map((tool) => (
                  <span
                    key={tool}
                    className="text-[10px] px-1.5 py-0.5 rounded bg-bg-elevated border border-border-subtle text-fg-secondary font-mono"
                  >
                    {tool}
                  </span>
                ))}
              </div>
            </div>
          )}

          {/* Signals */}
          {signals && Object.keys(signals).length > 0 && (
            <div>
              <div className="text-[10px] text-fg-muted uppercase tracking-wider mb-1">Signals</div>
              <pre className="text-[10px] text-fg-muted font-mono whitespace-pre-wrap break-all leading-relaxed max-h-32 overflow-y-auto">
                {JSON.stringify(signals, null, 2)}
              </pre>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

function formatTimeAgo(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime()
  const secs = Math.floor(diff / 1000)
  if (secs < 60) return `${secs}s ago`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m ago`
  return `${Math.floor(mins / 60)}h ago`
}
