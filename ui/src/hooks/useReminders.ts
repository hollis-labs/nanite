import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { AgentStateScope } from '@/lib/types'

// D1/D2 (CW-20260428-0014/0015): reminders hook for the Work panel.
// Lists unfired reminders visible to the active session (session-scoped +
// project-scoped reminders attached to the session's project).

export function useReminders(sessionId: string | null) {
  return useQuery({
    queryKey: ['reminders', sessionId],
    queryFn: () => api.listReminders(sessionId!),
    enabled: !!sessionId,
    refetchInterval: 5000, // refresh frequently — reminders may be set during a turn
  })
}

export function useDeleteReminder(sessionId: string | null) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.deleteReminder(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['reminders', sessionId] })
    },
  })
}

export function useUpdateReminderScope(sessionId: string | null) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ id, scope, projectId }: { id: string; scope: AgentStateScope; projectId?: string }) =>
      api.updateReminderScope(id, scope, projectId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['reminders', sessionId] })
    },
  })
}
