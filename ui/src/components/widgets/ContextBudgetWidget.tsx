import { Gauge } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { Widget } from './Widget'
import { useAppStore } from '@/stores/useAppStore'
import { api } from '@/lib/api'

const MAX_TOKENS = 200_000
const AVG_TOKENS_PER_MESSAGE = 800

function formatTokens(n: number): string {
  if (n >= 1000) return `${Math.round(n / 1000)}K`
  return String(n)
}

function getBarColor(pct: number): string {
  if (pct < 50) return 'bg-green-500'
  if (pct < 75) return 'bg-amber-500'
  return 'bg-red-500'
}

export function ContextBudgetWidget() {
  const activeSessionId = useAppStore((s) => s.activeSessionId)

  const { data: session } = useQuery({
    queryKey: ['session', activeSessionId],
    queryFn: () => api.getSession(activeSessionId!),
    enabled: !!activeSessionId,
  })

  const messageCount = session?.message_count ?? 0
  const estimatedTokens = messageCount * AVG_TOKENS_PER_MESSAGE
  const pct = Math.min((estimatedTokens / MAX_TOKENS) * 100, 100)
  const barColor = getBarColor(pct)

  return (
    <Widget id="context-budget" title="Context Budget" icon={Gauge}>
      <div className="space-y-2">
        <div className="flex justify-between text-xs text-zinc-400">
          <span>Context</span>
          <span className="text-zinc-300">
            ~{formatTokens(estimatedTokens)} / {formatTokens(MAX_TOKENS)} tokens
          </span>
        </div>
        <div className="w-full bg-zinc-800 rounded-full h-1.5">
          <div
            className={`${barColor} h-1.5 rounded-full transition-all duration-500`}
            style={{ width: `${Math.max(pct, 1)}%` }}
          />
        </div>
        <p className="text-xs text-zinc-600">{Math.round(pct)}% used</p>
      </div>
    </Widget>
  )
}
