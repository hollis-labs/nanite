import { AlertTriangle, RefreshCw } from 'lucide-react'

interface PluginLoadErrorCardProps {
  pluginId: string
  reason: string
  onRetry?: () => void
}

export function PluginLoadErrorCard({ pluginId, reason, onRetry }: PluginLoadErrorCardProps) {
  return (
    <div className="rounded-sm border border-danger/50 bg-danger/10 p-3">
      <div className="flex items-center gap-2 mb-1">
        <AlertTriangle className="w-3.5 h-3.5 text-danger shrink-0" />
        <span className="text-xs font-medium text-danger">
          Plugin failed to load: {pluginId}
        </span>
      </div>
      <p className="text-[11px] text-danger/70 leading-relaxed">{reason}</p>
      {onRetry && (
        <button
          type="button"
          onClick={onRetry}
          className="mt-2 inline-flex items-center gap-1.5 rounded-sm border border-danger/40 px-2 py-1 text-[11px] font-medium text-danger hover:bg-danger/20"
        >
          <RefreshCw className="w-3 h-3" />
          Retry
        </button>
      )}
    </div>
  )
}
