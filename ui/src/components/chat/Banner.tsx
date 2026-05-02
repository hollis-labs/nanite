import { AlertTriangle, CheckCircle2, Info, RefreshCw, X, type LucideIcon } from 'lucide-react'

/**
 * Banner — reusable alert/approval/notification surface.
 *
 * Mirrors the visual language of the ToolCallBanner (the original
 * ToolCallDrawer condensed header):
 *   - bg-bg-elevated, subtle border, soft shadow
 *   - rounded pill (rounded-[10px])
 *   - colored top accent ribbon (tone-driven)
 *   - leading icon chip (rounded-[5px], colored bg + colored icon)
 *   - tone-colored title; muted body text
 *   - action buttons: primary uses inverted fg/bg for high contrast,
 *     neutral is text-only with hover surface
 *
 * Two sizes: `sm` (matches the original ToolCallBanner dimensions) and
 * `md` (used for the in-drawer alert/approval overlay).
 *
 * Pure presentation — caller owns dismiss/timeout state.
 */

export type BannerTone = 'info' | 'warning' | 'danger' | 'success'

export interface BannerAction {
  label: string
  onClick: () => void
  /** Show a refresh icon next to the label (Retry, Reconnect, Reload). */
  refresh?: boolean
  /** Show the dismiss-X icon (Dismiss, Cancel). */
  dismiss?: boolean
  /** Filled, high-contrast treatment — use for the primary/confirm action. */
  primary?: boolean
}

export interface BannerProps {
  tone: BannerTone
  title: string
  body?: string
  size?: 'sm' | 'md'
  /** Override the leading icon. Defaults: info → Info, success → CheckCircle2,
   *  warning/danger → AlertTriangle. */
  icon?: LucideIcon
  actions?: BannerAction[]
  className?: string
}

const TONE_STYLES: Record<BannerTone, { ribbon: string; icon: string; chipBg: string }> = {
  info:    { ribbon: 'bg-info',    icon: 'text-info',    chipBg: 'bg-info-muted' },
  warning: { ribbon: 'bg-warning', icon: 'text-warning', chipBg: 'bg-warning-muted' },
  danger:  { ribbon: 'bg-danger',  icon: 'text-danger',  chipBg: 'bg-danger-muted' },
  success: { ribbon: 'bg-success', icon: 'text-success', chipBg: 'bg-success-muted' },
}

const ORIGINAL_SHADOW =
  'shadow-[0_4px_12px_-6px_rgba(0,0,0,0.22),0_-2px_0_-1px_rgba(0,0,0,0.04)]'

export function Banner({
  tone,
  title,
  body,
  size = 'md',
  icon: IconOverride,
  actions = [],
  className = '',
}: BannerProps) {
  const c = TONE_STYLES[tone]
  const Icon =
    IconOverride ??
    (tone === 'success'
      ? CheckCircle2
      : tone === 'info'
        ? Info
        : AlertTriangle)
  const isLarge = size === 'md'

  return (
    <div
      className={`relative overflow-hidden rounded-[10px] border border-border-subtle bg-bg-elevated ${ORIGINAL_SHADOW} ${className}`}
    >
      {/* Tone-colored top accent ribbon. */}
      <div className={`pointer-events-none absolute inset-x-0 top-0 h-[2px] ${c.ribbon} opacity-85`} />

      <div className={`flex items-start gap-3 ${isLarge ? 'p-4' : 'px-3.5 py-2.5'}`}>
        {/* Icon chip — round, tone-colored bg with tone-colored icon. */}
        <span
          className={`flex shrink-0 items-center justify-center rounded-[5px] ${c.chipBg} ${
            isLarge ? 'h-7 w-7' : 'h-[18px] w-[18px]'
          }`}
        >
          <Icon className={`${c.icon} ${isLarge ? 'h-4 w-4' : 'h-3 w-3'}`} />
        </span>

        <div className="flex-1 min-w-0">
          {/* Tone-colored title. */}
          <p className={`font-medium ${c.icon} ${isLarge ? 'text-sm' : 'text-xs'}`}>{title}</p>
          {body && (
            <p className={`mt-1 text-fg-muted ${isLarge ? 'text-xs' : 'text-[11px]'}`}>{body}</p>
          )}
          {actions.length > 0 && (
            <div className={`flex flex-wrap gap-2 ${isLarge ? 'mt-3' : 'mt-2'}`}>
              {actions.map((a) => (
                <button
                  key={a.label}
                  type="button"
                  onClick={a.onClick}
                  className={`inline-flex items-center gap-1.5 rounded-[6px] px-3 py-1.5 text-xs font-medium transition-colors ${
                    a.primary
                      ? 'bg-fg text-bg hover:bg-fg-secondary'
                      : 'text-fg-secondary hover:bg-surface-hover'
                  }`}
                >
                  {a.refresh && <RefreshCw className="w-3.5 h-3.5" />}
                  {a.dismiss && <X className="w-3.5 h-3.5" />}
                  {a.label}
                </button>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
