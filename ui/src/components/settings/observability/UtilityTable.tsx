import type { UtilityCallSummary } from '@/lib/types'

function formatDuration(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

function formatCost(usd: number): string {
  if (usd === 0) return '$0.00'
  if (usd < 0.01) return `$${usd.toFixed(4)}`
  return `$${usd.toFixed(2)}`
}

interface UtilityTableProps {
  data: UtilityCallSummary[]
}

export function UtilityTable({ data }: UtilityTableProps) {
  if (data.length === 0) {
    return <p className="text-xs text-zinc-600 italic py-2">No utility calls recorded yet</p>
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-xs">
        <thead>
          <tr className="text-[10px] uppercase tracking-wider text-zinc-500 border-b border-zinc-800">
            <th className="text-left py-2 pr-3 font-medium">Provider</th>
            <th className="text-left py-2 pr-3 font-medium">Model</th>
            <th className="text-left py-2 pr-3 font-medium">Type</th>
            <th className="text-right py-2 pr-3 font-medium">Count</th>
            <th className="text-right py-2 pr-3 font-medium">Avg</th>
            <th className="text-right py-2 pr-3 font-medium">Min</th>
            <th className="text-right py-2 pr-3 font-medium">Max</th>
            <th className="text-right py-2 pr-3 font-medium">Errors</th>
            <th className="text-right py-2 font-medium">Cost</th>
          </tr>
        </thead>
        <tbody>
          {data.map((row, i) => (
            <tr
              key={`${row.provider}-${row.model}-${row.call_type}`}
              className={`border-b border-zinc-800/50 ${i % 2 === 0 ? '' : 'bg-zinc-900/30'}`}
            >
              <td className="py-1.5 pr-3 text-zinc-300">{row.provider}</td>
              <td className="py-1.5 pr-3 font-mono text-zinc-400">{row.model}</td>
              <td className="py-1.5 pr-3 text-zinc-400">{row.call_type}</td>
              <td className="py-1.5 pr-3 text-right font-mono tabular-nums text-zinc-300">{row.call_count}</td>
              <td className="py-1.5 pr-3 text-right font-mono tabular-nums text-blue-400">{formatDuration(row.avg_duration_ms)}</td>
              <td className="py-1.5 pr-3 text-right font-mono tabular-nums text-zinc-500">{formatDuration(row.min_duration_ms)}</td>
              <td className="py-1.5 pr-3 text-right font-mono tabular-nums text-zinc-500">{formatDuration(row.max_duration_ms)}</td>
              <td className={`py-1.5 pr-3 text-right font-mono tabular-nums ${row.error_count > 0 ? 'text-red-400' : 'text-zinc-600'}`}>
                {row.error_count}
              </td>
              <td className="py-1.5 text-right font-mono tabular-nums text-amber-400">{formatCost(row.total_cost_usd)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
