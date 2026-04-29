import { create } from 'zustand'
import type { WorkDiff } from '@/lib/types'

// D2 (CW-20260428-0015): the Work panel scope filter is a 3-way chip
// (All / Session / Project). The "all" filter renders both session and
// project sections side by side; specific scopes render only that section.
export type WorkScope = 'all' | 'session' | 'project'

interface WorkChange {
  type: 'todo_checked' | 'todo_unchecked' | 'todo_added' | 'todo_reordered' |
        'plan_step_checked' | 'plan_step_unchecked' | 'plan_approved' | 'plan_rejected'
  id: string
  planId?: string
  reason?: string
}

interface WorkState {
  scope: WorkScope
  dirty: boolean
  changes: WorkChange[]
  toastMessage: string | null
  setScope: (scope: WorkScope) => void
  recordChange: (change: WorkChange) => void
  buildDiff: () => WorkDiff
  clearChanges: () => void
  showToast: (message: string) => void
  dismissToast: () => void
}

export const useWorkStore = create<WorkState>((set, get) => ({
  scope: 'session',
  dirty: false,
  changes: [],
  toastMessage: null,

  setScope: (scope) => set({ scope }),

  recordChange: (change) =>
    set((state) => ({
      dirty: true,
      changes: [...state.changes, change],
    })),

  buildDiff: () => {
    const { changes } = get()
    const diff: WorkDiff = {
      todos_checked: [],
      todos_unchecked: [],
      todos_added: [],
      todos_reordered: false,
      plan_steps_checked: [],
      plan_steps_unchecked: [],
      plans_approved: [],
      plans_rejected: [],
    }
    for (const c of changes) {
      switch (c.type) {
        case 'todo_checked':
          diff.todos_checked.push(c.id)
          break
        case 'todo_unchecked':
          diff.todos_unchecked.push({ id: c.id, reason: c.reason })
          break
        case 'todo_added':
          diff.todos_added.push(c.id)
          break
        case 'todo_reordered':
          diff.todos_reordered = true
          break
        case 'plan_step_checked':
          diff.plan_steps_checked.push({ plan_id: c.planId!, step_id: c.id })
          break
        case 'plan_step_unchecked':
          diff.plan_steps_unchecked.push({ plan_id: c.planId!, step_id: c.id, reason: c.reason })
          break
        case 'plan_approved':
          diff.plans_approved.push(c.id)
          break
        case 'plan_rejected':
          diff.plans_rejected.push(c.id)
          break
      }
    }
    return diff
  },

  clearChanges: () => set({ dirty: false, changes: [] }),

  showToast: (message) => set({ toastMessage: message }),
  dismissToast: () => set({ toastMessage: null }),
}))
