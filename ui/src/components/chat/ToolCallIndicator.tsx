import { Loader2, Wrench, CheckCircle2, XCircle } from 'lucide-react'
import type { ToolCall } from '@/lib/types'

interface ToolCallIndicatorProps {
  toolCall: ToolCall
}

export function ToolCallIndicator({ toolCall }: ToolCallIndicatorProps) {
  const { tool, status, summary } = toolCall

  return (
    <div className="flex items-center gap-2 py-1 px-2 rounded-md bg-zinc-800/50 border border-zinc-700/50 text-xs">
      {status === 'running' && (
        <Loader2 className="w-3.5 h-3.5 text-indigo-400 animate-spin shrink-0" />
      )}
      {status === 'done' && (
        <CheckCircle2 className="w-3.5 h-3.5 text-green-400 shrink-0" />
      )}
      {status === 'error' && (
        <XCircle className="w-3.5 h-3.5 text-red-400 shrink-0" />
      )}
      <Wrench className="w-3 h-3 text-zinc-500 shrink-0" />
      <span className="text-zinc-300 font-medium">{tool}</span>
      {summary && (
        <span className="text-zinc-500 truncate">{summary}</span>
      )}
      {status === 'running' && !summary && (
        <span className="text-zinc-500 italic">Running...</span>
      )}
    </div>
  )
}
