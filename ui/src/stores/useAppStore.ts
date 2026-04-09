import { create } from 'zustand'

const STORAGE_KEY = 'nanite-active-workspace'
const SESSION_STORAGE_KEY = 'nanite-active-session'

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
  activeWorkspaceId: localStorage.getItem(STORAGE_KEY),
  activeProjectId: null,
  activeSessionId: localStorage.getItem(SESSION_STORAGE_KEY),
  configVersion: 0,
  setActiveWorkspace: (id) => {
    localStorage.setItem(STORAGE_KEY, id)
    localStorage.removeItem(SESSION_STORAGE_KEY)
    set({ activeWorkspaceId: id, activeProjectId: null, activeSessionId: null })
  },
  setActiveProject: (id) => set({ activeProjectId: id }),
  setActiveSession: (id) => {
    if (id) {
      localStorage.setItem(SESSION_STORAGE_KEY, id)
    } else {
      localStorage.removeItem(SESSION_STORAGE_KEY)
    }
    set({ activeSessionId: id })
  },
  bumpConfigVersion: () => set((state) => ({ configVersion: state.configVersion + 1 })),
}))
