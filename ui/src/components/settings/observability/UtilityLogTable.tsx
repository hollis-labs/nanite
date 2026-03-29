import { useState, useMemo } from 'react'
import { ArrowUpDown } from 'lucide-react'
import type { ExecutionMetrics } from '@/lib/types'

type SortField = 'created_at' | 'duration_ms' | 'estimated_cost_usd'

function formatDuration(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  return `${(ms / 60000).toFixed(1)}m`
}

function formatCost(usd: number): string {
  if (usd === 0) return '—'
  if (usd < 0.01) return `$${usd.toFixed(4)}`
  return `$${usd.toFixed(2)}`
}

function timeAgo(dateStr: string): string {
  const diff = Date.now() - new Date(dateStr).getTime()
  const seconds = Math.floor(diff / 1000)
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

interface UtilityLogTableProps {
  data: ExecutionMetrics[]
}

export function UtilityLogTable({ data }: UtilityLogTableProps) {
  const [sortField, setSortField] = useState<SortField>('created_at')
  const [sortAsc, setSortAsc] = useState(false)

  const sorted = useMemo(() => {
    return [...data].sort((a, b) => {
      const av = a[sortField]
      const bv = b[sortField]
      if (typeof av === 'string') return sortAsc ? av.localeCompare(bv as string) : (bv as string).localeCompare(av)
      return sortAsc ? (av as number) - (bv as number) : (bv as number) - (av as number)
    })
  }, [data, sortField, sortAsc])

  function toggleSort(field: SortField) {
    if (sortField === field) {
      setSortAsc(!sortAsc)
    } else {
      setSortField(field)
      setSortAsc(false)
    }
  }

  if (data.length === 0) {
    return <p className="text-xs text-fg-faint italic py-2">No utility calls recorded yet</p>
  }

  const SortHeader = ({ field, label, align = 'left' }: { field: SortField; label: string; align?: 'left' | 'right' }) => (
    <th
      className={`py-2 pr-3 font-medium cursor-pointer hover:text-fg-secondary transition-colors ${align === 'right' ? 'text-right' : 'text-left'}`}
      onClick={() => toggleSort(field)}
    >
      <span className="inline-flex items-center gap-1">
        {label}
        {sortField === field && <ArrowUpDown className="w-2.5 h-2.5" />}
      </span>
    </th>
  )

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-xs">
        <thead>
          <tr className="text-[10px] uppercase tracking-wider text-fg-muted border-b border-border">
            <SortHeader field="created_at" label="Time" />
            <th className="text-left py-2 pr-3 font-medium">Provider</th>
            <th className="text-left py-2 pr-3 font-medium">Model</th>
            <th className="text-left py-2 pr-3 font-medium">Mode</th>
            <SortHeader field="duration_ms" label="Duration" align="right" />
            <SortHeader field="estimated_cost_usd" label="Cost" align="right" />
            <th className="text-center py-2 font-medium">Status</th>
          </tr>
        </thead>
        <tbody>
          {sorted.map((row) => (
            <tr key={row.id} className="border-b border-border/30 hover:bg-surface/20 transition-colors">
              <td className="py-1.5 pr-3 text-fg-muted whitespace-nowrap">{timeAgo(row.created_at)}</td>
              <td className="py-1.5 pr-3 text-fg-secondary">{row.provider}</td>
              <td className="py-1.5 pr-3 font-mono text-fg-secondary whitespace-nowrap">{row.model}</td>
              <td className="py-1.5 pr-3 text-fg-secondary">{row.mode}</td>
              <td className="py-1.5 pr-3 text-right font-mono tabular-nums text-blue-400">{formatDuration(row.duration_ms)}</td>
              <td className="py-1.5 pr-3 text-right font-mono tabular-nums text-amber-400">{formatCost(row.estimated_cost_usd)}</td>
              <td className="py-1.5 text-center">
                <span className={`inline-block w-2 h-2 rounded-full ${row.error ? 'bg-red-500' : 'bg-emerald-500'}`} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
