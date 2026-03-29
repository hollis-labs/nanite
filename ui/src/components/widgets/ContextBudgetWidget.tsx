import { useState } from 'react'
import { Gauge, Info } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { Widget } from './Widget'
import { useAppStore } from '@/stores/useAppStore'
import { useChatStore } from '@/stores/useChatStore'
import { api } from '@/lib/api'
import { ContextInspectorModal } from './ContextInspectorModal'

const BREAKDOWN_STALE = 30_000

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

function getBarTextColor(pct: number): string {
  if (pct < 50) return 'text-green-400'
  if (pct < 75) return 'text-amber-400'
  return 'text-red-400'
}

export function ContextBudgetWidget() {
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const isStreaming = useChatStore((s) => s.isStreaming)
  const [inspectorOpen, setInspectorOpen] = useState(false)

  const { data: usage } = useQuery({
    queryKey: ['session-usage', activeSessionId],
    queryFn: () => api.getSessionUsage(activeSessionId!),
    enabled: !!activeSessionId,
    refetchInterval: isStreaming ? 5000 : 30000,
  })

  const { data: breakdown } = useQuery({
    queryKey: ['context-breakdown', activeSessionId],
    queryFn: () => api.getContextBreakdown(activeSessionId!),
    enabled: !!activeSessionId,
    staleTime: BREAKDOWN_STALE,
    refetchInterval: isStreaming ? 10000 : 60000,
  })

  // Context window fill (current snapshot — what's in the window right now)
  const ctxTotal = breakdown?.total ?? 0
  const ctxCeiling = breakdown?.ceiling ?? 1
  const ctxPct = Math.min((ctxTotal / ctxCeiling) * 100, 100)
  const systemPromptTokens = breakdown?.system_prompt_tokens ?? 0
  const toolCallCount = breakdown?.tools?.length ?? 0
  const toolTokens = breakdown?.tool_tokens_total ?? 0
  const toolsAvailable = breakdown?.tools_available ?? 0

  // Cumulative token usage (grows over session lifetime)
  const totalTokens = usage?.total_tokens ?? 0
  const inputTokens = usage?.input_tokens ?? 0
  const outputTokens = usage?.output_tokens ?? 0
  const cost = usage?.estimated_cost_usd ?? 0
  const messageCount = usage?.message_count ?? 0
  const cacheCreation = usage?.cache_creation_tokens ?? 0
  const cacheRead = usage?.cache_read_tokens ?? 0
  const tokenPct = ctxCeiling > 1 ? (totalTokens / ctxCeiling) * 100 : 0

  return (
    <>
      <Widget id="context" title="Context" icon={Gauge}>
        <div className="space-y-2">
          {/* Context Window bar */}
          <div className="flex justify-between text-xs text-fg-secondary">
            <span className="flex items-center gap-1">
              Context Window
              {activeSessionId && (
                <button
                  onClick={() => setInspectorOpen(true)}
                  className="p-0.5 rounded hover:bg-surface-hover transition-colors"
                  title="View context breakdown"
                >
                  <Info className="w-3 h-3 text-fg-muted hover:text-fg-secondary" />
                </button>
              )}
            </span>
            <span className={getBarTextColor(ctxPct)}>
              {formatTokens(ctxTotal)} / {formatTokens(ctxCeiling)}
            </span>
          </div>
          <div className="w-full bg-surface rounded-full h-1.5">
            <div
              className={`${getBarColor(ctxPct)} h-1.5 rounded-full transition-all duration-500`}
              style={{ width: `${Math.max(ctxPct, 1)}%` }}
            />
          </div>
          <div className="flex justify-between text-xs text-fg-faint">
            <span>{Math.round(ctxPct)}% filled</span>
          </div>

          {/* Token Usage bar */}
          <div className="flex justify-between text-xs text-fg-secondary pt-1">
            <span>Token Usage</span>
            <span className={getBarTextColor(tokenPct)}>
              {formatTokens(totalTokens)} / {formatTokens(ctxCeiling)}
            </span>
          </div>
          <div className="w-full bg-surface rounded-full h-1.5">
            <div
              className={`${getBarColor(tokenPct)} h-1.5 rounded-full transition-all duration-500`}
              style={{ width: `${Math.min(Math.max(tokenPct, 1), 100)}%` }}
            />
          </div>
          <div className="flex justify-between text-xs text-fg-faint">
            <span>{Math.round(tokenPct)}% used</span>
            <span>{formatCost(cost)}</span>
          </div>

          {/* Stats breakdown */}
          {(totalTokens > 0 || ctxTotal > 0) && (
            <div className="space-y-0.5 pt-1 border-t border-border">
              <div className="flex justify-between text-xs">
                <span className="text-fg-muted">Input</span>
                <span className="text-fg-secondary">{formatTokens(inputTokens)}</span>
              </div>
              <div className="flex justify-between text-xs">
                <span className="text-fg-muted">Output</span>
                <span className="text-fg-secondary">{formatTokens(outputTokens)}</span>
              </div>
              <div className="flex justify-between text-xs">
                <span className="text-fg-muted">Messages</span>
                <span className="text-fg-secondary">{messageCount}</span>
              </div>
              <div className="flex justify-between text-xs">
                <span className="text-fg-muted">System Prompt</span>
                <span className="text-fg-secondary">{formatTokens(systemPromptTokens)}</span>
              </div>
              <div className="flex justify-between text-xs">
                <span className="text-fg-muted">Tool Calls ({toolCallCount})</span>
                <span className="text-fg-secondary">{formatTokens(toolTokens)}</span>
              </div>
              <div className="flex justify-between text-xs">
                <span className="text-fg-muted">Tools Available</span>
                <span className="text-fg-secondary">{toolsAvailable}</span>
              </div>
              {(cacheCreation > 0 || cacheRead > 0) && (
                <>
                  <div className="flex justify-between text-xs">
                    <span className="text-fg-muted">Cache Write</span>
                    <span className="text-fg-secondary">{formatTokens(cacheCreation)}</span>
                  </div>
                  <div className="flex justify-between text-xs">
                    <span className="text-fg-muted">Cache Read</span>
                    <span className="text-success">{formatTokens(cacheRead)}</span>
                  </div>
                </>
              )}
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
