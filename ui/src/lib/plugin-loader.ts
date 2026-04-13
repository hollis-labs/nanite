/**
 * Dynamic plugin component registry (Model A).
 *
 * The host publishes a yaml-authoritative registry at `GET /api/plugins/registry`.
 * `syncPluginRegistry` is the single entry point: given a registry response,
 * it dynamic-imports each plugin's ES module bundle, pulls the named exports
 * named by the registry (envelope/widget/slot `component` fields), and wires
 * them into the in-memory dynamic registry maps consumed by the generated
 * registry files and slot/widget renderers.
 *
 * Previous Model B (`createRegistryAPI` + plugin-driven `register()` callback)
 * is gone — plugins no longer declare what they register at runtime; the host
 * manifest is authoritative. See plan §D.1 / plugin-execution-plan §6.1.
 */
import { lazy, type ComponentType } from 'react'

// biome-ignore lint/suspicious/noExplicitAny: plugin components have varied props
type AnyComponent = ComponentType<any>
// biome-ignore lint/suspicious/noExplicitAny: matches build-time registry shape
type LazyComponent = React.LazyExoticComponent<ComponentType<any>>

export interface DynamicRegistryEntry {
  component: LazyComponent
  source: string // plugin id
}

// --- Registry response types (mirror internal/api/plugins_registry.go) ---

export interface PluginRegistryEnvelope {
  plugin_id: string
  component: string // named export to pull from the bundle
  version: number
  schema_url?: string
}

export interface PluginRegistryWidget {
  plugin_id: string
  name: string
  description?: string
}

export interface PluginRegistrySlot {
  id: string
  plugin_id: string
  label?: string
  icon?: string
  priority?: number
  component?: string // named export; empty when slot uses `action` instead
  action?: string
  props?: Record<string, unknown>
}

export interface PluginRegistryPlugin {
  bundle_url?: string
  stylesheet_url?: string
  bundle_hash: string
  react_version: string
}

export interface PluginRegistryResponse {
  envelopes: Record<string, PluginRegistryEnvelope>
  widgets: Record<string, PluginRegistryWidget>
  slots: Record<string, PluginRegistrySlot[]>
  plugins: Record<string, PluginRegistryPlugin>
}

// --- Internal stores ---

const dynamicEnvelopes = new Map<string, DynamicRegistryEntry>()
const dynamicWidgets = new Map<string, DynamicRegistryEntry>()
const dynamicSlotComponents = new Map<string, DynamicRegistryEntry>()

/**
 * Maps envelope type → owning plugin id, populated unconditionally from the
 * registry response. Survives load failures so the UI can resolve a
 * `plugin_id` for an envelope whose component never loaded.
 */
const envelopeTypeToPluginId = new Map<string, string>()

/** Last load error recorded per plugin id. Cleared on successful (re)load. */
const loadErrors = new Map<string, string>()

/**
 * Tracks what each loaded plugin bundle has registered so `unloadPlugin` can
 * remove exactly those entries without scanning the whole registry, and so
 * `syncPluginRegistry` can skip plugins whose bundle URL is unchanged.
 */
interface LoadedBundle {
  bundleUrl: string
  stylesheetUrl?: string
  stylesheetEl?: HTMLLinkElement
  envelopeTypes: Set<string>
  widgetIds: Set<string>
  slotComponentNames: Set<string>
}
const loadedBundles = new Map<string, LoadedBundle>()

// In-flight loads — dedupe concurrent syncs that race on the same plugin.
const inflight = new Map<string, Promise<void>>()

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

// --- Lookup (called by the generated registry files as fallback) ---

export function getDynamicEnvelope(type: string): DynamicRegistryEntry | undefined {
  return dynamicEnvelopes.get(type)
}

export function getDynamicWidget(id: string): DynamicRegistryEntry | undefined {
  return dynamicWidgets.get(id)
}

export function getDynamicSlotComponent(name: string): DynamicRegistryEntry | undefined {
  return dynamicSlotComponents.get(name)
}

/** Plugin id declared as the owner of an envelope type, regardless of load state. */
export function getEnvelopePluginId(type: string): string | undefined {
  return envelopeTypeToPluginId.get(type)
}

/** Last recorded load-error reason for a plugin, or undefined if the last load succeeded. */
export function getPluginLoadError(pluginId: string): string | undefined {
  return loadErrors.get(pluginId)
}

/** Snapshot of all current plugin load errors. Stable key order. */
export function getPluginLoadErrors(): Array<{ pluginId: string; reason: string }> {
  return Array.from(loadErrors, ([pluginId, reason]) => ({ pluginId, reason }))
}

/** Clear all dynamic entries (used when toggling recover mode). */
export function clearDynamicRegistry() {
  for (const bundle of loadedBundles.values()) {
    removeStylesheet(bundle)
  }
  dynamicEnvelopes.clear()
  dynamicWidgets.clear()
  dynamicSlotComponents.clear()
  envelopeTypeToPluginId.clear()
  loadErrors.clear()
  loadedBundles.clear()
  inflight.clear()
  notify()
}

// --- Public: sync entry point ---

/**
 * Reconcile the in-memory dynamic registry with a registry response from
 * `GET /api/plugins/registry`.
 *
 * - For each plugin in `data.plugins` with a `bundle_url`: if not already
 *   loaded at that URL, dynamic-import the bundle and register its components.
 * - For each plugin currently loaded but no longer in `data.plugins` (or whose
 *   `bundle_url` has changed): unload and re-load.
 *
 * Failures to load a single plugin are logged and swallowed — other plugins
 * still reconcile. Callers (React Query + `PluginLoadErrorCard`) surface
 * failures from their own error state.
 */
export async function syncPluginRegistry(data: PluginRegistryResponse): Promise<void> {
  // Rebuild the envelope-type → plugin-id map from the registry response.
  // Done unconditionally so consumers can resolve ownership even when a
  // plugin's bundle later fails to load.
  envelopeTypeToPluginId.clear()
  for (const [type, env] of Object.entries(data.envelopes)) {
    envelopeTypeToPluginId.set(type, env.plugin_id)
  }

  const desired = new Set<string>()
  const loads: Array<Promise<void>> = []

  for (const [pluginId, plugin] of Object.entries(data.plugins)) {
    if (!plugin.bundle_url) continue
    desired.add(pluginId)
    const current = loadedBundles.get(pluginId)
    if (current && current.bundleUrl === plugin.bundle_url) {
      // Already loaded at this URL — stylesheet + registrations still valid.
      // Update stylesheet element reference in case the caller cleared DOM.
      ensureStylesheet(pluginId, plugin.stylesheet_url, current)
      continue
    }
    if (current) unloadPlugin(pluginId)
    loads.push(loadPluginBundle(pluginId, plugin, data))
  }

  // Unload plugins that vanished from the registry.
  for (const pluginId of Array.from(loadedBundles.keys())) {
    if (!desired.has(pluginId)) unloadPlugin(pluginId)
  }
  // Clear stale errors for plugins no longer in the registry.
  for (const pluginId of Array.from(loadErrors.keys())) {
    if (!desired.has(pluginId)) loadErrors.delete(pluginId)
  }

  await Promise.allSettled(loads)
  notify()
}

// --- Private: load/unload/stylesheet helpers ---

/** Wraps a raw component in React.lazy so it matches the build-time shape. */
function wrapComponent(comp: AnyComponent): LazyComponent {
  return lazy(() => Promise.resolve({ default: comp }))
}

function loadPluginBundle(
  pluginId: string,
  plugin: PluginRegistryPlugin,
  data: PluginRegistryResponse,
): Promise<void> {
  const existing = inflight.get(pluginId)
  if (existing) return existing

  const bundleUrl = plugin.bundle_url
  if (!bundleUrl) return Promise.resolve()

  const promise = (async () => {
    // biome-ignore lint/suspicious/noExplicitAny: plugin module shape is dynamic
    let mod: Record<string, any>
    try {
      mod = (await import(/* @vite-ignore */ bundleUrl)) as Record<string, any>
    } catch (err) {
      const reason = err instanceof Error ? err.message : String(err)
      console.error(`[plugin-loader] Failed to import bundle for "${pluginId}" from ${bundleUrl}:`, err)
      loadErrors.set(pluginId, reason)
      throw err
    }

    const bundle: LoadedBundle = {
      bundleUrl,
      ...(plugin.stylesheet_url ? { stylesheetUrl: plugin.stylesheet_url } : {}),
      envelopeTypes: new Set(),
      widgetIds: new Set(),
      slotComponentNames: new Set(),
    }

    // Envelopes for this plugin.
    for (const [type, env] of Object.entries(data.envelopes)) {
      if (env.plugin_id !== pluginId) continue
      const comp = mod[env.component]
      if (typeof comp !== 'function') {
        console.warn(
          `[plugin-loader] Plugin "${pluginId}" bundle is missing named export "${env.component}" for envelope "${type}" — skipping`,
        )
        continue
      }
      dynamicEnvelopes.set(type, { component: wrapComponent(comp), source: pluginId })
      bundle.envelopeTypes.add(type)
    }

    // Widgets for this plugin.
    for (const [id, widget] of Object.entries(data.widgets)) {
      if (widget.plugin_id !== pluginId) continue
      // Widget registry entry doesn't carry a `component` field — it's the
      // generated frontend registry that maps widget ID → component. For
      // runtime-registered widgets, the bundle must export a named function
      // matching the widget's `name` (spec: widget name is the export name).
      const comp = mod[widget.name]
      if (typeof comp !== 'function') {
        console.warn(
          `[plugin-loader] Plugin "${pluginId}" bundle is missing named export "${widget.name}" for widget "${id}" — skipping`,
        )
        continue
      }
      dynamicWidgets.set(id, { component: wrapComponent(comp), source: pluginId })
      bundle.widgetIds.add(id)
    }

    // Slot components for this plugin.
    for (const entries of Object.values(data.slots)) {
      for (const entry of entries) {
        if (entry.plugin_id !== pluginId) continue
        if (!entry.component) continue
        const comp = mod[entry.component]
        if (typeof comp !== 'function') {
          console.warn(
            `[plugin-loader] Plugin "${pluginId}" bundle is missing named export "${entry.component}" for slot entry "${entry.id}" — skipping`,
          )
          continue
        }
        dynamicSlotComponents.set(entry.component, {
          component: wrapComponent(comp),
          source: pluginId,
        })
        bundle.slotComponentNames.add(entry.component)
      }
    }

    ensureStylesheet(pluginId, plugin.stylesheet_url, bundle)
    loadedBundles.set(pluginId, bundle)
    loadErrors.delete(pluginId)
  })()

  const tracked = promise.finally(() => {
    inflight.delete(pluginId)
  })
  inflight.set(pluginId, tracked)
  return tracked
}

function unloadPlugin(pluginId: string): void {
  const bundle = loadedBundles.get(pluginId)
  if (!bundle) return

  for (const type of bundle.envelopeTypes) {
    const entry = dynamicEnvelopes.get(type)
    if (entry?.source === pluginId) dynamicEnvelopes.delete(type)
  }
  for (const id of bundle.widgetIds) {
    const entry = dynamicWidgets.get(id)
    if (entry?.source === pluginId) dynamicWidgets.delete(id)
  }
  for (const name of bundle.slotComponentNames) {
    const entry = dynamicSlotComponents.get(name)
    if (entry?.source === pluginId) dynamicSlotComponents.delete(name)
  }
  removeStylesheet(bundle)
  loadedBundles.delete(pluginId)
}

function injectStylesheet(pluginId: string, url: string): HTMLLinkElement {
  const selector = `link[data-plugin="${cssEscape(pluginId)}"]`
  const existing = document.head.querySelector<HTMLLinkElement>(selector)
  if (existing) {
    if (existing.href !== url) existing.href = url
    return existing
  }
  const link = document.createElement('link')
  link.rel = 'stylesheet'
  link.href = url
  link.dataset.plugin = pluginId
  document.head.appendChild(link)
  return link
}

function ensureStylesheet(
  pluginId: string,
  url: string | undefined,
  bundle: LoadedBundle,
): void {
  if (!url) {
    removeStylesheet(bundle)
    bundle.stylesheetUrl = undefined
    bundle.stylesheetEl = undefined
    return
  }
  if (typeof document === 'undefined') return // SSR / test guard
  if (bundle.stylesheetEl && bundle.stylesheetUrl === url && bundle.stylesheetEl.isConnected) {
    return
  }
  bundle.stylesheetEl = injectStylesheet(pluginId, url)
  bundle.stylesheetUrl = url
}

function removeStylesheet(bundle: LoadedBundle): void {
  const el = bundle.stylesheetEl
  if (el && el.isConnected) el.remove()
  bundle.stylesheetEl = undefined
}

function cssEscape(value: string): string {
  // Minimal CSS attribute-selector escape: only the characters we expect in
  // plugin IDs can already include `-`; this guards anything else.
  return value.replace(/["\\]/g, '\\$&')
}
