import { create } from 'zustand'

interface SprintPlanningState {
  isOpen: boolean
  projectId: string | undefined
  openSprintPlanning: (projectId?: string) => void
  closeSprintPlanning: () => void
}

export const useSprintPlanningStore = create<SprintPlanningState>((set) => ({
  isOpen: false,
  projectId: undefined,
  openSprintPlanning: (projectId) => set({ isOpen: true, projectId }),
  closeSprintPlanning: () => set({ isOpen: false, projectId: undefined }),
}))
