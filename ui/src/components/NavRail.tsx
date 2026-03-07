import { MessageSquare, Search, Plus, Settings, User, ChevronDown, Loader2, Workflow } from 'lucide-react'
import { useState, useRef, useEffect, useCallback } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '@/components/ui/Button'
import { Tooltip } from '@/components/ui/Tooltip'
import { useAppStore } from '@/stores/useAppStore'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { api } from '@/lib/api'
import type { Workspace } from '@/lib/types'

const navItems = [
  { icon: MessageSquare, label: 'Chat', id: 'chat' },
  { icon: Search, label: 'Search', id: 'search' },
  { icon: Workflow, label: 'Workflows', id: 'workflows' },
  { icon: Plus, label: 'New Chat', id: 'new' },
  { icon: Settings, label: 'Settings', id: 'settings' },
] as const

export function NavRail() {
  const [activeItem, setActiveItem] = useState<string>('chat')
  const [workspaceDropdownOpen, setWorkspaceDropdownOpen] = useState(false)
  const dropdownRef = useRef<HTMLDivElement>(null)

  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId)
  const setActiveWorkspace = useAppStore((s) => s.setActiveWorkspace)
  const setActiveSession = useAppStore((s) => s.setActiveSession)
  const toggleWorkflowPanel = useLayoutStore((s) => s.toggleWorkflowPanel)
  const workflowPanelOpen = useLayoutStore((s) => s.workflowPanelOpen)
  const setLeftSidebar = useLayoutStore((s) => s.setLeftSidebar)
  const queryClient = useQueryClient()

  const createSessionMutation = useMutation({
    mutationFn: () => api.createSession({ workspace_id: activeWorkspaceId! }),
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
    if (workspaceDropdownOpen) {
      document.addEventListener('mousedown', handleClickOutside)
      return () => document.removeEventListener('mousedown', handleClickOutside)
    }
  }, [workspaceDropdownOpen])

  const activeWorkspace = workspaces.find((w: Workspace) => w.id === activeWorkspaceId)

  const handleSelectWorkspace = useCallback((id: string) => {
    setActiveWorkspace(id)
    setWorkspaceDropdownOpen(false)
  }, [setActiveWorkspace])

  const workspaceInitial = activeWorkspace
    ? (activeWorkspace.icon || activeWorkspace.name.charAt(0).toUpperCase())
    : '?'

  return (
    <nav className="flex flex-col items-center w-16 h-full bg-zinc-900 border-r border-zinc-800 py-3 shrink-0">
      {/* Workspace selector */}
      <div className="relative mb-3" ref={dropdownRef}>
        <Tooltip content={activeWorkspace?.name || 'Select workspace'} side="right">
          <button
            onClick={() => setWorkspaceDropdownOpen((o) => !o)}
            className="w-10 h-10 rounded-lg bg-zinc-800 hover:bg-zinc-700 flex items-center justify-center text-sm font-semibold text-zinc-200 transition-colors relative"
          >
            {loadingWorkspaces ? (
              <Loader2 className="w-4 h-4 animate-spin text-zinc-400" />
            ) : (
              <span>{workspaceInitial}</span>
            )}
            <ChevronDown className="w-3 h-3 text-zinc-500 absolute -bottom-0.5 -right-0.5" />
          </button>
        </Tooltip>

        {/* Dropdown */}
        {workspaceDropdownOpen && workspaces.length > 0 && (
          <div className="absolute left-full top-0 ml-2 w-48 bg-zinc-900 border border-zinc-700 rounded-lg shadow-xl z-50 py-1">
            <div className="px-3 py-1.5 text-xs font-medium text-zinc-500 uppercase tracking-wider">
              Workspaces
            </div>
            {workspaces.map((w: Workspace) => (
              <button
                key={w.id}
                onClick={() => handleSelectWorkspace(w.id)}
                className={`w-full text-left px-3 py-2 text-sm flex items-center gap-2 transition-colors ${
                  w.id === activeWorkspaceId
                    ? 'bg-zinc-800 text-zinc-100'
                    : 'text-zinc-400 hover:bg-zinc-800/60 hover:text-zinc-200'
                }`}
              >
                <span className="w-6 h-6 rounded bg-zinc-700 flex items-center justify-center text-xs font-medium shrink-0">
                  {w.icon || w.name.charAt(0).toUpperCase()}
                </span>
                <span className="truncate">{w.name}</span>
              </button>
            ))}
          </div>
        )}
      </div>

      <div className="w-8 border-t border-zinc-700 mb-2" />

      <div className="flex flex-col items-center gap-1 flex-1">
        {navItems.map(({ icon: Icon, label, id }) => {
          const isActive = id === 'workflows' ? workflowPanelOpen : activeItem === id
          return (
            <Tooltip key={id} content={label} side="right">
              <Button
                variant="ghost"
                size="icon"
                className={`w-10 h-10 rounded-lg ${
                  isActive
                    ? 'bg-zinc-800 text-indigo-400'
                    : 'text-zinc-400 hover:text-zinc-100'
                }`}
                onClick={() => {
                  if (id === 'workflows') {
                    toggleWorkflowPanel()
                  } else if (id === 'new') {
                    if (activeWorkspaceId) createSessionMutation.mutate()
                  } else if (id === 'search') {
                    setLeftSidebar(true)
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
      </div>
      <div className="mt-auto">
        <Tooltip content="Account" side="right">
          <Button
            variant="ghost"
            size="icon"
            className="w-10 h-10 rounded-lg text-zinc-400 hover:text-zinc-100"
          >
            <User className="w-5 h-5" />
          </Button>
        </Tooltip>
      </div>
    </nav>
  )
}
