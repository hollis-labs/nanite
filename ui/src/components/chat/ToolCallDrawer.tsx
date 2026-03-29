import { useCallback, useRef, useEffect } from 'react'
import { Wrench, Maximize2, Minimize2, X, Loader2 } from 'lucide-react'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useChatStore } from '@/stores/useChatStore'
import { ToolCallItem } from './ToolCallItem'

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
      className="h-1.5 cursor-row-resize flex items-center justify-center hover:bg-surface-hover/30 transition-colors group"
    >
      <div className="w-8 h-0.5 rounded-full bg-border-subtle group-hover:bg-fg-muted transition-colors" />
    </div>
  )
}

export function ToolCallDrawer() {
  const drawerState = useLayoutStore((s) => s.toolDrawerState)
  const drawerHeight = useLayoutStore((s) => s.toolDrawerHeight)
  const setDrawerState = useLayoutStore((s) => s.setToolDrawerState)
  const setDrawerHeight = useLayoutStore((s) => s.setToolDrawerHeight)
  const toolCalls = useChatStore((s) => s.toolCalls)
  const scrollRef = useRef<HTMLDivElement>(null)

  // Auto-scroll to bottom when new tool calls arrive.
  useEffect(() => {
    if (scrollRef.current && drawerState !== 'closed') {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
  }, [toolCalls.length, drawerState])

  if (drawerState === 'closed') {
    if (toolCalls.length === 0) return null
    return (
      <button
        onClick={() => setDrawerState('compact')}
        className="flex items-center gap-1.5 px-3 py-1 border-b border-border text-xs text-fg-muted hover:text-fg-secondary hover:bg-surface/30 transition-colors w-full"
      >
        <Wrench className="w-3 h-3" />
        <span>{toolCalls.length} tool call{toolCalls.length !== 1 ? 's' : ''}</span>
        {toolCalls.some((tc) => tc.status === 'running') && (
          <Loader2 className="w-3 h-3 animate-spin text-accent ml-1" />
        )}
      </button>
    )
  }

  const isExpanded = drawerState === 'expanded'

  return (
    <div
      className={`border-b border-border bg-bg flex flex-col ${isExpanded ? 'flex-1' : ''}`}
      style={isExpanded ? undefined : { height: `${drawerHeight}px` }}
    >
      {/* Header */}
      <div className="flex items-center justify-between px-3 py-1.5 border-b border-border/50 shrink-0">
        <div className="flex items-center gap-1.5 text-xs text-fg-secondary">
          <Wrench className="w-3 h-3" />
          <span>Tool Calls</span>
          <span className="bg-surface text-fg-muted px-1.5 py-0 rounded-full text-[10px] leading-relaxed tabular-nums">
            {toolCalls.length}
          </span>
          {toolCalls.some((tc) => tc.status === 'running') && (
            <Loader2 className="w-3 h-3 animate-spin text-accent" />
          )}
        </div>
        <div className="flex items-center gap-0.5">
          <button
            onClick={() => setDrawerState(isExpanded ? 'compact' : 'expanded')}
            className="p-1 rounded text-fg-faint hover:text-fg-secondary hover:bg-surface transition-colors"
            title={isExpanded ? 'Shrink' : 'Expand'}
          >
            {isExpanded ? <Minimize2 className="w-3 h-3" /> : <Maximize2 className="w-3 h-3" />}
          </button>
          <button
            onClick={() => setDrawerState('closed')}
            className="p-1 rounded text-fg-faint hover:text-fg-secondary hover:bg-surface transition-colors"
            title="Close drawer"
          >
            <X className="w-3 h-3" />
          </button>
        </div>
      </div>

      {/* Tool call list — uses shared ToolCallItem */}
      <div ref={scrollRef} className="flex-1 overflow-y-auto min-h-0">
        {toolCalls.length === 0 ? (
          <div className="flex items-center justify-center py-8 text-xs text-fg-faint">
            No tool calls yet
          </div>
        ) : (
          toolCalls.map((tc) => (
            <ToolCallItem key={tc.id} toolCall={tc} variant="drawer" />
          ))
        )}
      </div>

      {/* Drag handle (compact mode only) */}
      {!isExpanded && (
        <DragHandle onDrag={(delta) => setDrawerHeight(drawerHeight + delta)} />
      )}
    </div>
  )
}
