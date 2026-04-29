import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { Todo, TodoFilter, TodoStatus, AgentStateScope } from '@/lib/types'
import { useWorkStore } from '@/stores/useWorkStore'

export function useTodos(filter: TodoFilter) {
  return useQuery({
    queryKey: ['todos', filter],
    queryFn: () => api.listTodos(filter),
    enabled: !!filter.scope_id || filter.scope === 'workspace',
  })
}

export function useCreateTodo() {
  const queryClient = useQueryClient()
  const recordChange = useWorkStore((s) => s.recordChange)

  return useMutation({
    mutationFn: (data: {
      title: string
      scope: string
      scope_id?: string
      priority?: string
      description?: string
    }) => api.createTodo(data),
    onSuccess: (todo) => {
      queryClient.invalidateQueries({ queryKey: ['todos'] })
      recordChange({ type: 'todo_added', id: todo.id })
    },
  })
}

export function useUpdateTodo() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({
      id,
      updates,
    }: {
      id: string
      updates: Partial<Pick<Todo, 'title' | 'description' | 'status' | 'priority' | 'labels' | 'metadata'>>
    }) => api.updateTodo(id, updates),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['todos'] })
    },
  })
}

export function useDeleteTodo() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) => api.deleteTodo(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['todos'] })
    },
  })
}

// D2 (CW-20260428-0015): promote/demote a todo between session and project scope.
export function useUpdateTodoScope() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({
      id,
      scope,
      scopeId,
      projectId,
    }: {
      id: string
      scope: AgentStateScope
      scopeId: string
      projectId?: string
    }) => api.updateTodoScope(id, scope, scopeId, projectId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['todos'] })
    },
  })
}

export function useToggleTodo() {
  const updateTodo = useUpdateTodo()
  const recordChange = useWorkStore((s) => s.recordChange)

  return {
    check: (id: string) => {
      updateTodo.mutate({ id, updates: { status: 'done' as TodoStatus } })
      recordChange({ type: 'todo_checked', id })
    },
    uncheck: (id: string, reason?: string) => {
      updateTodo.mutate({
        id,
        updates: {
          status: 'pending' as TodoStatus,
          metadata: reason ? { reopen_reason: reason } : undefined,
        },
      })
      recordChange({ type: 'todo_unchecked', id, reason })
    },
  }
}
