import { Loader2, Wrench, CheckCircle2, XCircle } from 'lucide-react'
import type { ToolCall } from '@/lib/types'

interface ToolCallIndicatorProps {
  toolCall: ToolCall
}

export function ToolCallIndicator({ toolCall }: ToolCallIndicatorProps) {
  const { tool, status, summary } = toolCall

  return (
    <div className="flex items-center gap-2 py-1 px-2 rounded-md bg-surface/50 border border-border-subtle/50 text-xs">
      {status === 'running' && (
        <Loader2 className="w-3.5 h-3.5 text-primary animate-spin shrink-0" />
      )}
      {status === 'done' && (
        <CheckCircle2 className="w-3.5 h-3.5 text-success shrink-0" />
      )}
      {status === 'error' && (
        <XCircle className="w-3.5 h-3.5 text-danger shrink-0" />
      )}
      <Wrench className="w-3 h-3 text-fg-muted shrink-0" />
      <span className="text-fg-secondary font-medium">{tool}</span>
      {summary && (
        <span className="text-fg-muted truncate">{summary}</span>
      )}
      {status === 'running' && !summary && (
        <span className="text-fg-muted italic">Running...</span>
      )}
    </div>
  )
}
