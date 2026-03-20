import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useAppStore } from '@/stores/useAppStore'
import { api } from '@/lib/api'

interface TaskContext {
  isTaskSession: boolean
  taskId: string | null
}

/**
 * useTaskContext inspects the active session to determine if it is a
 * task-scoped session (context_type === 'task' with a valid context_id).
 * Returns { isTaskSession, taskId } so panels can conditionally render
 * A2A thread content for the linked Engine task.
 */
export function useTaskContext(): TaskContext {
  const activeSessionId = useAppStore((s) => s.activeSessionId)

  const { data: session } = useQuery({
    queryKey: ['session', activeSessionId],
    queryFn: () => api.getSession(activeSessionId!),
    enabled: !!activeSessionId,
    // Use a long stale time — session metadata rarely changes mid-conversation
    staleTime: 1000 * 60 * 5,
  })

  return useMemo<TaskContext>(() => {
    if (!session) {
      return { isTaskSession: false, taskId: null }
    }

    const isTask =
      session.context_type === 'task' &&
      typeof session.context_id === 'string' &&
      session.context_id.length > 0

    return {
      isTaskSession: isTask,
      taskId: isTask ? session.context_id : null,
    }
  }, [session])
}
