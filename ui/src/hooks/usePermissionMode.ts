import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { PermissionMode } from '@/lib/types'

export function usePermissionMode() {
  const queryClient = useQueryClient()

  const { data, isLoading } = useQuery({
    queryKey: ['permission-mode'],
    queryFn: api.getPermissionMode,
    staleTime: 5_000,
  })

  const mutation = useMutation({
    mutationFn: (mode: PermissionMode) => api.setPermissionMode(mode),
    onSuccess: (result) => {
      queryClient.setQueryData(['permission-mode'], result)
    },
  })

  return {
    mode: data?.mode ?? 'default' as PermissionMode,
    isLoading,
    setMode: mutation.mutate,
    isPending: mutation.isPending,
  }
}
