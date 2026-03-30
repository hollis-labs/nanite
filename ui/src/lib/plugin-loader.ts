/**
 * Dynamic plugin component registry.
 *
 * Plugins loaded at runtime (via ESM import) register their components here.
 * Build-time registries in generated/ always take precedence — dynamic entries
 * act as fallbacks for components not known at build time.
 *
 * The registry is a simple in-memory Map. Components are stored as lazy React
 * elements wrapping the raw component reference, matching the shape used by
 * the build-time registries (React.lazy).
 */
import { lazy, type ComponentType } from 'react'

// biome-ignore lint/suspicious/noExplicitAny: plugin components have varied props
type AnyComponent = ComponentType<any>
// biome-ignore lint/suspicious/noExplicitAny: matches build-time registry shape
type LazyComponent = React.LazyExoticComponent<ComponentType<any>>

export interface DynamicRegistryEntry {
  component: LazyComponent
  source: string // plugin name
}

// --- Internal stores ---

const dynamicEnvelopes = new Map<string, DynamicRegistryEntry>()
const dynamicWidgets = new Map<string, DynamicRegistryEntry>()
const dynamicSlotComponents = new Map<string, DynamicRegistryEntry>()

// Track which plugins have been loaded (or attempted).
const loadedPlugins = new Set<string>()

// Listeners notified when the registry changes (components re-render).
type Listener = () => void
const listeners = new Set<Listener>()
let registryVersion = 0

function notify() {
  registryVersion++
  for (const fn of listeners) fn()
}

// --- Public subscription (for React hooks) ---

export function subscribeRegistry(listener: Listener): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function getRegistryVersion(): number {
  return registryVersion
}

// --- Registration API (passed to plugin register() functions) ---

/** Wraps a raw component in React.lazy so it matches the build-time shape. */
function wrapComponent(comp: AnyComponent): LazyComponent {
  // React.lazy expects a thenable that resolves to { default: Component }.
  // Since we already have the component, wrap it in a resolved promise.
  return lazy(() => Promise.resolve({ default: comp }))
}

export interface PluginRegistryAPI {
  /** Register an envelope card component for a given envelope type string. */
  registerEnvelope(type: string, component: AnyComponent): void
  /** Register a widget component for a given widget ID. */
  registerWidget(id: string, component: AnyComponent): void
  /** Register a slot component by name. */
  registerSlotComponent(name: string, component: AnyComponent): void
}

/** Create a scoped registry API for a specific plugin. */
export function createRegistryAPI(pluginName: string): PluginRegistryAPI {
  return {
    registerEnvelope(type: string, component: AnyComponent) {
      dynamicEnvelopes.set(type, {
        component: wrapComponent(component),
        source: pluginName,
      })
      notify()
    },
    registerWidget(id: string, component: AnyComponent) {
      dynamicWidgets.set(id, {
        component: wrapComponent(component),
        source: pluginName,
      })
      notify()
    },
    registerSlotComponent(name: string, component: AnyComponent) {
      dynamicSlotComponents.set(name, {
        component: wrapComponent(component),
        source: pluginName,
      })
      notify()
    },
  }
}

// --- Lookup (called by the registry files as fallback) ---

export function getDynamicEnvelope(type: string): DynamicRegistryEntry | undefined {
  return dynamicEnvelopes.get(type)
}

export function getDynamicWidget(id: string): DynamicRegistryEntry | undefined {
  return dynamicWidgets.get(id)
}

export function getDynamicSlotComponent(name: string): DynamicRegistryEntry | undefined {
  return dynamicSlotComponents.get(name)
}

// --- Plugin load tracking ---

export function markPluginLoaded(name: string) {
  loadedPlugins.add(name)
}

export function isPluginLoaded(name: string): boolean {
  return loadedPlugins.has(name)
}

/** Clear all dynamic entries (used when toggling recover mode). */
export function clearDynamicRegistry() {
  dynamicEnvelopes.clear()
  dynamicWidgets.clear()
  dynamicSlotComponents.clear()
  loadedPlugins.clear()
  notify()
}
