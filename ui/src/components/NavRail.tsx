import { MessageSquare, Search, Plus, Settings, User, ChevronDown, Loader2, Sun, Moon } from 'lucide-react'
import { useState, useRef, useEffect, useCallback } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Tooltip } from '@/components/ui/tooltip'
import { useAppStore } from '@/stores/useAppStore'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useSettings } from '@/hooks/useSettings'
import { usePluginSlots } from '@/hooks/usePluginSlots'
import { useNavigationStore } from '@/stores/useNavigationStore'
import { updateSettingsHash } from '@/hooks/useHashRoute'
import { resolveIcon } from '@/lib/icons'
import { api } from '@/lib/api'
import type { Workspace } from '@/lib/types'

const CORE_NAV_ITEMS = [
  { icon: MessageSquare, label: 'Chat', id: 'chat' },
  { icon: Search, label: 'Search', id: 'search' },
  { icon: Plus, label: 'New Chat', id: 'new' },
  { icon: Settings, label: 'Settings', id: 'settings' },
] as const

export function NavRail() {
  const [activeItem, setActiveItem] = useState<string>('chat')
  const [workspaceDropdownOpen, setWorkspaceDropdownOpen] = useState(false)
  const dropdownRef = useRef<HTMLDivElement>(null)
  const pluginNavItems = usePluginSlots('nav-rail')

  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId)
  const setActiveWorkspace = useAppStore((s) => s.setActiveWorkspace)
  const setActiveSession = useAppStore((s) => s.setActiveSession)
  const currentPage = useLayoutStore((s) => s.currentPage)
  const setCurrentPage = useLayoutStore((s) => s.setCurrentPage)
  const theme = useLayoutStore((s) => s.theme)
  const toggleTheme = useLayoutStore((s) => s.toggleTheme)
  const queryClient = useQueryClient()
  const navPush = useNavigationStore((s) => s.push)
  const { data: userSettings } = useSettings()

  const createSessionMutation = useMutation({
    mutationFn: () => api.createSession({
      workspace_id: activeWorkspaceId!,
      provider: userSettings?.default_provider || undefined,
      model: userSettings?.default_model || undefined,
      agent_id: userSettings?.default_agent || undefined,
    }),
    onSuccess: (newSession) => {
      void queryClient.invalidateQueries({ queryKey: ['sessions'] })
      setActiveSession(newSession.id)
    },
  })

  const { data: workspaces = [], isLoading: loadingWorkspaces } = useQuery({
    queryKey: ['workspaces'],
    queryFn: api.listWorkspaces,
  })

  // Set default workspace on load
  useEffect(() => {
    if (!activeWorkspaceId && workspaces.length > 0) {
      setActiveWorkspace(workspaces[0].id)
    }
  }, [activeWorkspaceId, workspaces, setActiveWorkspace])

  // Close dropdown on outside click
  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (dropdownRef.current && !dropdownRef.current.contains(e.target as Node)) {
        setWorkspaceDropdownOpen(false)
      }
    }
    if (!workspaceDropdownOpen) return
    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [workspaceDropdownOpen])

  const activeWorkspace = workspaces.find((w: Workspace) => w.id === activeWorkspaceId)

  const handleCreateWorkspace = useCallback(async () => {
    const name = window.prompt('Workspace name:')
    if (!name?.trim()) return
    try {
      const ws = await api.createWorkspace({ name: name.trim() })
      void queryClient.invalidateQueries({ queryKey: ['workspaces'] })
      setActiveWorkspace(ws.id)
    } catch (err) {
      console.error('Failed to create workspace:', err)
    }
  }, [queryClient, setActiveWorkspace])

  const handleSelectWorkspace = useCallback((id: string) => {
    setActiveWorkspace(id)
    setWorkspaceDropdownOpen(false)
  }, [setActiveWorkspace])

  const workspaceInitial = activeWorkspace
    ? (activeWorkspace.icon || activeWorkspace.name.charAt(0).toUpperCase())
    : '?'

  return (
    <nav className="flex flex-col items-center w-16 h-full bg-bg-elevated border-r border-border py-3 shrink-0">
      {/* Workspace selector */}
      <div className="relative mb-3" ref={dropdownRef}>
        <Tooltip content={activeWorkspace?.name || 'Select workspace'} side="right">
          <button
            onClick={() => setWorkspaceDropdownOpen((o) => !o)}
            className={`w-10 h-10 rounded-lg flex items-center justify-center text-sm font-semibold transition-colors relative group ${
              workspaceDropdownOpen
                ? 'bg-surface-hover text-fg ring-2 ring-primary/40'
                : 'bg-surface hover:bg-surface-hover text-fg'
            }`}
          >
            {loadingWorkspaces ? (
              <Loader2 className="w-4 h-4 animate-spin text-fg-secondary" />
            ) : (
              <span>{workspaceInitial}</span>
            )}
            {/* Overlay chevron — visible on hover or when open */}
            <span className={`absolute inset-x-0 -bottom-1 flex justify-center transition-opacity ${
              workspaceDropdownOpen ? 'opacity-100' : 'opacity-0 group-hover:opacity-100'
            }`}>
              <ChevronDown className="w-3 h-3 text-fg-muted bg-bg-elevated rounded-full" />
            </span>
          </button>
        </Tooltip>

        {/* Dropdown */}
        {workspaceDropdownOpen && (
          <div className="absolute left-full top-0 ml-2 w-52 bg-bg-elevated border border-border-subtle rounded-lg shadow-xl z-50 py-1">
            <div className="px-3 py-1.5 text-xs font-medium text-fg-muted uppercase tracking-wider">
              Workspaces
            </div>
            {workspaces.map((w: Workspace) => (
              <button
                key={w.id}
                onClick={() => handleSelectWorkspace(w.id)}
                className={`w-full text-left px-3 py-2 text-sm flex items-center gap-2 transition-colors ${
                  w.id === activeWorkspaceId
                    ? 'bg-surface/60 text-fg'
                    : 'text-fg-secondary hover:bg-surface/40 hover:text-fg'
                }`}
              >
                <span className="w-6 h-6 rounded bg-surface-hover flex items-center justify-center text-xs font-medium shrink-0">
                  {w.icon || w.name.charAt(0).toUpperCase()}
                </span>
                <span className="truncate">{w.name}</span>
              </button>
            ))}
            <div className="my-1 border-t border-border" />
            <button
              onClick={() => { setWorkspaceDropdownOpen(false); void handleCreateWorkspace() }}
              className="w-full text-left px-3 py-2 text-sm flex items-center gap-2 text-fg-muted hover:text-fg-secondary hover:bg-surface/40 transition-colors"
            >
              <span className="w-6 h-6 rounded border border-dashed border-border-subtle flex items-center justify-center shrink-0">
                <Plus className="w-3 h-3" />
              </span>
              <span>Add Workspace</span>
            </button>
          </div>
        )}
      </div>

      <div className="w-8 border-t border-border-subtle mb-2" />

      <div className="flex flex-col items-center gap-1 flex-1">
        {/* Core nav items */}
        {CORE_NAV_ITEMS.map(({ icon: Icon, label, id }) => {
          const isActive = id === 'settings' ? currentPage === 'settings' :
                          id === 'chat' ? currentPage === 'chat' :
                          activeItem === id
          return (
            <Tooltip key={id} content={label} side="right">
              <Button
                variant="ghost"
                size="icon"
                className={`w-10 h-10 rounded-lg ${
                  isActive
                    ? 'bg-surface text-primary'
                    : 'text-fg-secondary hover:text-fg'
                }`}
                onClick={() => {
                  if (id === 'new') {
                    if (activeWorkspaceId) createSessionMutation.mutate()
                  } else if (id === 'search') {
                    window.dispatchEvent(new CustomEvent('open-search'))
                  } else if (id === 'settings') {
                    setCurrentPage('settings')
                    setActiveItem(id)
                  } else if (id === 'chat') {
                    setCurrentPage('chat')
                    setActiveItem(id)
                  } else {
                    setActiveItem(id)
                  }
                }}
              >
                <Icon className="w-5 h-5" />
              </Button>
            </Tooltip>
          )
        })}

        {/* Plugin-registered nav items */}
        {pluginNavItems.map((entry) => {
          const PluginIcon = resolveIcon(entry.icon)
          const isActive = currentPage === entry.id
          return (
            <Tooltip key={entry.id} content={entry.label} side="right">
              <Button
                variant="ghost"
                size="icon"
                className={`w-10 h-10 rounded-lg ${
                  isActive
                    ? 'bg-surface text-primary'
                    : 'text-fg-secondary hover:text-fg'
                }`}
                onClick={() => {
                  setCurrentPage(entry.id as any)
                  setActiveItem(entry.id)
                }}
              >
                <PluginIcon className="w-5 h-5" />
              </Button>
            </Tooltip>
          )
        })}
      </div>
      <div className="mt-auto flex flex-col items-center gap-1">
        <Tooltip content={theme === 'dark' || (theme === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches) ? 'Light mode' : 'Dark mode'} side="right">
          <Button
            variant="ghost"
            size="icon"
            className="w-10 h-10 rounded-lg text-fg-secondary hover:text-fg"
            onClick={toggleTheme}
          >
            {theme === 'dark' || (theme === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches) ? <Sun className="w-5 h-5" /> : <Moon className="w-5 h-5" />}
          </Button>
        </Tooltip>
        <Tooltip content="Profile" side="right">
          <Button
            variant="ghost"
            size="icon"
            className={`w-10 h-10 rounded-lg ${
              currentPage === 'settings' ? 'text-fg-secondary hover:text-fg' : 'text-fg-secondary hover:text-fg'
            }`}
            onClick={() => {
              setCurrentPage('settings')
              updateSettingsHash('profile' as never)
              navPush({ view: 'settings/profile', label: 'Profile' })
            }}
          >
            {userSettings?.ext_settings?.avatar_url ? (
              <img
                src={userSettings.ext_settings.avatar_url as string}
                alt="Profile"
                className="size-6 rounded-md object-cover"
              />
            ) : (
              <User className="w-5 h-5" />
            )}
          </Button>
        </Tooltip>
      </div>
    </nav>
  )
}
