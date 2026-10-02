import { Suspense } from 'react'
import { PluginRenderBoundary } from './PluginRenderBoundary'
import type { PluginPanelView } from '@/hooks/usePluginPanels'

export function PluginPanelBody({ panel, sessionId }: { panel: PluginPanelView; sessionId: string | null }) {
  const View = panel.View
  return (
    <PluginRenderBoundary resetKey={View} label="Plugin panel">
      <Suspense fallback={<p className="p-3 text-xs text-fg-muted">Loading plugin panel…</p>}>
        <View {...panel.props} session_id={sessionId} />
      </Suspense>
    </PluginRenderBoundary>
  )
}
