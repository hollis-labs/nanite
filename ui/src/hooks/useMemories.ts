import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { MemoryCreateRequest, MemoryUpdateRequest } from '@/lib/types'

interface MemoryFilters {
  scope?: string
  status?: string
  q?: string
  tags?: string
  limit?: number
  offset?: number
}

export function useMemories(filters?: MemoryFilters) {
  return useQuery({
    queryKey: ['memories', filters],
    queryFn: () => api.listMemories(filters),
  })
}

export function useCreateMemory() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (data: MemoryCreateRequest) => api.createMemory(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['memories'] })
    },
  })
}

export function useUpdateMemory() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ key, data }: { key: string; data: MemoryUpdateRequest }) =>
      api.updateMemory(key, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['memories'] })
    },
  })
}

export function useDeleteMemory() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (key: string) => api.deleteMemory(key),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['memories'] })
    },
  })
}

export function useUpdateMemoryStatus() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ key, status }: { key: string; status: string }) =>
      api.updateMemoryStatus(key, status),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['memories'] })
    },
  })
}
