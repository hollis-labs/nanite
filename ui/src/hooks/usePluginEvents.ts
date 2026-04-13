/**
 * usePluginEvents — subscribes to the plugin lifecycle SSE at
 * GET /api/plugins/events and invalidates the registry query on every
 * relevant event so usePluginRegistry re-fetches and re-syncs.
 *
 * One connection per browser tab. EventSource handles reconnection.
 * Registered lifecycle types (per internal/api/plugins_events.go):
 *   plugin.installed, plugin.uninstalled, plugin.updated,
 *   plugin.enabled,   plugin.disabled,     plugin.load_failed
 *
 * Any event in this set invalidates ['plugins', 'registry']; other event
 * types are filtered server-side so we don't need a client-side check.
 */
import { useEffect, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'

export function usePluginEvents() {
  const esRef = useRef<EventSource | null>(null)
  const queryClient = useQueryClient()

  useEffect(() => {
    const es = new EventSource('/api/plugins/events')
    esRef.current = es

    const invalidate = () => {
      void queryClient.invalidateQueries({ queryKey: ['plugins', 'registry'] })
      // The PluginManager UI also queries the legacy /plugins/managed list.
      void queryClient.invalidateQueries({ queryKey: ['plugins-managed'] })
    }

    es.addEventListener('plugin.installed', invalidate)
    es.addEventListener('plugin.uninstalled', invalidate)
    es.addEventListener('plugin.updated', invalidate)
    es.addEventListener('plugin.enabled', invalidate)
    es.addEventListener('plugin.disabled', invalidate)
    es.addEventListener('plugin.load_failed', invalidate)

    es.onerror = () => {
      // EventSource auto-reconnects; nothing to do.
    }

    return () => {
      es.close()
      esRef.current = null
    }
  }, [queryClient])
}
