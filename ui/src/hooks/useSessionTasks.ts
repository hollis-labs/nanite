import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { SessionTaskStatus } from '@/lib/types'

export function useSessionTasks(sessionId: string | null) {
  return useQuery({
    queryKey: ['session-tasks', sessionId],
    queryFn: () => api.listSessionTasks(sessionId!),
    enabled: !!sessionId,
  })
}

export function useCreateSessionTask() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (data: { title: string; session_id: string; description?: string }) =>
      api.createSessionTask(data),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: ['session-tasks', variables.session_id] })
    },
  })
}

export function useTransitionSessionTask() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ id, status }: { id: string; status: SessionTaskStatus }) =>
      api.transitionSessionTask(id, status),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['session-tasks'] })
    },
  })
}
