import type { LucideIcon } from 'lucide-react'

interface StatCardProps {
  label: string
  value: string
  subValue?: string
  icon: LucideIcon
  color?: string
}

export function StatCard({ label, value, subValue, icon: Icon, color = 'text-zinc-100' }: StatCardProps) {
  return (
    <div className="rounded-lg border border-zinc-800 bg-zinc-900/50 px-4 py-3">
      <div className="flex items-center gap-2 mb-1">
        <Icon className="w-3.5 h-3.5 text-zinc-500" />
        <span className="text-[10px] uppercase tracking-wider text-zinc-500">{label}</span>
      </div>
      <div className={`text-xl font-mono tabular-nums ${color}`}>{value}</div>
      {subValue && (
        <div className="text-[10px] text-zinc-500 font-mono tabular-nums mt-0.5">{subValue}</div>
      )}
    </div>
  )
}
