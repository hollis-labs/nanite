import { useState } from 'react'
import { Loader2, X, Wrench } from 'lucide-react'
import { ToolCallItem, formatToolName } from './ToolCallItem'
import type { ToolCall, ToolCallDisplayMode } from '@/lib/types'

interface ToolCallDisplayProps {
  toolCalls: ToolCall[]
  displayMode: ToolCallDisplayMode
  onCycleMode: () => void
}

function ModeToggle({ mode, onCycle }: { mode: ToolCallDisplayMode; onCycle: () => void }) {
  const icons: Record<ToolCallDisplayMode, string> = {
    indicator: '◦',
    minimal: '─',
    compact: '▪',
    full: '▣',
  }
  const labels: Record<ToolCallDisplayMode, string> = {
    indicator: 'Indicator',
    minimal: 'Minimal',
    compact: 'Compact',
    full: 'Full',
  }
  return (
    <button
      onClick={onCycle}
      className="text-fg-muted hover:text-fg-secondary text-xs font-mono px-1 rounded hover:bg-surface-hover/50 transition-colors"
      title={`Tool display: ${labels[mode]} (click to cycle)`}
    >
      {icons[mode]}
    </button>
  )
}

function IndicatorMode({ toolCalls, expanded, onToggle }: { toolCalls: ToolCall[]; expanded: boolean; onToggle: () => void }) {
  const names = toolCalls.map((tc) => formatToolName(tc.tool))
  const uniqueNames = [...new Set(names)]
  const allDone = toolCalls.every((tc) => tc.status === 'done')
  const hasError = toolCalls.some((tc) => tc.status === 'error')
  const isRunning = toolCalls.some((tc) => tc.status === 'running')

  return (
    <div>
      <div className="flex items-center gap-1.5 py-1 px-2 text-xs text-fg-secondary">
        {isRunning ? (
          <Loader2 className="w-3 h-3 animate-spin text-accent shrink-0" />
        ) : hasError ? (
          <X className="w-3 h-3 text-red-400 shrink-0" />
        ) : (
          <Wrench className="w-3 h-3 shrink-0" />
        )}
        <span>
          {allDone ? 'Used' : 'Using'} {toolCalls.length} tool{toolCalls.length !== 1 ? 's' : ''}: {uniqueNames.join(' \u00B7 ')}
        </span>
        <button onClick={onToggle} className="text-fg-muted hover:text-fg-secondary ml-1 font-mono text-[10px]">
          {expanded ? '▾' : '▸'}
        </button>
      </div>
      {expanded && (
        <div className="text-xs space-y-0">
          {toolCalls.map((tc, i) => (
            <ToolCallItem key={tc.id} toolCall={tc} variant="inline" index={i} total={toolCalls.length} />
          ))}
        </div>
      )}
    </div>
  )
}

export function ToolCallDisplay({ toolCalls, displayMode, onCycleMode }: ToolCallDisplayProps) {
  const [indicatorExpanded, setIndicatorExpanded] = useState(false)

  if (toolCalls.length === 0) return null

  return (
    <div className="relative">
      <div className="absolute top-0 right-0">
        <ModeToggle mode={displayMode} onCycle={onCycleMode} />
      </div>
      {displayMode === 'indicator' && (
        <IndicatorMode
          toolCalls={toolCalls}
          expanded={indicatorExpanded}
          onToggle={() => setIndicatorExpanded((v) => !v)}
        />
      )}
      {displayMode === 'minimal' && (
        <div className="text-xs space-y-0">
          {toolCalls.map((tc, i) => (
            <ToolCallItem key={tc.id} toolCall={tc} variant="inline" index={i} total={toolCalls.length} />
          ))}
        </div>
      )}
      {displayMode === 'compact' && (
        <div className="text-xs space-y-0">
          {toolCalls.map((tc, i) => (
            <ToolCallItem key={tc.id} toolCall={tc} variant="inline" index={i} total={toolCalls.length} defaultExpanded={false} />
          ))}
        </div>
      )}
      {displayMode === 'full' && (
        <div className="space-y-1">
          {toolCalls.map((tc) => (
            <ToolCallItem key={tc.id} toolCall={tc} variant="full" />
          ))}
        </div>
      )}
    </div>
  )
}
