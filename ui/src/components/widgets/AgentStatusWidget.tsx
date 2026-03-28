import { Bot } from 'lucide-react'
import { Widget } from './Widget'
import { useChatStore } from '@/stores/useChatStore'
import { useModels } from '@/hooks/useSettings'
import type { AgentMode } from '@/lib/types'

const MODE_BADGE_STYLES: Record<AgentMode, { bg: string; text: string }> = {
  default: { bg: 'bg-blue-500/15', text: 'text-blue-400' },
  architect: { bg: 'bg-purple-500/15', text: 'text-purple-400' },
  planner: { bg: 'bg-green-500/15', text: 'text-green-400' },
  writer: { bg: 'bg-amber-500/15', text: 'text-amber-400' },
}

function StatusIndicator({ isStreaming, hasToolCalls }: { isStreaming: boolean; hasToolCalls: boolean }) {
  if (hasToolCalls) {
    return (
      <div className="flex items-center gap-1.5">
        <span className="w-2 h-2 rounded-full bg-amber-500 shrink-0 animate-pulse" />
        <span className="text-amber-400">Tool pending</span>
      </div>
    )
  }
  if (isStreaming) {
    return (
      <div className="flex items-center gap-1.5">
        <span className="w-2 h-2 rounded-full bg-emerald-500 shrink-0 animate-pulse" />
        <span className="text-emerald-400">Streaming</span>
      </div>
    )
  }
  return (
    <div className="flex items-center gap-1.5">
      <span className="w-2 h-2 rounded-full bg-zinc-600 shrink-0" />
      <span className="text-zinc-400">Idle</span>
    </div>
  )
}

export function AgentStatusWidget() {
  const activeMode = useChatStore((s) => s.activeMode)
  const activeModel = useChatStore((s) => s.activeModel)
  const isStreaming = useChatStore((s) => s.isStreaming)
  const toolCalls = useChatStore((s) => s.toolCalls)

  const { data: models } = useModels()
  const modelLabel = models?.find((m) => m.model_id === activeModel)?.display_name || activeModel
  const modeStyle = MODE_BADGE_STYLES[activeMode]
  const hasToolCalls = toolCalls.length > 0

  return (
    <Widget id="agent-status" title="Agent Status" icon={Bot}>
      <div className="space-y-2.5">
        <div className="flex justify-between text-xs">
          <span className="text-zinc-500">Status</span>
          <StatusIndicator isStreaming={isStreaming} hasToolCalls={hasToolCalls} />
        </div>
        <div className="flex justify-between text-xs">
          <span className="text-zinc-500">Agent</span>
          <span className="text-zinc-300">Conduit</span>
        </div>
        <div className="flex justify-between text-xs items-center">
          <span className="text-zinc-500">Mode</span>
          <span
            className={`px-1.5 py-0.5 rounded text-xs ${modeStyle.bg} ${modeStyle.text} capitalize`}
          >
            {activeMode}
          </span>
        </div>
        <div className="flex justify-between text-xs">
          <span className="text-zinc-500">Model</span>
          <span className="text-zinc-300 truncate ml-2">{modelLabel}</span>
        </div>
      </div>
    </Widget>
  )
}
