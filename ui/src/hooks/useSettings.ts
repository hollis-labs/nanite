import { useQuery, useMutation, useQueryClient, keepPreviousData } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { UserSettings, ModelRecord, ProviderConfig } from '@/lib/types'

export function useSettings() {
  return useQuery<UserSettings>({
    queryKey: ['settings'],
    queryFn: api.getSettings,
    staleTime: 30_000,
    placeholderData: keepPreviousData,
  })
}

export function useSettingsMutation() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (data: Partial<UserSettings>) => api.updateSettings(data),
    onMutate: async (data) => {
      await queryClient.cancelQueries({ queryKey: ['settings'] })
      const previous = queryClient.getQueryData<UserSettings>(['settings'])
      if (previous) {
        queryClient.setQueryData<UserSettings>(['settings'], { ...previous, ...data })
      }
      return { previous }
    },
    onError: (_err, _data, context) => {
      if (context?.previous) {
        queryClient.setQueryData(['settings'], context.previous)
      }
    },
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ['settings'] })
    },
  })
}

export function useModels() {
  return useQuery<ModelRecord[]>({
    queryKey: ['models'],
    queryFn: api.listModels,
    staleTime: 5 * 60 * 1000,
    placeholderData: keepPreviousData,
  })
}

export function useProviders() {
  return useQuery<ProviderConfig[]>({
    queryKey: ['providers'],
    queryFn: api.listProviders,
    staleTime: 5 * 60 * 1000,
    placeholderData: keepPreviousData,
  })
}
