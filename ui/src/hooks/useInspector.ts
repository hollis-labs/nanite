// useInspector.ts — I1 (CW-20260426-0004)
//
// React Query hooks for the developer-mode inspector panel.
// All hooks are enabled=false when sessionId is null so they
// never fire in non-dev-mode contexts.

import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'

/** List recent turn snapshots for a session. */
export function useInspectorTurns(sessionId: string | null, limit = 20) {
  return useQuery({
    queryKey: ['inspector', 'turns', sessionId, limit],
    queryFn: () => api.getInspectorTurns(sessionId!, limit),
    enabled: !!sessionId,
    staleTime: 3_000,
    refetchInterval: 5_000,
  })
}

/** Fetch the full TurnSnapshot for a specific turn. */
export function useInspectorTurn(sessionId: string | null, turnId: string | null) {
  return useQuery({
    queryKey: ['inspector', 'turn', sessionId, turnId],
    queryFn: () => api.getInspectorTurn(sessionId!, turnId!),
    enabled: !!sessionId && !!turnId,
    staleTime: 10_000,
  })
}
