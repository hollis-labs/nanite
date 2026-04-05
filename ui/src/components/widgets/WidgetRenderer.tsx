import { Suspense, Component, useSyncExternalStore, type ReactNode } from 'react'
import { AlertTriangle, Puzzle } from 'lucide-react'
import { Skeleton } from '@/components/ui/skeleton'
import { getWidgetComponent } from '@/generated/plugin-widgets'
import { subscribeRegistry, getRegistryVersion } from '@/lib/plugin-loader'
import type { PluginUIComponent } from '@/lib/types'

/** Error boundary scoped to a single widget — prevents one broken widget from crashing the rail. */
class WidgetErrorBoundary extends Component<
  { widgetId: string; children: ReactNode },
  { error: Error | null }
> {
  state: { error: Error | null } = { error: null }

  static getDerivedStateFromError(error: Error) {
    return { error }
  }

  render() {
    if (this.state.error) {
      return (
        <div className="rounded-lg border border-danger/50 bg-danger/10 p-3">
          <div className="flex items-center gap-2 mb-1">
            <AlertTriangle className="w-3.5 h-3.5 text-danger shrink-0" />
            <span className="text-xs font-medium text-danger">
              Widget failed: {this.props.widgetId}
            </span>
          </div>
          <p className="text-[11px] text-danger/70 leading-relaxed">
            {this.state.error.message}
          </p>
        </div>
      )
    }
    return this.props.children
  }
}

/** Loading fallback shown while a lazy widget component loads. */
function WidgetLoadingFallback() {
  return (
    <div className="rounded-lg border border-border bg-bg-elevated/50 p-3 space-y-2">
      <Skeleton className="h-3.5 w-1/3" />
      <Skeleton className="h-3 w-full" />
      <Skeleton className="h-3 w-2/3" />
    </div>
  )
}

/** Fallback card for widgets not in the registry (plugin metadata only). */
function UnknownWidgetCard({ component }: { component: PluginUIComponent }) {
  return (
    <div className="rounded-lg border border-border bg-bg-elevated/50 p-3">
      <div className="flex items-center gap-2 mb-1">
        <Puzzle className="w-3 h-3 text-primary shrink-0" />
        <span className="text-xs font-medium text-fg truncate">{component.name}</span>
        {component.plugin_id && (
          <span className="text-[10px] text-fg-faint ml-auto">{component.plugin_id}</span>
        )}
      </div>
      {component.description && (
        <p className="text-[11px] text-fg-muted leading-relaxed">{component.description}</p>
      )}
    </div>
  )
}

interface WidgetRendererProps {
  /** The widget metadata from the API. */
  component: PluginUIComponent
}

/**
 * Renders a widget by looking up its ID in the widget registry.
 * - Known widgets: lazy-loaded React component wrapped in Suspense + error boundary.
 * - Unknown widgets: metadata-only fallback card.
 */
export function WidgetRenderer({ component }: WidgetRendererProps) {
  // Re-render when dynamic plugins register new widget components.
  useSyncExternalStore(subscribeRegistry, getRegistryVersion)

  const WidgetComponent = getWidgetComponent(component.id)

  if (!WidgetComponent) {
    return <UnknownWidgetCard component={component} />
  }

  return (
    <WidgetErrorBoundary widgetId={component.id}>
      <Suspense fallback={<WidgetLoadingFallback />}>
        <WidgetComponent />
      </Suspense>
    </WidgetErrorBoundary>
  )
}
