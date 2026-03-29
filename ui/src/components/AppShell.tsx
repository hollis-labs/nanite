import { useCallback, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { NavRail } from './NavRail'
import { LeftSidebar } from './sidebar/LeftSidebar'
import { ChatMain } from './chat/ChatMain'
import { RightRail } from './RightRail'
import { useKeyboardShortcuts } from '@/hooks/useKeyboardShortcuts'
import { useAppStore } from '@/stores/useAppStore'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { api } from '@/lib/api'
import SettingsPage from './settings/SettingsPage'
import { SprintPlanningModal } from './plugins/sprint/SprintPlanningModal'
import { useSprintPlanningStore } from './plugins/sprint/useSprintPlanningStore'
import { useToolRefresh } from '@/hooks/useToolRefresh'
import { usePresence } from '@/hooks/usePresence'
import { useHashRoute } from '@/hooks/useHashRoute'

export function AppShell() {
  const focusRef = useRef<(() => void) | null>(null)
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId)
  const currentPage = useLayoutStore((s) => s.currentPage)
  const sprintOpen = useSprintPlanningStore((s) => s.isOpen)
  const sprintProjectId = useSprintPlanningStore((s) => s.projectId)
  const closeSprintPlanning = useSprintPlanningStore((s) => s.closeSprintPlanning)

  // Global tool refresh on session switch — runs even when ToolDashboard isn't mounted
  useToolRefresh()

  // Global presence SSE — one connection per browser tab
  usePresence()

  // Sync navigation state with URL hash
  useHashRoute()

  const { data: sessions = [] } = useQuery({
    queryKey: ['sessions', activeWorkspaceId],
    queryFn: () => api.listSessions(activeWorkspaceId ?? undefined),
    enabled: !!activeWorkspaceId,
  })

  // Get the active session's primary agent for inbox panel
  const activeSessionId = useAppStore((s) => s.activeSessionId)

  const { data: sessionAgents = [] } = useQuery({
    queryKey: ['session-agents', activeSessionId],
    queryFn: () => api.listSessionAgents(activeSessionId!),
    enabled: !!activeSessionId,
  })

  const primarySessionAgent = sessionAgents.find(
    (a) => (a as any).is_primary === true || (a as any).is_primary === 1 || a.role === 'primary',
  )
  const inboxAgentId = primarySessionAgent?.agent_id || 'mentat-001'

  const focusComposer = useCallback(() => {
    focusRef.current?.()
  }, [])

  const handleEditorReady = useCallback((focus: () => void) => {
    focusRef.current = focus
  }, [])

  useKeyboardShortcuts({
    focusComposer,
    sessions: (sessions ?? []).map((s) => ({ id: s.id })),
  })

  return (
    <div className="flex h-screen w-screen overflow-hidden bg-bg">
      <NavRail />
      {currentPage === 'chat' && <LeftSidebar />}
      {currentPage === 'chat' ? (
        <ChatMain onEditorReady={handleEditorReady} />
      ) : currentPage === 'settings' ? (
        <SettingsPage />
      ) : null}
      {currentPage === 'chat' && <RightRail inboxAgentId={inboxAgentId} />}
      {sprintOpen && (
        <SprintPlanningModal
          projectId={sprintProjectId}
          onClose={closeSprintPlanning}
        />
      )}
    </div>
  )
}
