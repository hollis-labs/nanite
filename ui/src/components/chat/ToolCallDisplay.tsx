import { useState, useCallback } from 'react'
import { Loader2, Check, X, Wrench, Copy, ChevronRight } from 'lucide-react'
import type { ToolCall, ToolCallDisplayMode } from '@/lib/types'

interface ToolCallDisplayProps {
  toolCalls: ToolCall[]
  displayMode: ToolCallDisplayMode
  onCycleMode: () => void
}

/** Strip mcp__<server>__ prefix for readability */
function formatToolName(name: string): string {
  if (name.startsWith('mcp__')) {
    const secondUnderscoreIdx = name.indexOf('__', 5)
    if (secondUnderscoreIdx !== -1) {
      return name.slice(secondUnderscoreIdx + 2)
    }
  }
  return name
}

/** Detect content type from summary text */
function detectContentType(text: string): 'html' | 'json' | 'text' {
  const trimmed = text.trimStart()
  if (trimmed.startsWith('<!DOCTYPE') || trimmed.startsWith('<html')) return 'html'
  if (trimmed.startsWith('{') || trimmed.startsWith('[')) return 'json'
  return 'text'
}

function StatusIcon({ status }: { status: ToolCall['status'] }) {
  if (status === 'running') return <Loader2 className="w-3 h-3 animate-spin text-indigo-400 shrink-0" />
  if (status === 'done') return <Check className="w-3 h-3 text-green-500 shrink-0" />
  return <X className="w-3 h-3 text-red-400 shrink-0" />
}

function TreeConnector({ index, total }: { index: number; total: number }) {
  const char = index === 0 && total === 1
    ? '─'
    : index === 0
      ? '┌'
      : index === total - 1
        ? '└'
        : '├'
  return <span className="text-zinc-600 w-3 text-center font-mono select-none">{char}</span>
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
      className="text-zinc-500 hover:text-zinc-300 text-xs font-mono px-1 rounded hover:bg-zinc-700/50 transition-colors"
      title={`Tool display: ${labels[mode]} (click to cycle)`}
    >
      {icons[mode]}
    </button>
  )
}

/** Expandable summary content for Compact mode */
function ExpandedSummary({ summary }: { summary: string }) {
  const [copied, setCopied] = useState(false)
  const contentType = detectContentType(summary)

  const handleCopy = useCallback(() => {
    void navigator.clipboard.writeText(summary)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }, [summary])

  if (contentType === 'html') {
    const len = summary.length
    return (
      <div className="flex items-center gap-2 pl-7 py-0.5">
        <span className="text-[10px] text-zinc-500 font-mono">text/html &middot; {len.toLocaleString()} chars</span>
      </div>
    )
  }

  return (
    <div className="relative pl-7 pr-1 py-0.5 group/summary">
      <div className="max-h-24 overflow-y-auto rounded bg-zinc-800/60 border border-zinc-700/40 px-2 py-1">
        <pre className={`text-[10px] text-zinc-400 whitespace-pre-wrap break-all ${contentType === 'json' ? 'font-mono' : ''}`}>
          {summary}
        </pre>
      </div>
      <button
        onClick={handleCopy}
        className="absolute top-1 right-2 text-zinc-500 hover:text-zinc-300 opacity-0 group-hover/summary:opacity-100 transition-opacity"
        title="Copy result"
      >
        {copied ? <Check className="w-3 h-3 text-green-400" /> : <Copy className="w-3 h-3" />}
      </button>
    </div>
  )
}

// --- Mode renderers ---

function IndicatorMode({ toolCalls, expanded, onToggle }: { toolCalls: ToolCall[]; expanded: boolean; onToggle: () => void }) {
  const names = toolCalls.map((tc) => formatToolName(tc.tool))
  const uniqueNames = [...new Set(names)]
  const allDone = toolCalls.every((tc) => tc.status === 'done')
  const hasError = toolCalls.some((tc) => tc.status === 'error')
  const isRunning = toolCalls.some((tc) => tc.status === 'running')

  return (
    <div>
      <div className="flex items-center gap-1.5 py-1 px-2 text-xs text-zinc-400">
        {isRunning ? (
          <Loader2 className="w-3 h-3 animate-spin text-indigo-400 shrink-0" />
        ) : hasError ? (
          <X className="w-3 h-3 text-red-400 shrink-0" />
        ) : (
          <Wrench className="w-3 h-3 shrink-0" />
        )}
        <span>
          {allDone ? 'Used' : 'Using'} {toolCalls.length} tool{toolCalls.length !== 1 ? 's' : ''}: {uniqueNames.join(' \u00B7 ')}
        </span>
        <button onClick={onToggle} className="text-zinc-500 hover:text-zinc-300 ml-1 font-mono text-[10px]">
          {expanded ? '▾' : '▸'}
        </button>
      </div>
      {expanded && (
        <MinimalMode toolCalls={toolCalls} />
      )}
    </div>
  )
}

function MinimalMode({ toolCalls }: { toolCalls: ToolCall[] }) {
  return (
    <div className="text-xs space-y-0">
      {toolCalls.map((tc, i) => (
        <div key={tc.id} className="flex items-center gap-1.5 py-0.5 px-2 text-zinc-400">
          <TreeConnector index={i} total={toolCalls.length} />
          <StatusIcon status={tc.status} />
          <span className="font-mono">{formatToolName(tc.tool)}</span>
        </div>
      ))}
    </div>
  )
}

function CompactMode({ toolCalls }: { toolCalls: ToolCall[] }) {
  const [expandedIds, setExpandedIds] = useState<Set<string>>(new Set())

  const toggleExpand = useCallback((id: string) => {
    setExpandedIds((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }, [])

  return (
    <div className="text-xs space-y-0">
      {toolCalls.map((tc, i) => {
        const isExpanded = expandedIds.has(tc.id)
        const hasSummary = tc.summary && tc.summary.length > 0
        return (
          <div key={tc.id}>
            <div
              className={`flex items-center gap-1.5 py-0.5 px-2 text-zinc-400 ${hasSummary ? 'hover:text-zinc-300 cursor-pointer' : ''}`}
              onClick={() => hasSummary && toggleExpand(tc.id)}
            >
              <TreeConnector index={i} total={toolCalls.length} />
              <StatusIcon status={tc.status} />
              <span className="font-mono">{formatToolName(tc.tool)}</span>
              {hasSummary && (
                <ChevronRight className={`w-3 h-3 text-zinc-600 transition-transform ${isExpanded ? 'rotate-90' : ''}`} />
              )}
            </div>
            {isExpanded && tc.summary && (
              <ExpandedSummary summary={tc.summary} />
            )}
          </div>
        )
      })}
    </div>
  )
}

function FullMode({ toolCalls }: { toolCalls: ToolCall[] }) {
  return (
    <div className="space-y-1">
      {toolCalls.map((tc) => (
        <div key={tc.id} className="flex items-center gap-2 py-1 px-2 rounded-md bg-zinc-800/50 border border-zinc-700/50 text-xs">
          <StatusIcon status={tc.status} />
          <Wrench className="w-3 h-3 text-zinc-500 shrink-0" />
          <span className="text-zinc-300 font-medium">{tc.tool}</span>
          {tc.summary && (
            <span className="text-zinc-500 truncate">{tc.summary}</span>
          )}
          {tc.status === 'running' && !tc.summary && (
            <span className="text-zinc-500 italic">Running...</span>
          )}
        </div>
      ))}
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
      {displayMode === 'minimal' && <MinimalMode toolCalls={toolCalls} />}
      {displayMode === 'compact' && <CompactMode toolCalls={toolCalls} />}
      {displayMode === 'full' && <FullMode toolCalls={toolCalls} />}
    </div>
  )
}
