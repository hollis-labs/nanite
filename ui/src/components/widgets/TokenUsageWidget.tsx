import { Coins } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { Widget, WidgetRow } from './Widget'
import { useAppStore } from '@/stores/useAppStore'
import { useChatStore } from '@/stores/useChatStore'
import { api } from '@/lib/api'

function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`
  return String(n)
}

function formatCost(usd: number): string {
  if (usd === 0) return '$0.00'
  if (usd < 0.001) return `$${usd.toFixed(4)}`
  if (usd < 0.01) return `$${usd.toFixed(3)}`
  return `$${usd.toFixed(2)}`
}

export function TokenUsageWidget() {
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const isStreaming = useChatStore((s) => s.isStreaming)

  const { data: sessionUsage } = useQuery({
    queryKey: ['session-usage', activeSessionId],
    queryFn: () => api.getSessionUsage(activeSessionId!),
    enabled: !!activeSessionId,
    refetchInterval: isStreaming ? 5000 : 30000,
  })

  const { data: globalUsage } = useQuery({
    queryKey: ['usage-summary'],
    queryFn: () => api.getUsageSummary(),
    refetchInterval: 60000,
  })

  const input = sessionUsage?.input_tokens ?? 0
  const output = sessionUsage?.output_tokens ?? 0
  const total = sessionUsage?.total_tokens ?? 0
  const toolInput = sessionUsage?.tool_input_tokens ?? 0
  const contentInput = Math.max(0, input - toolInput)
  const cost = sessionUsage?.estimated_cost_usd ?? 0
  const messages = sessionUsage?.message_count ?? 0

  const globalTotal = globalUsage?.total_tokens ?? 0
  const globalCost = globalUsage?.total_cost ?? 0

  return (
    <Widget id="token-usage" title="Token Usage" icon={Coins} accent="text-info">
      <div className="flex flex-col gap-1.5">
        {activeSessionId ? (
          <>
            <WidgetRow label="Input">
              <span className="font-mono text-[11px] text-info">{formatTokens(input)}</span>
            </WidgetRow>
            {toolInput > 0 && (
              <>
                <WidgetRow label="  Content">
                  <span className="font-mono text-[11px] text-info opacity-85">{formatTokens(contentInput)}</span>
                </WidgetRow>
                <WidgetRow label="  Tools">
                  <span className="font-mono text-[11px] text-primary">{formatTokens(toolInput)}</span>
                </WidgetRow>
              </>
            )}
            <WidgetRow label="Output">
              <span className="font-mono text-[11px] text-success">{formatTokens(output)}</span>
            </WidgetRow>

            <div className="h-px bg-divider my-1.5" />

            <WidgetRow label="Total" mono>{formatTokens(total)}</WidgetRow>
            <WidgetRow label="Cost">
              <span className="font-mono text-[11px] text-warning">{formatCost(cost)}</span>
            </WidgetRow>
            <WidgetRow label="Messages" mono>{String(messages)}</WidgetRow>
          </>
        ) : (
          <p className="text-[12px] text-fg-faint italic">No active session</p>
        )}

        {globalTotal > 0 && (
          <div className="pt-2 mt-0.5 border-t border-divider">
            <div className="font-mono text-[10px] font-semibold uppercase tracking-[0.04em] text-fg-faint mb-1.5">
              Cumulative
            </div>
            <WidgetRow label="All sessions" mono>{formatTokens(globalTotal)}</WidgetRow>
            <WidgetRow label="Total cost">
              <span className="font-mono text-[11px] text-warning opacity-70">{formatCost(globalCost)}</span>
            </WidgetRow>
          </div>
        )}
      </div>
    </Widget>
  )
}
