import type { LucideIcon } from 'lucide-react'

interface StatCardProps {
  label: string
  value: string
  subValue?: string
  icon: LucideIcon
  color?: string
}

export function StatCard({ label, value, subValue, icon: Icon, color = 'text-fg' }: StatCardProps) {
  return (
    <div className="rounded-xl border border-border-subtle bg-white dark:bg-bg-elevated/60 shadow-sm px-4 py-3">
      <div className="flex items-center gap-2 mb-1">
        <Icon className="w-3.5 h-3.5 text-fg-muted" />
        <span className="text-[10px] uppercase tracking-wider text-fg-muted font-medium">{label}</span>
      </div>
      <div className={`text-xl font-mono tabular-nums ${color}`}>{value}</div>
      {subValue && (
        <div className="text-[10px] text-fg-muted font-mono tabular-nums mt-0.5">{subValue}</div>
      )}
    </div>
  )
}
