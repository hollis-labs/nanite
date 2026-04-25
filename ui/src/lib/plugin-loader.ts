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

// J.3: verbose dev-mode logging. In prod, devLog is a dead no-op so calls
// cost nothing. In dev, each major loader transition gets a single line at
// info level so plugin authors can follow what the registry sync is doing.
const devLog: (...args: unknown[]) => void =
  import.meta.env.DEV
    ? // eslint-disable-next-line no-console
      (...args) => console.info('[plugin-loader]', ...args)
    : () => {}

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
 * The registry-declared bundle URL we currently want loaded per plugin,
 * keyed by plugin id and storing the post-cache-bust "effective" URL.
 * Updated synchronously at the start of each syncPluginRegistry call;
 * in-flight loads check this before committing so a load that started
 * under an older registry view cannot re-register a plugin the latest
 * view has dropped or moved.
 */
const desiredBundles = new Map<string, string>()

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

/** Snapshot of all current plugin load errors, sorted by plugin id. */
export function getPluginLoadErrors(): Array<{ pluginId: string; reason: string }> {
  return Array.from(loadErrors, ([pluginId, reason]) => ({ pluginId, reason })).sort(
    (a, b) => a.pluginId.localeCompare(b.pluginId),
  )
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
  desiredBundles.clear()
  loadedBundles.clear()
  inflight.clear()
  notify()
}

/**
 * Build the effective import URL for a plugin bundle. When the registry
 * carries a `bundle_hash`, append it as a query param so URL-keyed ESM
 * module caches pick up re-installs/updates that keep the same canonical
 * path (e.g. `/api/plugins/{id}/ui/dist/index.js`). When no hash is
 * available yet, fall back to the raw URL — reinstalls at the same path
 * will continue to be cached until the hash is populated.
 */
function effectiveBundleUrl(plugin: PluginRegistryPlugin): string {
  const url = plugin.bundle_url ?? ''
  if (!url || !plugin.bundle_hash) return url
  const sep = url.includes('?') ? '&' : '?'
  return `${url}${sep}v=${encodeURIComponent(plugin.bundle_hash)}`
}

/**
 * React accepts more than plain functions as valid element types —
 * React.memo and React.forwardRef both return objects. Accept anything
 * truthy and either callable or an object; the renderer (Suspense + error
 * boundary) will surface a sensible error if the export is still invalid.
 */
function isComponentLike(value: unknown): boolean {
  return typeof value === 'function' || (typeof value === 'object' && value !== null)
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
  devLog('syncPluginRegistry: plugins=', Object.keys(data.plugins).length,
    'envelopes=', Object.keys(data.envelopes).length,
    'widgets=', Object.keys(data.widgets).length,
    'slots=', Object.values(data.slots).reduce((n, arr) => n + arr.length, 0))
  // Rebuild the envelope-type → plugin-id map from the registry response.
  // Done unconditionally so consumers can resolve ownership even when a
  // plugin's bundle later fails to load.
  envelopeTypeToPluginId.clear()
  for (const [type, env] of Object.entries(data.envelopes)) {
    envelopeTypeToPluginId.set(type, env.plugin_id)
  }

  // Snapshot the desired (pluginId → effective URL) view synchronously
  // before any awaits. In-flight loads validate against this map before
  // committing so a newer sync that drops or re-URLs a plugin wins over
  // older loads whose imports resolve out of order.
  desiredBundles.clear()
  const desired = new Set<string>()
  const loads: Array<Promise<void>> = []

  for (const [pluginId, plugin] of Object.entries(data.plugins)) {
    if (!plugin.bundle_url) continue
    const effectiveUrl = effectiveBundleUrl(plugin)
    desired.add(pluginId)
    desiredBundles.set(pluginId, effectiveUrl)

    const current = loadedBundles.get(pluginId)
    if (current && current.bundleUrl === effectiveUrl) {
      // Already loaded at this URL — stylesheet + registrations still valid.
      ensureStylesheet(pluginId, plugin.stylesheet_url, current)
      continue
    }
    if (current) {
      devLog('reload:', pluginId, 'url changed', current.bundleUrl, '→', effectiveUrl)
      unloadPlugin(pluginId)
    } else {
      devLog('load:', pluginId, effectiveUrl)
    }
    loads.push(loadPluginBundle(pluginId, plugin, effectiveUrl, data))
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
  effectiveUrl: string,
  data: PluginRegistryResponse,
): Promise<void> {
  if (!effectiveUrl) return Promise.resolve()

  // Key inflight entries on (pluginId, effectiveUrl) so a later sync that
  // retargets the same plugin to a different URL starts a new import
  // instead of adopting the old in-flight promise.
  const inflightKey = `${pluginId}::${effectiveUrl}`
  const existing = inflight.get(inflightKey)
  if (existing) return existing

  const promise = (async () => {
    // biome-ignore lint/suspicious/noExplicitAny: plugin module shape is dynamic
    let mod: Record<string, any>
    try {
      mod = (await import(/* @vite-ignore */ effectiveUrl)) as Record<string, any>
    } catch (err) {
      // Only record the error if this load is still wanted. A load that
      // raced against an unload/retarget should not poison the error state
      // for the newer desired bundle.
      if (desiredBundles.get(pluginId) === effectiveUrl) {
        const reason = err instanceof Error ? err.message : String(err)
        console.error(
          `[plugin-loader] Failed to import bundle for "${pluginId}" from ${effectiveUrl}:`,
          err,
        )
        loadErrors.set(pluginId, reason)
      }
      throw err
    }

    // The registry may have moved on while we awaited. Drop the load if
    // this plugin is no longer desired at this URL.
    if (desiredBundles.get(pluginId) !== effectiveUrl) {
      return
    }

    const bundle: LoadedBundle = {
      bundleUrl: effectiveUrl,
      ...(plugin.stylesheet_url ? { stylesheetUrl: plugin.stylesheet_url } : {}),
      envelopeTypes: new Set(),
      widgetIds: new Set(),
      slotComponentNames: new Set(),
    }

    // Envelopes for this plugin.
    for (const [type, env] of Object.entries(data.envelopes)) {
      if (env.plugin_id !== pluginId) continue
      const comp = mod[env.component]
      if (!isComponentLike(comp)) {
        console.warn(
          `[plugin-loader] Plugin "${pluginId}" bundle is missing named export "${env.component}" for envelope "${type}" — skipping`,
        )
        continue
      }
      dynamicEnvelopes.set(type, { component: wrapComponent(comp), source: pluginId })
      bundle.envelopeTypes.add(type)
    }

    // Widgets for this plugin. The registry carries only `name` (the
    // widget ID), which is assumed to match a JS export on the bundle.
    // This asymmetry with envelopes is tracked: the registry should
    // eventually carry an explicit `export` field so IDs can contain
    // hyphens without breaking lookup.
    // Fallback: try PascalCase(widget.name) when the direct name lookup fails
    // (e.g. "mux-activity-widget" → "MuxActivityWidget"). Bundles targeting
    // ES2020 can't use string-named exports, so PascalCase is the only viable
    // convention for kebab-case widget IDs.
    for (const [id, widget] of Object.entries(data.widgets)) {
      if (widget.plugin_id !== pluginId) continue
      const pascalName = widget.name
        .split('-')
        .map((s: string) => s.charAt(0).toUpperCase() + s.slice(1))
        .join('')
      const comp = mod[widget.name] ?? mod[pascalName]
      if (!isComponentLike(comp)) {
        console.warn(
          `[plugin-loader] Plugin "${pluginId}" bundle is missing named export "${widget.name}" (or "${pascalName}") for widget "${id}" — skipping`,
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
        if (!isComponentLike(comp)) {
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
    if (inflight.get(inflightKey) === tracked) inflight.delete(inflightKey)
  })
  inflight.set(inflightKey, tracked)
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
    // Compare the attribute directly — `existing.href` is a resolved
    // absolute URL, so `existing.href !== url` would always fire for the
    // relative paths we get from the registry.
    if (existing.getAttribute('href') !== url) existing.href = url
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

// ---------------------------------------------------------------------------
// J.3 developer-mode affordances.
//
// In dev builds (`import.meta.env.DEV`), attach inspection + reload helpers to
// `window` so plugin authors can poke at the registry and trigger reloads
// from devtools. Prod builds skip this entirely so no debug surface leaks.
// ---------------------------------------------------------------------------

/** Snapshot of the dynamic registry for dev tooling. */
export interface PluginRegistrySnapshot {
  envelopes: string[]
  widgets: string[]
  slotComponents: string[]
  loadedPlugins: Array<{
    id: string
    bundleUrl: string
    stylesheetUrl?: string
    envelopeTypes: string[]
    widgetIds: string[]
    slotComponentNames: string[]
  }>
  loadErrors: Array<{ pluginId: string; reason: string }>
}

function snapshotDynamicRegistry(): PluginRegistrySnapshot {
  const loadedPlugins: PluginRegistrySnapshot['loadedPlugins'] = []
  for (const [id, bundle] of loadedBundles) {
    loadedPlugins.push({
      id,
      bundleUrl: bundle.bundleUrl,
      stylesheetUrl: bundle.stylesheetUrl,
      envelopeTypes: [...bundle.envelopeTypes],
      widgetIds: [...bundle.widgetIds],
      slotComponentNames: [...bundle.slotComponentNames],
    })
  }
  return {
    envelopes: [...dynamicEnvelopes.keys()],
    widgets: [...dynamicWidgets.keys()],
    slotComponents: [...dynamicSlotComponents.keys()],
    loadedPlugins,
    loadErrors: getPluginLoadErrors(),
  }
}

/**
 * Trigger a server-side reload of `pluginId` and refetch the registry so the
 * frontend picks up the new bundle. Returns the reload response.
 *
 * Exposed as `window.__nanite_reloadPlugin` in dev builds so authors can
 * iterate without restarting the service or fighting the Plugin Manager UI.
 */
async function reloadPluginFromBrowser(pluginId: string): Promise<unknown> {
  const resp = await fetch('/api/plugins/reload', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name: pluginId }),
  })
  const body = await resp.json().catch(() => ({}))
  if (!resp.ok) {
    throw new Error(`reload ${pluginId}: ${resp.status} ${JSON.stringify(body)}`)
  }
  // Nudge the loader to drop the old bundle so syncPluginRegistry's next run
  // re-imports with the updated bundle_hash.
  const prev = loadedBundles.get(pluginId)
  if (prev) {
    dynamicEnvelopes.forEach((e, t) => {
      if (e.source === pluginId) dynamicEnvelopes.delete(t)
    })
    dynamicWidgets.forEach((w, id) => {
      if (w.source === pluginId) dynamicWidgets.delete(id)
    })
    dynamicSlotComponents.forEach((s, n) => {
      if (s.source === pluginId) dynamicSlotComponents.delete(n)
    })
    removeStylesheet(prev)
    loadedBundles.delete(pluginId)
    notify()
  }
  return body
}

/**
 * Install dev-only helpers on `window`. Safe to call multiple times; later
 * calls overwrite earlier references (useful across Vite HMR reloads).
 *
 * Attaches:
 *   - `window.__nanite_reloadPlugin(id)` → triggers server reload + UI drop.
 *   - `window.__nanite_pluginRegistry()` → returns a snapshot of the
 *     in-memory registry state (callable so it's always fresh).
 */
export function installPluginDevHelpers(): void {
  if (!import.meta.env.DEV) return
  if (typeof window === 'undefined') return
  // biome-ignore lint/suspicious/noExplicitAny: window is dynamically extended
  const w = window as any
  w.__nanite_reloadPlugin = reloadPluginFromBrowser
  w.__nanite_pluginRegistry = snapshotDynamicRegistry
  // Best-effort banner so authors know these are live. Keep it cheap — one
  // log on install, not per snapshot call.
  if (!w.__nanite_devHelpersAnnounced) {
    // eslint-disable-next-line no-console
    console.info(
      '[nanite/plugins] dev helpers installed: window.__nanite_reloadPlugin(id), window.__nanite_pluginRegistry()',
    )
    w.__nanite_devHelpersAnnounced = true
  }
}
