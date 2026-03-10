import { useState } from 'react'
import { Gauge, Info } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { Widget } from './Widget'
import { useAppStore } from '@/stores/useAppStore'
import { useChatStore } from '@/stores/useChatStore'
import { api } from '@/lib/api'
import { ContextInspectorModal } from './ContextInspectorModal'

const MAX_TOKENS = 200_000

function formatTokens(n: number): string {
  if (n >= 1000) return `${Math.round(n / 1000)}K`
  return String(n)
}

function formatCost(usd: number): string {
  if (usd < 0.01) return '<$0.01'
  return `$${usd.toFixed(2)}`
}

function getBarColor(pct: number): string {
  if (pct < 50) return 'bg-green-500'
  if (pct < 75) return 'bg-amber-500'
  return 'bg-red-500'
}

export function ContextBudgetWidget() {
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const isStreaming = useChatStore((s) => s.isStreaming)
  const [inspectorOpen, setInspectorOpen] = useState(false)

  const { data: usage } = useQuery({
    queryKey: ['session-usage', activeSessionId],
    queryFn: () => api.getSessionUsage(activeSessionId!),
    enabled: !!activeSessionId,
    // Poll every 5s while streaming, every 30s otherwise
    refetchInterval: isStreaming ? 5000 : 30000,
  })

  const totalTokens = usage?.total_tokens ?? 0
  const inputTokens = usage?.input_tokens ?? 0
  const outputTokens = usage?.output_tokens ?? 0
  const cost = usage?.estimated_cost_usd ?? 0
  const messageCount = usage?.message_count ?? 0
  const pct = Math.min((totalTokens / MAX_TOKENS) * 100, 100)
  const barColor = getBarColor(pct)

  return (
    <>
      <Widget id="context-budget" title="Context Budget" icon={Gauge}>
        <div className="space-y-2">
          <div className="flex justify-between text-xs text-zinc-400">
            <span className="flex items-center gap-1">
              Tokens
              {activeSessionId && (
                <button
                  onClick={() => setInspectorOpen(true)}
                  className="p-0.5 rounded hover:bg-zinc-700 transition-colors"
                  title="View context breakdown"
                >
                  <Info className="w-3 h-3 text-zinc-500 hover:text-zinc-300" />
                </button>
              )}
            </span>
            <span className="text-zinc-300">
              {formatTokens(totalTokens)} / {formatTokens(MAX_TOKENS)}
            </span>
          </div>
          <div className="w-full bg-zinc-800 rounded-full h-1.5">
            <div
              className={`${barColor} h-1.5 rounded-full transition-all duration-500`}
              style={{ width: `${Math.max(pct, 1)}%` }}
            />
          </div>
          <div className="flex justify-between text-xs text-zinc-600">
            <span>{Math.round(pct)}% used</span>
            <span>{formatCost(cost)}</span>
          </div>
          {totalTokens > 0 && (
            <div className="space-y-0.5 pt-1 border-t border-zinc-800">
              <div className="flex justify-between text-xs">
                <span className="text-zinc-500">Input</span>
                <span className="text-zinc-400">{formatTokens(inputTokens)}</span>
              </div>
              <div className="flex justify-between text-xs">
                <span className="text-zinc-500">Output</span>
                <span className="text-zinc-400">{formatTokens(outputTokens)}</span>
              </div>
              <div className="flex justify-between text-xs">
                <span className="text-zinc-500">Messages</span>
                <span className="text-zinc-400">{messageCount}</span>
              </div>
            </div>
          )}
        </div>
      </Widget>

      {activeSessionId && (
        <ContextInspectorModal
          sessionId={activeSessionId}
          open={inspectorOpen}
          onClose={() => setInspectorOpen(false)}
        />
      )}
    </>
  )
}
