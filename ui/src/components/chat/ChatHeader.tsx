import { useState, useCallback, useRef, useEffect } from 'react'
import { PanelLeft, PanelRight, Bot, ChevronDown, Users, Calendar } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { Button } from '@/components/ui/Button'
import { Tooltip } from '@/components/ui/Tooltip'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useAppStore } from '@/stores/useAppStore'
import { useChatStore } from '@/stores/useChatStore'
import { api } from '@/lib/api'
import { AGENT_MODES, type AgentMode } from '@/lib/types'
import { AgentRoster } from './AgentRoster'
import { useSprintPlanningStore } from '@/stores/useSprintPlanningStore'

const MODE_BADGE_STYLES: Record<AgentMode, { bg: string; border: string; text: string }> = {
  default: { bg: 'bg-blue-500/15', border: 'border-blue-500/25', text: 'text-blue-400' },
  architect: { bg: 'bg-purple-500/15', border: 'border-purple-500/25', text: 'text-purple-400' },
  planner: { bg: 'bg-green-500/15', border: 'border-green-500/25', text: 'text-green-400' },
  writer: { bg: 'bg-amber-500/15', border: 'border-amber-500/25', text: 'text-amber-400' },
}

export function ChatHeader() {
  const toggleLeftSidebar = useLayoutStore((s) => s.toggleLeftSidebar)
  const toggleRightRail = useLayoutStore((s) => s.toggleRightRail)
  const leftOpen = useLayoutStore((s) => s.leftSidebarOpen)
  const rightOpen = useLayoutStore((s) => s.rightRailOpen)
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const activeMode = useChatStore((s) => s.activeMode)
  const setActiveMode = useChatStore((s) => s.setActiveMode)

  const [isEditing, setIsEditing] = useState(false)
  const [editValue, setEditValue] = useState('')
  const [modeDropdownOpen, setModeDropdownOpen] = useState(false)
  const [rosterOpen, setRosterOpen] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)
  const modeDropdownRef = useRef<HTMLDivElement>(null)

  const { data: session } = useQuery({
    queryKey: ['session', activeSessionId],
    queryFn: () => api.getSession(activeSessionId!),
    enabled: !!activeSessionId,
  })

  const { data: sessionAgents = [] } = useQuery({
    queryKey: ['session-agents', activeSessionId],
    queryFn: () => api.listSessionAgents(activeSessionId!),
    enabled: !!activeSessionId,
  })

  const agentCount = sessionAgents.length
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

  // Close mode dropdown on outside click
  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (modeDropdownRef.current && !modeDropdownRef.current.contains(e.target as Node)) {
        setModeDropdownOpen(false)
      }
    }
    if (modeDropdownOpen) {
      document.addEventListener('mousedown', handleClickOutside)
      return () => document.removeEventListener('mousedown', handleClickOutside)
    }
  }, [modeDropdownOpen])

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

  const handleModeSelect = useCallback(async (mode: AgentMode) => {
    setActiveMode(mode)
    setModeDropdownOpen(false)
    if (activeSessionId) {
      try {
        await api.switchMode(activeSessionId, mode)
      } catch (err) {
        console.error('Failed to switch mode:', err)
      }
    }
  }, [activeSessionId, setActiveMode])

  const modeStyle = MODE_BADGE_STYLES[activeMode]

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

          {/* Mode badge + dropdown */}
          <div className="relative" ref={modeDropdownRef}>
            <button
              onClick={() => setModeDropdownOpen((o) => !o)}
              className={`flex items-center gap-1 px-1.5 py-0.5 rounded ${modeStyle.bg} border ${modeStyle.border} transition-colors hover:brightness-125`}
            >
              <Bot className={`w-3 h-3 ${modeStyle.text}`} />
              <span className={`text-xs ${modeStyle.text}`}>
                Mentat{activeMode !== 'default' ? ` \u00B7 ${activeMode}` : ''}
              </span>
              <ChevronDown className={`w-3 h-3 ${modeStyle.text}`} />
            </button>

            {modeDropdownOpen && (
              <div className="absolute top-full left-0 mt-1 w-40 bg-zinc-900 border border-zinc-700 rounded-lg shadow-xl z-50 py-1">
                <div className="px-3 py-1.5 text-xs font-medium text-zinc-500 uppercase tracking-wider">
                  Agent Mode
                </div>
                {AGENT_MODES.map((mode) => {
                  const style = MODE_BADGE_STYLES[mode]
                  return (
                    <button
                      key={mode}
                      onClick={() => void handleModeSelect(mode)}
                      className={`w-full text-left px-3 py-1.5 text-sm flex items-center gap-2 transition-colors ${
                        mode === activeMode
                          ? 'bg-zinc-800 text-zinc-100'
                          : 'text-zinc-400 hover:bg-zinc-800/60 hover:text-zinc-200'
                      }`}
                    >
                      <span className={`w-2 h-2 rounded-full ${style.bg.replace('/15', '')}`} />
                      <span className="capitalize">{mode}</span>
                    </button>
                  )
                })}
              </div>
            )}
          </div>
        </div>
      </div>
      <div className="flex items-center gap-1">
        {/* Sprint planning */}
        <Tooltip content="Sprint Planning" side="bottom">
          <button
            onClick={() => useSprintPlanningStore.getState().openSprintPlanning()}
            className="flex items-center gap-1 px-2 py-1 rounded-md text-xs text-zinc-400 hover:text-zinc-200 hover:bg-zinc-800 transition-colors"
          >
            <Calendar className="w-3.5 h-3.5" />
          </button>
        </Tooltip>
        {/* Agent count badge */}
        {agentCount > 1 && (
          <Tooltip content="View agents in session" side="bottom">
            <button
              onClick={() => setRosterOpen(true)}
              className="flex items-center gap-1 px-2 py-1 rounded-md text-xs text-zinc-400 hover:text-zinc-200 hover:bg-zinc-800 transition-colors"
            >
              <Users className="w-3.5 h-3.5" />
              <span>{agentCount}</span>
            </button>
          </Tooltip>
        )}
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

      {/* Agent Roster modal */}
      {rosterOpen && activeSessionId && (
        <AgentRoster sessionId={activeSessionId} onClose={() => setRosterOpen(false)} />
      )}
    </header>
  )
}
