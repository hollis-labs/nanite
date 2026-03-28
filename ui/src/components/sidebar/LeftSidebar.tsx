import { useState, useMemo, useEffect, useCallback, type ReactNode } from 'react'
import { Plus, Hash, Pin, PinOff, Loader2, ChevronRight, ChevronDown } from 'lucide-react'
import { AdapterBadge } from '@/components/chat/AdapterBadge'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '@/components/ui/Button'
import { ScrollArea } from '@/components/ui/ScrollArea'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useAppStore } from '@/stores/useAppStore'
import { useChatStore } from '@/stores/useChatStore'
import { useSettings, useModels, useProviders } from '@/hooks/useSettings'
import { api } from '@/lib/api'
import type { Session, UserSettings, ModelRecord, ProviderConfig, Agent } from '@/lib/types'

const ADAPTER_OPTIONS = [
  { value: '', label: 'Default' },
  { value: 'http', label: 'HTTP (API)' },
  { value: 'pty', label: 'PTY (CLI)' },
  { value: 'subprocess', label: 'Subprocess' },
]

function NewSessionForm({
  defaults,
  models,
  providers,
  agents,
  isPending,
  onCreate,
}: {
  defaults: UserSettings | undefined
  models: ModelRecord[]
  providers: ProviderConfig[]
  agents: Agent[]
  isPending: boolean
  onCreate: (overrides: { provider?: string; model?: string; agent_id?: string }) => void
}) {
  const [provider, setProvider] = useState(defaults?.default_provider ?? '')
  const [model, setModel] = useState(defaults?.default_model ?? '')
  const [agent, setAgent] = useState(defaults?.default_agent ?? '')

  const providerOptions = useMemo(() =>
    providers.map((p) => ({ value: p.provider_type, label: p.name })),
    [providers],
  )

  const modelOptions = useMemo(() => {
    const filtered = provider ? models.filter((m) => m.provider_type === provider) : models
    return filtered.filter((m) => m.is_enabled).map((m) => ({ value: m.model_id, label: m.display_name }))
  }, [models, provider])

  const agentOptions = useMemo(() =>
    agents.map((a) => ({ value: a.id, label: a.name })),
    [agents],
  )

  return (
    <div className="px-3 pb-3 space-y-2">
      <div className="space-y-1.5">
        <FormSelect label="Provider" value={provider} options={providerOptions} onChange={(v) => { setProvider(v); setModel('') }} />
        <FormSelect label="Model" value={model} options={modelOptions} onChange={setModel} />
        <FormSelect label="Adapter" value="" options={ADAPTER_OPTIONS} onChange={() => {}} disabled />
        <FormSelect label="Agent" value={agent} options={agentOptions} onChange={setAgent} />
      </div>
      <button
        onClick={() => onCreate({
          provider: provider || undefined,
          model: model || undefined,
          agent_id: agent || undefined,
        })}
        disabled={isPending}
        className="w-full py-1.5 rounded-md bg-indigo-600 hover:bg-indigo-500 text-white text-xs font-medium transition-colors disabled:opacity-50"
      >
        {isPending ? 'Creating...' : 'Create Session'}
      </button>
    </div>
  )
}

function FormSelect({
  label,
  value,
  options,
  onChange,
  disabled,
}: {
  label: string
  value: string
  options: { value: string; label: string }[]
  onChange: (value: string) => void
  disabled?: boolean
}) {
  return (
    <div className="flex items-center gap-2">
      <span className="text-[10px] uppercase tracking-wider text-zinc-500 w-14 shrink-0">{label}</span>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        className="flex-1 bg-zinc-900 border border-zinc-700 rounded px-2 py-1 text-xs text-zinc-200 focus:outline-none focus:ring-1 focus:ring-indigo-500 disabled:opacity-50 disabled:cursor-not-allowed"
      >
        <option value="">Default</option>
        {options.map((opt) => (
          <option key={opt.value} value={opt.value}>{opt.label}</option>
        ))}
      </select>
    </div>
  )
}

const TASK_CONTEXT_TYPES = new Set(['task', 'sprint', 'review', 'workflow'])
const TASKS_COLLAPSED_KEY = 'sidebar-tasks-collapsed'

function formatRelativeTime(dateStr: string): string {
  const date = new Date(dateStr)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffMins = Math.floor(diffMs / 1000 / 60)
  const diffHours = Math.floor(diffMins / 60)
  const diffDays = Math.floor(diffHours / 24)

  if (diffMins < 1) return 'now'
  if (diffMins < 60) return `${diffMins}m`
  if (diffHours < 24) return `${diffHours}h`
  if (diffDays < 7) return `${diffDays}d`
  return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

function sortByActivity(a: Session, b: Session): number {
  return new Date(b.last_activity).getTime() - new Date(a.last_activity).getTime()
}

function isTaskSession(s: Session): boolean {
  return !!s.context_type && TASK_CONTEXT_TYPES.has(s.context_type)
}

function isConversationSession(s: Session): boolean {
  return !s.context_type || s.context_type === 'chat' || s.context_type === ''
}

export function LeftSidebar() {
  const open = useLayoutStore((s) => s.leftSidebarOpen)
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId)
  const setActiveSession = useAppStore((s) => s.setActiveSession)
  const queryClient = useQueryClient()
  const activeStreams = useChatStore((s) => s.activeStreams)
  const pendingTools = useChatStore((s) => s.pendingTools)
  const cliActiveSessions = useChatStore((s) => s.cliActiveSessions)

  const { data: userSettings } = useSettings()
  const { data: models } = useModels()
  const { data: providers } = useProviders()
  const { data: agents } = useQuery({
    queryKey: ['agents'],
    queryFn: api.listAgents,
    staleTime: 5 * 60 * 1000,
  })

  const [showCreateForm, setShowCreateForm] = useState(false)

  const [tasksCollapsed, setTasksCollapsed] = useState(() => {
    try {
      return localStorage.getItem(TASKS_COLLAPSED_KEY) !== 'false'
    } catch {
      return true
    }
  })

  useEffect(() => {
    try {
      localStorage.setItem(TASKS_COLLAPSED_KEY, String(tasksCollapsed))
    } catch {
      // ignore
    }
  }, [tasksCollapsed])

  const { data: sessions = [], isLoading } = useQuery({
    queryKey: ['sessions', activeWorkspaceId],
    queryFn: () => api.listSessions(activeWorkspaceId ?? undefined),
    enabled: !!activeWorkspaceId,
  })

  const createWithDefaults = useCallback(() => {
    return api.createSession({
      workspace_id: activeWorkspaceId!,
      provider: userSettings?.default_provider || undefined,
      model: userSettings?.default_model || undefined,
      agent_id: userSettings?.default_agent || undefined,
    })
  }, [activeWorkspaceId, userSettings])

  const createMutation = useMutation({
    mutationFn: (overrides?: { provider?: string; model?: string; agent_id?: string }) =>
      overrides
        ? api.createSession({ workspace_id: activeWorkspaceId!, ...overrides })
        : createWithDefaults(),
    onSuccess: (newSession) => {
      void queryClient.invalidateQueries({ queryKey: ['sessions'] })
      setActiveSession(newSession.id)
      setShowCreateForm(false)
    },
    onError: (err) => {
      console.error('Failed to create session:', err)
    },
  })

  const pinMutation = useMutation({
    mutationFn: ({ id, pinned }: { id: string; pinned: boolean }) => api.pinSession(id, pinned),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['sessions'] })
    },
    onError: (err) => {
      console.error('Failed to toggle pin:', err)
    },
  })

  // Check if ANY session has a non-null, non-empty, non-chat context_type.
  // If all are null/empty/chat, we degrade to flat list (no zone headers).
  const hasAnyContextType = useMemo(
    () => sessions.some((s: Session) => s.context_type && s.context_type !== '' && s.context_type !== 'chat'),
    [sessions],
  )

  // Zone grouping — pinned sessions are excluded from Conversations/Tasks
  const pinned = useMemo(
    () => sessions.filter((s: Session) => s.is_pinned).sort(sortByActivity),
    [sessions],
  )

  const unpinned = useMemo(
    () => sessions.filter((s: Session) => !s.is_pinned),
    [sessions],
  )

  const conversations = useMemo(
    () => unpinned.filter(isConversationSession).sort(sortByActivity),
    [unpinned],
  )

  const tasks = useMemo(
    () => unpinned.filter(isTaskSession).sort(sortByActivity),
    [unpinned],
  )

  // Flat mode: no context_type diversity — show pinned + recent like before
  const useFlatMode = !hasAnyContextType

  return (
    <aside
      className={`h-full bg-zinc-950 border-r border-zinc-800 flex flex-col transition-all duration-200 ease-in-out overflow-hidden ${
        open ? 'w-68' : 'w-0'
      }`}
    >
      <div className="min-w-68 flex flex-col h-full">
        {/* Header */}
        <div className="border-b border-zinc-800 shrink-0">
          <div className="flex items-center justify-between px-4 py-3">
            <h2 className="text-sm font-semibold text-zinc-100">Sessions</h2>
            <div className="flex items-center">
              <Button
                variant="ghost"
                size="icon"
                className="w-7 h-7 text-zinc-400 hover:text-zinc-100"
                onClick={() => createMutation.mutate(undefined)}
                disabled={!activeWorkspaceId || createMutation.isPending}
                title="New session with defaults"
              >
                {createMutation.isPending ? (
                  <Loader2 className="w-4 h-4 animate-spin" />
                ) : (
                  <Plus className="w-4 h-4" />
                )}
              </Button>
              <Button
                variant="ghost"
                size="icon"
                className="w-5 h-7 text-zinc-500 hover:text-zinc-300"
                onClick={() => setShowCreateForm((o) => !o)}
                title="Session options"
              >
                <ChevronDown className={`w-3 h-3 transition-transform duration-150 ${showCreateForm ? 'rotate-180' : ''}`} />
              </Button>
            </div>
          </div>

          {showCreateForm && (
            <NewSessionForm
              defaults={userSettings}
              models={models ?? []}
              providers={providers ?? []}
              agents={agents ?? []}
              isPending={createMutation.isPending}
              onCreate={(overrides) => createMutation.mutate(overrides)}
            />
          )}
        </div>

        {/* Session list */}
        <ScrollArea className="flex-1 min-h-0">
          <div className="px-2 py-2">
            {isLoading && (
              <div className="flex items-center justify-center py-8">
                <Loader2 className="w-5 h-5 text-zinc-500 animate-spin" />
              </div>
            )}

            {!isLoading && sessions.length === 0 && (
              <div className="px-2 py-8 text-center">
                <p className="text-xs text-zinc-500">No sessions yet</p>
                <p className="text-xs text-zinc-600 mt-1">Click + to start a chat</p>
              </div>
            )}

            {/* Zone 1: Pinned */}
            {pinned.length > 0 && (
              <>
                <ZoneHeader icon={<Pin className="w-3 h-3" />} label="Pinned" />
                {pinned.map((session: Session) => (
                  <SessionItem
                    key={session.id}
                    session={session}
                    isActive={session.id === activeSessionId}
                    onClick={() => setActiveSession(session.id)}
                    onTogglePin={() => pinMutation.mutate({ id: session.id, pinned: !session.is_pinned })}
                    statusIndicator={getPresenceIndicator(session.id, activeStreams, pendingTools, cliActiveSessions)}
                  />
                ))}
              </>
            )}

            {useFlatMode ? (
              /* Flat mode — no zone headers for unpinned, same as legacy */
              <>
                {unpinned.length > 0 && (
                  <>
                    {pinned.length > 0 && <ZoneHeader label="Recent" />}
                    {unpinned.sort(sortByActivity).map((session: Session) => (
                      <SessionItem
                        key={session.id}
                        session={session}
                        isActive={session.id === activeSessionId}
                        onClick={() => setActiveSession(session.id)}
                        onTogglePin={() => pinMutation.mutate({ id: session.id, pinned: !session.is_pinned })}
                        statusIndicator={getPresenceIndicator(session.id, activeStreams, pendingTools, cliActiveSessions)}
                      />
                    ))}
                  </>
                )}
              </>
            ) : (
              /* Zoned mode */
              <>
                {/* Zone 2: Conversations */}
                {conversations.length > 0 && (
                  <>
                    <ZoneHeader label="Conversations" />
                    {conversations.map((session: Session) => (
                      <SessionItem
                        key={session.id}
                        session={session}
                        isActive={session.id === activeSessionId}
                        onClick={() => setActiveSession(session.id)}
                        onTogglePin={() => pinMutation.mutate({ id: session.id, pinned: !session.is_pinned })}
                        statusIndicator={getPresenceIndicator(session.id, activeStreams, pendingTools, cliActiveSessions)}
                      />
                    ))}
                  </>
                )}

                {/* Zone 3: Tasks (collapsible) */}
                {tasks.length > 0 && (
                  <>
                    <button
                      onClick={() => setTasksCollapsed((c) => !c)}
                      className="w-full flex items-center gap-1 px-2 pt-3 pb-1 text-xs font-medium text-zinc-500 uppercase tracking-wider hover:text-zinc-400 transition-colors"
                    >
                      <ChevronRight
                        className={`w-3 h-3 transition-transform duration-150 ${
                          tasksCollapsed ? '' : 'rotate-90'
                        }`}
                      />
                      <span>Tasks</span>
                      <span className="ml-auto text-[10px] bg-zinc-800 text-zinc-400 px-1.5 py-0.5 rounded-full tabular-nums leading-none">
                        {tasks.length}
                      </span>
                    </button>
                    {!tasksCollapsed &&
                      tasks.map((session: Session) => (
                        <SessionItem
                          key={session.id}
                          session={session}
                          isActive={session.id === activeSessionId}
                          onClick={() => setActiveSession(session.id)}
                          onTogglePin={() => pinMutation.mutate({ id: session.id, pinned: !session.is_pinned })}
                          statusIndicator={getPresenceIndicator(session.id, activeStreams, pendingTools, cliActiveSessions)}
                        />
                      ))}
                  </>
                )}
              </>
            )}
          </div>
        </ScrollArea>
      </div>
    </aside>
  )
}

function PresenceDot({ variant }: { variant: 'streaming' | 'tool-pending' | 'cli-active' }) {
  if (variant === 'streaming') {
    return (
      <span
        className="w-2 h-2 rounded-full bg-emerald-500 shrink-0 animate-pulse"
        title="Streaming"
      />
    )
  }
  if (variant === 'cli-active') {
    return (
      <span
        className="w-2 h-2 rounded-full bg-cyan-500 shrink-0 animate-pulse"
        title="CLI active"
      />
    )
  }
  return (
    <span
      className="w-2 h-2 rounded-full bg-amber-500 shrink-0"
      title="Tool approval pending"
    />
  )
}

function getPresenceIndicator(
  sessionId: string,
  activeStreams: Map<string, unknown>,
  pendingTools: Map<string, unknown>,
  cliActiveSessions: Map<string, unknown>,
): ReactNode | undefined {
  if (pendingTools.has(sessionId)) {
    return <PresenceDot variant="tool-pending" />
  }
  if (activeStreams.has(sessionId)) {
    return <PresenceDot variant="streaming" />
  }
  if (cliActiveSessions.has(sessionId)) {
    return <PresenceDot variant="cli-active" />
  }
  return undefined
}

function ZoneHeader({ label, icon }: { label: string; icon?: ReactNode }) {
  return (
    <div className="px-2 pt-3 pb-1 text-xs font-medium text-zinc-500 uppercase tracking-wider flex items-center gap-1">
      {icon}
      {label}
    </div>
  )
}

function SessionItem({
  session,
  isActive,
  onClick,
  onTogglePin,
  statusIndicator,
}: {
  session: Session
  isActive: boolean
  onClick: () => void
  onTogglePin: () => void
  statusIndicator?: ReactNode
}) {
  const [hovered, setHovered] = useState(false)
  const displayTitle = session.custom_name || session.title || `#${session.short_code}`

  // Parse tags from JSON string.
  const tags: string[] = useMemo(() => {
    try {
      const parsed = JSON.parse(session.tags || '[]')
      return Array.isArray(parsed) ? parsed : []
    } catch {
      return []
    }
  }, [session.tags])

  return (
    <button
      onClick={onClick}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      className={`w-full flex flex-col gap-1 px-2 py-2 rounded-md text-sm text-left transition-colors group ${
        isActive
          ? 'bg-zinc-800/60 text-zinc-100'
          : 'text-zinc-400 hover:bg-zinc-800/40 hover:text-zinc-200'
      }`}
    >
      <div className="flex items-center gap-2 w-full">
        {statusIndicator}
        <Hash className="w-3.5 h-3.5 shrink-0 opacity-50" />
        <span className="truncate flex-1">{displayTitle}</span>
        {session.provider && <AdapterBadge provider={session.provider} size="sm" />}
        <div className="flex items-center gap-2 shrink-0">
          {hovered && (
            <span
              role="button"
              onClick={(e) => {
                e.stopPropagation()
                onTogglePin()
              }}
              className="p-0.5 rounded text-zinc-500 hover:text-zinc-200 hover:bg-zinc-700 transition-colors"
              aria-label={session.is_pinned ? 'Unpin session' : 'Pin session'}
            >
              {session.is_pinned ? (
                <PinOff className="w-3 h-3" />
              ) : (
                <Pin className="w-3 h-3" />
              )}
            </span>
          )}
          {!hovered && session.is_pinned && (
            <Pin className="w-3 h-3 text-zinc-600" />
          )}
          {session.message_count > 0 && (
            <span className="text-xs text-zinc-600 tabular-nums">{session.message_count}</span>
          )}
          <span className="text-xs text-zinc-600">
            {formatRelativeTime(session.last_activity)}
          </span>
        </div>
      </div>
      {tags.length > 0 && (
        <div className="flex gap-1 flex-wrap pl-5">
          {tags.slice(0, 3).map((tag) => (
            <span
              key={tag}
              className="text-[10px] px-1.5 py-0 rounded-full bg-zinc-800 text-zinc-500 leading-relaxed"
            >
              {tag}
            </span>
          ))}
        </div>
      )}
    </button>
  )
}
