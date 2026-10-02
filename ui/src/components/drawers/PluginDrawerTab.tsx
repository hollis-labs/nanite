import { usePluginRegistryVersion } from '@hollis-labs/plugin-registry/react'
import { Component, Suspense, type ReactNode } from 'react'
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

class PluginTabBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false }
  static getDerivedStateFromError() { return { failed: true } }
  render() {
    return this.state.failed ? <p role="alert" className="p-3 text-xs text-danger">Plugin tab could not render.</p> : this.props.children
  }
}

export function PluginDrawerTabBody({ entry, sessionId }: { entry?: PluginDrawerTab; sessionId: string }) {
  const View = entry?.component ? getSlotComponent(entry.component, entry.plugin_id, entry.id) : undefined
  if (!entry || !View) return <p className="p-3 text-xs text-fg-muted">Plugin tab is unavailable.</p>
  return (
    <PluginTabBoundary key={`${entry.tabId}:${entry.component}:${browserPluginRegistry.version()}`}>
      <Suspense fallback={<p className="p-3 text-xs text-fg-muted">Loading plugin tab…</p>}>
        <View {...entry.props} session_id={sessionId} />
      </Suspense>
    </PluginTabBoundary>
  )
}
