import { useEffect, useRef } from 'react'
import { useQueryClient, type QueryClient } from '@tanstack/react-query'
import { useChatStore } from '@/stores/useChatStore'
import type { PresenceEvent } from '@/lib/types'

/**
 * Pure dispatcher for presence events. Extracted from the hook body so it
 * can be unit-tested without spinning up a React tree / EventSource.
 */
export interface PresenceHandlers {
  setActiveStream: (sessionId: string, info: { agentId: string; startedAt: string }) => void
  removeActiveStream: (sessionId: string) => void
  setPendingTool: (sessionId: string, info: { toolName: string }) => void
  removePendingTool: (sessionId: string) => void
  setCLIActive: (sessionId: string, info: { lastSeen: string }) => void
  removeCLIActive: (sessionId: string) => void
  queryClient: Pick<QueryClient, 'invalidateQueries'>
}

export function dispatchPresenceEvent(evt: PresenceEvent, h: PresenceHandlers): void {
  switch (evt.type) {
    case 'stream_start':
      h.setActiveStream(evt.session_id, {
        agentId: evt.agent_id ?? '',
        startedAt: evt.timestamp,
      })
      h.removeCLIActive(evt.session_id)
      break

    case 'stream_end':
      h.removeActiveStream(evt.session_id)
      h.removePendingTool(evt.session_id)
      h.removeCLIActive(evt.session_id)
      break

    case 'tool_pending':
      h.setPendingTool(evt.session_id, {
        toolName: evt.tool_name ?? '',
      })
      break

    case 'tool_resolved':
      h.removePendingTool(evt.session_id)
      break

    case 'cli_active':
      h.setCLIActive(evt.session_id, {
        lastSeen: evt.timestamp,
      })
      break

    case 'session_archived':
      h.removeActiveStream(evt.session_id)
      h.removePendingTool(evt.session_id)
      h.removeCLIActive(evt.session_id)
      h.queryClient.invalidateQueries({ queryKey: ['sessions'] })
      break

    case 'work_changed':
      h.queryClient.invalidateQueries({ queryKey: ['todos'] })
      h.queryClient.invalidateQueries({ queryKey: ['plans'] })
      h.queryClient.invalidateQueries({ queryKey: ['workflow-runs'] })
      break
  }
}

/**
 * usePresence connects to the /api/presence SSE endpoint and updates
 * the Zustand store with active streams, pending tool approvals,
 * CLI activity, and session lifecycle events.
 * One connection per browser tab.
 */
export function usePresence() {
  const esRef = useRef<EventSource | null>(null)
  const queryClient = useQueryClient()

  const setActiveStream = useChatStore((s) => s.setActiveStream)
  const removeActiveStream = useChatStore((s) => s.removeActiveStream)
  const setPendingTool = useChatStore((s) => s.setPendingTool)
  const removePendingTool = useChatStore((s) => s.removePendingTool)
  const setCLIActive = useChatStore((s) => s.setCLIActive)
  const removeCLIActive = useChatStore((s) => s.removeCLIActive)

  useEffect(() => {
    const es = new EventSource('/api/presence')
    esRef.current = es

    es.onmessage = (e: MessageEvent) => {
      try {
        const evt: PresenceEvent = JSON.parse(e.data as string)
        dispatchPresenceEvent(evt, {
          setActiveStream,
          removeActiveStream,
          setPendingTool,
          removePendingTool,
          setCLIActive,
          removeCLIActive,
          queryClient,
        })
      } catch {
        // Ignore malformed events.
      }
    }

    es.onerror = () => {
      // EventSource auto-reconnects; nothing to do.
    }

    return () => {
      es.close()
      esRef.current = null
    }
  }, [setActiveStream, removeActiveStream, setPendingTool, removePendingTool, setCLIActive, removeCLIActive, queryClient])
}
