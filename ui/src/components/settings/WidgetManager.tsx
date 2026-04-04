import { useState, useCallback } from 'react'
import { useQuery } from '@tanstack/react-query'
import { GripVertical, Eye, EyeOff, LayoutGrid, Settings2 } from 'lucide-react'
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription } from '@/components/ui/empty'
import { api } from '@/lib/api'
import { useSettings, useSettingsMutation } from '@/hooks/useSettings'
import { PluginConfigPanel } from './PluginConfigPanel'
import { WidgetDetailView } from './WidgetDetailView'
import { DEVELOPER_ONLY_WIDGETS, isValidWidgetId } from '@/generated/plugin-widgets'
import { buildWidgetOrder } from '@/lib/widget-order'
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
  const savedOrder = settings?.ext_settings?.widget_order as string[] | undefined

  const orderedIds = buildWidgetOrder(savedOrder, widgets)

  const developerMode = settings?.developer_mode ?? false
  const displayIds = orderedIds.filter((id) => !DEVELOPER_ONLY_WIDGETS.has(id) || developerMode)

  // Plugin config panel state.
  const [configuringPluginId, setConfiguringPluginId] = useState<string | null>(null)
  // Widget detail view state.
  const [detailWidgetId, setDetailWidgetId] = useState<string | null>(null)

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
    // Reorder displayIds, then reconstruct full order preserving hidden widgets
    const reordered = [...displayIds]
    const [moved] = reordered.splice(dragIdx, 1)
    reordered.splice(idx, 0, moved)
    // Merge back: hidden (filtered-out) widgets keep their relative positions,
    // visible widgets use the new order.
    const hiddenIds = orderedIds.filter((id) => !displayIds.includes(id))
    const fullOrder = [...reordered, ...hiddenIds]
    setDragIdx(idx)
    savePreferences(visibility, fullOrder)
  }

  const handleDragEnd = () => {
    setDragIdx(null)
  }

  // Show widget detail view when a widget is selected.
  if (detailWidgetId) {
    const detailMeta = widgetMap.get(detailWidgetId)
    if (detailMeta) {
      return (
        <WidgetDetailView
          widget={detailMeta}
          widgetId={detailWidgetId}
          visible={isVisible(detailWidgetId)}
          onToggleVisibility={() => toggleVisibility(detailWidgetId)}
          onConfigurePlugin={(pluginId) => {
            setDetailWidgetId(null)
            setConfiguringPluginId(pluginId)
          }}
          onBack={() => setDetailWidgetId(null)}
        />
      )
    }
    // Fallback: widget not in map — render a safe fallback with a back button.
    return (
      <div className="p-4 text-sm text-fg-muted">
        <button onClick={() => setDetailWidgetId(null)} className="text-xs text-fg-secondary hover:text-fg">
          &larr; Back
        </button>
        <p className="mt-2">Widget &quot;{detailWidgetId}&quot; not found.</p>
      </div>
    )
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
    <div className="space-y-4">
      {/* Toolbar */}
      <div className="flex items-center gap-3">
        <p className="text-xs text-fg-muted">Drag to reorder. Toggle visibility with the eye icon.</p>
      </div>

      {displayIds.length === 0 ? (
        <Empty className="py-12">
          <EmptyHeader>
            <EmptyMedia variant="icon"><LayoutGrid /></EmptyMedia>
            <EmptyTitle className="text-sm">No widgets registered</EmptyTitle>
            <EmptyDescription className="text-xs">Plugins can register widgets via the plugin system</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <div className="grid gap-3 grid-cols-2">
          {displayIds.map((id, idx) => {
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
                className={`rounded-xl border shadow-sm overflow-hidden transition-all cursor-grab active:cursor-grabbing ${
                  visible
                    ? 'border-border-subtle bg-white dark:bg-bg-elevated/60'
                    : 'border-border bg-white dark:bg-bg/30 opacity-45'
                } ${dragIdx === idx ? 'ring-1 ring-accent/30 shadow-md' : ''}`}
              >
                {/* Header */}
                <div className="flex items-center gap-2.5 px-3.5 py-3">
                  <span className={`inline-flex items-center justify-center w-9 h-9 rounded-lg shrink-0 ${
                    visible ? 'bg-zinc-700 text-zinc-300' : 'bg-zinc-300 text-zinc-500'
                  }`}>
                    <GripVertical className="w-4 h-4" />
                  </span>
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <button
                        type="button"
                        onClick={(e) => { e.stopPropagation(); setDetailWidgetId(id) }}
                        className={`text-sm font-semibold truncate hover:underline text-left ${visible ? 'text-fg' : 'text-fg-muted'}`}
                      >
                        {meta?.name ?? id}
                      </button>
                      {visible && <span className="w-1.5 h-1.5 rounded-full bg-success shrink-0" />}
                    </div>
                    {meta?.plugin_id && (
                      <span className="text-[11px] text-fg-muted truncate block mt-0.5">{meta.plugin_id}</span>
                    )}
                  </div>
                  <div className="flex items-center gap-0.5 shrink-0">
                    {meta?.plugin_id && (
                      <button
                        onClick={(e) => { e.stopPropagation(); setConfiguringPluginId(meta.plugin_id!) }}
                        className="p-1.5 rounded text-fg-faint hover:text-fg-secondary hover:bg-surface transition-colors"
                        title="Configure plugin"
                      >
                        <Settings2 className="w-3.5 h-3.5" />
                      </button>
                    )}
                    <button
                      onClick={(e) => { e.stopPropagation(); toggleVisibility(id) }}
                      className="p-1.5 rounded text-fg-faint hover:text-fg-secondary hover:bg-surface transition-colors"
                      title={visible ? 'Hide widget' : 'Show widget'}
                    >
                      {visible ? (
                        <Eye className="w-3.5 h-3.5" />
                      ) : (
                        <EyeOff className="w-3.5 h-3.5" />
                      )}
                    </button>
                  </div>
                </div>

                {/* Detail footer */}
                {meta?.description && (
                  <div className="border-t border-border/50 px-3.5 py-2 bg-bg-elevated/40">
                    <p className="text-[11px] text-fg-muted line-clamp-2">{meta.description}</p>
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
