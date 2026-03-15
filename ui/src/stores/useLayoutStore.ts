import { create } from 'zustand'
import { persist } from 'zustand/middleware'

interface LayoutState {
  leftSidebarOpen: boolean
  rightRailOpen: boolean
  artifactsDrawerOpen: boolean
  workflowPanelOpen: boolean
  currentPage: 'chat' | 'settings'
  toggleLeftSidebar: () => void
  toggleRightRail: () => void
  toggleArtifactsDrawer: () => void
  toggleWorkflowPanel: () => void
  setLeftSidebar: (open: boolean) => void
  setRightRail: (open: boolean) => void
  setArtifactsDrawer: (open: boolean) => void
  setWorkflowPanel: (open: boolean) => void
  setCurrentPage: (page: 'chat' | 'settings') => void
}

export const useLayoutStore = create<LayoutState>()(
  persist(
    (set) => ({
      leftSidebarOpen: true,
      rightRailOpen: true,
      artifactsDrawerOpen: false,
      workflowPanelOpen: false,
      currentPage: 'chat',
      toggleLeftSidebar: () =>
        set((state) => ({ leftSidebarOpen: !state.leftSidebarOpen })),
      toggleRightRail: () =>
        set((state) => ({ rightRailOpen: !state.rightRailOpen })),
      toggleArtifactsDrawer: () =>
        set((state) => ({ artifactsDrawerOpen: !state.artifactsDrawerOpen })),
      toggleWorkflowPanel: () =>
        set((state) => ({ workflowPanelOpen: !state.workflowPanelOpen })),
      setLeftSidebar: (open) => set({ leftSidebarOpen: open }),
      setRightRail: (open) => set({ rightRailOpen: open }),
      setArtifactsDrawer: (open) => set({ artifactsDrawerOpen: open }),
      setWorkflowPanel: (open) => set({ workflowPanelOpen: open }),
      setCurrentPage: (page) => set({ currentPage: page }),
    }),
    {
      name: 'conduit-layout',
    }
  )
)
