import { useEffect, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useChatStore } from '@/stores/useChatStore'
import { dispatchPresenceEvent } from '@/hooks/usePresence'
import type { PresenceEvent } from '@/lib/types'

/**
 * useUnifiedEvents connects to the single /api/events SSE endpoint multiplexing
 * presence, plugin lifecycle, and workflow updates.
 *
 * One persistent connection per browser tab replaces separate connections to:
 * - /api/presence
 * - /api/plugins/events
 */
export function useUnifiedEvents() {
  const esRef = useRef<EventSource | null>(null)
  const queryClient = useQueryClient()

  const setActiveStream = useChatStore((s) => s.setActiveStream)
  const removeActiveStream = useChatStore((s) => s.removeActiveStream)
  const setPendingTool = useChatStore((s) => s.setPendingTool)
  const removePendingTool = useChatStore((s) => s.removePendingTool)
  const setCLIActive = useChatStore((s) => s.setCLIActive)
  const removeCLIActive = useChatStore((s) => s.removeCLIActive)

  useEffect(() => {
    const es = new EventSource('/api/events')
    esRef.current = es

    // 1. Presence events: stream start/end, tool pending/resolved, cli active, work changed
    const onPresence = (e: MessageEvent) => {
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

    // 2. Plugin lifecycle events: installed, uninstalled, enabled, disabled, updated, load_failed
    const onPlugin = () => {
      void queryClient.invalidateQueries({ queryKey: ['plugins', 'registry'] })
      void queryClient.invalidateQueries({ queryKey: ['plugins-managed'] })
    }

    es.addEventListener('presence', onPresence)
    es.addEventListener('plugin', onPlugin)

    es.onerror = () => {
      // EventSource auto-reconnects; nothing to do.
    }

    return () => {
      es.removeEventListener('presence', onPresence)
      es.removeEventListener('plugin', onPlugin)
      es.close()
      esRef.current = null
    }
  }, [setActiveStream, removeActiveStream, setPendingTool, removePendingTool, setCLIActive, removeCLIActive, queryClient])
}
