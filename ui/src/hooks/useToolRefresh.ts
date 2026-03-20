import { useEffect, useRef, useCallback } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useAppStore } from '@/stores/useAppStore'
import { api } from '@/lib/api'

/**
 * Global hook that triggers tool discovery refresh when the active session changes.
 * Mount once in AppShell so tools refresh even when the ToolDashboard isn't open.
 *
 * Returns { refreshTools, isRefreshing } for manual refresh triggers.
 */
export function useToolRefresh() {
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const previousSessionId = useRef<string | null>(null)
  const queryClient = useQueryClient()

  const refreshMutation = useMutation({
    mutationFn: api.refreshTools,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tools'] })
      queryClient.invalidateQueries({ queryKey: ['tool-servers'] })
      queryClient.invalidateQueries({ queryKey: ['context-breakdown'] })
    },
  })

  const refreshTools = useCallback(() => {
    refreshMutation.mutate()
  }, [refreshMutation.mutate])

  // Auto-refresh when session changes
  useEffect(() => {
    if (previousSessionId.current === null) {
      // Initial mount — just record the session, don't refresh
      previousSessionId.current = activeSessionId
      return
    }

    if (previousSessionId.current !== activeSessionId && activeSessionId !== null) {
      // Immediately invalidate cached tools to show loading state
      queryClient.invalidateQueries({ queryKey: ['tools'] })
      queryClient.invalidateQueries({ queryKey: ['tool-servers'] })
      // Trigger server-side re-discovery
      refreshMutation.mutate()
    }

    previousSessionId.current = activeSessionId
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeSessionId])

  return {
    refreshTools,
    isRefreshing: refreshMutation.isPending,
  }
}
