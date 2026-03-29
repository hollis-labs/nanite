import { create } from 'zustand'
import { persist } from 'zustand/middleware'

type ToolDrawerState = 'closed' | 'compact' | 'expanded'
type Theme = 'dark' | 'light'
type RightRailTab = 'widgets' | 'inbox' | 'artifacts'

interface LayoutState {
  leftSidebarOpen: boolean
  rightRailOpen: boolean
  rightRailTab: RightRailTab
  taskThreadOpen: boolean
  toolDrawerState: ToolDrawerState
  toolDrawerHeight: number
  currentPage: 'chat' | 'settings'
  theme: Theme
  toggleLeftSidebar: () => void
  toggleRightRail: () => void
  toggleArtifactsDrawer: () => void
  toggleTaskThread: () => void
  setLeftSidebar: (open: boolean) => void
  setRightRail: (open: boolean) => void
  setRightRailTab: (tab: RightRailTab) => void
  setArtifactsDrawer: (open: boolean) => void
  setTaskThread: (open: boolean) => void
  setToolDrawerState: (state: ToolDrawerState) => void
  setToolDrawerHeight: (height: number) => void
  setCurrentPage: (page: 'chat' | 'settings') => void
  toggleTheme: () => void
  setTheme: (theme: Theme) => void
}

function applyThemeClass(theme: Theme) {
  const root = document.documentElement
  root.classList.remove('dark', 'light')
  root.classList.add(theme)
}

export const useLayoutStore = create<LayoutState>()(
  persist(
    (set) => ({
      leftSidebarOpen: true,
      rightRailOpen: true,
      rightRailTab: 'widgets' as RightRailTab,
      taskThreadOpen: true,
      toolDrawerState: 'closed' as ToolDrawerState,
      toolDrawerHeight: 200,
      currentPage: 'chat',
      theme: 'dark' as Theme,
      toggleLeftSidebar: () =>
        set((state) => ({ leftSidebarOpen: !state.leftSidebarOpen })),
      toggleRightRail: () =>
        set((state) => ({ rightRailOpen: !state.rightRailOpen })),
      toggleArtifactsDrawer: () =>
        set((state) => {
          if (state.rightRailOpen && state.rightRailTab === 'artifacts') {
            return { rightRailOpen: false }
          }
          return { rightRailOpen: true, rightRailTab: 'artifacts' as RightRailTab }
        }),
      toggleTaskThread: () =>
        set((state) => ({ taskThreadOpen: !state.taskThreadOpen })),
      setLeftSidebar: (open) => set({ leftSidebarOpen: open }),
      setRightRail: (open) => set({ rightRailOpen: open }),
      setRightRailTab: (tab) => set({ rightRailTab: tab, rightRailOpen: true }),
      setArtifactsDrawer: (open) => {
        if (open) {
          set({ rightRailOpen: true, rightRailTab: 'artifacts' as RightRailTab })
        } else {
          set({ rightRailTab: 'widgets' as RightRailTab })
        }
      },
      setTaskThread: (open) => set({ taskThreadOpen: open }),
      setToolDrawerState: (state) => set({ toolDrawerState: state }),
      setToolDrawerHeight: (height) => set({ toolDrawerHeight: Math.max(100, Math.min(600, height)) }),
      setCurrentPage: (page) => set({ currentPage: page }),
      toggleTheme: () =>
        set((state) => {
          const next = state.theme === 'dark' ? 'light' : 'dark'
          applyThemeClass(next)
          return { theme: next }
        }),
      setTheme: (theme) => {
        applyThemeClass(theme)
        set({ theme })
      },
    }),
    {
      name: 'conduit-layout',
      onRehydrateStorage: () => (state) => {
        if (state?.theme) {
          applyThemeClass(state.theme)
        }
      },
    }
  )
)
