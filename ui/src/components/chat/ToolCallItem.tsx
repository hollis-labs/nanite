import { useState, useCallback } from 'react'
import { Loader2, Check, X, ChevronRight, Wrench } from 'lucide-react'
import { ContentActions } from './ContentActions'
import type { ToolCall } from '@/lib/types'

/** Strip mcp__<server>__ prefix for readability. */
export function formatToolName(name: string): string {
  if (name.startsWith('mcp__')) {
    const idx = name.indexOf('__', 5)
    if (idx !== -1) return name.slice(idx + 2)
  }
  return name
}

export function StatusIcon({ status }: { status: ToolCall['status'] }) {
  if (status === 'running') return <Loader2 className="w-3 h-3 animate-spin text-indigo-400 shrink-0" />
  if (status === 'done') return <Check className="w-3 h-3 text-green-500 shrink-0" />
  return <X className="w-3 h-3 text-red-400 shrink-0" />
}

export function TreeConnector({ index, total }: { index: number; total: number }) {
  const char = index === 0 && total === 1
    ? '─'
    : index === 0
      ? '┌'
      : index === total - 1
        ? '└'
        : '├'
  return <span className="text-zinc-600 w-3 text-center font-mono select-none">{char}</span>
}

interface ToolCallItemProps {
  toolCall: ToolCall
  variant: 'inline' | 'drawer' | 'full'
  index?: number
  total?: number
  defaultExpanded?: boolean
}

export function ToolCallItem({ toolCall, variant, index = 0, total = 1, defaultExpanded = false }: ToolCallItemProps) {
  const [expanded, setExpanded] = useState(defaultExpanded)
  const [hovered, setHovered] = useState(false)
  const hasSummary = !!toolCall.summary

  const toggleExpand = useCallback(() => {
    if (hasSummary) setExpanded((v) => !v)
  }, [hasSummary])

  if (variant === 'full') {
    return (
      <div
        className="flex items-center gap-2 py-1 px-2 rounded-md bg-zinc-800/50 border border-zinc-700/50 text-xs"
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
      >
        <StatusIcon status={toolCall.status} />
        <Wrench className="w-3 h-3 text-zinc-500 shrink-0" />
        <span className="text-zinc-300 font-medium">{toolCall.tool}</span>
        {toolCall.summary && (
          <span className="text-zinc-500 truncate flex-1">{toolCall.summary}</span>
        )}
        {toolCall.status === 'running' && !toolCall.summary && (
          <span className="text-zinc-500 italic">Running...</span>
        )}
        {hasSummary && (
          <ContentActions content={toolCall.summary!} visible={hovered} />
        )}
      </div>
    )
  }

  if (variant === 'drawer') {
    return (
      <div
        className="border-b border-zinc-800/50 last:border-b-0"
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
      >
        <div
          className={`flex items-center gap-2 px-3 py-1.5 text-xs ${hasSummary ? 'cursor-pointer hover:bg-zinc-800/40' : ''}`}
          onClick={toggleExpand}
        >
          <StatusIcon status={toolCall.status} />
          <span className="font-mono text-zinc-300">{formatToolName(toolCall.tool)}</span>
          {toolCall.status === 'running' && (
            <span className="text-zinc-600 italic ml-auto">running...</span>
          )}
          {hasSummary && !hovered && (
            <ChevronRight className={`w-3 h-3 text-zinc-600 ml-auto transition-transform ${expanded ? 'rotate-90' : ''}`} />
          )}
          {hasSummary && hovered && (
            <span className="ml-auto" onClick={(e) => e.stopPropagation()}>
              <ContentActions content={toolCall.summary!} visible={true} />
            </span>
          )}
        </div>
        {expanded && toolCall.summary && (
          <div className="px-3 pb-2">
            <div className="max-h-32 overflow-y-auto rounded bg-zinc-900 border border-zinc-700/40 px-2 py-1">
              <pre className="text-[10px] text-zinc-400 whitespace-pre-wrap break-all font-mono">
                {toolCall.summary}
              </pre>
            </div>
          </div>
        )}
      </div>
    )
  }

  // variant === 'inline'
  return (
    <div
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
      <div
        className={`flex items-center gap-1.5 py-0.5 px-2 text-xs text-zinc-400 ${hasSummary ? 'hover:text-zinc-300 cursor-pointer' : ''}`}
        onClick={toggleExpand}
      >
        <TreeConnector index={index} total={total} />
        <StatusIcon status={toolCall.status} />
        <span className="font-mono">{formatToolName(toolCall.tool)}</span>
        {hasSummary && (
          <ChevronRight className={`w-3 h-3 text-zinc-600 transition-transform ${expanded ? 'rotate-90' : ''}`} />
        )}
        {hasSummary && hovered && (
          <span className="ml-auto" onClick={(e) => e.stopPropagation()}>
            <ContentActions content={toolCall.summary!} visible={true} />
          </span>
        )}
      </div>
      {expanded && toolCall.summary && (
        <div className="pl-7 pr-1 py-0.5">
          <div className="max-h-24 overflow-y-auto rounded bg-zinc-800/60 border border-zinc-700/40 px-2 py-1">
            <pre className="text-[10px] text-zinc-400 whitespace-pre-wrap break-all font-mono">
              {toolCall.summary}
            </pre>
          </div>
        </div>
      )}
    </div>
  )
}
