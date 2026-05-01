/**
 * RightRailV2 — multi-panel widget host (J9, CW-20260426-0007).
 *
 * Layout: tabbed — one active panel at a time. Rationale: matches the existing
 * right-rail UX; tabs give a clear active-state signal that J8's panel_open /
 * panel_close tools need for predictable open/close semantics. Multiple-stacked
 * (docked) and dropdown variants are v2 follow-ups.
 *
 * Registration: all panels (built-ins, plugins, J8 agent opens) go through
 * usePanelRegistry. Built-ins self-register below via useEffect on mount.
 *
 * Plugin panels: registered when usePanelRegistry receives declarations from
 * the plugin manifest `panels` field (wired by usePluginPanelSync, below).
 * The actual render function for plugin panels is a placeholder in v1 — real
 * rendering is a follow-up (plugin panel rendering deferral per J9 scope).
 *
 * J8 seams exposed:
 *   - layout store: setPanelOpen(id), setRightRail(false) for open/close
 *   - layout store: panelPrefs.dismissedByUser — J8 reads/clears
 *   - emitPanelDismiss(id) — fires on user tab switch / close (J8 dismiss policy)
 *
 * CW-20260426-0007
 */

import { Suspense, useEffect, useCallback, useState } from 'react'
import {
  LayoutGrid,
  Mail,
  Package,
  ListTodo,
  GitBranch,
  Pencil,
  GripVertical,
  Eye,
  EyeOff,
  Layers,
} from 'lucide-react'
import {
  DndContext,
  closestCenter,
  PointerSensor,
  KeyboardSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from '@dnd-kit/core'
import {
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
  arrayMove,
} from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import { Tooltip } from '@/components/ui/tooltip'
import { Skeleton } from '@/components/ui/skeleton'
import { useQuery } from '@tanstack/react-query'
import { ScrollArea } from '@/components/ui/scroll-area'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useSettings, useSettingsMutation } from '@/hooks/useSettings'
import { usePluginSlots } from '@/hooks/usePluginSlots'
import { resolveIcon } from '@/lib/icons'
import { getSlotComponent } from '@/lib/plugin-slot-lookup'
import type { UserSettings } from '@/lib/types'
import { WidgetRenderer } from './widgets/WidgetRenderer'
import { DEVELOPER_ONLY_WIDGETS } from '@/generated/plugin-widgets'
import { buildWidgetOrder, filterVisibleWidgets } from '@/lib/widget-order'
import { ArtifactsContent } from './drawers/ArtifactsContent'
import { InboxContent } from './messaging/InboxContent'
import { WorkTab } from './work/WorkTab'
import { WorkflowTab } from './workflows/WorkflowTab'
import { api } from '@/lib/api'
import type { PluginUIComponent } from '@/lib/types'
import {
  usePanelRegistry,
  emitPanelDismiss,
  type PanelDef,
} from '@/hooks/usePanelRegistry'

// ---- Built-in panel definitions -------------------------------------------

/**
 * Core panels registered by the host. Each has a stable id that J8 uses in
 * panel_open / panel_close tool calls.
 *
 * IDs must stay stable across releases — J8 references 'widgets', 'work',
 * 'workflows', 'inbox', 'artifacts' by ID.
 */
const BUILTIN_PANELS: PanelDef[] = [
  { id: 'widgets',   label: 'Widgets',   icon: LayoutGrid, source: 'builtin', order: 0,  defaultVisible: true  },
  { id: 'work',      label: 'Plan',      icon: ListTodo,   source: 'builtin', order: 10, defaultVisible: true  },
  { id: 'workflows', label: 'Workflows', icon: GitBranch,  source: 'builtin', order: 20, defaultVisible: true  },
  { id: 'inbox',     label: 'Inbox',     icon: Mail,       source: 'builtin', order: 30, defaultVisible: true  },
  { id: 'artifacts', label: 'Artifacts', icon: Package,    source: 'builtin', order: 40, defaultVisible: true  },
]

// ---- Widget-organize row (for edit mode) ----------------------------------

function SortableWidgetRow({
  id, name, isVisible, onToggleVisibility,
}: {
  id: string; name: string; isVisible: boolean; onToggleVisibility: () => void
}) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id })
  return (
    <div
      ref={setNodeRef}
      style={{ transform: CSS.Transform.toString(transform), transition, opacity: isDragging ? 0.5 : undefined }}
      className={`flex items-center gap-2 px-3 py-2 rounded-lg border border-border-subtle ${
        isVisible ? 'bg-bg-elevated' : 'bg-bg-elevated/20 opacity-50'
      }`}
    >
      <button
        type="button"
        className="p-0.5 text-fg-faint/50 hover:text-fg-muted cursor-grab active:cursor-grabbing touch-none"
        {...attributes}
        {...listeners}
      >
        <GripVertical className="size-3.5" />
      </button>
      <span className="text-xs text-fg flex-1 truncate">{name}</span>
      <Tooltip content={isVisible ? 'Hide widget' : 'Show widget'} side="left">
        <button
          type="button"
          onClick={onToggleVisibility}
          className="p-1 rounded text-fg-muted hover:text-fg transition-colors"
        >
          {isVisible ? <Eye className="size-3.5" /> : <EyeOff className="size-3.5" />}
        </button>
      </Tooltip>
    </div>
  )
}

// ---- Plugin panel sync hook -----------------------------------------------

/**
 * Syncs plugin-contributed right-rail-tab slots into the panel registry.
 * This bridges the existing slot system (used by legacy plugins) with the new
 * panel registry. New plugins should declare panels via the manifest `panels`
 * field instead (wired through the Go backend + api.listPanels).
 *
 * Plugin panel rendering in v1 = placeholder. Real render function is a
 * follow-up (plugin panel rendering deferral, CW-20260426-0007 scope note).
 */
function usePluginPanelSync() {
  const pluginTabs = usePluginSlots('right-rail-tab')
  const { register } = usePanelRegistry()

  useEffect(() => {
    // Register each plugin tab slot entry as a panel
    for (const entry of pluginTabs) {
      register({
        id: entry.id,
        label: entry.label ?? entry.id,
        icon: resolveIcon(entry.icon),
        source: 'plugin',
        pluginId: entry.id, // slot entry id doubles as plugin indicator
        order: 100 + (entry.priority ?? 0),
        defaultVisible: false,
      })
    }
    // Note: unregister on unmount is handled by the slot system. We don't
    // need to clean up here because usePanelRegistry entries persist and get
    // refreshed on the next usePluginSlots update cycle.
  }, [pluginTabs, register])

  return pluginTabs
}

// ---- Main component -------------------------------------------------------

interface RightRailV2Props {
  inboxAgentId?: string
}

export function RightRailV2({ inboxAgentId = 'file-default' }: RightRailV2Props) {
  const open = useLayoutStore((s) => s.rightRailOpen)
  const activeTab = useLayoutStore((s) => s.rightRailTab)
  const setRightRailTab = useLayoutStore((s) => s.setRightRailTab)
  const setPanelOpen = useLayoutStore((s) => s.setPanelOpen)
  const panelPrefs = useLayoutStore((s) => s.panelPrefs)
  const markPanelDismissed = useLayoutStore((s) => s.markPanelDismissed)

  const { panels, orderedIds, register, unregister: _unregister } = usePanelRegistry()
  const { data: settings } = useSettings()
  const settingsMutation = useSettingsMutation()
  const recoverMode = settings?.recover_mode ?? false
  const [editMode, setEditMode] = useState(false)

  // Dynamic title for artifacts preview
  const [artifactsTitle, setArtifactsTitle] = useState('Artifacts')
  const handleArtifactsTitleChange = useCallback((title: string) => {
    setArtifactsTitle(title)
  }, [])

  // Widget data (for the 'widgets' panel content)
  const { data: components = [] } = useQuery({
    queryKey: ['plugin-ui-components'],
    queryFn: api.listUIComponents,
    staleTime: 60_000,
  })

  const allWidgets = components.filter((c) => c.type === 'widget')
  const widgetMap = new Map<string, PluginUIComponent>()
  for (const w of allWidgets) {
    widgetMap.set(w.id, w)
  }

  const developerMode = settings?.developer_mode ?? false
  const visibility = settings?.ext_settings?.widget_visibility as Record<string, boolean> | undefined
  const userOrder = settings?.ext_settings?.widget_order as string[] | undefined
  const orderedWidgetIds = buildWidgetOrder(userOrder, allWidgets, { skipPluginAppend: recoverMode })
  const visibleWidgetIds = filterVisibleWidgets(orderedWidgetIds, developerMode, visibility)

  const renderWidgetList: PluginUIComponent[] = visibleWidgetIds.map((id) =>
    widgetMap.get(id) ?? { id, type: 'widget' as const, name: id, description: '' },
  )

  const toggleWidgetVisibility = useCallback((widgetId: string) => {
    const currentVisibility = (settings?.ext_settings?.widget_visibility ?? {}) as Record<string, boolean>
    const updated = { ...currentVisibility, [widgetId]: currentVisibility[widgetId] === false }
    settingsMutation.mutate({
      ext_settings: { ...settings?.ext_settings, widget_visibility: updated },
    } as Partial<UserSettings>)
  }, [settings?.ext_settings, settingsMutation])

  const dragSensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  )

  const handleWidgetDragEnd = useCallback((event: DragEndEvent) => {
    const { active, over } = event
    if (!over || active.id === over.id) return
    const editIds = orderedWidgetIds.filter((id) => !DEVELOPER_ONLY_WIDGETS.has(id) || developerMode)
    const oldIdx = editIds.indexOf(String(active.id))
    const newIdx = editIds.indexOf(String(over.id))
    if (oldIdx === -1 || newIdx === -1) return
    const reordered = arrayMove(editIds, oldIdx, newIdx)
    const merged = orderedWidgetIds.filter((id) => !editIds.includes(id))
    const newOrder = [...reordered, ...merged]
    settingsMutation.mutate({
      ext_settings: { ...settings?.ext_settings, widget_order: newOrder },
    } as Partial<UserSettings>)
  }, [orderedWidgetIds, developerMode, settings?.ext_settings, settingsMutation])

  // Register built-in panels on mount
  useEffect(() => {
    for (const def of BUILTIN_PANELS) {
      register(def)
    }
  }, [register])

  // Sync plugin slot tabs into the panel registry
  usePluginPanelSync()

  // Compute the visible panels list applying user enable prefs
  const visiblePanelIds = orderedIds.filter((id) => {
    const def = panels[id]
    if (!def) return false
    // Built-in panels: visible by default unless user explicitly disabled
    if (def.source === 'builtin') {
      return panelPrefs.panelEnabled[id] !== false
    }
    // Plugin panels: hidden by default unless user explicitly enabled
    return panelPrefs.panelEnabled[id] === true || def.defaultVisible === true
  })

  // Apply user custom ordering on top of registry order
  const userPanelOrder = panelPrefs.panelOrder
  const finalPanelIds: string[] = userPanelOrder.length > 0
    ? [
        ...userPanelOrder.filter((id) => visiblePanelIds.includes(id)),
        ...visiblePanelIds.filter((id) => !userPanelOrder.includes(id)),
      ]
    : visiblePanelIds

  // Resolve active panel — fall back to default or first visible
  const resolvedActiveTab: string = finalPanelIds.includes(activeTab)
    ? activeTab
    : panelPrefs.defaultPanel && finalPanelIds.includes(panelPrefs.defaultPanel)
      ? panelPrefs.defaultPanel
      : finalPanelIds[0] ?? 'widgets'

  // Handle user tab switch: emit dismiss for the previous tab, update store.
  // J8 v1 (CW-20260426-0006) — go through setPanelOpen with source='user' so
  // the 4-state dismiss machine attributes the new tab as user_opened (which
  // overrides the dismiss flag and refuses subsequent agent-driven closes).
  const handleTabSwitch = useCallback((id: string) => {
    const prev = resolvedActiveTab
    if (prev !== id) {
      emitPanelDismiss(prev)
      markPanelDismissed(prev)
    }
    setPanelOpen(id, 'user')
    // setRightRailTab kept as a fallback for code paths that need the old
    // signature (no source attribution); not strictly required after J8 v1
    // because setPanelOpen now atomically sets rail-open + tab.
    void setRightRailTab
  }, [resolvedActiveTab, setRightRailTab, setPanelOpen, markPanelDismissed])

  // Resolve display title
  const displayTitle = activeTab === 'artifacts'
    ? artifactsTitle
    : panels[resolvedActiveTab]?.label ?? resolvedActiveTab

  // Plugin slot tabs (for content rendering — backward compat)
  const pluginTabs = usePluginSlots('right-rail-tab')
  const pluginTabMap = new Map(pluginTabs.map((e) => [e.id, e]))

  return (
    <aside
      className={`h-full bg-bg border-l border-border flex flex-col transition-all duration-200 ease-in-out overflow-hidden ${
        open ? 'w-96' : 'w-0'
      }`}
    >
      <div className="min-w-96 h-full flex flex-col overflow-hidden relative">

        {/* Header */}
        <div className="px-4 h-[52px] flex items-center justify-between border-b border-border shrink-0">
          <h2 className="text-sm font-semibold text-fg truncate">{displayTitle}</h2>
          <div className="flex items-center gap-0.5">
            {resolvedActiveTab === 'widgets' && (
              <Tooltip content={editMode ? 'Done organizing' : 'Organize widgets'} side="bottom">
                <button
                  type="button"
                  onClick={() => setEditMode((e) => !e)}
                  className={`p-1.5 rounded transition-colors ${
                    editMode
                      ? 'text-primary bg-primary-muted'
                      : 'text-fg-faint hover:text-fg-secondary hover:bg-surface'
                  }`}
                >
                  <Pencil className="w-3.5 h-3.5" />
                </button>
              </Tooltip>
            )}
          </div>
        </div>

        {/* Tab strip — horizontal scroll when many panels */}
        {finalPanelIds.length > 1 && (
          <div className="flex border-b border-border shrink-0 overflow-x-auto no-scrollbar">
            {finalPanelIds.map((id) => {
              const def = panels[id]
              const Icon = def?.icon ?? Layers
              const isActive = id === resolvedActiveTab
              return (
                <button
                  key={id}
                  type="button"
                  onClick={() => handleTabSwitch(id)}
                  className={`flex items-center gap-1.5 px-3 py-2 text-xs whitespace-nowrap border-b-2 transition-colors shrink-0 ${
                    isActive
                      ? 'border-primary text-fg font-medium'
                      : 'border-transparent text-fg-muted hover:text-fg hover:bg-surface/50'
                  }`}
                >
                  <Icon className="w-3.5 h-3.5 shrink-0" />
                  <span>{def?.label ?? id}</span>
                </button>
              )
            })}
          </div>
        )}

        {/* Content panels — all kept mounted to avoid flash of loading state on tab switch */}

        {/* Widgets panel */}
        <div className={`flex-1 min-h-0 ${resolvedActiveTab === 'widgets' ? 'flex flex-col' : 'hidden'}`}>
          <ScrollArea className="flex-1 min-h-0">
            <div className="p-3 space-y-3">
              {editMode ? (
                (() => {
                  const editIds = orderedWidgetIds.filter((id) => !DEVELOPER_ONLY_WIDGETS.has(id) || developerMode)
                  return (
                    <DndContext sensors={dragSensors} collisionDetection={closestCenter} onDragEnd={handleWidgetDragEnd}>
                      <SortableContext items={editIds} strategy={verticalListSortingStrategy}>
                        <div className="space-y-1.5">
                          {editIds.map((id) => {
                            const meta = widgetMap.get(id)
                            const isVisible = !visibility || visibility[id] !== false
                            return (
                              <SortableWidgetRow
                                key={id}
                                id={id}
                                name={meta?.name || id}
                                isVisible={isVisible}
                                onToggleVisibility={() => toggleWidgetVisibility(id)}
                              />
                            )
                          })}
                        </div>
                      </SortableContext>
                    </DndContext>
                  )
                })()
              ) : (
                renderWidgetList.map((w) => <WidgetRenderer key={w.id} component={w} />)
              )}
            </div>
          </ScrollArea>
        </div>

        {/* Work panel */}
        <div className={resolvedActiveTab === 'work' ? 'flex flex-1 min-h-0 flex-col' : 'hidden'}>
          <WorkTab />
        </div>

        {/* Workflows panel */}
        <div className={resolvedActiveTab === 'workflows' ? 'flex flex-1 min-h-0 flex-col' : 'hidden'}>
          <WorkflowTab />
        </div>

        {/* Artifacts panel */}
        <div className={resolvedActiveTab === 'artifacts' ? 'flex flex-1 min-h-0 flex-col' : 'hidden'}>
          <ArtifactsContent onTitleChange={handleArtifactsTitleChange} />
        </div>

        {/* Inbox panel */}
        <div className={resolvedActiveTab === 'inbox' ? 'flex flex-1 min-h-0 flex-col' : 'hidden'}>
          <InboxContent agentId={inboxAgentId} />
        </div>

        {/* Plugin tab panels — lazily mounted on first activation, then kept alive */}
        {pluginTabs.map((entry) => {
          const PluginComponent = entry.component ? getSlotComponent(entry.component) : null
          if (!PluginComponent) return null
          return (
            <div key={entry.id} className={resolvedActiveTab === entry.id ? 'flex flex-1 min-h-0 flex-col' : 'hidden'}>
              <Suspense fallback={<Skeleton className="h-32 w-full m-3" />}>
                <PluginComponent {...(entry.props ?? {})} />
              </Suspense>
            </div>
          )
        })}

        {/* Plugin panels from manifest declarations (v1: placeholder render) */}
        {orderedIds
          .filter((id) => panels[id]?.source === 'plugin' && !pluginTabMap.has(id))
          .map((id) => (
            <div key={id} className={resolvedActiveTab === id ? 'flex flex-1 min-h-0 flex-col items-center justify-center' : 'hidden'}>
              <p className="text-xs text-fg-muted p-4 text-center">
                Plugin panel <code className="font-mono">{id}</code> registered.
                <br />
                Render function pending (follow-up ticket).
              </p>
            </div>
          ))}

      </div>
    </aside>
  )
}
