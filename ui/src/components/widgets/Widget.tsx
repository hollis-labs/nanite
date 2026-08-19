import { useState, useEffect, type ReactNode } from 'react'
import { ChevronDown, ChevronRight, type LucideIcon } from 'lucide-react'

// ---------------------------------------------------------------------------
// Widget shell
// ---------------------------------------------------------------------------

interface WidgetProps {
  id: string
  title: string
  icon: LucideIcon
  children: ReactNode
  /** Tailwind color class for the icon, e.g. `text-brand` */
  accent?: string
  /** Small metadata string shown right of title */
  meta?: ReactNode
  /** Initial open state — persisted in localStorage */
  defaultOpen?: boolean
}

export function Widget({ id, title, icon: Icon, children, accent, meta, defaultOpen = true }: WidgetProps) {
  const storageKey = `nanite-widget-${id}`

  const [open, setOpen] = useState(() => {
    try {
      const stored = localStorage.getItem(storageKey)
      if (stored === 'open') return true
      if (stored === 'minimized') return false
      return defaultOpen
    } catch {
      return defaultOpen
    }
  })

  useEffect(() => {
    try {
      localStorage.setItem(storageKey, open ? 'open' : 'minimized')
    } catch {
      // ignore
    }
  }, [open, storageKey])

  return (
    <div className="rounded-[10px] border border-border-subtle bg-bg-elevated overflow-hidden">
      <button
        onClick={() => setOpen((o) => !o)}
        className={`flex items-center gap-2 w-full px-3 py-2.5 hover:bg-surface transition-colors ${open ? 'border-b border-divider' : ''}`}
      >
        <Icon className={`w-3 h-3 shrink-0 ${accent ?? 'text-fg-muted'}`} />
        <h3 className="font-mono text-[11px] font-semibold uppercase tracking-[0.04em] text-fg-muted flex-1 text-left">
          {title}
        </h3>
        {meta != null && (
          <span className="font-mono text-[10px] text-fg-faint">{meta}</span>
        )}
        {open ? (
          <ChevronDown className="w-3 h-3 text-fg-faint shrink-0" />
        ) : (
          <ChevronRight className="w-3 h-3 text-fg-faint shrink-0" />
        )}
      </button>
      {open && <div className="px-3 pt-2.5 pb-3">{children}</div>}
    </div>
  )
}

// ---------------------------------------------------------------------------
// WidgetRow — two-column label / value row
// ---------------------------------------------------------------------------

export function WidgetRow({
  label,
  children,
  mono = false,
}: {
  label: ReactNode
  children: ReactNode
  mono?: boolean
}) {
  return (
    <div className="flex items-center justify-between gap-[10px] min-h-[20px] text-[12px]">
      <span className="text-fg-muted shrink-0">{label}</span>
      <span
        className={`text-fg-secondary text-right min-w-0 overflow-hidden text-ellipsis whitespace-nowrap ${
          mono ? 'font-mono text-[11px]' : ''
        }`}
      >
        {children}
      </span>
    </div>
  )
}

// ---------------------------------------------------------------------------
// StatusDot — inline dot + label
// ---------------------------------------------------------------------------

type StatusTone = 'neutral' | 'success' | 'warning' | 'danger' | 'info' | 'primary'

const DOT_COLORS: Record<StatusTone, string> = {
  neutral: 'bg-fg-faint',
  success: 'bg-success',
  warning: 'bg-warning',
  danger:  'bg-danger',
  info:    'bg-info',
  primary: 'bg-primary',
}

export function StatusDot({
  tone = 'neutral',
  pulse = false,
  children,
}: {
  tone?: StatusTone
  pulse?: boolean
  children?: ReactNode
}) {
  return (
    <span className="inline-flex items-center gap-1.5 text-fg-secondary text-[12px]">
      <span
        className={`w-1.5 h-1.5 rounded-full shrink-0 ${DOT_COLORS[tone]} ${pulse ? 'animate-pulse' : ''}`}
      />
      {children}
    </span>
  )
}

// ---------------------------------------------------------------------------
// Bar — thin progress bar
// ---------------------------------------------------------------------------

type BarTone = 'neutral' | 'success' | 'warning' | 'danger'

const BAR_FILL: Record<BarTone, string> = {
  neutral: 'bg-fg-muted',
  success: 'bg-success',
  warning: 'bg-warning',
  danger:  'bg-danger',
}

export function Bar({ pct, tone = 'neutral' }: { pct: number; tone?: BarTone }) {
  return (
    <div className="w-full h-[4px] rounded-[3px] bg-surface overflow-hidden">
      <div
        className={`h-full ${BAR_FILL[tone]} transition-[width] duration-[400ms]`}
        style={{ width: `${Math.max(2, Math.min(100, pct))}%` }}
      />
    </div>
  )
}

// ---------------------------------------------------------------------------
// pctTone — bar color helper
// ---------------------------------------------------------------------------

export function pctTone(pct: number): BarTone {
  if (pct < 50) return 'success'
  if (pct < 75) return 'warning'
  return 'danger'
}

