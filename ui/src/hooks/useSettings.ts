import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { UserSettings, ModelRecord, ProviderConfig } from '@/lib/types'

export function useSettings() {
  return useQuery<UserSettings>({
    queryKey: ['settings'],
    queryFn: api.getSettings,
    staleTime: 30_000,
  })
}

export function useSettingsMutation() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (data: Partial<UserSettings>) => api.updateSettings(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['settings'] })
    },
  })
}

export function useModels() {
  return useQuery<ModelRecord[]>({
    queryKey: ['models'],
    queryFn: api.listModels,
    staleTime: 5 * 60 * 1000,
  })
}

export function useProviders() {
  return useQuery<ProviderConfig[]>({
    queryKey: ['providers'],
    queryFn: api.listProviders,
    staleTime: 5 * 60 * 1000,
  })
}
