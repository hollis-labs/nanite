import { Suspense, useState, useCallback } from 'react'
import { LayoutGrid, Mail, Package, ListTodo, GitBranch, Pencil, GripVertical, Eye, EyeOff } from 'lucide-react'
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

function SortableWidgetRow({
  id,
  name,
  isVisible,
  onToggleVisibility,
}: {
  id: string
  name: string
  isVisible: boolean
  onToggleVisibility: () => void
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

const CORE_TABS = [
  { id: 'widgets' as const, icon: LayoutGrid, label: 'Widgets' },
  { id: 'work' as const, icon: ListTodo, label: 'Work' },
  { id: 'workflows' as const, icon: GitBranch, label: 'Workflows' },
  { id: 'inbox' as const, icon: Mail, label: 'Inbox' },
  { id: 'artifacts' as const, icon: Package, label: 'Artifacts' },
]

interface RightRailProps {
  inboxAgentId?: string
}

export function RightRail({ inboxAgentId = 'file-default' }: RightRailProps) {
  const open = useLayoutStore((s) => s.rightRailOpen)
  const activeTab = useLayoutStore((s) => s.rightRailTab)

  const { data: settings } = useSettings()
  const settingsMutation = useSettingsMutation()
  const recoverMode = settings?.recover_mode ?? false
  const [editMode, setEditMode] = useState(false)
  const pluginTabs = usePluginSlots('right-rail-tab')

  // Dynamic title for artifacts preview
  const [artifactsTitle, setArtifactsTitle] = useState('Artifacts')
  const handleArtifactsTitleChange = useCallback((title: string) => {
    setArtifactsTitle(title)
  }, [])

  // Widget data
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

  const orderedIds = buildWidgetOrder(userOrder, allWidgets, { skipPluginAppend: recoverMode })
  const visibleIds = filterVisibleWidgets(orderedIds, developerMode, visibility)

  const renderList: PluginUIComponent[] = visibleIds.map((id) => {
    return widgetMap.get(id) ?? {
      id,
      type: 'widget' as const,
      name: id,
      description: '',
    }
  })

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
    const editIds = orderedIds.filter((id) => !DEVELOPER_ONLY_WIDGETS.has(id) || developerMode)
    const oldIdx = editIds.indexOf(String(active.id))
    const newIdx = editIds.indexOf(String(over.id))
    if (oldIdx === -1 || newIdx === -1) return
    const reordered = arrayMove(editIds, oldIdx, newIdx)
    // Merge reordered edit IDs back into full orderedIds
    const merged = orderedIds.filter((id) => !editIds.includes(id))
    const newOrder = [...reordered, ...merged]
    settingsMutation.mutate({
      ext_settings: { ...settings?.ext_settings, widget_order: newOrder },
    } as Partial<UserSettings>)
  }, [orderedIds, developerMode, settings?.ext_settings, settingsMutation])

  // Build merged tabs list
  const allTabs = [
    ...CORE_TABS.map((t) => ({ ...t })),
    ...pluginTabs.map((entry) => ({
      id: entry.id,
      icon: resolveIcon(entry.icon),
      label: entry.label,
    })),
  ]

  // Resolve display title
  const displayTitle = activeTab === 'artifacts' ? artifactsTitle : allTabs.find(t => t.id === activeTab)?.label ?? 'Widgets'

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
          {/* Tab-specific actions — only show what's relevant to the active tab */}
          <div className="flex items-center gap-0.5">
            {activeTab === 'widgets' && (
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

        {/* Content — all panels stay mounted to avoid flash of loading state on tab switch */}

        <div className={`flex-1 min-h-0 ${activeTab === 'widgets' ? 'flex flex-col' : 'hidden'}`}>
          <ScrollArea className="flex-1 min-h-0">
            <div className="p-3 space-y-3">
              {editMode ? (
                (() => {
                  const editIds = orderedIds.filter((id) => !DEVELOPER_ONLY_WIDGETS.has(id) || developerMode)
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
                renderList.map((w) => (
                  <WidgetRenderer key={w.id} component={w} />
                ))
              )}
            </div>
          </ScrollArea>
        </div>

        <div className={activeTab === 'work' ? 'flex flex-1 min-h-0 flex-col' : 'hidden'}>
          <WorkTab />
        </div>

        <div className={activeTab === 'workflows' ? 'flex flex-1 min-h-0 flex-col' : 'hidden'}>
          <WorkflowTab />
        </div>

        <div className={activeTab === 'artifacts' ? 'flex flex-1 min-h-0 flex-col' : 'hidden'}>
          <ArtifactsContent onTitleChange={handleArtifactsTitleChange} />
        </div>

        <div className={activeTab === 'inbox' ? 'flex flex-1 min-h-0 flex-col' : 'hidden'}>
          <InboxContent agentId={inboxAgentId} />
        </div>

        {/* Plugin tabs — lazily mounted on first activation, then kept alive */}
        {pluginTabs.map((entry) => {
          const PluginComponent = entry.component ? getSlotComponent(entry.component) : null
          if (!PluginComponent) return null
          return (
            <div key={entry.id} className={activeTab === entry.id ? 'flex flex-1 min-h-0 flex-col' : 'hidden'}>
              <Suspense fallback={<Skeleton className="h-32 w-full m-3" />}>
                <PluginComponent {...(entry.props ?? {})} />
              </Suspense>
            </div>
          )
        })}
      </div>
    </aside>
  )
}
