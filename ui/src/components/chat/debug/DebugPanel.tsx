import { useState } from 'react'
import { ChevronDown, ChevronRight } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'

interface DebugPanelProps {
  title: string
  icon: LucideIcon
  children: React.ReactNode
  defaultOpen?: boolean
  badge?: string | number
}

/**
 * Collapsible debug panel — only rendered in developer_mode.
 * Follows Variation F card pattern with compact header.
 */
export function DebugPanel({ title, icon: Icon, children, defaultOpen = false, badge }: DebugPanelProps) {
  const [open, setOpen] = useState(defaultOpen)

  return (
    <div className="rounded-xl border border-border-subtle bg-white dark:bg-bg-elevated/60 shadow-sm overflow-hidden">
      <button
        onClick={() => setOpen((o) => !o)}
        className="w-full flex items-center gap-2 px-3.5 py-2 text-left hover:bg-surface/30 transition-colors"
      >
        <div className="w-6 h-6 rounded-md bg-surface/50 flex items-center justify-center shrink-0">
          <Icon className="w-3.5 h-3.5 text-fg-muted" />
        </div>
        <span className="text-[11px] uppercase tracking-wider text-fg-muted font-medium flex-1">{title}</span>
        {badge != null && (
          <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-bg-elevated border border-border-subtle text-fg-muted leading-none">
            {badge}
          </span>
        )}
        {open ? (
          <ChevronDown className="w-3.5 h-3.5 text-fg-faint shrink-0" />
        ) : (
          <ChevronRight className="w-3.5 h-3.5 text-fg-faint shrink-0" />
        )}
      </button>
      {open && (
        <div className="border-t border-border/50 px-3.5 py-2.5">
          {children}
        </div>
      )}
    </div>
  )
}
