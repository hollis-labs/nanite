import { useState, useEffect, type ReactNode } from 'react'
import { ChevronDown, ChevronRight, type LucideIcon } from 'lucide-react'

interface WidgetProps {
  id: string
  title: string
  icon: LucideIcon
  children: ReactNode
}

export function Widget({ id, title, icon: Icon, children }: WidgetProps) {
  const storageKey = `conduit-widget-${id}`

  const [minimized, setMinimized] = useState(() => {
    try {
      return localStorage.getItem(storageKey) === 'minimized'
    } catch {
      return false
    }
  })

  useEffect(() => {
    try {
      localStorage.setItem(storageKey, minimized ? 'minimized' : 'open')
    } catch {
      // ignore
    }
  }, [minimized, storageKey])

  return (
    <div className="rounded-lg border border-border bg-bg-elevated/50">
      <button
        onClick={() => setMinimized((m) => !m)}
        className="flex items-center gap-2 w-full px-3 py-2.5 hover:bg-surface/30 transition-colors rounded-t-lg"
      >
        <Icon className="w-3.5 h-3.5 text-fg-muted shrink-0" />
        <h3 className="text-xs font-medium text-fg-secondary uppercase tracking-wider flex-1 text-left">
          {title}
        </h3>
        {minimized ? (
          <ChevronRight className="w-3.5 h-3.5 text-fg-faint" />
        ) : (
          <ChevronDown className="w-3.5 h-3.5 text-fg-faint" />
        )}
      </button>
      {!minimized && <div className="px-3 pb-3">{children}</div>}
    </div>
  )
}
