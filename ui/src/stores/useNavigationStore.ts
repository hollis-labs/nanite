import { create } from 'zustand'

/**
 * Navigation history stack for Escape-to-go-back behavior.
 *
 * Each entry represents a view the user navigated to. Pressing Escape
 * pops the stack and returns to the previous view. When the stack is
 * empty, Escape returns to the chat home.
 *
 * Views are identified by a string key like:
 *   "chat"
 *   "settings/agents"
 *   "settings/agents/detail:abc123"
 *   "settings/tools/server:xyz"
 *
 * Components push entries when navigating into sub-views and the
 * Escape handler pops them.
 */

interface NavigationEntry {
  /** View key, e.g. "settings/agents/detail:abc123" */
  view: string
  /** Optional label for debugging */
  label?: string
}

interface NavigationState {
  /** Stack of navigation entries (most recent = last) */
  stack: NavigationEntry[]

  /** Push a new entry onto the stack. Deduplicates consecutive identical views. */
  push: (entry: NavigationEntry) => void

  /** Pop the most recent entry and return it (or undefined if empty). */
  pop: () => NavigationEntry | undefined

  /** Peek at the most recent entry without removing it. */
  peek: () => NavigationEntry | undefined

  /** Replace the top entry (used when switching sections at the same depth). */
  replaceTop: (entry: NavigationEntry) => void

  /** Clear the entire stack (e.g., on session switch). */
  clear: () => void

  /** Current depth of the stack */
  depth: () => number
}

export const useNavigationStore = create<NavigationState>()((set, get) => ({
  stack: [],

  push: (entry) =>
    set((state) => {
      const top = state.stack[state.stack.length - 1]
      if (top?.view === entry.view) return state
      return { stack: [...state.stack, entry] }
    }),

  pop: () => {
    const { stack } = get()
    if (stack.length === 0) return undefined
    const popped = stack[stack.length - 1]
    set({ stack: stack.slice(0, -1) })
    return popped
  },

  peek: () => {
    const { stack } = get()
    return stack[stack.length - 1]
  },

  replaceTop: (entry) =>
    set((state) => {
      if (state.stack.length === 0) return { stack: [entry] }
      return { stack: [...state.stack.slice(0, -1), entry] }
    }),

  clear: () => set({ stack: [] }),

  depth: () => get().stack.length,
}))
