import { useState, useCallback, useRef, useEffect } from 'react'
import { PanelLeft, PanelRight, Bot, ChevronDown, Users, Copy, GitFork } from 'lucide-react'
import { usePluginSlots } from '@/hooks/usePluginSlots'
import { resolveIcon } from '@/lib/icons'
import { SourceBadge } from '@/components/agents/SourceBadge'
import { AdapterBadge } from './AdapterBadge'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Tooltip } from '@/components/ui/tooltip'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useAppStore } from '@/stores/useAppStore'
import { useChatStore } from '@/stores/useChatStore'
import { api } from '@/lib/api'
import { AgentRoster } from './AgentRoster'
import type { UISlotEntry } from '@/lib/types'

export function ChatHeader() {
  const toggleLeftSidebar = useLayoutStore((s) => s.toggleLeftSidebar)
  const toggleRightRail = useLayoutStore((s) => s.toggleRightRail)
  const leftOpen = useLayoutStore((s) => s.leftSidebarOpen)
  const rightOpen = useLayoutStore((s) => s.rightRailOpen)
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const activeMode = useChatStore((s) => s.activeMode)
  const activeModel = useChatStore((s) => s.activeModel)
  const toolCalls = useChatStore((s) => s.toolCalls)
  const queryClient = useQueryClient()

  const [dropdownOpen, setDropdownOpen] = useState(false)
  const [rosterOpen, setRosterOpen] = useState(false)
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

  const primaryAgent = sessionAgents.find((a) => a.role === 'primary')
  const primaryAgentProfile = allAgents.find((a) => a.id === primaryAgent?.agent_id)
  const activeAgentName = primaryAgentProfile?.name || 'Conduit'

  const agentCount = sessionAgents.length
  const shortCode = session?.short_code
  const toolCount = tools.length + (toolCalls?.length || 0)

  // Provider — getSession may return empty; fall back to the sessions list
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId)
  const { data: sessions = [] } = useQuery({
    queryKey: ['sessions', activeWorkspaceId],
    queryFn: () => api.listSessions(activeWorkspaceId ?? undefined),
    enabled: !!activeWorkspaceId,
  })
  const sessionFromList = sessions.find((s) => s.id === activeSessionId)
  const sessionProvider = session?.provider || sessionFromList?.provider || ''

  // Model display — from session or agent profile
  const modelName = session?.model || activeModel || primaryAgentProfile?.default_model || null
  const shortModel = modelName ? modelName.split('/').pop()?.replace(/-\d{8}$/, '') : null

  // Switch primary agent
  const switchAgentMutation = useMutation({
    mutationFn: async (agentId: string) => {
      if (!activeSessionId) return
      if (primaryAgent) {
        try {
          await api.removeSessionAgent(activeSessionId, primaryAgent.agent_id)
        } catch { /* ignore */ }
      }
      return api.addSessionAgent(activeSessionId, agentId, 'primary')
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['session-agents', activeSessionId] })
    },
  })

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

  const pluginActions = usePluginSlots('chat-header-action')

  // Dispatch plugin slot actions by type
  const handlePluginAction = useCallback((entry: UISlotEntry) => {
    switch (entry.action) {
      case 'command':
        // Execute a slash command
        if (activeSessionId && entry.props?.command) {
          void api.executeCommand(String(entry.props.command), activeSessionId, '')
        }
        break
      case 'navigate':
        // Navigate to a page/hash
        if (entry.props?.hash) {
          window.location.hash = String(entry.props.hash)
        }
        break
      case 'handler':
        // Dispatch a custom event that the plugin's modal/component listens for
        window.dispatchEvent(new CustomEvent('plugin-action', { detail: { id: entry.id, entry } }))
        break
      case 'modal':
        // Dispatch a modal-open event
        window.dispatchEvent(new CustomEvent('plugin-modal', { detail: { id: entry.id, component: entry.component, props: entry.props } }))
        break
      default:
        console.warn('Unknown plugin action type:', entry.action)
    }
  }, [activeSessionId])

  return (
    <header className="flex items-center justify-between px-4 h-12 border-b border-border shrink-0">
      <div className="flex items-center gap-3">
        <Tooltip content={leftOpen ? 'Hide sidebar (Cmd+B)' : 'Show sidebar (Cmd+B)'} side="bottom">
          <Button
            variant="ghost"
            size="icon"
            className={`w-8 h-8 ${leftOpen ? 'text-fg-secondary' : 'text-fg-faint'} hover:text-fg`}
            onClick={toggleLeftSidebar}
          >
            <PanelLeft className="w-4 h-4" />
          </Button>
        </Tooltip>

        {/* Agent info — 2 column: avatar + info rows */}
        <div className="flex items-center gap-2.5">
          {/* Avatar */}
          <div className="w-8 h-8 rounded-lg bg-surface flex items-center justify-center shrink-0">
            <Bot className="w-4 h-4 text-fg-secondary" />
          </div>

          {/* Info columns */}
          <div className="flex flex-col justify-center min-w-0 gap-0.5">
            {/* Row 1: Agent dropdown + adapter badge + fork */}
            <div className="flex items-center gap-1.5">
              <div className="relative" ref={dropdownRef}>
                <button
                  onClick={() => setDropdownOpen((o) => !o)}
                  className="flex items-center gap-1 text-sm font-medium text-fg hover:text-fg transition-colors"
                >
                  <span className="truncate max-w-[180px]">{activeAgentName}</span>
                  <ChevronDown className="w-3 h-3 text-fg-muted shrink-0" />
                </button>

                {dropdownOpen && (
                  <div className="absolute top-full left-0 mt-1 w-52 bg-bg-elevated border border-border-subtle rounded-sm shadow-xl z-50 py-1">
                    <div className="px-3 py-1.5 text-[10px] font-medium text-fg-muted uppercase tracking-wider">
                      Switch Agent
                    </div>
                    {allAgents.filter((a) => a.status !== 'disabled').map((agent) => (
                      <button
                        key={agent.id}
                        onClick={() => handleAgentSelect(agent.id)}
                        disabled={switchAgentMutation.isPending}
                        className={`w-full text-left px-3 py-1.5 text-xs flex items-center gap-2 transition-colors ${
                          primaryAgent?.agent_id === agent.id
                            ? 'bg-surface text-fg'
                            : 'text-fg-secondary hover:bg-surface/60 hover:text-fg'
                        }`}
                      >
                        <Bot className="w-3 h-3 shrink-0" />
                        <span className="truncate">{agent.name}</span>
                        {agent.source && <SourceBadge source={agent.source} className="ml-auto" />}
                      </button>
                    ))}
                  </div>
                )}
              </div>

              {session && (
                <AdapterBadge provider={sessionProvider || 'api'} size="sm" />
              )}

              <div className="relative" ref={forkMenuRef}>
                <Tooltip content="Clone or fork" side="bottom">
                  <button
                    onClick={() => setForkMenuOpen((o) => !o)}
                    disabled={forkMutation.isPending}
                    className="p-0.5 rounded text-fg-faint hover:text-fg-secondary hover:bg-surface transition-colors"
                  >
                    <Copy className="w-3 h-3" />
                  </button>
                </Tooltip>
                {forkMenuOpen && (
                  <div className="absolute top-full left-0 mt-1 w-44 bg-bg-elevated border border-border-subtle rounded-sm shadow-xl z-50 py-1">
                    <button
                      onClick={() => forkMutation.mutate(false)}
                      className="w-full text-left px-3 py-1.5 text-xs flex items-center gap-2 text-fg-secondary hover:bg-surface transition-colors"
                    >
                      <Copy className="w-3 h-3 text-fg-muted" />
                      Clone (empty)
                    </button>
                    <button
                      onClick={() => forkMutation.mutate(true)}
                      className="w-full text-left px-3 py-1.5 text-xs flex items-center gap-2 text-fg-secondary hover:bg-surface transition-colors"
                    >
                      <GitFork className="w-3 h-3 text-fg-muted" />
                      Fork (with history)
                    </button>
                  </div>
                )}
              </div>
            </div>

            {/* Row 2: Info pills */}
            <div className="flex items-center gap-1">
              {shortCode && (
                <span className="px-1 py-0 rounded text-[10px] font-mono text-fg-faint bg-surface/50 leading-relaxed">
                  #{shortCode}
                </span>
              )}
              {shortModel && (
                <span className="px-1 py-0 rounded text-[10px] text-fg-muted bg-surface/50 leading-relaxed truncate max-w-[120px]">
                  {shortModel}
                </span>
              )}
              {toolCount > 0 && (
                <span className="px-1 py-0 rounded text-[10px] text-fg-muted bg-surface/50 leading-relaxed">
                  {toolCount} tools
                </span>
              )}
              {activeMode && activeMode !== 'default' && (
                <span className="px-1 py-0 rounded text-[10px] text-amber-400 bg-amber-500/10 leading-relaxed">
                  {activeMode}
                </span>
              )}
              {primaryAgentProfile?.source && (
                <SourceBadge source={primaryAgentProfile.source} />
              )}
            </div>
          </div>
        </div>
      </div>

      {/* Right side controls */}
      <div className="flex items-center gap-1">
        {/* Sprint button removed — now delivered via chat-header-action plugin slot */}
        {agentCount > 1 && (
          <Tooltip content="View agents in session" side="bottom">
            <button
              onClick={() => setRosterOpen(true)}
              className="flex items-center gap-1 px-2 py-1 rounded-md text-xs text-fg-secondary hover:text-fg hover:bg-surface transition-colors"
            >
              <Users className="w-3.5 h-3.5" />
              <span>{agentCount}</span>
            </button>
          </Tooltip>
        )}
        {/* Plugin-registered header actions */}
        {pluginActions.map((entry) => {
          const PluginIcon = resolveIcon(entry.icon)
          return (
            <Tooltip key={entry.id} content={entry.label} side="bottom">
              <button
                onClick={() => handlePluginAction(entry)}
                className="flex items-center gap-1 px-2 py-1 rounded-md text-xs text-fg-secondary hover:text-fg hover:bg-surface transition-colors"
              >
                <PluginIcon className="w-3.5 h-3.5" />
              </button>
            </Tooltip>
          )
        })}
        <Tooltip content={rightOpen ? 'Hide panel (Cmd+/)' : 'Show panel (Cmd+/)'} side="bottom">
          <Button
            variant="ghost"
            size="icon"
            className={`w-8 h-8 ${rightOpen ? 'text-fg-secondary' : 'text-fg-faint'} hover:text-fg`}
            onClick={toggleRightRail}
          >
            <PanelRight className="w-4 h-4" />
          </Button>
        </Tooltip>
      </div>

      {rosterOpen && activeSessionId && (
        <AgentRoster sessionId={activeSessionId} onClose={() => setRosterOpen(false)} />
      )}
    </header>
  )
}
