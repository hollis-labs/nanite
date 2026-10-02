/** Nanite taxonomy and core-name policy over the released browser registry. */
import type { ComponentType, LazyExoticComponent } from 'react'
import { createReactPluginRegistry } from '@hollis-labs/plugin-registry/react'
import type { AdoptedContribution, PluginRegistryOptions, PluginRegistryResponse } from '@hollis-labs/plugin-registry'
import { ENVELOPE_REGISTRY } from '@/generated/plugin-envelopes'
import { WIDGET_REGISTRY } from '@/generated/plugin-widgets'

export type { PluginRegistryResponse } from '@hollis-labs/plugin-registry'

export interface DynamicRegistryEntry {
  // Plugin components own their prop shapes; callers narrow at render sites.
  // biome-ignore lint/suspicious/noExplicitAny: host component boundary.
  component: LazyExoticComponent<ComponentType<any>>
  source: string
}

const CORE_PANEL_IDS = new Set(['widgets', 'work', 'workflows', 'inbox', 'artifacts'])

export function createNanitePluginRegistry(options: PluginRegistryOptions = {}) {
  return createReactPluginRegistry({
    ...options,
    importModule: options.importModule ?? ((url) => import(/* @vite-ignore */ url)),
    reserved: (kind, key) => (
      kind === 'envelope' && ENVELOPE_REGISTRY[key]?.source === 'core'
    ) || (
      kind === 'widget' && WIDGET_REGISTRY[key]?.source === 'core'
    ) || (
      kind === 'panel' && CORE_PANEL_IDS.has(key)
    ) || (
      kind === 'slot' && key.startsWith('right-rail-tab/') && CORE_PANEL_IDS.has(key.slice('right-rail-tab/'.length))
    ) || (options.reserved?.(kind, key) ?? false),
  })
}

export const browserPluginRegistry = createNanitePluginRegistry()
export const subscribeRegistry = browserPluginRegistry.subscribe
export const getRegistryVersion = browserPluginRegistry.version
export const clearDynamicRegistry = browserPluginRegistry.clear

export async function syncPluginRegistry(response: PluginRegistryResponse): Promise<void> {
  const result = await browserPluginRegistry.sync(response)
  if (!result.accepted) throw new Error('Plugin registry protocol or structure was refused')
}

function componentEntry(contribution?: AdoptedContribution): DynamicRegistryEntry | undefined {
  if (!contribution) return undefined
  return { component: contribution.value as DynamicRegistryEntry['component'], source: contribution.pluginId }
}

export function getDynamicEnvelope(type: string) {
  return componentEntry(browserPluginRegistry.get('envelope', type))
}
export function getDynamicWidget(id: string) {
  return componentEntry(browserPluginRegistry.get('widget', id))
}
export function getDynamicSlotComponent(name: string, owner?: string, id?: string) {
  const matches = browserPluginRegistry.list('slot').filter((entry) => {
    if (entry.exportName !== name || (owner && entry.pluginId !== owner)) return false
    if (!id) return true
    const meta = entry.meta
    return !!meta && typeof meta === 'object' && 'id' in meta && meta.id === id
  })
  // A component name alone is ambiguous when different plugins export it.
  if (!owner && new Set(matches.map((entry) => entry.pluginId)).size > 1) return undefined
  return componentEntry(matches[0])
}
export function getEnvelopePluginId(type: string) {
  return browserPluginRegistry.ownerOf('envelope', type)
}
export function getPluginLoadError(id: string) {
  return browserPluginRegistry.errors().find((entry) => entry.pluginId === id)?.reason
}
export function getPluginLoadErrors() {
  return [...browserPluginRegistry.errors()].map(({ pluginId, reason }) => ({ pluginId, reason }))
    .sort((a, b) => a.pluginId.localeCompare(b.pluginId))
}

async function reloadPluginFromBrowser(id: string): Promise<unknown> {
  const response = await fetch('/api/plugins/reload', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: id }),
  })
  const body = await response.json()
  if (!response.ok) throw new Error(`Reload failed: ${response.status}`)
  browserPluginRegistry.unload(id)
  return body
}

export function installPluginDevHelpers() {
  if (!import.meta.env.DEV || typeof window === 'undefined') return
  Object.assign(window, {
    __nanite_reloadPlugin: reloadPluginFromBrowser,
    __nanite_pluginRegistry: browserPluginRegistry.snapshot,
  })
}
