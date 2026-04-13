/**
 * usePluginRegistry — fetches the host's plugin registry and reconciles it
 * into the in-memory dynamic registry via syncPluginRegistry.
 *
 * Pairs with usePluginEvents: lifecycle SSE events invalidate this query so
 * the fetch + sync cycle re-runs whenever a plugin is installed / enabled /
 * disabled / uninstalled / load-failed.
 *
 * Called once from AppShell. Re-renders consumers (EnvelopeRenderer,
 * WidgetRenderer, slot renderers) piggyback on the loader's
 * subscribeRegistry/getRegistryVersion useSyncExternalStore pattern.
 */
import { useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { syncPluginRegistry, clearDynamicRegistry } from '@/lib/plugin-loader'
import { useSettings } from './useSettings'

export function usePluginRegistry() {
  const { data: settings } = useSettings()
  const recoverMode = settings?.recover_mode ?? false

  const query = useQuery({
    queryKey: ['plugins', 'registry'],
    queryFn: api.fetchPluginRegistry,
    staleTime: Infinity,
    enabled: !recoverMode,
  })

  useEffect(() => {
    if (recoverMode) {
      clearDynamicRegistry()
      return
    }
    if (query.data) {
      void syncPluginRegistry(query.data)
    }
  }, [query.data, recoverMode])

  return {
    ready: !recoverMode && !!query.data,
    error: query.error,
    refetch: query.refetch,
  }
}
