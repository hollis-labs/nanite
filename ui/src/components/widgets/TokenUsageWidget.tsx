import { Coins } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { Widget } from './Widget'
import { useAppStore } from '@/stores/useAppStore'
import { useChatStore } from '@/stores/useChatStore'
import { api } from '@/lib/api'

function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 10_000) return `${(n / 1000).toFixed(1)}k`
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`
  return String(n)
}

function formatCost(usd: number): string {
  if (usd === 0) return '$0.00'
  if (usd < 0.001) return `$${usd.toFixed(4)}`
  if (usd < 0.01) return `$${usd.toFixed(3)}`
  return `$${usd.toFixed(2)}`
}

function TokenRow({
  label,
  value,
  colorClass,
}: {
  label: string
  value: string
  colorClass: string
}) {
  return (
    <div className="flex justify-between items-center text-xs">
      <span className="text-fg-muted">{label}</span>
      <span
        className={`${colorClass} font-mono tabular-nums transition-all duration-300`}
      >
        {value}
      </span>
    </div>
  )
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
  const cost = sessionUsage?.estimated_cost_usd ?? 0
  const messages = sessionUsage?.message_count ?? 0

  const globalTotal = globalUsage?.total_tokens ?? 0
  const globalCost = globalUsage?.total_cost ?? 0

  return (
    <Widget id="token-usage" title="Token Usage" icon={Coins}>
      <div className="space-y-2">
        {/* Session usage */}
        {activeSessionId ? (
          <>
            <TokenRow
              label="Input"
              value={formatTokens(input)}
              colorClass="text-blue-400"
            />
            <TokenRow
              label="Output"
              value={formatTokens(output)}
              colorClass="text-success"
            />

            <div className="border-t border-border pt-1.5 mt-1.5">
              <TokenRow
                label="Total"
                value={formatTokens(total)}
                colorClass="text-fg-secondary"
              />
              <TokenRow
                label="Cost"
                value={formatCost(cost)}
                colorClass="text-status-warn"
              />
              <TokenRow
                label="Messages"
                value={String(messages)}
                colorClass="text-fg-secondary"
              />
            </div>
          </>
        ) : (
          <p className="text-xs text-fg-faint italic">No active session</p>
        )}

        {/* Cumulative usage */}
        {globalTotal > 0 && (
          <div className="border-t border-border pt-1.5 mt-1.5">
            <p className="text-[10px] uppercase tracking-wider text-fg-faint mb-1">
              Cumulative
            </p>
            <TokenRow
              label="All sessions"
              value={formatTokens(globalTotal)}
              colorClass="text-fg-secondary"
            />
            <TokenRow
              label="Total cost"
              value={formatCost(globalCost)}
              colorClass="text-status-warn/70"
            />
          </div>
        )}
      </div>
    </Widget>
  )
}
