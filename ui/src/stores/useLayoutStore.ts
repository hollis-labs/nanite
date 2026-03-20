import { create } from 'zustand'
import { persist } from 'zustand/middleware'

interface LayoutState {
  leftSidebarOpen: boolean
  rightRailOpen: boolean
  artifactsDrawerOpen: boolean
  workflowPanelOpen: boolean
  taskThreadOpen: boolean
  inboxPanelOpen: boolean
  currentPage: 'chat' | 'settings'
  toggleLeftSidebar: () => void
  toggleRightRail: () => void
  toggleArtifactsDrawer: () => void
  toggleWorkflowPanel: () => void
  toggleTaskThread: () => void
  toggleInboxPanel: () => void
  setLeftSidebar: (open: boolean) => void
  setRightRail: (open: boolean) => void
  setArtifactsDrawer: (open: boolean) => void
  setWorkflowPanel: (open: boolean) => void
  setTaskThread: (open: boolean) => void
  setInboxPanel: (open: boolean) => void
  setCurrentPage: (page: 'chat' | 'settings') => void
}

export const useLayoutStore = create<LayoutState>()(
  persist(
    (set) => ({
      leftSidebarOpen: true,
      rightRailOpen: true,
      artifactsDrawerOpen: false,
      workflowPanelOpen: false,
      taskThreadOpen: true,
      inboxPanelOpen: false,
      currentPage: 'chat',
      toggleLeftSidebar: () =>
        set((state) => ({ leftSidebarOpen: !state.leftSidebarOpen })),
      toggleRightRail: () =>
        set((state) => ({ rightRailOpen: !state.rightRailOpen })),
      toggleArtifactsDrawer: () =>
        set((state) => ({ artifactsDrawerOpen: !state.artifactsDrawerOpen })),
      toggleWorkflowPanel: () =>
        set((state) => ({ workflowPanelOpen: !state.workflowPanelOpen })),
      toggleTaskThread: () =>
        set((state) => ({ taskThreadOpen: !state.taskThreadOpen })),
      toggleInboxPanel: () =>
        set((state) => ({ inboxPanelOpen: !state.inboxPanelOpen })),
      setLeftSidebar: (open) => set({ leftSidebarOpen: open }),
      setRightRail: (open) => set({ rightRailOpen: open }),
      setArtifactsDrawer: (open) => set({ artifactsDrawerOpen: open }),
      setWorkflowPanel: (open) => set({ workflowPanelOpen: open }),
      setTaskThread: (open) => set({ taskThreadOpen: open }),
      setInboxPanel: (open) => set({ inboxPanelOpen: open }),
      setCurrentPage: (page) => set({ currentPage: page }),
    }),
    {
      name: 'conduit-layout',
    }
  )
)
