import { useQuery } from '@tanstack/react-query'
import { ScrollArea } from '@/components/ui/ScrollArea'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useSettings } from '@/hooks/useSettings'
import { WidgetRenderer } from './widgets/WidgetRenderer'
import { DEFAULT_WIDGET_ORDER } from '@/generated/plugin-widgets'
import { api } from '@/lib/api'
import type { PluginUIComponent } from '@/lib/types'

export function RightRail() {
  const open = useLayoutStore((s) => s.rightRailOpen)
  const { data: settings } = useSettings()
  const recoverMode = settings?.recover_mode ?? false

  // Fetch registered widget components from the plugin system.
  const { data: components = [] } = useQuery({
    queryKey: ['plugin-ui-components'],
    queryFn: api.listUIComponents,
    staleTime: 60_000,
  })

  // Filter to widget type only.
  const allWidgets = components.filter((c) => c.type === 'widget')

  // Build a lookup map for quick access.
  const widgetMap = new Map<string, PluginUIComponent>()
  for (const w of allWidgets) {
    widgetMap.set(w.id, w)
  }

  // Read user preferences from ext_settings.
  const visibility = settings?.ext_settings?.widget_visibility
  const userOrder = settings?.ext_settings?.widget_order

  // Determine display order: user preference > default order > any remaining API widgets.
  const baseOrder = userOrder?.length ? userOrder : DEFAULT_WIDGET_ORDER
  const seen = new Set<string>()
  const orderedIds: string[] = []

  // Add widgets in preferred order.
  for (const id of baseOrder) {
    if (widgetMap.has(id) || DEFAULT_WIDGET_ORDER.includes(id)) {
      orderedIds.push(id)
      seen.add(id)
    }
  }

  // Append any newly registered widgets not in the order list (unless in recover mode).
  if (!recoverMode) {
    for (const w of allWidgets) {
      if (!seen.has(w.id)) {
        orderedIds.push(w.id)
        seen.add(w.id)
      }
    }
  }

  // Apply visibility filter. Default: visible unless explicitly hidden.
  const visibleIds = orderedIds.filter((id) => {
    if (!visibility) return true
    return visibility[id] !== false
  })

  // Build final render list. For core widgets not yet in the API (during startup race),
  // create synthetic entries so the registry can still render them.
  const renderList: PluginUIComponent[] = visibleIds.map((id) => {
    return widgetMap.get(id) ?? {
      id,
      type: 'widget' as const,
      name: id,
      description: '',
    }
  })

  return (
    <aside
      className={`h-full bg-zinc-950 border-l border-zinc-800 flex flex-col transition-all duration-200 ease-in-out overflow-hidden ${
        open ? 'w-96' : 'w-0'
      }`}
    >
      <div className="min-w-96 h-full flex flex-col overflow-hidden">
        {/* Header */}
        <div className="px-4 py-3 border-b border-zinc-800 shrink-0">
          <h2 className="text-sm font-semibold text-zinc-100">Widgets</h2>
        </div>

        {/* Widget cards */}
        <ScrollArea className="flex-1 min-h-0">
          <div className="p-3 space-y-3">
            {renderList.map((w) => (
              <WidgetRenderer key={w.id} component={w} />
            ))}
          </div>
        </ScrollArea>
      </div>
    </aside>
  )
}
