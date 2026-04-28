/**
 * usePanelRegistry — J9 right-rail v2 panel registration API.
 *
 * All surfaces (built-ins, plugins, J8 agent calls) register panels through
 * this module. The registry is a singleton held in-memory; panels are declared
 * at module-load / component-mount time and persist for the session.
 *
 * Design: Tabs layout, one-at-a-time visibility. Rationale: the existing
 * right-rail already used tabs; tabs give a clear active-state signal that J8
 * needs for predictable panel_open/panel_close semantics. Docked stacking and
 * dropdown variants are v2 follow-ups.
 *
 * J8 seams exposed by this module:
 *   openPanel(id)   — dispatch open; J8 calls this from panel_open tool
 *   closePanel(id)  — dispatch close; J8 calls this from panel_close tool
 *   onDismiss(id)   — J8 registers a dismiss listener to enforce dismiss policy
 *   activePanel     — current panel ID for mode-signal preset handler
 *
 * CW-20260426-0007
 */

import { create } from 'zustand'
import type { LucideIcon } from 'lucide-react'
import { LayoutGrid } from 'lucide-react'

// ---- Types ----------------------------------------------------------------

/** A registered panel definition. */
export interface PanelDef {
  /** Stable identifier — must be unique across all panels. */
  id: string
  /** Human-readable label shown in the tab strip. */
  label: string
  /** Lucide icon component. Defaults to LayoutGrid when omitted. */
  icon?: LucideIcon
  /**
   * Whether this panel is a built-in (shipped by the host) vs plugin-contributed.
   * Plugin panels use "plugin"; built-ins use "builtin".
   */
  source: 'builtin' | 'plugin'
  /**
   * Plugin ID for plugin-contributed panels. Empty for built-ins.
   * Used by permission checks and unload cleanup.
   */
  pluginId?: string
  /**
   * Optional sort order hint. Lower values appear first.
   * Built-in panels default to 0–49; plugin panels default to 100+.
   */
  order?: number
  /**
   * Whether this panel is visible by default (before user prefs apply).
   * Built-in panels default to true; plugin panels default to false.
   */
  defaultVisible?: boolean
}

// ---- Registry store (in-memory, not persisted) ----------------------------

interface PanelRegistryState {
  /** All registered panel definitions, keyed by id. */
  panels: Record<string, PanelDef>

  /** Register a panel. Idempotent — re-registration updates the definition. */
  register: (def: PanelDef) => void

  /** Unregister a panel by id. Used on plugin unload. */
  unregister: (id: string) => void

  /** Unregister all panels contributed by a plugin. */
  unregisterPlugin: (pluginId: string) => void

  /**
   * Ordered list of panel IDs as they should appear in the tab strip,
   * after sorting by `order` and user-preference ordering is applied.
   * Re-computed on every register/unregister.
   */
  orderedIds: string[]
}

function computeOrderedIds(panels: Record<string, PanelDef>): string[] {
  return Object.values(panels)
    .sort((a, b) => (a.order ?? 100) - (b.order ?? 100))
    .map((p) => p.id)
}

export const usePanelRegistryStore = create<PanelRegistryState>()((set) => ({
  panels: {},
  orderedIds: [],

  register: (def) => {
    set((state) => {
      const panels = { ...state.panels, [def.id]: def }
      return { panels, orderedIds: computeOrderedIds(panels) }
    })
  },

  unregister: (id) => {
    set((state) => {
      const panels = { ...state.panels }
      delete panels[id]
      return { panels, orderedIds: computeOrderedIds(panels) }
    })
  },

  unregisterPlugin: (pluginId) => {
    set((state) => {
      const panels = Object.fromEntries(
        Object.entries(state.panels).filter(([, def]) => def.pluginId !== pluginId),
      )
      return { panels, orderedIds: computeOrderedIds(panels) }
    })
  },
}))

// ---- Dismiss listener registry (J8 seam) ----------------------------------

type DismissListener = (panelId: string) => void
const dismissListeners: Set<DismissListener> = new Set()

/**
 * J8 seam: register a listener that fires when the user manually dismisses a
 * panel (clicks close or switches away). J8 uses this to enforce its dismiss
 * policy — once a panel is user-dismissed, the agent cannot re-open it until
 * a new conversational trigger fires.
 */
export function onPanelDismiss(fn: DismissListener): () => void {
  dismissListeners.add(fn)
  return () => dismissListeners.delete(fn)
}

/** Internal: fire all dismiss listeners for a panel. */
export function emitPanelDismiss(panelId: string): void {
  dismissListeners.forEach((fn) => fn(panelId))
}

// ---- Open/close dispatch seam (J8 seam) -----------------------------------

/**
 * J8 seam: programmatic open a panel by ID.
 * J8's panel_open tool calls this. The call routes through the layout store so
 * the right-rail opens and the active tab switches atomically.
 *
 * Import lazily to avoid circular dep: useLayoutStore → usePanelRegistry.
 * Resolved at call time instead of module-load time.
 */
export function openPanel(id: string): void {
  // Lazy import to avoid circular reference: layout store imports nothing from
  // panel registry; panel registry references layout store at call time only.
  const { useLayoutStore } = require('@/stores/useLayoutStore') as typeof import('@/stores/useLayoutStore')
  useLayoutStore.getState().setPanelOpen(id)
}

/**
 * J8 seam: programmatic close of the active panel.
 * When `id` matches the active panel, closes the rail. When the rail shows a
 * different panel, this is a no-op (don't close something the user chose).
 */
export function closePanel(id: string): void {
  const { useLayoutStore } = require('@/stores/useLayoutStore') as typeof import('@/stores/useLayoutStore')
  const state = useLayoutStore.getState()
  if (state.rightRailTab === id) {
    useLayoutStore.getState().setRightRail(false)
  }
}

// ---- Convenience hook -----------------------------------------------------

/**
 * Returns the panel registry as a React hook for components that need to
 * iterate panel definitions (e.g. RightRailV2 tab strip, PanelManager settings).
 */
export function usePanelRegistry() {
  const panels = usePanelRegistryStore((s) => s.panels)
  const orderedIds = usePanelRegistryStore((s) => s.orderedIds)
  const register = usePanelRegistryStore((s) => s.register)
  const unregister = usePanelRegistryStore((s) => s.unregister)
  const unregisterPlugin = usePanelRegistryStore((s) => s.unregisterPlugin)
  return { panels, orderedIds, register, unregister, unregisterPlugin }
}

// ---- Fallback icon --------------------------------------------------------
export { LayoutGrid as DefaultPanelIcon }
