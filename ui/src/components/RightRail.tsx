import { useState, useCallback } from 'react'
import { LayoutGrid, Mail, Package } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { ScrollArea } from '@/components/ui/ScrollArea'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useSettings } from '@/hooks/useSettings'
import { WidgetRenderer } from './widgets/WidgetRenderer'
import { DEFAULT_WIDGET_ORDER } from '@/generated/plugin-widgets'
import { ArtifactsContent } from './drawers/ArtifactsContent'
import { InboxContent } from './a2a/InboxContent'
import { api } from '@/lib/api'
import type { PluginUIComponent } from '@/lib/types'

const TABS = [
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
  const recoverMode = settings?.recover_mode ?? false

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

  // Resolve display title
  const displayTitle = activeTab === 'artifacts' ? artifactsTitle : TABS.find(t => t.id === activeTab)?.label ?? 'Widgets'

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
            {TABS.map((tab) => {
              const Icon = tab.icon
              const isActive = activeTab === tab.id
              return (
                <button
                  key={tab.id}
                  type="button"
                  onClick={() => setTab(tab.id)}
                  className={`p-1.5 rounded transition-colors ${
                    isActive
                      ? 'text-fg bg-surface'
                      : 'text-fg-faint hover:text-fg-secondary hover:bg-surface/50'
                  }`}
                  title={tab.label}
                >
                  <Icon className="w-4 h-4" />
                </button>
              )
            })}
          </div>
        </div>

        {/* Content */}
        {activeTab === 'widgets' && (
          <ScrollArea className="flex-1 min-h-0">
            <div className="p-3 space-y-3">
              {renderList.map((w) => (
                <WidgetRenderer key={w.id} component={w} />
              ))}
            </div>
          </ScrollArea>
        )}

        {activeTab === 'artifacts' && (
          <ArtifactsContent onTitleChange={handleArtifactsTitleChange} />
        )}

        {activeTab === 'inbox' && (
          <InboxContent agentId={inboxAgentId} />
        )}
      </div>
    </aside>
  )
}
