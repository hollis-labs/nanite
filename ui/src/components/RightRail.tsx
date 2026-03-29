import { Suspense, useState, useCallback } from 'react'
import { LayoutGrid, Mail, Package, Pencil, GripVertical, Eye, EyeOff } from 'lucide-react'
import { Tooltip } from '@/components/ui/tooltip'
import { Skeleton } from '@/components/ui/skeleton'
import { useQuery } from '@tanstack/react-query'
import { ScrollArea } from '@/components/ui/scroll-area'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useSettings, useSettingsMutation } from '@/hooks/useSettings'
import { usePluginSlots } from '@/hooks/usePluginSlots'
import { resolveIcon } from '@/lib/icons'
import { getSlotComponent } from '@/generated/plugin-slot-components'
import type { UserSettings } from '@/lib/types'
import { WidgetRenderer } from './widgets/WidgetRenderer'
import { DEFAULT_WIDGET_ORDER } from '@/generated/plugin-widgets'
import { ArtifactsContent } from './drawers/ArtifactsContent'
import { InboxContent } from './a2a/InboxContent'
import { api } from '@/lib/api'
import type { PluginUIComponent } from '@/lib/types'

const CORE_TABS = [
  { id: 'widgets' as const, icon: LayoutGrid, label: 'Widgets' },
  { id: 'inbox' as const, icon: Mail, label: 'Inbox' },
  { id: 'artifacts' as const, icon: Package, label: 'Artifacts' },
]

interface RightRailProps {
  inboxAgentId?: string
}

export function RightRail({ inboxAgentId = 'mentat-001' }: RightRailProps) {
  const open = useLayoutStore((s) => s.rightRailOpen)
  const activeTab = useLayoutStore((s) => s.rightRailTab)
  const setTab = useLayoutStore((s) => s.setRightRailTab)
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

  const visibility = settings?.ext_settings?.widget_visibility
  const userOrder = settings?.ext_settings?.widget_order

  const baseOrder = userOrder?.length ? userOrder : DEFAULT_WIDGET_ORDER
  const seen = new Set<string>()
  const orderedIds: string[] = []

  for (const id of baseOrder) {
    if (widgetMap.has(id) || DEFAULT_WIDGET_ORDER.includes(id)) {
      orderedIds.push(id)
      seen.add(id)
    }
  }

  if (!recoverMode) {
    for (const w of allWidgets) {
      if (!seen.has(w.id)) {
        orderedIds.push(w.id)
        seen.add(w.id)
      }
    }
  }

  const visibleIds = orderedIds.filter((id) => {
    if (!visibility) return true
    return visibility[id] !== false
  })

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
      ext_settings: {
        ...settings?.ext_settings,
        widget_visibility: updated,
      },
    } as Partial<UserSettings>)
  }, [settings?.ext_settings, settingsMutation])

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
        <div className="px-4 h-12 flex items-center justify-between border-b border-border shrink-0">
          <h2 className="text-sm font-semibold text-fg truncate">{displayTitle}</h2>
          <div className="flex items-center gap-0.5">
            {activeTab === 'widgets' && (
              <Tooltip content={editMode ? 'Done editing' : 'Organize widgets'} side="bottom">
                <button
                  type="button"
                  onClick={() => setEditMode((e) => !e)}
                  className={`p-1.5 rounded transition-colors ${
                    editMode
                      ? 'text-accent bg-accent/10'
                      : 'text-fg-faint hover:text-fg-secondary hover:bg-surface/50'
                  }`}
                >
                  <Pencil className="w-3.5 h-3.5" />
                </button>
              </Tooltip>
            )}
            {allTabs.map((tab) => {
              const Icon = tab.icon
              const isActive = activeTab === tab.id
              return (
                <Tooltip key={tab.id} content={tab.label} side="bottom">
                  <button
                    type="button"
                    onClick={() => setTab(tab.id as any)}
                    className={`p-1.5 rounded transition-colors ${
                      isActive
                        ? 'text-fg bg-surface'
                        : 'text-fg-faint hover:text-fg-secondary hover:bg-surface/50'
                    }`}
                  >
                    <Icon className="w-4 h-4" />
                  </button>
                </Tooltip>
              )
            })}
          </div>
        </div>

        {/* Content */}
        {activeTab === 'widgets' && (
          <ScrollArea className="flex-1 min-h-0">
            <div className="p-3 space-y-3">
              {editMode ? (
                // Edit mode: show all widgets with visibility toggles
                orderedIds.map((id) => {
                  const meta = widgetMap.get(id)
                  const isVisible = !visibility || visibility[id] !== false
                  return (
                    <div
                      key={id}
                      className={`flex items-center gap-2 px-3 py-2 rounded-lg border border-border-subtle ${
                        isVisible ? 'bg-bg-elevated/50' : 'bg-bg-elevated/20 opacity-50'
                      }`}
                    >
                      <GripVertical className="size-3.5 text-fg-faint cursor-grab shrink-0" />
                      <span className="text-xs text-fg flex-1 truncate">{meta?.name || id}</span>
                      <Tooltip content={isVisible ? 'Hide widget' : 'Show widget'} side="left">
                        <button
                          type="button"
                          onClick={() => toggleWidgetVisibility(id)}
                          className="p-1 rounded text-fg-muted hover:text-fg transition-colors"
                        >
                          {isVisible ? <Eye className="size-3.5" /> : <EyeOff className="size-3.5" />}
                        </button>
                      </Tooltip>
                    </div>
                  )
                })
              ) : (
                renderList.map((w) => (
                  <WidgetRenderer key={w.id} component={w} />
                ))
              )}
            </div>
          </ScrollArea>
        )}

        {activeTab === 'artifacts' && (
          <ArtifactsContent onTitleChange={handleArtifactsTitleChange} />
        )}

        {activeTab === 'inbox' && (
          <InboxContent agentId={inboxAgentId} />
        )}

        {/* Plugin-registered right rail tab content */}
        {!['widgets', 'artifacts', 'inbox'].includes(activeTab) && (() => {
          const pluginEntry = pluginTabs.find((e) => e.id === activeTab)
          if (!pluginEntry?.component) return null
          const PluginComponent = getSlotComponent(pluginEntry.component)
          if (!PluginComponent) return null
          return (
            <Suspense fallback={<Skeleton className="h-32 w-full m-3" />}>
              <PluginComponent {...(pluginEntry.props ?? {})} />
            </Suspense>
          )
        })()}
      </div>
    </aside>
  )
}
