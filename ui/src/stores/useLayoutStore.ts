import { create } from 'zustand'
import { persist } from 'zustand/middleware'

type ToolDrawerState = 'closed' | 'compact' | 'expanded'
type Theme = 'dark' | 'light' | 'system'
type RightRailTab = 'widgets' | 'inbox' | 'artifacts' | (string & {})
export type LayoutPreset = 'focus' | 'default' | 'workspace' | 'reading'

/**
 * J9: per-panel user preferences persisted in layout store.
 * - defaultPanel:  which panel ID is active on fresh open (user-configured).
 * - panelEnabled:  map of panelId → enabled (false = hidden from tab strip).
 * - panelOrder:    user-reordered sequence of panel IDs (subset or full list).
 * - dismissedByUser: set of panel IDs user has dismissed since last conversational
 *   trigger. Owned by layout store; J8 reads/clears this for its dismiss policy.
 */
export interface PanelPrefs {
  defaultPanel?: string
  panelEnabled: Record<string, boolean>
  panelOrder: string[]
  dismissedByUser: Record<string, boolean>
}

interface LayoutState {
  leftSidebarOpen: boolean
  rightRailOpen: boolean
  rightRailTab: RightRailTab
  taskThreadOpen: boolean
  toolDrawerEnabled: boolean
  toolDrawerState: ToolDrawerState
  leftRailWorkspaceVisible: boolean
  leftRailNewChatVisible: boolean
  leftRailSearchVisible: boolean
  toolDrawerHeight: number
  headerChipsVisible: boolean
  currentPage: string
  theme: Theme
  toggleLeftSidebar: () => void
  toggleRightRail: () => void
  toggleArtifactsDrawer: () => void
  toggleTaskThread: () => void
  toggleToolDrawer: () => void
  toggleHeaderChips: () => void
  toggleLeftRailWorkspace: () => void
  toggleLeftRailNewChat: () => void
  toggleLeftRailSearch: () => void
  setLeftSidebar: (open: boolean) => void
  setRightRail: (open: boolean) => void
  setRightRailTab: (tab: RightRailTab) => void
  setArtifactsDrawer: (open: boolean) => void
  setTaskThread: (open: boolean) => void
  setToolDrawerState: (state: ToolDrawerState) => void
  setToolDrawerHeight: (height: number) => void
  setHeaderChipsVisible: (visible: boolean) => void
  applyLayoutPreset: (preset: LayoutPreset) => void
  setCurrentPage: (page: string) => void
  toggleTheme: () => void
  setTheme: (theme: Theme) => void
  memoryModalOpen: boolean
  setMemoryModalOpen: (open: boolean) => void

  // J9: panel preference actions
  panelPrefs: PanelPrefs
  /** Open rail and switch to panel id (J8 seam: panel_open). */
  setPanelOpen: (id: string) => void
  /** Set which panel is the user's default (shown on fresh open). */
  setDefaultPanel: (id: string) => void
  /** Toggle whether a panel appears in the tab strip. */
  setPanelEnabled: (id: string, enabled: boolean) => void
  /** Persist a user-chosen panel ordering. */
  setPanelOrder: (order: string[]) => void
  /** Mark a panel dismissed by user (J8 dismiss policy storage seam). */
  markPanelDismissed: (id: string) => void
  /** Clear the dismissed flag for a panel (J8 re-open after new trigger). */
  clearPanelDismissed: (id: string) => void
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
      toolDrawerEnabled: true,
      toolDrawerState: 'closed' as ToolDrawerState,
      leftRailWorkspaceVisible: true,
      leftRailNewChatVisible: true,
      leftRailSearchVisible: true,
      toolDrawerHeight: 240,
      headerChipsVisible: true,
      currentPage: 'chat',
      theme: 'dark' as Theme,
      toggleLeftSidebar: () =>
        set((state) => ({ leftSidebarOpen: !state.leftSidebarOpen })),
      toggleRightRail: () =>
        set((state) => ({ rightRailOpen: !state.rightRailOpen })),
      toggleToolDrawer: () =>
        set((state) => ({ toolDrawerEnabled: !state.toolDrawerEnabled })),
      toggleHeaderChips: () =>
        set((state) => ({ headerChipsVisible: !state.headerChipsVisible })),
      toggleLeftRailWorkspace: () =>
        set((state) => ({ leftRailWorkspaceVisible: !state.leftRailWorkspaceVisible })),
      toggleLeftRailNewChat: () =>
        set((state) => ({ leftRailNewChatVisible: !state.leftRailNewChatVisible })),
      toggleLeftRailSearch: () =>
        set((state) => ({ leftRailSearchVisible: !state.leftRailSearchVisible })),
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
      setHeaderChipsVisible: (visible) => set({ headerChipsVisible: visible }),
      applyLayoutPreset: (preset) => {
        const presets: Record<LayoutPreset, Partial<LayoutState>> = {
          focus:     { leftSidebarOpen: false, rightRailOpen: false, toolDrawerEnabled: false, headerChipsVisible: false },
          default:   { leftSidebarOpen: true,  rightRailOpen: false, toolDrawerEnabled: true,  headerChipsVisible: true  },
          workspace: { leftSidebarOpen: true,  rightRailOpen: true,  toolDrawerEnabled: true,  headerChipsVisible: true  },
          reading:   { leftSidebarOpen: false, rightRailOpen: false, toolDrawerEnabled: false, headerChipsVisible: true  },
        }
        set(presets[preset] as Partial<LayoutState>)
      },
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
      memoryModalOpen: false,
      setMemoryModalOpen: (open) => set({ memoryModalOpen: open }),

      // J9: panel preferences (persisted via Zustand persist)
      panelPrefs: {
        panelEnabled: {},
        panelOrder: [],
        dismissedByUser: {},
      },
      setPanelOpen: (id) =>
        set({ rightRailOpen: true, rightRailTab: id as RightRailTab }),
      setDefaultPanel: (id) =>
        set((s) => ({ panelPrefs: { ...s.panelPrefs, defaultPanel: id } })),
      setPanelEnabled: (id, enabled) =>
        set((s) => ({
          panelPrefs: {
            ...s.panelPrefs,
            panelEnabled: { ...s.panelPrefs.panelEnabled, [id]: enabled },
          },
        })),
      setPanelOrder: (order) =>
        set((s) => ({ panelPrefs: { ...s.panelPrefs, panelOrder: order } })),
      markPanelDismissed: (id) =>
        set((s) => ({
          panelPrefs: {
            ...s.panelPrefs,
            dismissedByUser: { ...s.panelPrefs.dismissedByUser, [id]: true },
          },
        })),
      clearPanelDismissed: (id) =>
        set((s) => {
          const { [id]: _, ...rest } = s.panelPrefs.dismissedByUser
          return { panelPrefs: { ...s.panelPrefs, dismissedByUser: rest } }
        }),
    }),
    {
      name: 'nanite-layout',
      migrate: () => {
        // One-time migration from conduit-layout to nanite-layout
        const old = localStorage.getItem('conduit-layout')
        if (old && !localStorage.getItem('nanite-layout')) {
          localStorage.setItem('nanite-layout', old)
          localStorage.removeItem('conduit-layout')
        }
      },
      onRehydrateStorage: () => (state) => {
        if (state?.theme) {
          applyThemeClass(state.theme)
        }
        // Never restore modal open state from persisted storage
        if (state) {
          state.memoryModalOpen = false
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
