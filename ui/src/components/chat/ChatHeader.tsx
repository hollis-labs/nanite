import { useState, useCallback, useRef, useEffect } from 'react'
import { PanelLeft, PanelRight, Bot } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { Button } from '@/components/ui/Button'
import { Tooltip } from '@/components/ui/Tooltip'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useAppStore } from '@/stores/useAppStore'
import { api } from '@/lib/api'

export function ChatHeader() {
  const toggleLeftSidebar = useLayoutStore((s) => s.toggleLeftSidebar)
  const toggleRightRail = useLayoutStore((s) => s.toggleRightRail)
  const leftOpen = useLayoutStore((s) => s.leftSidebarOpen)
  const rightOpen = useLayoutStore((s) => s.rightRailOpen)
  const activeSessionId = useAppStore((s) => s.activeSessionId)

  const [isEditing, setIsEditing] = useState(false)
  const [editValue, setEditValue] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)

  const { data: session } = useQuery({
    queryKey: ['session', activeSessionId],
    queryFn: () => api.getSession(activeSessionId!),
    enabled: !!activeSessionId,
  })

  const title = session?.custom_name || session?.title || 'New Chat'
  const shortCode = session?.short_code

  const handleDoubleClick = useCallback(() => {
    setEditValue(title)
    setIsEditing(true)
  }, [title])

  useEffect(() => {
    if (isEditing) {
      inputRef.current?.focus()
      inputRef.current?.select()
    }
  }, [isEditing])

  const handleSave = useCallback(async () => {
    if (!activeSessionId) return
    const trimmed = editValue.trim()
    if (trimmed && trimmed !== title) {
      try {
        await api.updateSession(activeSessionId, { custom_name: trimmed } as Partial<typeof session & { custom_name: string }>)
      } catch (err) {
        console.error('Failed to update session title:', err)
      }
    }
    setIsEditing(false)
  }, [activeSessionId, editValue, title, session])

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      void handleSave()
    } else if (e.key === 'Escape') {
      setIsEditing(false)
    }
  }, [handleSave])

  return (
    <header className="flex items-center justify-between px-4 h-12 border-b border-zinc-800 shrink-0">
      <div className="flex items-center gap-3">
        <Tooltip content={leftOpen ? 'Hide sidebar (Cmd+B)' : 'Show sidebar (Cmd+B)'} side="bottom">
          <Button
            variant="ghost"
            size="icon"
            className={`w-8 h-8 ${leftOpen ? 'text-zinc-400' : 'text-zinc-600'} hover:text-zinc-100`}
            onClick={toggleLeftSidebar}
          >
            <PanelLeft className="w-4 h-4" />
          </Button>
        </Tooltip>
        <div className="flex items-center gap-2">
          {isEditing ? (
            <input
              ref={inputRef}
              value={editValue}
              onChange={(e) => setEditValue(e.target.value)}
              onBlur={() => void handleSave()}
              onKeyDown={handleKeyDown}
              className="text-sm font-medium text-zinc-100 bg-zinc-800 border border-zinc-700 rounded px-2 py-0.5 outline-none focus:border-indigo-500"
            />
          ) : (
            <div className="flex items-center gap-2" onDoubleClick={handleDoubleClick}>
              <h1 className="text-sm font-medium text-zinc-100 cursor-default">{title}</h1>
              {shortCode && (
                <span className="text-xs text-zinc-500">#{shortCode}</span>
              )}
            </div>
          )}
          <div className="flex items-center gap-1 px-1.5 py-0.5 rounded bg-indigo-500/15 border border-indigo-500/25">
            <Bot className="w-3 h-3 text-indigo-400" />
            <span className="text-xs text-indigo-400">Agent</span>
          </div>
        </div>
      </div>
      <div className="flex items-center gap-1">
        <Tooltip content={rightOpen ? 'Hide widgets (Cmd+/)' : 'Show widgets (Cmd+/)'} side="bottom">
          <Button
            variant="ghost"
            size="icon"
            className={`w-8 h-8 ${rightOpen ? 'text-zinc-400' : 'text-zinc-600'} hover:text-zinc-100`}
            onClick={toggleRightRail}
          >
            <PanelRight className="w-4 h-4" />
          </Button>
        </Tooltip>
      </div>
    </header>
  )
}
