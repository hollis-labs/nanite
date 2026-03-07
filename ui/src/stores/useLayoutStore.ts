import { create } from 'zustand'
import { persist } from 'zustand/middleware'

interface LayoutState {
  leftSidebarOpen: boolean
  rightRailOpen: boolean
  artifactsDrawerOpen: boolean
  workflowPanelOpen: boolean
  toggleLeftSidebar: () => void
  toggleRightRail: () => void
  toggleArtifactsDrawer: () => void
  toggleWorkflowPanel: () => void
  setLeftSidebar: (open: boolean) => void
  setRightRail: (open: boolean) => void
  setArtifactsDrawer: (open: boolean) => void
  setWorkflowPanel: (open: boolean) => void
}

export const useLayoutStore = create<LayoutState>()(
  persist(
    (set) => ({
      leftSidebarOpen: true,
      rightRailOpen: true,
      artifactsDrawerOpen: false,
      workflowPanelOpen: false,
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
    }),
    {
      name: 'mentat-chat-layout',
    }
  )
)
