import { create } from 'zustand'

interface AppState {
  activeWorkspaceId: string | null
  activeProjectId: string | null
  activeSessionId: string | null
  setActiveWorkspace: (id: string) => void
  setActiveProject: (id: string | null) => void
  setActiveSession: (id: string | null) => void
}

export const useAppStore = create<AppState>((set) => ({
  activeWorkspaceId: null,
  activeProjectId: null,
  activeSessionId: null,
  setActiveWorkspace: (id) => set({ activeWorkspaceId: id }),
  setActiveProject: (id) => set({ activeProjectId: id }),
  setActiveSession: (id) => set({ activeSessionId: id }),
}))
