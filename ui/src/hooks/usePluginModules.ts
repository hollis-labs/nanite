/**
 * Hook that loads dynamic plugin UI bundles at app startup.
 *
 * Fetches the plugin list, filters to active non-core plugins, and triggers
 * ESM bundle loading for each. Skips everything in recover mode.
 *
 * Call once from AppShell — it's a fire-and-forget effect, not a data hook.
 */
import { useEffect, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { useSettings } from './useSettings'
import { loadAllPluginModules } from '@/lib/plugin-esm'
import { clearDynamicRegistry } from '@/lib/plugin-loader'

export function usePluginModules() {
  const { data: settings } = useSettings()
  const recoverMode = settings?.recover_mode ?? false
  const hasLoaded = useRef(false)

  // Fetch active plugins (same query the PluginManager uses).
  const { data: plugins } = useQuery({
    queryKey: ['plugins-for-esm'],
    queryFn: api.listPlugins,
    staleTime: 60_000,
    // Don't fetch if recover mode — no dynamic loading.
    enabled: !recoverMode,
  })

  useEffect(() => {
    // In recover mode, clear any previously loaded dynamic components.
    if (recoverMode) {
      clearDynamicRegistry()
      hasLoaded.current = false
      return
    }

    if (!plugins || hasLoaded.current) return
    hasLoaded.current = true

    // Load bundles for active, non-core plugins.
    const candidates = plugins
      .filter((p) => p.status === 'active' && p.type !== 'core')
      .map((p) => p.name)

    if (candidates.length === 0) return

    loadAllPluginModules(candidates).then((loaded) => {
      if (loaded.length > 0) {
        console.log(`[plugin-esm] Loaded UI bundles: ${loaded.join(', ')}`)
      }
    })
  }, [plugins, recoverMode])
}
