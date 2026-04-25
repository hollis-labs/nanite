import { Bot } from 'lucide-react'
import { Widget, WidgetRow, StatusDot, ModeChip } from './Widget'
import { useChatStore } from '@/stores/useChatStore'
import { useModels } from '@/hooks/useSettings'

export function AgentStatusWidget() {
  const activeMode = useChatStore((s) => s.activeMode)
  const activeModel = useChatStore((s) => s.activeModel)
  const isStreaming = useChatStore((s) => s.isStreaming)
  const toolCalls = useChatStore((s) => s.toolCalls)

  const { data: models } = useModels()
  const modelLabel = models?.find((m) => m.model_id === activeModel)?.display_name || activeModel
  const hasToolCalls = toolCalls.length > 0

  const statusDot = hasToolCalls
    ? <StatusDot tone="warning" pulse>Tool pending</StatusDot>
    : isStreaming
      ? <StatusDot tone="success" pulse>Streaming</StatusDot>
      : <StatusDot tone="neutral">Idle</StatusDot>

  return (
    <Widget id="agent-status" title="Agent" icon={Bot} accent="text-brand">
      <div className="flex flex-col gap-1.5">
        <WidgetRow label="Status">{statusDot}</WidgetRow>
        <WidgetRow label="Mode"><ModeChip mode={activeMode} /></WidgetRow>
        <WidgetRow label="Model" mono>{modelLabel || '—'}</WidgetRow>
      </div>
    </Widget>
  )
}
