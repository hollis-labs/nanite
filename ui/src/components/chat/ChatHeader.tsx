import { useState, useCallback, useRef, useEffect } from 'react'
import { PanelLeft, PanelRight, Bot, ChevronDown, Users, Calendar, Copy, GitFork, Wrench } from 'lucide-react'
import { AdapterBadge } from './AdapterBadge'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '@/components/ui/Button'
import { Tooltip } from '@/components/ui/Tooltip'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useAppStore } from '@/stores/useAppStore'
import { useChatStore } from '@/stores/useChatStore'
import { api } from '@/lib/api'
import { AgentRoster } from './AgentRoster'
import { useSprintPlanningStore } from '@/stores/useSprintPlanningStore'

// Capability pills — detected, not user-set
interface Capability {
  label: string
  color: string // tailwind text color class
  bg: string    // tailwind bg class
}

function detectCapabilities(provider?: string, mode?: string, toolCount?: number): Capability[] {
  const caps: Capability[] = []

  // Agent mode — always present
  caps.push({ label: 'Agent', color: 'text-blue-400', bg: 'bg-blue-500/15' })

  // Plan mode — detected when mode is planner or architect
  if (mode === 'planner' || mode === 'architect') {
    caps.push({ label: 'Plan', color: 'text-green-400', bg: 'bg-green-500/15' })
  }

  // PTY — detected when using a PTY/subprocess adapter
  if (provider?.startsWith('pty')) {
    caps.push({ label: 'PTY', color: 'text-cyan-400', bg: 'bg-cyan-500/15' })
  }

  // Tools — detected when MCP tools are available
  if (toolCount && toolCount > 0) {
    caps.push({ label: 'Tools', color: 'text-amber-400', bg: 'bg-amber-500/15' })
  }

  return caps
}

export function ChatHeader() {
  const toggleLeftSidebar = useLayoutStore((s) => s.toggleLeftSidebar)
  const toggleRightRail = useLayoutStore((s) => s.toggleRightRail)
  const leftOpen = useLayoutStore((s) => s.leftSidebarOpen)
  const rightOpen = useLayoutStore((s) => s.rightRailOpen)
  const toolDrawerState = useLayoutStore((s) => s.toolDrawerState)
  const setToolDrawerState = useLayoutStore((s) => s.setToolDrawerState)
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const activeMode = useChatStore((s) => s.activeMode)
  const toolCalls = useChatStore((s) => s.toolCalls)
  const queryClient = useQueryClient()

  const [isEditing, setIsEditing] = useState(false)
  const [editValue, setEditValue] = useState('')
  const [dropdownOpen, setDropdownOpen] = useState(false)
  const [rosterOpen, setRosterOpen] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)
  const dropdownRef = useRef<HTMLDivElement>(null)

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

  // Tool count from the tools API for capability detection
  const { data: tools = [] } = useQuery({
    queryKey: ['tools'],
    queryFn: api.fetchTools,
    staleTime: 60_000,
  })

  const configVersion = useAppStore((s) => s.configVersion)

  const { data: allAgents = [] } = useQuery({
    queryKey: ['agents', configVersion],
    queryFn: api.listAgents,
  })

  // Find the primary agent for this session
  const primaryAgent = sessionAgents.find((a) => (a as any).is_primary === true || (a as any).is_primary === 1 || a.role === 'primary')
  const primaryAgentProfile = allAgents.find((a) => a.id === primaryAgent?.agent_id)
  const activeAgentName = primaryAgentProfile?.name || 'Conduit'

  const agentCount = sessionAgents.length
  const title = session?.custom_name || session?.title || 'New Chat'
  const shortCode = session?.short_code

  // Detect capabilities
  const capabilities = detectCapabilities(
    session?.provider,
    activeMode,
    tools.length + (toolCalls?.length || 0),
  )

  // Switch primary agent for this session
  const switchAgentMutation = useMutation({
    mutationFn: async (agentId: string) => {
      if (!activeSessionId) return
      // Remove current primary if exists
      if (primaryAgent) {
        try {
          await api.removeSessionAgent(activeSessionId, primaryAgent.agent_id)
        } catch { /* ignore if not found */ }
      }
      // Add new agent as primary
      return api.addSessionAgent(activeSessionId, agentId, 'primary')
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['session-agents', activeSessionId] })
    },
  })

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

  // Close dropdown on outside click
  useEffect(() => {
    if (!dropdownOpen) return
    function handleClickOutside(e: MouseEvent) {
      if (dropdownRef.current && !dropdownRef.current.contains(e.target as Node)) {
        setDropdownOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [dropdownOpen])

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

  const handleAgentSelect = useCallback((agentId: string) => {
    switchAgentMutation.mutate(agentId)
    setDropdownOpen(false)
  }, [switchAgentMutation])

  const setActiveSession = useAppStore((s) => s.setActiveSession)

  const [forkMenuOpen, setForkMenuOpen] = useState(false)
  const forkMenuRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!forkMenuOpen) return
    function handleClick(e: MouseEvent) {
      if (forkMenuRef.current && !forkMenuRef.current.contains(e.target as Node)) {
        setForkMenuOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [forkMenuOpen])

  const forkMutation = useMutation({
    mutationFn: (includeMessages: boolean) => {
      if (!activeSessionId) throw new Error('No active session')
      return api.forkSession(activeSessionId, { include_messages: includeMessages })
    },
    onSuccess: (newSession) => {
      void queryClient.invalidateQueries({ queryKey: ['sessions'] })
      setActiveSession(newSession.id)
      setForkMenuOpen(false)
    },
  })

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
              {session?.provider && (
                <span className="flex items-center gap-1">
                  <AdapterBadge provider={session.provider} size="md" />
                  <div className="relative" ref={forkMenuRef}>
                    <Tooltip content="Clone or fork session" side="bottom">
                      <button
                        onClick={() => setForkMenuOpen((o) => !o)}
                        disabled={forkMutation.isPending}
                        className="p-0.5 rounded text-zinc-600 hover:text-zinc-300 hover:bg-zinc-800 transition-colors"
                      >
                        <Copy className="w-3 h-3" />
                      </button>
                    </Tooltip>
                    {forkMenuOpen && (
                      <div className="absolute top-full left-0 mt-1 w-44 bg-zinc-900 border border-zinc-700 rounded-lg shadow-xl z-50 py-1">
                        <button
                          onClick={() => forkMutation.mutate(false)}
                          className="w-full text-left px-3 py-1.5 text-xs flex items-center gap-2 text-zinc-300 hover:bg-zinc-800 transition-colors"
                        >
                          <Copy className="w-3 h-3 text-zinc-500" />
                          Clone (empty)
                        </button>
                        <button
                          onClick={() => forkMutation.mutate(true)}
                          className="w-full text-left px-3 py-1.5 text-xs flex items-center gap-2 text-zinc-300 hover:bg-zinc-800 transition-colors"
                        >
                          <GitFork className="w-3 h-3 text-zinc-500" />
                          Fork (with history)
                        </button>
                      </div>
                    )}
                  </div>
                </span>
              )}
            </div>
          )}

          {/* Agent badge + dropdown */}
          <div className="relative" ref={dropdownRef}>
            <button
              onClick={() => setDropdownOpen((o) => !o)}
              className="flex items-center gap-1 px-1.5 py-0.5 rounded bg-zinc-800 border border-zinc-700 transition-colors hover:border-zinc-600"
            >
              <Bot className="w-3 h-3 text-zinc-400" />
              <span className="text-xs text-zinc-300">
                {activeAgentName}
              </span>
              <ChevronDown className="w-3 h-3 text-zinc-500" />
            </button>

            {dropdownOpen && (
              <div className="absolute top-full left-0 mt-1 w-52 bg-zinc-900 border border-zinc-700 rounded-lg shadow-xl z-50 py-1">
                {/* Agent section */}
                <div className="px-3 py-1.5 text-xs font-medium text-zinc-500 uppercase tracking-wider">
                  Agent
                </div>
                {allAgents.map((agent) => (
                  <button
                    key={agent.id}
                    onClick={() => handleAgentSelect(agent.id)}
                    disabled={switchAgentMutation.isPending}
                    className={`w-full text-left px-3 py-1.5 text-sm flex items-center gap-2 transition-colors ${
                      primaryAgent?.agent_id === agent.id
                        ? 'bg-zinc-800 text-zinc-100'
                        : 'text-zinc-400 hover:bg-zinc-800/60 hover:text-zinc-200'
                    }`}
                  >
                    <Bot className="w-3 h-3 shrink-0" />
                    <span className="truncate">{agent.name}</span>
                  </button>
                ))}

              </div>
            )}
          </div>

          {/* Capability indicator pills */}
          <div className="flex items-center gap-1">
            {capabilities.map((cap) => (
              <span
                key={cap.label}
                className={`px-1.5 py-0.5 rounded text-[10px] font-medium ${cap.color} ${cap.bg}`}
              >
                {cap.label}
              </span>
            ))}
          </div>
        </div>
      </div>
      <div className="flex items-center gap-1">
        {/* Tool drawer toggle */}
        <Tooltip content={toolDrawerState === 'closed' ? 'Show tool calls' : 'Hide tool calls'} side="bottom">
          <button
            onClick={() => {
              const next = toolDrawerState === 'closed' ? 'compact' : toolDrawerState === 'compact' ? 'expanded' : 'closed'
              setToolDrawerState(next)
            }}
            className={`flex items-center gap-1 px-2 py-1 rounded-md text-xs transition-colors ${
              toolDrawerState !== 'closed'
                ? 'text-indigo-400 hover:text-indigo-300 hover:bg-indigo-500/10'
                : 'text-zinc-400 hover:text-zinc-200 hover:bg-zinc-800'
            }`}
          >
            <Wrench className="w-3.5 h-3.5" />
          </button>
        </Tooltip>
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
