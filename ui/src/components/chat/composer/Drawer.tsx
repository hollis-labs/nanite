import { X } from 'lucide-react'
import type { ReactNode } from 'react'

type Tone = 'primary' | 'warning' | 'info' | 'danger' | 'success' | 'brand'
type Size = 'compact' | 'tall'

const TONE_BG: Record<Tone, string> = {
  primary: 'bg-primary',
  warning: 'bg-warning',
  info: 'bg-info',
  danger: 'bg-danger',
  success: 'bg-success',
  brand: 'bg-brand',
}

const TONE_FG: Record<Tone, string> = {
  primary: 'text-primary',
  warning: 'text-warning',
  info: 'text-info',
  danger: 'text-danger',
  success: 'text-success',
  brand: 'text-brand',
}

interface DrawerProps {
  tone?: Tone
  kind: string
  title?: string
  count?: string | number
  size?: Size
  onDismiss?: () => void
  footer?: ReactNode
  children: ReactNode
}

export function Drawer({
  tone = 'primary',
  kind,
  title,
  count,
  size = 'compact',
  onDismiss,
  footer,
  children,
}: DrawerProps) {
  const isTall = size === 'tall'

  return (
    <div className="relative z-[1]">
      <div
        className={[
          'relative overflow-hidden border border-border-subtle bg-bg-surface',
          'rounded-t-[10px] rounded-b-none',
          'shadow-[0_-2px_6px_-3px_rgba(0,0,0,0.18),0_2px_0_-1px_rgba(0,0,0,0.04)]',
          isTall ? 'flex h-[480px] flex-col' : '',
        ].join(' ')}
      >
        <div className={`absolute inset-x-0 top-0 h-[3px] ${TONE_BG[tone]}`} />

        <div className="flex items-center justify-between gap-3 border-b border-divider px-4 py-3">
          <div className="flex min-w-0 flex-1 items-center gap-2.5">
            <span className={`font-mono text-[10px] font-semibold uppercase tracking-wide ${TONE_FG[tone]}`}>
              {kind}
            </span>
            {count != null && (
              <span className="rounded-sm bg-surface px-1.5 py-px font-mono text-[10px] font-medium text-fg-muted">
                {count}
              </span>
            )}
            {title && (
              <>
                <div className="h-2.5 w-px bg-divider" />
                <span className="truncate text-xs text-fg-secondary">{title}</span>
              </>
            )}
          </div>
          {onDismiss && (
            <button
              type="button"
              title="Dismiss"
              onClick={onDismiss}
              className="flex h-[22px] w-[22px] items-center justify-center rounded text-fg-muted hover:bg-surface hover:text-fg-secondary"
            >
              <X className="h-3 w-3" />
            </button>
          )}
        </div>

        <div className={isTall ? 'flex-1 overflow-auto px-4 py-3.5' : 'px-4 pb-3.5 pt-3'}>
          {children}
        </div>

        {footer && (
          <div className="flex items-center justify-end gap-1.5 border-t border-divider bg-bg-elevated px-3.5 py-2.5">
            {footer}
          </div>
        )}
      </div>
    </div>
  )
}
