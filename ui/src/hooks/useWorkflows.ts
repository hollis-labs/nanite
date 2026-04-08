import { useEffect, useRef } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'

export function useWorkflowRuns(filter?: { status?: string; pipeline_id?: string }) {
  return useQuery({
    queryKey: ['workflow-runs', filter],
    queryFn: () => api.listWorkflowRuns(filter),
    refetchInterval: 10_000,
  })
}

export function useWorkflowRun(runId: string | null) {
  return useQuery({
    queryKey: ['workflow-run', runId],
    queryFn: () => api.getWorkflowRun(runId!),
    enabled: !!runId,
    refetchInterval: 5_000,
  })
}

export function useCancelWorkflowRun() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (runId: string) => api.cancelWorkflowRun(runId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflow-runs'] })
      queryClient.invalidateQueries({ queryKey: ['workflow-run'] })
    },
  })
}

export function useWorkflowEvents() {
  const queryClient = useQueryClient()
  const esRef = useRef<EventSource | null>(null)

  useEffect(() => {
    const es = new EventSource('/api/workflows/events')
    esRef.current = es

    es.onmessage = () => {
      queryClient.invalidateQueries({ queryKey: ['workflow-runs'] })
      queryClient.invalidateQueries({ queryKey: ['workflow-run'] })
    }

    es.onerror = () => {
      // EventSource auto-reconnects
    }

    return () => {
      es.close()
      esRef.current = null
    }
  }, [queryClient])
}
