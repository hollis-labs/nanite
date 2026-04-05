import { Bot } from 'lucide-react'
import { Widget } from './Widget'
import { useChatStore } from '@/stores/useChatStore'
import { useModels } from '@/hooks/useSettings'
import type { AgentMode } from '@/lib/types'

const MODE_BADGE_STYLES: Record<AgentMode, { bg: string; text: string }> = {
  default: { bg: 'bg-mode-default/15', text: 'text-mode-default' },
  architect: { bg: 'bg-mode-architect/15', text: 'text-mode-architect' },
  planner: { bg: 'bg-mode-planner/15', text: 'text-mode-planner' },
  writer: { bg: 'bg-mode-writer/15', text: 'text-mode-writer' },
}

function StatusIndicator({ isStreaming, hasToolCalls }: { isStreaming: boolean; hasToolCalls: boolean }) {
  if (hasToolCalls) {
    return (
      <div className="flex items-center gap-1.5">
        <span className="w-2 h-2 rounded-full bg-status-warn shrink-0 animate-pulse" />
        <span className="text-status-warn">Tool pending</span>
      </div>
    )
  }
  if (isStreaming) {
    return (
      <div className="flex items-center gap-1.5">
        <span className="w-2 h-2 rounded-full bg-status-ok shrink-0 animate-pulse" />
        <span className="text-status-ok">Streaming</span>
      </div>
    )
  }
  return (
    <div className="flex items-center gap-1.5">
      <span className="w-2 h-2 rounded-full bg-surface-active shrink-0" />
      <span className="text-fg-secondary">Idle</span>
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
          <span className="text-fg-muted">Status</span>
          <StatusIndicator isStreaming={isStreaming} hasToolCalls={hasToolCalls} />
        </div>
        <div className="flex justify-between text-xs">
          <span className="text-fg-muted">Agent</span>
          <span className="text-fg-secondary">Nanite</span>
        </div>
        <div className="flex justify-between text-xs items-center">
          <span className="text-fg-muted">Mode</span>
          <span
            className={`px-1.5 py-0.5 rounded text-xs ${modeStyle.bg} ${modeStyle.text} capitalize`}
          >
            {activeMode}
          </span>
        </div>
        <div className="flex justify-between text-xs">
          <span className="text-fg-muted">Model</span>
          <span className="text-fg-secondary truncate ml-2">{modelLabel}</span>
        </div>
      </div>
    </Widget>
  )
}
