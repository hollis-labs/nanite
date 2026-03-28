import { Suspense, Component, type ReactNode } from 'react'
import { AlertTriangle, Loader2, Puzzle } from 'lucide-react'
import { getWidgetComponent } from '@/generated/plugin-widgets'
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
        <div className="rounded-lg border border-red-900/50 bg-red-950/20 p-3">
          <div className="flex items-center gap-2 mb-1">
            <AlertTriangle className="w-3.5 h-3.5 text-red-400 shrink-0" />
            <span className="text-xs font-medium text-red-300">
              Widget failed: {this.props.widgetId}
            </span>
          </div>
          <p className="text-[11px] text-red-400/70 leading-relaxed">
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
    <div className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-3 flex items-center justify-center gap-2">
      <Loader2 className="w-3.5 h-3.5 text-zinc-500 animate-spin" />
      <span className="text-xs text-zinc-500">Loading widget...</span>
    </div>
  )
}

/** Fallback card for widgets not in the registry (plugin metadata only). */
function UnknownWidgetCard({ component }: { component: PluginUIComponent }) {
  return (
    <div className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-3">
      <div className="flex items-center gap-2 mb-1">
        <Puzzle className="w-3 h-3 text-indigo-400 shrink-0" />
        <span className="text-xs font-medium text-zinc-200 truncate">{component.name}</span>
        {component.plugin_id && (
          <span className="text-[10px] text-zinc-600 ml-auto">{component.plugin_id}</span>
        )}
      </div>
      {component.description && (
        <p className="text-[11px] text-zinc-500 leading-relaxed">{component.description}</p>
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
