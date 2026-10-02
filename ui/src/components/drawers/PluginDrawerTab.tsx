import { usePluginRegistryVersion } from '@hollis-labs/plugin-registry/react'
import { Suspense } from 'react'
import { PluginRenderBoundary } from '@/components/plugins/PluginRenderBoundary'
import { browserPluginRegistry } from '@/lib/plugin-loader'
import { getSlotComponent } from '@/lib/plugin-slot-lookup'
import type { UISlotEntry } from '@/lib/types'

export interface PluginDrawerTab extends UISlotEntry {
  tabId: string
}

/** Resolved declarations only: unloading removes tabs without a polling delay. */
export function usePluginDrawerTabs(slot: 'drawer.primary.tabs' | 'drawer.working.tabs'): PluginDrawerTab[] {
  usePluginRegistryVersion(browserPluginRegistry)
  return browserPluginRegistry.list('slot')
    .filter((entry) => entry.key.startsWith(`${slot}/`))
    .map((entry) => {
      const metadata = entry.meta as UISlotEntry
      return { ...metadata, plugin_id: entry.pluginId, component: entry.exportName, slot,
        tabId: `plugin:${encodeURIComponent(entry.pluginId)}:${encodeURIComponent(metadata.id)}` }
    })
    .sort((a, b) => (b.priority ?? 0) - (a.priority ?? 0) || a.tabId.localeCompare(b.tabId))
}

export function PluginDrawerTabBody({ entry, sessionId }: { entry?: PluginDrawerTab; sessionId: string }) {
  const View = entry?.component ? getSlotComponent(entry.component, entry.plugin_id, entry.id) : undefined
  if (!entry || !View) return <p className="p-3 text-xs text-fg-muted">Plugin tab is unavailable.</p>
  return (
    <PluginRenderBoundary key={entry.tabId} resetKey={View} label="Plugin tab">
      <Suspense fallback={<p className="p-3 text-xs text-fg-muted">Loading plugin tab…</p>}>
        <View {...entry.props} session_id={sessionId} />
      </Suspense>
    </PluginRenderBoundary>
  )
}
