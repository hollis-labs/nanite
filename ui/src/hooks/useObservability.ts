import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'

export function useRecentExecutions(limit = 50) {
  return useQuery({
    queryKey: ['metrics', 'executions', limit],
    queryFn: () => api.getRecentExecutions(limit),
    staleTime: 15_000,
    refetchInterval: 30_000,
  })
}

export function useUtilityCallSummary() {
  return useQuery({
    queryKey: ['metrics', 'utility-summary'],
    queryFn: api.getUtilityCallSummary,
    staleTime: 30_000,
    refetchInterval: 60_000,
  })
}

export function useUtilityCallLog(limit = 50) {
  return useQuery({
    queryKey: ['metrics', 'utility-log', limit],
    queryFn: () => api.getUtilityCallLog(limit),
    staleTime: 15_000,
  })
}

export function useSessionMetrics(sessionId: string | null) {
  return useQuery({
    queryKey: ['metrics', 'session', sessionId],
    queryFn: () => api.getSessionMetrics(sessionId!),
    enabled: !!sessionId,
    staleTime: 15_000,
  })
}

export function useProcessHealth() {
  return useQuery({
    queryKey: ['processes', 'health'],
    queryFn: api.getProcessHealth,
    staleTime: 10_000,
    refetchInterval: 15_000,
  })
}

export function useKillStaleProcesses() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: api.killStaleProcesses,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['processes', 'health'] })
    },
  })
}
