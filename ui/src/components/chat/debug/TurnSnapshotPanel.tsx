import { useState, useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Activity, ChevronDown, ChevronRight, Clock, Repeat } from 'lucide-react'
import { api } from '@/lib/api'
import type { TurnSnapshot, TurnSnapshotToolCall } from '@/lib/types'
import { DebugPanel } from './DebugPanel'

interface TurnSnapshotPanelProps {
  sessionId: string
}

export function TurnSnapshotContent({ sessionId }: TurnSnapshotPanelProps) {
  const { data: metrics = [], isLoading } = useQuery({
    queryKey: ['execution-metrics', sessionId],
    queryFn: () => api.getExecutionMetrics(sessionId),
    enabled: !!sessionId,
    staleTime: 10_000,
  })

  const snapshots = useMemo(() => {
    const all: TurnSnapshot[] = []
    for (const m of metrics) {
      if (!m.debug_snapshots || m.debug_snapshots === 'null') continue
      try {
        const parsed = JSON.parse(m.debug_snapshots)
        if (!Array.isArray(parsed)) continue
        for (const snap of parsed) {
          if (snap && typeof snap === 'object') {
            all.push({
              site: snap.continue_site ?? snap.site ?? '',
              reason: snap.reason ?? '',
              tool_calls: Array.isArray(snap.tool_calls) ? snap.tool_calls : [],
              iteration: snap.iteration ?? 0,
              max_turns: snap.max_turns ?? 0,
              timestamp: snap.timestamp ?? '',
            })
          }
        }
      } catch { /* ignore malformed JSON */ }
    }
    return all
  }, [metrics])

  if (isLoading) return <p className="text-[11px] text-fg-muted">Loading...</p>
  if (snapshots.length === 0) return <p className="text-[11px] text-fg-faint">No snapshots captured. Enable debug mode on the agent or turn on developer mode in settings.</p>

  return (
    <div className="space-y-1">
      {snapshots.map((snap, idx) => (
        <SnapshotRow key={`${snap.timestamp}-${idx}`} snapshot={snap} />
      ))}
    </div>
  )
}

export function TurnSnapshotPanel({ sessionId }: TurnSnapshotPanelProps) {
  return (
    <DebugPanel title="Turn Snapshots" icon={Activity}>
      <TurnSnapshotContent sessionId={sessionId} />
    </DebugPanel>
  )
}

function SnapshotRow({ snapshot }: { snapshot: TurnSnapshot }) {
  const [expanded, setExpanded] = useState(false)

  const toolCalls = snapshot.tool_calls ?? []
  const totalToolTime = toolCalls.reduce((sum, tc) => sum + (tc.duration_ms ?? (tc.duration_ns ? tc.duration_ns / 1e6 : 0)), 0)
  const hasParallel = toolCalls.some((tc) => tc.parallel)

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
        <span className="text-[11px] text-fg-secondary truncate flex-1">
          <span className="font-medium text-fg">{snapshot.site || 'unknown'}</span>
          {snapshot.reason && (
            <span className="text-fg-muted"> — {snapshot.reason}</span>
          )}
        </span>
        <span className="flex items-center gap-1 text-[10px] text-fg-muted shrink-0">
          <Repeat className="w-3 h-3" />
          {snapshot.iteration}/{snapshot.max_turns}
        </span>
        {toolCalls.length > 0 && (
          <span className="flex items-center gap-1 text-[10px] text-fg-muted shrink-0">
            <Clock className="w-3 h-3" />
            {formatMs(totalToolTime)}
          </span>
        )}
      </button>

      {expanded && (
        <div className="px-2.5 py-2 border-t border-border/20 bg-surface/10 space-y-2">
          {/* Tool calls */}
          {toolCalls.length > 0 && (
            <div>
              <div className="text-[10px] text-fg-muted uppercase tracking-wider mb-1">
                Tool Calls {hasParallel && '(some parallel)'}
              </div>
              <div className="space-y-0.5">
                {toolCalls.map((tc, idx) => (
                  <ToolCallRow key={`${tc.name}-${idx}`} toolCall={tc} />
                ))}
              </div>
            </div>
          )}

          {/* Metadata */}
          <div className="flex gap-4 text-[10px] text-fg-faint">
            <span>Iteration: {snapshot.iteration}/{snapshot.max_turns}</span>
            {snapshot.timestamp && (
              <span>{new Date(snapshot.timestamp).toLocaleTimeString()}</span>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

function ToolCallRow({ toolCall }: { toolCall: TurnSnapshotToolCall }) {
  return (
    <div className="flex items-center gap-2 py-0.5">
      <span className="text-[10px] font-mono text-fg-secondary truncate flex-1">
        {toolCall.name}
      </span>
      {toolCall.parallel && (
        <span className="text-[9px] px-1 py-0 rounded bg-surface border border-border-subtle text-fg-secondary">
          parallel
        </span>
      )}
      <span className="text-[10px] font-mono tabular-nums text-fg-muted shrink-0">
        {formatMs(toolCall.duration_ms ?? (toolCall.duration_ns ? toolCall.duration_ns / 1e6 : 0))}
      </span>
    </div>
  )
}

function formatMs(ms: number): string {
  if (!ms) return '—'
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}
