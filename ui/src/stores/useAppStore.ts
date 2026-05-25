import { create } from 'zustand'

const STORAGE_KEY = 'nanite-active-workspace'
const SESSION_STORAGE_KEY = 'nanite-active-session'

function safeLocalStorageGet(key: string): string | null {
  if (typeof localStorage === 'undefined') return null
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

function safeLocalStorageSet(key: string, value: string) {
  if (typeof localStorage === 'undefined') return
  try {
    localStorage.setItem(key, value)
  } catch {
    // Ignore storage failures in non-browser / restricted contexts.
  }
}

function safeLocalStorageRemove(key: string) {
  if (typeof localStorage === 'undefined') return
  try {
    localStorage.removeItem(key)
  } catch {
    // Ignore storage failures in non-browser / restricted contexts.
  }
}

interface AppState {
  activeWorkspaceId: string | null
  activeProjectId: string | null
  activeSessionId: string | null
  configVersion: number
  setActiveWorkspace: (id: string) => void
  setActiveProject: (id: string | null) => void
  setActiveSession: (id: string | null) => void
  bumpConfigVersion: () => void
}

export const useAppStore = create<AppState>((set) => ({
  activeWorkspaceId: safeLocalStorageGet(STORAGE_KEY),
  activeProjectId: null,
  activeSessionId: safeLocalStorageGet(SESSION_STORAGE_KEY),
  configVersion: 0,
  setActiveWorkspace: (id) => {
    safeLocalStorageSet(STORAGE_KEY, id)
    safeLocalStorageRemove(SESSION_STORAGE_KEY)
    set({ activeWorkspaceId: id, activeProjectId: null, activeSessionId: null })
  },
  setActiveProject: (id) => set({ activeProjectId: id }),
  setActiveSession: (id) => {
    if (id) {
      safeLocalStorageSet(SESSION_STORAGE_KEY, id)
    } else {
      safeLocalStorageRemove(SESSION_STORAGE_KEY)
    }
    set({ activeSessionId: id })
  },
  bumpConfigVersion: () => set((state) => ({ configVersion: state.configVersion + 1 })),
}))
