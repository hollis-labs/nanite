/**
 * PanelManager — settings panel for right-rail v2 panel management (J9).
 *
 * Lets the user:
 *   - Toggle panel visibility (show/hide from tab strip)
 *   - Set default panel (opened on fresh right-rail open)
 *   - Reorder panels via drag-and-drop
 *
 * Lives at Settings > Extensions > Panels.
 *
 * CW-20260426-0007
 */

import { useCallback, useState } from 'react'
import { GripVertical, Eye, EyeOff, Star, StarOff, Layers } from 'lucide-react'
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription } from '@/components/ui/empty'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { usePanelRegistry } from '@/hooks/usePanelRegistry'

export function PanelManager() {
  const panelPrefs = useLayoutStore((s) => s.panelPrefs)
  const setPanelEnabled = useLayoutStore((s) => s.setPanelEnabled)
  const setDefaultPanel = useLayoutStore((s) => s.setDefaultPanel)
  const setPanelOrder = useLayoutStore((s) => s.setPanelOrder)

  const { panels, orderedIds } = usePanelRegistry()

  // Drag state
  const [dragIdx, setDragIdx] = useState<number | null>(null)
  const [dragOrder, setDragOrder] = useState<string[] | null>(null)

  const effectiveOrder = dragOrder ?? orderedIds

  const isPanelEnabled = useCallback((id: string) => {
    const def = panels[id]
    if (!def) return false
    if (def.source === 'builtin') return panelPrefs.panelEnabled[id] !== false
    return panelPrefs.panelEnabled[id] === true || def.defaultVisible === true
  }, [panels, panelPrefs.panelEnabled])

  const handleDragStart = (idx: number) => {
    setDragIdx(idx)
    setDragOrder(null)
  }

  const handleDragOver = (e: React.DragEvent, idx: number) => {
    e.preventDefault()
    if (dragIdx === null || dragIdx === idx) return
    const base = dragOrder ?? orderedIds
    const reordered = [...base]
    const [moved] = reordered.splice(dragIdx, 1)
    reordered.splice(idx, 0, moved)
    setDragIdx(idx)
    setDragOrder(reordered)
  }

  const handleDragEnd = () => {
    if (dragOrder) {
      setPanelOrder(dragOrder)
    }
    setDragIdx(null)
    setDragOrder(null)
  }

  if (orderedIds.length === 0) {
    return (
      <Empty className="py-12">
        <EmptyHeader>
          <EmptyMedia variant="icon"><Layers /></EmptyMedia>
          <EmptyTitle className="text-sm">No panels registered</EmptyTitle>
          <EmptyDescription className="text-xs">
            Built-in panels register on first open of the right rail
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <p className="text-xs text-fg-muted">
          Drag to reorder panels. Toggle visibility or set a default.
        </p>
      </div>

      <div className="space-y-2">
        {effectiveOrder.map((id, idx) => {
          const def = panels[id]
          if (!def) return null
          const Icon = def.icon ?? Layers
          const enabled = isPanelEnabled(id)
          const isDefault = panelPrefs.defaultPanel === id

          return (
            <div
              key={id}
              draggable
              onDragStart={() => handleDragStart(idx)}
              onDragOver={(e) => handleDragOver(e, idx)}
              onDragEnd={handleDragEnd}
              className={`flex items-center gap-2.5 px-3.5 py-3 rounded-xl border cursor-grab active:cursor-grabbing transition-all ${
                enabled
                  ? 'border-border-subtle bg-bg-elevated border-l-2 border-l-status-ok'
                  : 'border-border bg-bg/30 opacity-50 border-l-2 border-l-fg-faint'
              } ${dragIdx === idx ? 'ring-1 ring-brand/30' : ''}`}
            >
              <GripVertical className="w-4 h-4 text-fg-faint shrink-0" />

              <span className={`inline-flex items-center justify-center w-8 h-8 rounded-lg shrink-0 ${
                enabled ? 'bg-surface text-fg-secondary' : 'bg-surface text-fg-muted'
              }`}>
                <Icon className="w-4 h-4" />
              </span>

              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2">
                  <span className={`text-sm font-semibold truncate ${enabled ? 'text-fg' : 'text-fg-muted'}`}>
                    {def.label}
                  </span>
                  {isDefault && (
                    <span className="text-[10px] bg-primary/10 text-primary px-1.5 py-0.5 rounded font-medium">
                      default
                    </span>
                  )}
                </div>
                <span className="text-[11px] text-fg-faint">
                  {def.source === 'builtin' ? 'built-in' : `plugin: ${def.pluginId ?? id}`}
                </span>
              </div>

              <div className="flex items-center gap-0.5 shrink-0">
                <button
                  type="button"
                  onClick={() => setDefaultPanel(isDefault ? '' : id)}
                  className="p-1.5 rounded text-fg-faint hover:text-fg-secondary hover:bg-surface transition-colors"
                  title={isDefault ? 'Remove as default' : 'Set as default panel'}
                >
                  {isDefault
                    ? <Star className="w-3.5 h-3.5 text-primary fill-primary" />
                    : <StarOff className="w-3.5 h-3.5" />}
                </button>
                <button
                  type="button"
                  onClick={() => setPanelEnabled(id, !enabled)}
                  className="p-1.5 rounded text-fg-faint hover:text-fg-secondary hover:bg-surface transition-colors"
                  title={enabled ? 'Hide panel' : 'Show panel'}
                >
                  {enabled ? <Eye className="w-3.5 h-3.5" /> : <EyeOff className="w-3.5 h-3.5" />}
                </button>
              </div>
            </div>
          )
        })}
      </div>

      <p className="text-[11px] text-fg-faint pt-2">
        Plugins can contribute additional panels via the plugin manifest{' '}
        <code className="font-mono">panels</code> field.
      </p>
    </div>
  )
}
