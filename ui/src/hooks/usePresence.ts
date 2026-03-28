import { useEffect, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useChatStore } from '@/stores/useChatStore'
import type { PresenceEvent } from '@/lib/types'

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

        switch (evt.type) {
          case 'stream_start':
            setActiveStream(evt.session_id, {
              agentId: evt.agent_id ?? '',
              startedAt: evt.timestamp,
            })
            removeCLIActive(evt.session_id)
            break

          case 'stream_end':
            removeActiveStream(evt.session_id)
            removePendingTool(evt.session_id)
            removeCLIActive(evt.session_id)
            break

          case 'tool_pending':
            setPendingTool(evt.session_id, {
              toolName: evt.tool_name ?? '',
            })
            break

          case 'tool_resolved':
            removePendingTool(evt.session_id)
            break

          case 'cli_active':
            setCLIActive(evt.session_id, {
              lastSeen: evt.timestamp,
            })
            break

          case 'session_archived':
            removeActiveStream(evt.session_id)
            removePendingTool(evt.session_id)
            removeCLIActive(evt.session_id)
            queryClient.invalidateQueries({ queryKey: ['sessions'] })
            break
        }
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
