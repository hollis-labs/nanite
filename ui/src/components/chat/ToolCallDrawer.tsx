import { useCallback, useRef, useEffect } from 'react'
import { Wrench, Loader2 } from 'lucide-react'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useChatStore } from '@/stores/useChatStore'
import { ToolCallItem } from './ToolCallItem'

export function ToolCallDrawer() {
  const drawerState = useLayoutStore((s) => s.toolDrawerState)
  const drawerHeight = useLayoutStore((s) => s.toolDrawerHeight)
  const setDrawerState = useLayoutStore((s) => s.setToolDrawerState)
  const setDrawerHeight = useLayoutStore((s) => s.setToolDrawerHeight)
  const toolCalls = useChatStore((s) => s.toolCalls)
  const scrollRef = useRef<HTMLDivElement>(null)
  const dragging = useRef(false)
  const startY = useRef(0)
  const startHeight = useRef(0)
  const didDrag = useRef(false)

  const isOpen = drawerState !== 'closed'
  const hasTools = toolCalls.length > 0
  const hasRunning = toolCalls.some((tc) => tc.status === 'running')

  // Auto-scroll to bottom when new tool calls arrive
  useEffect(() => {
    if (scrollRef.current && isOpen) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
  }, [toolCalls.length, isOpen])

  // Tab click: toggle open/closed (only fires if user didn't drag)
  const handleTabClick = useCallback(() => {
    if (didDrag.current) {
      didDrag.current = false
      return
    }
    setDrawerState(isOpen ? 'closed' : 'compact')
  }, [isOpen, setDrawerState])

  // Tab mousedown: start drag to resize (only when open)
  const handleTabMouseDown = useCallback((e: React.MouseEvent) => {
    if (!isOpen) return
    e.preventDefault()
    dragging.current = true
    didDrag.current = false
    startY.current = e.clientY
    startHeight.current = drawerHeight

    const handleMouseMove = (ev: MouseEvent) => {
      if (!dragging.current) return
      const delta = ev.clientY - startY.current
      if (Math.abs(delta) > 3) didDrag.current = true
      setDrawerHeight(startHeight.current + delta)
    }

    const handleMouseUp = () => {
      dragging.current = false
      window.removeEventListener('mousemove', handleMouseMove)
      window.removeEventListener('mouseup', handleMouseUp)
    }

    window.addEventListener('mousemove', handleMouseMove)
    window.addEventListener('mouseup', handleMouseUp)
  }, [isOpen, drawerHeight, setDrawerHeight])

  // Tab is always at the bottom of the drawer unit.
  // When closed: only the tab is visible (panel hidden).
  // When open: panel above, tab below — tab is the drawer's bottom edge.
  return (
    <div className="shrink-0">
      {/* Panel — only when open, above the tab */}
      {isOpen && (
        <div
          className="bg-bg-elevated border-b border-border-subtle overflow-hidden flex flex-col"
          style={{ height: `${drawerHeight}px` }}
        >
          {/* Tool call list or empty state */}
          <div
            ref={scrollRef}
            className="flex-1 overflow-y-auto min-h-0 chat-scroll"
          >
            {hasTools ? (
              toolCalls.map((tc) => (
                <ToolCallItem key={tc.id} toolCall={tc} variant="drawer" />
              ))
            ) : (
              <div className="flex flex-col items-center justify-center h-full text-center px-6">
                <Wrench className="w-8 h-8 text-fg-faint mb-3" />
                <p className="text-xs text-fg-secondary">Tool calls will appear here as the agent uses tools.</p>
                <p className="text-[10px] text-fg-faint mt-1">Tool call history is kept per session and clears after 15 minutes of inactivity.</p>
              </div>
            )}
          </div>

          {/* Bottom drag handle */}
          <div
            onMouseDown={(e) => {
              e.preventDefault()
              dragging.current = true
              startY.current = e.clientY
              startHeight.current = drawerHeight
              const move = (ev: MouseEvent) => {
                if (!dragging.current) return
                setDrawerHeight(startHeight.current + (ev.clientY - startY.current))
              }
              const up = () => {
                dragging.current = false
                window.removeEventListener('mousemove', move)
                window.removeEventListener('mouseup', up)
              }
              window.addEventListener('mousemove', move)
              window.addEventListener('mouseup', up)
            }}
            className="h-1.5 cursor-row-resize flex items-center justify-center hover:bg-surface-hover/30 transition-colors group shrink-0"
          >
            <div className="w-8 h-0.5 rounded-full bg-border-subtle group-hover:bg-fg-muted transition-colors" />
          </div>
        </div>
      )}

      {/* Tab — always visible, right-aligned, attached to bottom of drawer */}
      <div className="flex justify-end pr-2.5">
        <button
          type="button"
          onClick={handleTabClick}
          onMouseDown={handleTabMouseDown}
          className={`
            flex items-center gap-1.5 px-4 py-1 rounded-b-lg text-xs transition-all
            border border-t-0 border-border-subtle shadow-sm
            ${isOpen
              ? 'bg-bg-elevated text-fg-secondary cursor-row-resize'
              : 'bg-bg-elevated/80 text-fg-muted hover:text-fg-secondary hover:bg-bg-elevated cursor-pointer'
            }
          `}
        >
          <Wrench className="size-3" />
          <span className="tabular-nums">{toolCalls.length}</span>
          {hasRunning && <Loader2 className="size-3 animate-spin text-accent" />}
        </button>
      </div>
    </div>
  )
}
