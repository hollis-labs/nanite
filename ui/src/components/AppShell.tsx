import { Activity, Suspense, useState, useCallback, useRef, useEffect } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Dialog, DialogContent } from '@/components/ui/dialog'
import { NavRail } from './NavRail'
import { CommandPalette } from './CommandPalette'
import { SearchModal } from './SearchModal'
import { LeftSidebar } from './sidebar/LeftSidebar'
import { ChatMain } from './chat/ChatMain'
import { RightRailV2 } from './RightRailV2'
import { useKeyboardShortcuts } from '@/hooks/useKeyboardShortcuts'
import { usePluginSlots } from '@/hooks/usePluginSlots'
import { getSlotComponent } from '@/lib/plugin-slot-lookup'
import { useAppStore } from '@/stores/useAppStore'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { api } from '@/lib/api'
import SettingsPage from './settings/SettingsPage'
import { MemoryModal } from './memory/MemoryModal'
import { useToolRefresh } from '@/hooks/useToolRefresh'
import { usePresence } from '@/hooks/usePresence'
import { useHashRoute } from '@/hooks/useHashRoute'
import { usePluginRegistry } from '@/hooks/usePluginRegistry'
import { usePluginEvents } from '@/hooks/usePluginEvents'
import { installPluginDevHelpers } from '@/lib/plugin-loader'

// J.3: dev-only window helpers (__nanite_reloadPlugin, __nanite_pluginRegistry).
// No-op in prod builds.
installPluginDevHelpers()

export function AppShell() {
  const [commandPaletteOpen, setCommandPaletteOpen] = useState(false)
  const [searchOpen, setSearchOpen] = useState(false)
  const [pluginModal, setPluginModal] = useState<{ component: string; props?: Record<string, unknown> } | null>(null)
  const focusRef = useRef<(() => void) | null>(null)
  const queryClient = useQueryClient()
  const currentPage = useLayoutStore((s) => s.currentPage)
  const [visitedPages, setVisitedPages] = useState(() => new Set(['chat', currentPage]))
  if (!visitedPages.has(currentPage)) {
    setVisitedPages(new Set([...visitedPages, currentPage]))
  }

  // Listen for plugin-modal events
  useEffect(() => {
    function handlePluginModal(e: Event) {
      const detail = (e as CustomEvent).detail
      if (detail?.component) {
        // Generic plugin modal — render via slot component registry
        setPluginModal({ component: detail.component, props: detail.props })
      }
    }
    // Also listen for plugin-action events with handler type (backward compat)
    function handlePluginAction(_e: Event) {
      // reserved for future plugin-action handling
    }
    function handleOpenSearch() {
      setSearchOpen(true)
    }
    window.addEventListener('plugin-modal', handlePluginModal)
    window.addEventListener('plugin-action', handlePluginAction)
    window.addEventListener('open-search', handleOpenSearch)
    return () => {
      window.removeEventListener('plugin-modal', handlePluginModal)
      window.removeEventListener('plugin-action', handlePluginAction)
      window.removeEventListener('open-search', handleOpenSearch)
    }
  }, [])

  // Global tool refresh on session switch — runs even when ToolDashboard isn't mounted
  useToolRefresh()

  // Global presence SSE — one connection per browser tab
  usePresence()

  // Sync navigation state with URL hash
  useHashRoute()

  // Plugin registry: fetch + reconcile into dynamic registry, and invalidate
  // the query whenever a plugin lifecycle event fires.
  usePluginRegistry()
  usePluginEvents()

  const { data: sessions = [] } = useQuery({
    queryKey: ['sessions'],
    queryFn: () => api.listSessions(),
  })

  // Get the active session's primary agent for inbox panel
  const activeSessionId = useAppStore((s) => s.activeSessionId)

  const { data: sessionAgents = [] } = useQuery({
    queryKey: ['session-agents', activeSessionId],
    queryFn: () => api.listSessionAgents(activeSessionId!),
    enabled: !!activeSessionId,
  })

  const primarySessionAgent = sessionAgents.find((a) => a.role === 'primary')
  const inboxAgentId = primarySessionAgent?.agent_id || 'file-default'

  const focusComposer = useCallback(() => {
    useLayoutStore.getState().setCurrentPage('chat')
    requestAnimationFrame(() => focusRef.current?.())
  }, [])

  const handleEditorReady = useCallback((focus: () => void) => {
    focusRef.current = focus
  }, [])

  const handleNewSessionFromPalette = useCallback(async () => {
    try {
      const newSession = await api.createSession({})
      void queryClient.invalidateQueries({ queryKey: ['sessions'] })
      useAppStore.getState().setActiveSession(newSession.id)
      useLayoutStore.getState().setCurrentPage('chat')
      window.location.hash = '#chat'
    } catch {
      // handled by UI
    }
  }, [queryClient])

  useKeyboardShortcuts({
    focusComposer,
    sessions: (sessions ?? []).map((s) => ({ id: s.id })),
    openCommandPalette: () => setCommandPaletteOpen(true),
    openSearch: () => setSearchOpen(true),
  })

  const pluginNavItems = usePluginSlots('nav-rail')

  const renderPluginPage = (page: string) => {
    const entry = pluginNavItems.find((e) => e.id === page)
    if (!entry?.component) return null
    const PluginComponent = getSlotComponent(entry.component)
    if (!PluginComponent) return null
    return (
      <Suspense fallback={<div className="flex-1 flex items-center justify-center text-fg-muted text-sm">Loading...</div>}>
        <PluginComponent {...(entry.props ?? {})} />
      </Suspense>
    )
  }


  return (
    <div className="flex h-screen w-screen overflow-hidden bg-bg">
      {currentPage !== 'chat' && <NavRail />}
      <Activity mode={currentPage === 'chat' ? 'visible' : 'hidden'}>
        <LeftSidebar />
      </Activity>
      {/* The chat controller stays active; only its view effects are paused. */}
      <ChatMain active={currentPage === 'chat'} onEditorReady={handleEditorReady} />
      {visitedPages.has('settings') && (
        <Activity mode={currentPage === 'settings' ? 'visible' : 'hidden'}>
          <SettingsPage />
        </Activity>
      )}
      {pluginNavItems.filter((entry) => visitedPages.has(entry.id)).map((entry) => (
        <Activity key={entry.id} mode={currentPage === entry.id ? 'visible' : 'hidden'}>
          {renderPluginPage(entry.id)}
        </Activity>
      ))}
      <Activity mode={currentPage === 'chat' ? 'visible' : 'hidden'}>
        <RightRailV2 inboxAgentId={inboxAgentId} />
      </Activity>
      <MemoryModal />
      <CommandPalette
        open={commandPaletteOpen}
        onOpenChange={setCommandPaletteOpen}
        onNewSession={handleNewSessionFromPalette}
      />
      <SearchModal
        open={searchOpen}
        onOpenChange={setSearchOpen}
      />
      {/* modal slot — generic plugin modals */}
      {pluginModal && (() => {
        const PluginModalComponent = getSlotComponent(pluginModal.component)
        if (!PluginModalComponent) return null
        return (
          <Dialog open onOpenChange={(open) => { if (!open) setPluginModal(null) }}>
            <DialogContent className="sm:max-w-lg">
              <Suspense fallback={<div className="flex items-center justify-center py-8 text-fg-muted text-sm">Loading...</div>}>
                <PluginModalComponent {...(pluginModal.props ?? {})} onClose={() => setPluginModal(null)} />
              </Suspense>
            </DialogContent>
          </Dialog>
        )
      })()}
    </div>
  )
}
