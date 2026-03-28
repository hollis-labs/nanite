import { useState, useCallback, useRef, useEffect } from 'react'
import { Wrench, Maximize2, Minimize2, X, Loader2, Check, Copy, ChevronRight } from 'lucide-react'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useChatStore } from '@/stores/useChatStore'
import type { ToolCall } from '@/lib/types'

function formatToolName(name: string): string {
  if (name.startsWith('mcp__')) {
    const idx = name.indexOf('__', 5)
    if (idx !== -1) return name.slice(idx + 2)
  }
  return name
}

function StatusIcon({ status }: { status: ToolCall['status'] }) {
  if (status === 'running') return <Loader2 className="w-3 h-3 animate-spin text-indigo-400 shrink-0" />
  if (status === 'done') return <Check className="w-3 h-3 text-green-500 shrink-0" />
  return <X className="w-3 h-3 text-red-400 shrink-0" />
}

function ToolCallRow({ tc }: { tc: ToolCall }) {
  const [expanded, setExpanded] = useState(false)
  const [copied, setCopied] = useState(false)
  const hasSummary = !!tc.summary

  const handleCopy = useCallback(() => {
    if (!tc.summary) return
    void navigator.clipboard.writeText(tc.summary)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }, [tc.summary])

  return (
    <div className="border-b border-zinc-800/50 last:border-b-0">
      <div
        className={`flex items-center gap-2 px-3 py-1.5 text-xs ${hasSummary ? 'cursor-pointer hover:bg-zinc-800/40' : ''}`}
        onClick={() => hasSummary && setExpanded((v) => !v)}
      >
        <StatusIcon status={tc.status} />
        <span className="font-mono text-zinc-300">{formatToolName(tc.tool)}</span>
        {tc.status === 'running' && (
          <span className="text-zinc-600 italic ml-auto">running...</span>
        )}
        {hasSummary && (
          <ChevronRight className={`w-3 h-3 text-zinc-600 ml-auto transition-transform ${expanded ? 'rotate-90' : ''}`} />
        )}
      </div>
      {expanded && tc.summary && (
        <div className="relative px-3 pb-2 group/result">
          <div className="max-h-32 overflow-y-auto rounded bg-zinc-900 border border-zinc-700/40 px-2 py-1">
            <pre className="text-[10px] text-zinc-400 whitespace-pre-wrap break-all font-mono">
              {tc.summary}
            </pre>
          </div>
          <button
            onClick={(e) => { e.stopPropagation(); handleCopy() }}
            className="absolute top-0 right-4 text-zinc-600 hover:text-zinc-300 opacity-0 group-hover/result:opacity-100 transition-opacity"
            title="Copy result"
          >
            {copied ? <Check className="w-3 h-3 text-green-400" /> : <Copy className="w-3 h-3" />}
          </button>
        </div>
      )}
    </div>
  )
}

function DragHandle({ onDrag }: { onDrag: (deltaY: number) => void }) {
  const dragging = useRef(false)
  const startY = useRef(0)

  const handleMouseDown = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    dragging.current = true
    startY.current = e.clientY

    const handleMouseMove = (ev: MouseEvent) => {
      if (!dragging.current) return
      const delta = ev.clientY - startY.current
      startY.current = ev.clientY
      onDrag(delta)
    }

    const handleMouseUp = () => {
      dragging.current = false
      window.removeEventListener('mousemove', handleMouseMove)
      window.removeEventListener('mouseup', handleMouseUp)
    }

    window.addEventListener('mousemove', handleMouseMove)
    window.addEventListener('mouseup', handleMouseUp)
  }, [onDrag])

  return (
    <div
      onMouseDown={handleMouseDown}
      className="h-1.5 cursor-row-resize flex items-center justify-center hover:bg-zinc-700/30 transition-colors group"
    >
      <div className="w-8 h-0.5 rounded-full bg-zinc-700 group-hover:bg-zinc-500 transition-colors" />
    </div>
  )
}

export function ToolCallDrawer() {
  const drawerState = useLayoutStore((s) => s.toolDrawerState)
  const drawerHeight = useLayoutStore((s) => s.toolDrawerHeight)
  const setDrawerState = useLayoutStore((s) => s.setToolDrawerState)
  const setDrawerHeight = useLayoutStore((s) => s.setToolDrawerHeight)
  const toolCalls = useChatStore((s) => s.toolCalls)
  const isStreaming = useChatStore((s) => s.isStreaming)
  const scrollRef = useRef<HTMLDivElement>(null)

  // Auto-scroll to bottom when new tool calls arrive.
  useEffect(() => {
    if (scrollRef.current && drawerState !== 'closed') {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
  }, [toolCalls.length, drawerState])

  // Auto-open drawer when streaming with tool calls (if user hasn't closed it).
  useEffect(() => {
    if (isStreaming && toolCalls.length > 0 && drawerState === 'closed') {
      // Only auto-open in drawer-auto mode — we check toolCallDisplayMode.
      // For now, respect if the user closed it.
    }
  }, [isStreaming, toolCalls.length, drawerState])

  if (drawerState === 'closed') {
    // Show a subtle toggle bar when there are tool calls.
    if (toolCalls.length === 0) return null
    return (
      <button
        onClick={() => setDrawerState('compact')}
        className="flex items-center gap-1.5 px-3 py-1 border-b border-zinc-800 text-xs text-zinc-500 hover:text-zinc-300 hover:bg-zinc-800/30 transition-colors w-full"
      >
        <Wrench className="w-3 h-3" />
        <span>{toolCalls.length} tool call{toolCalls.length !== 1 ? 's' : ''}</span>
        {toolCalls.some((tc) => tc.status === 'running') && (
          <Loader2 className="w-3 h-3 animate-spin text-indigo-400 ml-1" />
        )}
      </button>
    )
  }

  const isExpanded = drawerState === 'expanded'

  return (
    <div
      className={`border-b border-zinc-800 bg-zinc-950 flex flex-col ${isExpanded ? 'flex-1' : ''}`}
      style={isExpanded ? undefined : { height: `${drawerHeight}px` }}
    >
      {/* Header */}
      <div className="flex items-center justify-between px-3 py-1.5 border-b border-zinc-800/50 shrink-0">
        <div className="flex items-center gap-1.5 text-xs text-zinc-400">
          <Wrench className="w-3 h-3" />
          <span>Tool Calls</span>
          <span className="bg-zinc-800 text-zinc-500 px-1.5 py-0 rounded-full text-[10px] leading-relaxed tabular-nums">
            {toolCalls.length}
          </span>
          {toolCalls.some((tc) => tc.status === 'running') && (
            <Loader2 className="w-3 h-3 animate-spin text-indigo-400" />
          )}
        </div>
        <div className="flex items-center gap-0.5">
          <button
            onClick={() => setDrawerState(isExpanded ? 'compact' : 'expanded')}
            className="p-1 rounded text-zinc-600 hover:text-zinc-300 hover:bg-zinc-800 transition-colors"
            title={isExpanded ? 'Shrink' : 'Expand'}
          >
            {isExpanded ? <Minimize2 className="w-3 h-3" /> : <Maximize2 className="w-3 h-3" />}
          </button>
          <button
            onClick={() => setDrawerState('closed')}
            className="p-1 rounded text-zinc-600 hover:text-zinc-300 hover:bg-zinc-800 transition-colors"
            title="Close drawer"
          >
            <X className="w-3 h-3" />
          </button>
        </div>
      </div>

      {/* Tool call list */}
      <div ref={scrollRef} className="flex-1 overflow-y-auto min-h-0">
        {toolCalls.length === 0 ? (
          <div className="flex items-center justify-center py-8 text-xs text-zinc-600">
            No tool calls yet
          </div>
        ) : (
          toolCalls.map((tc) => <ToolCallRow key={tc.id} tc={tc} />)
        )}
      </div>

      {/* Drag handle (compact mode only) */}
      {!isExpanded && (
        <DragHandle onDrag={(delta) => setDrawerHeight(drawerHeight + delta)} />
      )}
    </div>
  )
}
