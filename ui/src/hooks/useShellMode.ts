import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { ShellMode } from '@/lib/types'

export function useShellMode(sessionId: string | null) {
  const queryClient = useQueryClient()

  const { data, isLoading } = useQuery({
    queryKey: ['shell-mode', sessionId],
    queryFn: () => api.getShellMode(sessionId!),
    enabled: !!sessionId,
    staleTime: 10_000,
  })

  const mutation = useMutation({
    mutationFn: (mode: ShellMode) => api.setShellMode(sessionId!, mode),
    onSuccess: (result) => {
      queryClient.setQueryData(['shell-mode', sessionId], result)
    },
  })

  const shellMode = (data?.mode ?? 'ask') as ShellMode

  // Cycle: ask -> session -> yolo -> ask
  function cycleMode() {
    const next: Record<ShellMode, ShellMode> = {
      ask: 'session',
      session: 'yolo',
      yolo: 'ask',
    }
    mutation.mutate(next[shellMode])
  }

  return {
    mode: shellMode,
    isLoading,
    setMode: mutation.mutate,
    cycleMode,
    isPending: mutation.isPending,
  }
}
