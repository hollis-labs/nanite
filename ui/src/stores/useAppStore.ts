import { create } from 'zustand'

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
  activeProjectId: string | null
  activeSessionId: string | null
  configVersion: number
  setActiveProject: (id: string | null) => void
  setActiveSession: (id: string | null) => void
  bumpConfigVersion: () => void
}

export const useAppStore = create<AppState>((set) => ({
  activeProjectId: null,
  activeSessionId: safeLocalStorageGet(SESSION_STORAGE_KEY),
  configVersion: 0,
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
