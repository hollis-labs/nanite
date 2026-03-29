import { create } from 'zustand'
import { persist } from 'zustand/middleware'

type ToolDrawerState = 'closed' | 'compact' | 'expanded'
type Theme = 'dark' | 'light' | 'system'
type RightRailTab = 'widgets' | 'inbox' | 'artifacts'

interface LayoutState {
  leftSidebarOpen: boolean
  rightRailOpen: boolean
  rightRailTab: RightRailTab
  taskThreadOpen: boolean
  toolDrawerState: ToolDrawerState
  toolDrawerHeight: number
  currentPage: string
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
  setCurrentPage: (page: string) => void
  toggleTheme: () => void
  setTheme: (theme: Theme) => void
}

function resolveTheme(theme: Theme): 'dark' | 'light' {
  if (theme === 'system') {
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
  }
  return theme
}

function applyThemeClass(theme: Theme) {
  const root = document.documentElement
  const resolved = resolveTheme(theme)
  root.classList.remove('dark', 'light')
  root.classList.add(resolved)
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
          const resolved = resolveTheme(state.theme)
          const next = resolved === 'dark' ? 'light' : 'dark'
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
        // Listen for OS theme changes when in system mode
        window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
          const current = useLayoutStore.getState().theme
          if (current === 'system') {
            applyThemeClass('system')
          }
        })
      },
    }
  )
)
