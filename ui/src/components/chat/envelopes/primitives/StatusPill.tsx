import * as React from 'react'
import { cn } from '@/lib/utils'

/**
 * StatusPill — the ONE pill shape for envelopes.
 *
 * Replaces the patchwork of `rounded`, `rounded-full`, variable padding,
 * and ad-hoc `bg-success/15 text-success border-success/25` combos.
 *
 * Shape: 4px rounded rectangle (matches primary UI radii scale: 4/6/10).
 * Type:  10px JetBrains Mono, uppercase, +0.4 letter-spacing.
 * Tone:  semantic token, muted background, 40% saturation.
 *
 * DO use for: status labels, severity, source badges, IDs, flags.
 * DON'T use for: filter chips (use shadcn Badge), avatars (use Avatar).
 */

export type StatusTone =
  | 'neutral'
  | 'primary'
  | 'success'
  | 'warning'
  | 'danger'
  | 'info'
  | 'brand'

const TONE_CLASS: Record<StatusTone, string> = {
  neutral: 'bg-surface text-fg-secondary',
  primary: 'bg-primary/15 text-primary',
  success: 'bg-success/15 text-success',
  warning: 'bg-warning/15 text-warning',
  danger:  'bg-danger/15 text-danger',
  info:    'bg-info/15 text-info',
  brand:   'bg-brand/15 text-brand',
}

interface StatusPillProps extends React.HTMLAttributes<HTMLSpanElement> {
  tone?: StatusTone
  /** Solid fill instead of muted — use sparingly for "live" signals */
  solid?: boolean
  children: React.ReactNode
}

export function StatusPill({
  tone = 'neutral',
  solid = false,
  className,
  children,
  ...rest
}: StatusPillProps) {
  return (
    <span
      data-slot="status-pill"
      data-tone={tone}
      className={cn(
        'inline-flex items-center gap-1 rounded-[4px] px-1.5 py-0.5 font-mono text-[10px] font-semibold uppercase leading-[1.4] tracking-wide',
        solid ? toneToSolid(tone) : TONE_CLASS[tone],
        className,
      )}
      {...rest}
    >
      {children}
    </span>
  )
}

function toneToSolid(tone: StatusTone) {
  switch (tone) {
    case 'success': return 'bg-success text-white'
    case 'warning': return 'bg-warning text-white'
    case 'danger':  return 'bg-danger text-white'
    case 'info':    return 'bg-info text-white'
    case 'primary': return 'bg-primary text-primary-foreground'
    case 'brand':   return 'bg-brand text-brand-fg'
    default:        return 'bg-fg-muted text-bg'
  }
}
