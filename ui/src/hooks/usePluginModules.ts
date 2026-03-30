/**
 * Hook that loads dynamic plugin UI bundles.
 *
 * Fetches the plugin list, filters to active non-core plugins, and triggers
 * ESM bundle loading for each. Skips everything in recover mode.
 * Re-runs when the plugin list changes (e.g., after a catalog install)
 * but skips already-loaded plugins via isPluginLoaded().
 *
 * Call once from AppShell.
 */
import { useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { useSettings } from './useSettings'
import { loadAllPluginModules } from '@/lib/plugin-esm'
import { clearDynamicRegistry, isPluginLoaded } from '@/lib/plugin-loader'

export function usePluginModules() {
  const { data: settings } = useSettings()
  const recoverMode = settings?.recover_mode ?? false

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
      return
    }

    if (!plugins) return

    // Load bundles for active, non-core plugins that haven't been loaded yet.
    const candidates = plugins
      .filter((p) => p.status === 'active' && p.type !== 'core' && !isPluginLoaded(p.name))
      .map((p) => p.name)

    if (candidates.length === 0) return

    loadAllPluginModules(candidates).then((loaded) => {
      if (loaded.length > 0) {
        console.log(`[plugin-esm] Loaded UI bundles: ${loaded.join(', ')}`)
      }
    })
  }, [plugins, recoverMode])
}
