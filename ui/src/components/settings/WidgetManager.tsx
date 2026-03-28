import { useState, useCallback } from 'react'
import { useQuery } from '@tanstack/react-query'
import { GripVertical, Eye, EyeOff, Puzzle, Settings2 } from 'lucide-react'
import { api } from '@/lib/api'
import { useSettings, useSettingsMutation } from '@/hooks/useSettings'
import { PluginConfigPanel } from './PluginConfigPanel'
import { DEFAULT_WIDGET_ORDER, isValidWidgetId } from '@/generated/plugin-widgets'
import type { PluginUIComponent } from '@/lib/types'

export function WidgetManager() {
  const { data: settings } = useSettings()
  const mutation = useSettingsMutation()

  const { data: components = [] } = useQuery({
    queryKey: ['plugin-ui-components'],
    queryFn: api.listUIComponents,
    staleTime: 60_000,
  })

  const widgets = components.filter((c) => c.type === 'widget')
  const widgetMap = new Map<string, PluginUIComponent>()
  for (const w of widgets) {
    widgetMap.set(w.id, w)
  }

  // Current preferences.
  const visibility = settings?.ext_settings?.widget_visibility ?? {}
  const savedOrder = settings?.ext_settings?.widget_order ?? []

  // Build ordered list: saved order first, then any new widgets not yet in the order.
  const baseOrder = savedOrder.length ? savedOrder : DEFAULT_WIDGET_ORDER
  const seen = new Set<string>()
  const orderedIds: string[] = []
  for (const id of baseOrder) {
    if (widgetMap.has(id) || DEFAULT_WIDGET_ORDER.includes(id)) {
      orderedIds.push(id)
      seen.add(id)
    }
  }
  for (const w of widgets) {
    if (!seen.has(w.id)) {
      orderedIds.push(w.id)
    }
  }

  // Plugin config panel state.
  const [configuringPluginId, setConfiguringPluginId] = useState<string | null>(null)

  // Drag state.
  const [dragIdx, setDragIdx] = useState<number | null>(null)

  const isVisible = (id: string) => visibility[id] !== false

  const savePreferences = useCallback(
    (newVisibility: Record<string, boolean>, newOrder: string[]) => {
      mutation.mutate({
        ext_settings: {
          widget_visibility: newVisibility,
          widget_order: newOrder,
        },
      })
    },
    [mutation],
  )

  const toggleVisibility = (id: string) => {
    const next = { ...visibility, [id]: !isVisible(id) }
    savePreferences(next, orderedIds)
  }

  const handleDragStart = (idx: number) => {
    setDragIdx(idx)
  }

  const handleDragOver = (e: React.DragEvent, idx: number) => {
    e.preventDefault()
    if (dragIdx === null || dragIdx === idx) return
    const reordered = [...orderedIds]
    const [moved] = reordered.splice(dragIdx, 1)
    reordered.splice(idx, 0, moved)
    // We don't save on every drag-over — just update visual via state.
    // Save happens on drop.
    setDragIdx(idx)
    // Optimistically update order in settings.
    savePreferences(visibility, reordered)
  }

  const handleDragEnd = () => {
    setDragIdx(null)
  }

  // Show plugin config panel when a plugin is selected.
  if (configuringPluginId) {
    return (
      <PluginConfigPanel
        pluginId={configuringPluginId}
        pluginName={configuringPluginId}
        onBack={() => setConfiguringPluginId(null)}
      />
    )
  }

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-lg font-semibold text-zinc-100 mb-1">Widgets</h2>
        <p className="text-sm text-zinc-400 mb-4">
          Toggle visibility and drag to reorder right-rail widgets.
        </p>
      </div>

      <div className="space-y-2">
        {orderedIds.map((id, idx) => {
          if (!isValidWidgetId(id)) return null
          const meta = widgetMap.get(id)
          const visible = isVisible(id)

          return (
            <div
              key={id}
              draggable
              onDragStart={() => handleDragStart(idx)}
              onDragOver={(e) => handleDragOver(e, idx)}
              onDragEnd={handleDragEnd}
              className={`flex items-center gap-3 px-3 py-2.5 rounded-lg border transition-colors ${
                visible
                  ? 'border-zinc-800 bg-zinc-900/50'
                  : 'border-zinc-800/50 bg-zinc-950 opacity-60'
              } ${dragIdx === idx ? 'ring-1 ring-indigo-500/50' : ''}`}
            >
              <GripVertical className="w-4 h-4 text-zinc-600 cursor-grab shrink-0" />

              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium text-zinc-200 truncate">
                    {meta?.name ?? id}
                  </span>
                  {meta?.plugin_id && (
                    <span className="text-[10px] px-1.5 py-0.5 rounded bg-indigo-950/60 text-indigo-400 flex items-center gap-1">
                      <Puzzle className="w-2.5 h-2.5" />
                      {meta.plugin_id}
                    </span>
                  )}
                </div>
                {meta?.description && (
                  <p className="text-[11px] text-zinc-500 truncate mt-0.5">{meta.description}</p>
                )}
              </div>

              {meta?.plugin_id && (
                <button
                  onClick={() => setConfiguringPluginId(meta.plugin_id!)}
                  className="p-1.5 rounded hover:bg-zinc-800 transition-colors"
                  title={`Configure ${meta.plugin_id} plugin`}
                >
                  <Settings2 className="w-4 h-4 text-zinc-500" />
                </button>
              )}

              <button
                onClick={() => toggleVisibility(id)}
                className="p-1.5 rounded hover:bg-zinc-800 transition-colors"
                title={visible ? 'Hide widget' : 'Show widget'}
              >
                {visible ? (
                  <Eye className="w-4 h-4 text-zinc-400" />
                ) : (
                  <EyeOff className="w-4 h-4 text-zinc-600" />
                )}
              </button>
            </div>
          )
        })}
      </div>

      {orderedIds.length === 0 && (
        <div className="text-center py-8 text-zinc-500 text-sm">
          No widgets registered. Plugins can register widgets via the plugin system.
        </div>
      )}
    </div>
  )
}
