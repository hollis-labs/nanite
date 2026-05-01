import { useCallback, useRef, useEffect, useState } from 'react'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useChatStore } from '@/stores/useChatStore'
import { useSettings } from '@/hooks/useSettings'
import { ToolCallItem } from './ToolCallItem'
import { ToolCallBanner } from './ToolCallBanner'

export function ToolCallDrawer() {
  const { data: settings } = useSettings()
  const retention = settings?.tool_drawer_retention ?? 15
  const drawerState = useLayoutStore((s) => s.toolDrawerState)
  const drawerHeight = useLayoutStore((s) => s.toolDrawerHeight)
  const setDrawerState = useLayoutStore((s) => s.setToolDrawerState)
  const setDrawerHeight = useLayoutStore((s) => s.setToolDrawerHeight)
  const toolCalls = useChatStore((s) => s.toolCalls)

  const scrollRef = useRef<HTMLDivElement>(null)
  const dragging = useRef(false)
  const didDrag = useRef(false)
  const startY = useRef(0)
  const startHeight = useRef(0)

  const toolDrawerEnabled = useLayoutStore((s) => s.toolDrawerEnabled)
  const isStreaming = useChatStore((s) => s.isStreaming)
  const isOpen = drawerState !== 'closed'
  const hasTools = toolCalls.length > 0

  // Component-level visibility (separate from open/closed body state)
  const [visible, setVisible] = useState(false)
  const hideTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const prevEnabledRef = useRef(toolDrawerEnabled)

  const startHideTimer = useCallback(() => {
    if (hideTimerRef.current) return
    hideTimerRef.current = setTimeout(() => {
      setVisible(false)
      setDrawerState('closed')
      hideTimerRef.current = null
    }, 30_000)
  }, [setDrawerState])

  const cancelHideTimer = useCallback(() => {
    if (hideTimerRef.current) { clearTimeout(hideTimerRef.current); hideTimerRef.current = null }
  }, [])

  // Clear hide timer on unmount
  useEffect(() => () => { if (hideTimerRef.current) clearTimeout(hideTimerRef.current) }, [])

  // Show/hide the entire drawer based on streaming activity + manual toggle
  useEffect(() => {
    const justEnabled = toolDrawerEnabled && !prevEnabledRef.current
    prevEnabledRef.current = toolDrawerEnabled

    if (!toolDrawerEnabled) {
      // User turned it off — hide immediately, cancel timer
      cancelHideTimer()
      setVisible(false)
      return
    }

    if (isStreaming && hasTools) {
      // Active tool use — show header bar, cancel any pending hide
      cancelHideTimer()
      setVisible(true)
      // Do NOT auto-expand body; user decides
    } else if (!isStreaming && !isOpen) {
      // Once the turn is finished, don't leave a collapsed "ghost" drawer
      // under the transcript. Retention is only useful if the user actually
      // opened the drawer to inspect tool details.
      cancelHideTimer()
      setVisible(false)
    } else if (justEnabled) {
      // User manually turned it on — show header bar, start 30s timer
      cancelHideTimer()
      setVisible(true)
      startHideTimer()
    } else if (!isStreaming && visible && !hideTimerRef.current) {
      // Streaming just stopped — start 30s auto-hide
      startHideTimer()
    }
  }, [isStreaming, hasTools, toolDrawerEnabled, visible, cancelHideTimer, startHideTimer])

  // Scroll to bottom when new tool calls arrive
  useEffect(() => {
    if (scrollRef.current && isOpen) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
  }, [toolCalls.length, isOpen])

  // Click header to toggle open/closed
  const handleHeaderClick = useCallback(() => {
    if (didDrag.current) { didDrag.current = false; return }
    setDrawerState(isOpen ? 'closed' : 'compact')
  }, [isOpen, setDrawerState])

  // Drag handle — works from both collapsed and expanded states
  const startDrag = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    e.stopPropagation()
    dragging.current = true
    didDrag.current = false
    startY.current = e.clientY
    startHeight.current = isOpen ? drawerHeight : 0

    const onMove = (ev: MouseEvent) => {
      if (!dragging.current) return
      const delta = ev.clientY - startY.current
      if (Math.abs(delta) > 3) didDrag.current = true
      const newH = startHeight.current + delta

      if (!isOpen) {
        // Collapsed: drag downward expands
        if (delta > 40) {
          setDrawerState('compact')
          setDrawerHeight(Math.max(80, delta))
        }
      } else {
        // Expanded: drag up/down to resize; close when dragged near the top
        if (newH < 80) {
          setDrawerState('closed')
        } else {
          setDrawerHeight(newH)
        }
      }
    }

    const onUp = () => {
      dragging.current = false
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
    }

    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
  }, [isOpen, drawerHeight, setDrawerState, setDrawerHeight])

  if (!toolDrawerEnabled || !visible) return null

  return (
    <div className="shrink-0 px-4">
      <div className="relative overflow-hidden rounded-b-[10px] border border-t-0 border-border-subtle bg-bg-elevated shadow-[0_4px_12px_-6px_rgba(0,0,0,0.22),0_-2px_0_-1px_rgba(0,0,0,0.04)]">

        {/* ── Header row — always visible when hasTools ── */}
        <ToolCallBanner
          toolCalls={toolCalls}
          isOpen={isOpen}
          retention={retention}
          onClick={handleHeaderClick}
        />

        {/* ── Tool list — only when open ── */}
        {isOpen && (
          <div
            ref={scrollRef}
            className="chat-scroll min-h-0 overflow-y-auto"
            style={{ height: `${drawerHeight}px` }}
          >
            {toolCalls.map((tc) => (
              <ToolCallItem key={tc.id} toolCall={tc} variant="drawer" />
            ))}
          </div>
        )}

        {/* ── Resize handle — on header row when collapsed, bottom of list when open ── */}
        <div
          onMouseDown={startDrag}
          onDoubleClick={() => setDrawerState(isOpen ? 'closed' : 'compact')}
          className="group flex h-2.5 cursor-row-resize items-center justify-center transition-colors hover:bg-surface"
        >
          <div className="h-0.5 w-8 rounded-full bg-border-subtle transition-colors group-hover:bg-fg-muted" />
        </div>

        {/* Bottom accent stripe */}
        <div className="h-[3px] bg-primary" />
      </div>
    </div>
  )
}
