import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { Envelope } from '@/lib/types'

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
 *
 * J8 v1 (CW-20260426-0006): the 4-state dismiss state machine derives its
 * state from these two maps:
 *   - state(panel) = `user_dismissed`  iff dismissedByUser[panel] === true
 *   - state(panel) = `user_opened`     iff panelOpenSource[panel] === 'user'
 *   - state(panel) = `agent_opened`    iff panelOpenSource[panel] === 'agent'
 *   - state(panel) = `closed`          otherwise
 *
 * panelOpenSource attribution rules:
 *   - user toggles a panel via UI         → 'user'
 *   - agent calls panel_open / signal_mode → 'agent'   (subject to dismiss gate)
 *   - panel closed                         → null      (entry deleted)
 *
 * dismissedByUser is cleared en bloc by clearDismissedAll() on the next
 * user-message turn (the "any new user turn resets" simplification — see
 * the followups_j8_classified_dismiss_reset note for the smarter classified
 * version).
 */
export interface PanelPrefs {
  defaultPanel?: string
  panelEnabled: Record<string, boolean>
  panelOrder: string[]
  dismissedByUser: Record<string, boolean>
  /** Who last opened this panel — drives the 4-state dismiss machine. */
  panelOpenSource: Record<string, 'agent' | 'user'>
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
  /** Open rail and switch to panel id (J8 seam: panel_open). Records source. */
  setPanelOpen: (id: string, source?: 'agent' | 'user') => void
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
  /**
   * J8 v1 dismiss-reset hook: clears the dismissedByUser map en bloc.
   * Called by the chat composer on every new user-message turn (the
   * v1 simplification "any new user turn resets all dismiss state for
   * all drawers"). Smarter classified-trigger version is captured as
   * followups_j8_classified_dismiss_reset.
   */
  clearAllPanelDismissed: () => void

  // J8 v1 — bottom_chat_drawer (CW-20260426-0006). The bottom chat drawer
  // is a separate UI surface from the right-rail panel host; it sits below
  // the chat transcript and surfaces long-form reference content (documents,
  // scratchpads). It is NOT in J9's right-rail catalog (widgets/work/
  // workflows/inbox/artifacts) so it has its own open/close state and source
  // attribution.
  bottomChatDrawerOpen: boolean
  /** Open/close the bottom chat drawer with source attribution for the dismiss machine. */
  setBottomDrawerOpen: (open: boolean, source?: 'agent' | 'user') => void

  // A2 v1 — per-panel envelope inbox (CW-20260428-0008). When an envelope
  // arrives with `render_target` set, applyEnvelopePanelEffects pushes it
  // into panelEnvelopes[render_target] and the panel's inbox slot picks it
  // up. NOT persisted across reload (Q3 — transient render-target
  // envelopes don't auto-restore; the chat-history stub re-routes on
  // click). Reset on rehydrate.
  panelEnvelopes: Record<string, Envelope[]>
  /** Push an envelope into a panel's inbox slot. Caller is responsible for
   *  dismiss-machine gating; this just stores. */
  pushPanelEnvelope: (panelId: string, envelope: Envelope) => void
  /** Clear all envelopes routed to a single panel (drawer-owned policy). */
  clearPanelEnvelopes: (panelId: string) => void

  // C1 (CW-20260428-0012) — bottom-drawer default-tab preference.
  // Which built-in tab opens when the drawer is opened with no specific
  // target. Defaults to 'scratchpad' (matches today's behavior). Pinned
  // cards live server-side and are not selectable as the default — that's
  // a v2 concern.
  defaultDrawerTab: string
  setDefaultDrawerTab: (tab: string) => void
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
        panelOpenSource: {},
      },
      setPanelOpen: (id, source = 'user') =>
        set((s) => {
          // J8 v1 dismiss machine: agent-driven opens are blocked when the
          // user has dismissed the panel since the last conversational
          // trigger. User-driven opens override the dismiss flag (manual
          // open == intent re-affirmation).
          const dismissed = s.panelPrefs.dismissedByUser[id] === true
          if (source === 'agent' && dismissed) {
            return s // NO-OP — agent silently fails the open per J8 contract.
          }
          // User-driven opens clear the dismiss flag for the panel they opened.
          let nextDismissed = s.panelPrefs.dismissedByUser
          if (source === 'user' && dismissed) {
            const { [id]: _drop, ...rest } = nextDismissed
            nextDismissed = rest
          }
          return {
            rightRailOpen: true,
            rightRailTab: id as RightRailTab,
            panelPrefs: {
              ...s.panelPrefs,
              dismissedByUser: nextDismissed,
              panelOpenSource: { ...s.panelPrefs.panelOpenSource, [id]: source },
            },
          }
        }),
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
        set((s) => {
          // Dismiss clears the source attribution — the panel is no longer
          // "agent_opened" or "user_opened"; it's `user_dismissed`.
          const { [id]: _drop, ...nextSource } = s.panelPrefs.panelOpenSource
          return {
            panelPrefs: {
              ...s.panelPrefs,
              dismissedByUser: { ...s.panelPrefs.dismissedByUser, [id]: true },
              panelOpenSource: nextSource,
            },
          }
        }),
      clearPanelDismissed: (id) =>
        set((s) => {
          const { [id]: _, ...rest } = s.panelPrefs.dismissedByUser
          return { panelPrefs: { ...s.panelPrefs, dismissedByUser: rest } }
        }),
      clearAllPanelDismissed: () =>
        set((s) => ({
          panelPrefs: { ...s.panelPrefs, dismissedByUser: {} },
        })),

      // J8 v1 — bottom_chat_drawer state (separate from right-rail).
      bottomChatDrawerOpen: false,
      setBottomDrawerOpen: (open, source = 'user') =>
        set((s) => {
          const id = 'bottom_chat_drawer'
          if (open) {
            const dismissed = s.panelPrefs.dismissedByUser[id] === true
            if (source === 'agent' && dismissed) {
              return s // NO-OP — dismiss-gated agent open.
            }
            let nextDismissed = s.panelPrefs.dismissedByUser
            if (source === 'user' && dismissed) {
              const { [id]: _drop, ...rest } = nextDismissed
              nextDismissed = rest
            }
            return {
              bottomChatDrawerOpen: true,
              panelPrefs: {
                ...s.panelPrefs,
                dismissedByUser: nextDismissed,
                panelOpenSource: { ...s.panelPrefs.panelOpenSource, [id]: source },
              },
            }
          }
          // Close path. User-close marks dismissed; agent-close drops the source
          // entry but does not touch dismiss state.
          if (source === 'user') {
            const { [id]: _drop, ...nextSource } = s.panelPrefs.panelOpenSource
            return {
              bottomChatDrawerOpen: false,
              panelPrefs: {
                ...s.panelPrefs,
                dismissedByUser: { ...s.panelPrefs.dismissedByUser, [id]: true },
                panelOpenSource: nextSource,
              },
            }
          }
          // Agent-close: only honored when the panel was agent-opened.
          // Refuse to close a user-opened drawer (J8 user-overrides-agent rule).
          if (s.panelPrefs.panelOpenSource[id] === 'user') {
            return s
          }
          const { [id]: _drop, ...nextSource } = s.panelPrefs.panelOpenSource
          return {
            bottomChatDrawerOpen: false,
            panelPrefs: { ...s.panelPrefs, panelOpenSource: nextSource },
          }
        }),

      // A2 v1 panel-envelope inbox (CW-20260428-0008).
      panelEnvelopes: {} as Record<string, Envelope[]>,
      pushPanelEnvelope: (panelId, envelope) =>
        set((s) => ({
          panelEnvelopes: {
            ...s.panelEnvelopes,
            [panelId]: [...(s.panelEnvelopes[panelId] ?? []), envelope],
          },
        })),
      clearPanelEnvelopes: (panelId) =>
        set((s) => {
          const { [panelId]: _drop, ...rest } = s.panelEnvelopes
          return { panelEnvelopes: rest }
        }),

      // C1 — bottom drawer default-tab preference.
      defaultDrawerTab: 'scratchpad',
      setDefaultDrawerTab: (tab) => set({ defaultDrawerTab: tab }),
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
          // A2 — Q3: transient render-target envelopes do NOT auto-restore
          // on session reload. The chat-history stub stays (envelopes are in
          // the transcript) and clicking it re-routes the card.
          state.panelEnvelopes = {}
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
