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

  const isOpen = drawerState !== 'closed'
  const hasTools = toolCalls.length > 0
  const hasRunning = toolCalls.some((tc) => tc.status === 'running')

  // Auto-scroll to bottom when new tool calls arrive
  useEffect(() => {
    if (scrollRef.current && isOpen) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
  }, [toolCalls.length, isOpen])

  // Double-click tab: toggle open/closed
  const handleTabDoubleClick = useCallback(() => {
    setDrawerState(isOpen ? 'closed' : 'compact')
  }, [isOpen, setDrawerState])

  // Single click tab: open if closed
  const handleTabClick = useCallback(() => {
    if (!isOpen) setDrawerState('compact')
  }, [isOpen, setDrawerState])

  // Drag tab to resize
  const handleTabMouseDown = useCallback((e: React.MouseEvent) => {
    if (!isOpen) return
    e.preventDefault()
    dragging.current = true
    startY.current = e.clientY
    startHeight.current = drawerHeight

    const handleMouseMove = (ev: MouseEvent) => {
      if (!dragging.current) return
      const delta = ev.clientY - startY.current
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

  // Don't render anything if there are no tool calls
  if (!hasTools) return null

  return (
    <div className="relative shrink-0">
      {/* Pull tab — always visible, centered under the header */}
      <div className="flex justify-center">
        <button
          type="button"
          onClick={handleTabClick}
          onDoubleClick={handleTabDoubleClick}
          onMouseDown={handleTabMouseDown}
          className={`
            flex items-center gap-1.5 px-3 py-1 rounded-b-lg text-xs transition-all
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

      {/* Drawer panel — floats above chat */}
      {isOpen && (
        <div
          className="absolute left-[10%] right-[10%] top-0 z-10 bg-bg-elevated border border-border-subtle rounded-b-xl shadow-xl overflow-hidden flex flex-col"
          style={{ height: `${drawerHeight}px` }}
        >
          {/* Tool call list */}
          <div ref={scrollRef} className="flex-1 overflow-y-auto min-h-0">
            {toolCalls.map((tc) => (
              <ToolCallItem key={tc.id} toolCall={tc} variant="drawer" />
            ))}
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
    </div>
  )
}
