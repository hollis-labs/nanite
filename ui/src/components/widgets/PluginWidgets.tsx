import { useQuery } from '@tanstack/react-query'
import { Puzzle } from 'lucide-react'
import { Widget } from './Widget'
import { api } from '@/lib/api'
import { useSettings } from '@/hooks/useSettings'
import type { PluginUIComponent } from '@/lib/types'

function PluginWidgetCard({ component }: { component: PluginUIComponent }) {
  return (
    <div className="rounded-md border border-zinc-800 bg-zinc-900/30 p-3">
      <div className="flex items-center gap-2 mb-1">
        <Puzzle className="w-3 h-3 text-indigo-400 shrink-0" />
        <span className="text-xs font-medium text-zinc-200 truncate">{component.name}</span>
      </div>
      {component.description && (
        <p className="text-[11px] text-zinc-500 leading-relaxed">{component.description}</p>
      )}
      {component.props && Object.keys(component.props).length > 0 && (
        <div className="mt-2 space-y-1">
          {Object.entries(component.props).map(([key, value]) => (
            <div key={key} className="flex items-baseline gap-2 text-[11px]">
              <span className="text-zinc-500 font-mono">{key}:</span>
              <span className="text-zinc-300 truncate">{String(value)}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

export function PluginWidgets() {
  const { data: settings } = useSettings()
  const developerMode = settings?.developer_mode ?? false
  const recoverMode = settings?.recover_mode ?? false

  const { data: components = [] } = useQuery({
    queryKey: ['plugin-ui-components'],
    queryFn: api.listUIComponents,
    staleTime: 60_000,
    enabled: developerMode && !recoverMode,
  })

  // Only show widget-type components.
  const widgets = components.filter((c) => c.type === 'widget')

  // Don't render if not in developer mode, recover mode is on, or no widgets.
  if (!developerMode || recoverMode || widgets.length === 0) return null

  return (
    <Widget id="plugin-widgets" title="Plugin Widgets" icon={Puzzle}>
      <div className="space-y-2">
        {widgets.map((w) => (
          <PluginWidgetCard key={w.id} component={w} />
        ))}
      </div>
    </Widget>
  )
}
