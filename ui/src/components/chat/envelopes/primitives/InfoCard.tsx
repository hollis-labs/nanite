import { Info, CheckCircle, AlertTriangle, XCircle, type LucideIcon } from 'lucide-react'

interface InfoCardData {
  title: string
  body: string
  variant?: 'info' | 'success' | 'warning' | 'danger'
}

interface InfoCardProps {
  data: InfoCardData
}

const VARIANT_STYLES: Record<string, { border: string; bg: string; text: string; icon: LucideIcon }> = {
  info:    { border: 'border-info/30',    bg: 'bg-info/5',    text: 'text-info',    icon: Info },
  success: { border: 'border-success/30', bg: 'bg-success/5', text: 'text-success', icon: CheckCircle },
  warning: { border: 'border-warning/30', bg: 'bg-warning/5', text: 'text-warning', icon: AlertTriangle },
  danger:  { border: 'border-danger/30',  bg: 'bg-danger/5',  text: 'text-danger',  icon: XCircle },
}

export function InfoCard({ data }: InfoCardProps) {
  const variant = data.variant || 'info'
  const style = VARIANT_STYLES[variant] ?? VARIANT_STYLES.info
  const Icon = style.icon

  return (
    <div className={`rounded-sm border-l-4 ${style.border} ${style.bg} p-4`}>
      <div className="flex items-start gap-3">
        <Icon className={`w-4 h-4 mt-0.5 shrink-0 ${style.text}`} />
        <div className="min-w-0">
          <h4 className="text-sm font-medium text-fg">{data.title}</h4>
          <p className="text-sm text-fg-secondary mt-1">{data.body}</p>
        </div>
      </div>
    </div>
  )
}
