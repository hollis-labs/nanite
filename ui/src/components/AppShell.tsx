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
import { api } from '@/lib/api'

export function AppShell() {
  const focusRef = useRef<(() => void) | null>(null)
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId)

  const { data: sessions = [] } = useQuery({
    queryKey: ['sessions', activeWorkspaceId],
    queryFn: () => api.listSessions(activeWorkspaceId ?? undefined),
    enabled: !!activeWorkspaceId,
  })

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
      <LeftSidebar />
      <ChatMain onEditorReady={handleEditorReady} />
      <RightRail />
      <ArtifactsDrawer />
      <WorkflowPanel />
    </div>
  )
}
