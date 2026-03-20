import { useCallback, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { NavRail } from './NavRail'
import { LeftSidebar } from './sidebar/LeftSidebar'
import { ChatMain } from './chat/ChatMain'
import { RightRail } from './RightRail'
import { ArtifactsDrawer } from './drawers/ArtifactsDrawer'
import { WorkflowPanel } from './workflows/WorkflowPanel'
import { useKeyboardShortcuts } from '@/hooks/useKeyboardShortcuts'
import { useAppStore } from '@/stores/useAppStore'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { api } from '@/lib/api'
import SettingsPage from './settings/SettingsPage'
import { SprintPlanningModal } from './modals/SprintPlanningModal'
import { useSprintPlanningStore } from '@/stores/useSprintPlanningStore'
import { useToolRefresh } from '@/hooks/useToolRefresh'
import { usePresence } from '@/hooks/usePresence'
import { InboxPanel } from './a2a/InboxPanel'

export function AppShell() {
  const focusRef = useRef<(() => void) | null>(null)
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId)
  const currentPage = useLayoutStore((s) => s.currentPage)
  const inboxPanelOpen = useLayoutStore((s) => s.inboxPanelOpen)
  const setInboxPanel = useLayoutStore((s) => s.setInboxPanel)
  const sprintOpen = useSprintPlanningStore((s) => s.isOpen)
  const sprintProjectId = useSprintPlanningStore((s) => s.projectId)
  const closeSprintPlanning = useSprintPlanningStore((s) => s.closeSprintPlanning)

  // Global tool refresh on session switch — runs even when ToolDashboard isn't mounted
  useToolRefresh()

  // Global presence SSE — one connection per browser tab
  usePresence()

  const { data: sessions = [] } = useQuery({
    queryKey: ['sessions', activeWorkspaceId],
    queryFn: () => api.listSessions(activeWorkspaceId ?? undefined),
    enabled: !!activeWorkspaceId,
  })

  // Get first agent for inbox panel
  const { data: agents = [] } = useQuery({
    queryKey: ['agents'],
    queryFn: api.listAgents,
  })
  const firstAgentId = agents.length > 0 ? agents[0].id : ''

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
    <div className="flex h-screen w-screen overflow-hidden bg-zinc-950">
      <NavRail />
      {currentPage === 'chat' && <LeftSidebar />}
      {currentPage === 'chat' ? (
        <ChatMain onEditorReady={handleEditorReady} />
      ) : currentPage === 'settings' ? (
        <SettingsPage />
      ) : null}
      {currentPage === 'chat' && <RightRail />}
      <ArtifactsDrawer />
      <WorkflowPanel />
      <InboxPanel
        agentId={firstAgentId}
        open={inboxPanelOpen}
        onClose={() => setInboxPanel(false)}
      />
      {sprintOpen && (
        <SprintPlanningModal
          projectId={sprintProjectId}
          onClose={closeSprintPlanning}
        />
      )}
    </div>
  )
}
