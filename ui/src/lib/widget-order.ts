import { DEFAULT_WIDGET_ORDER, DEVELOPER_ONLY_WIDGETS } from '@/generated/plugin-widgets'
import type { PluginUIComponent } from '@/lib/types'

/**
 * Build the canonical widget order by merging:
 * 1. User's saved order (or DEFAULT_WIDGET_ORDER if none saved)
 * 2. Any DEFAULT_WIDGET_ORDER entries missing from saved order (newly added core widgets)
 * 3. Any API-registered widgets not yet in the list (plugin widgets)
 *
 * This ensures that new widgets — whether core or plugin — always appear for
 * users who already have a saved widget_order preference.
 */
export function buildWidgetOrder(
  savedOrder: string[] | undefined,
  apiWidgets: PluginUIComponent[],
  opts?: { skipPluginAppend?: boolean },
): string[] {
  const widgetIds = new Set(apiWidgets.map((w) => w.id))
  const baseOrder = savedOrder?.length ? savedOrder : DEFAULT_WIDGET_ORDER
  const seen = new Set<string>()
  const result: string[] = []

  // 1. Saved/default order — include if known to registry or default list
  for (const id of baseOrder) {
    if (widgetIds.has(id) || DEFAULT_WIDGET_ORDER.includes(id)) {
      result.push(id)
      seen.add(id)
    }
  }

  // 2. Backfill core widgets missing from saved order
  for (const id of DEFAULT_WIDGET_ORDER) {
    if (!seen.has(id)) {
      result.push(id)
      seen.add(id)
    }
  }

  // 3. Append new plugin widgets not yet in the order
  if (!opts?.skipPluginAppend) {
    for (const w of apiWidgets) {
      if (!seen.has(w.id)) {
        result.push(w.id)
        seen.add(w.id)
      }
    }
  }

  return result
}

/** Filter widget IDs based on developer_mode and per-widget visibility settings. */
export function filterVisibleWidgets(
  ids: string[],
  developerMode: boolean,
  visibility?: Record<string, boolean>,
): string[] {
  return ids.filter((id) => {
    if (DEVELOPER_ONLY_WIDGETS.has(id) && !developerMode) return false
    if (!visibility) return true
    return visibility[id] !== false
  })
}
