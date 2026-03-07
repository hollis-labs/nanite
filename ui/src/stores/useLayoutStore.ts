import { create } from 'zustand'
import { persist } from 'zustand/middleware'

interface LayoutState {
  leftSidebarOpen: boolean
  rightRailOpen: boolean
  toggleLeftSidebar: () => void
  toggleRightRail: () => void
  setLeftSidebar: (open: boolean) => void
  setRightRail: (open: boolean) => void
}

export const useLayoutStore = create<LayoutState>()(
  persist(
    (set) => ({
      leftSidebarOpen: true,
      rightRailOpen: true,
      toggleLeftSidebar: () =>
        set((state) => ({ leftSidebarOpen: !state.leftSidebarOpen })),
      toggleRightRail: () =>
        set((state) => ({ rightRailOpen: !state.rightRailOpen })),
      setLeftSidebar: (open) => set({ leftSidebarOpen: open }),
      setRightRail: (open) => set({ rightRailOpen: open }),
    }),
    {
      name: 'mentat-chat-layout',
    }
  )
)
