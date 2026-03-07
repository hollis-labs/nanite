import { create } from 'zustand'
import { persist } from 'zustand/middleware'

interface LayoutState {
  leftSidebarOpen: boolean
  rightRailOpen: boolean
  artifactsDrawerOpen: boolean
  toggleLeftSidebar: () => void
  toggleRightRail: () => void
  toggleArtifactsDrawer: () => void
  setLeftSidebar: (open: boolean) => void
  setRightRail: (open: boolean) => void
  setArtifactsDrawer: (open: boolean) => void
}

export const useLayoutStore = create<LayoutState>()(
  persist(
    (set) => ({
      leftSidebarOpen: true,
      rightRailOpen: true,
      artifactsDrawerOpen: false,
      toggleLeftSidebar: () =>
        set((state) => ({ leftSidebarOpen: !state.leftSidebarOpen })),
      toggleRightRail: () =>
        set((state) => ({ rightRailOpen: !state.rightRailOpen })),
      toggleArtifactsDrawer: () =>
        set((state) => ({ artifactsDrawerOpen: !state.artifactsDrawerOpen })),
      setLeftSidebar: (open) => set({ leftSidebarOpen: open }),
      setRightRail: (open) => set({ rightRailOpen: open }),
      setArtifactsDrawer: (open) => set({ artifactsDrawerOpen: open }),
    }),
    {
      name: 'mentat-chat-layout',
    }
  )
)
