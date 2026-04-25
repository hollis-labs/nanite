import * as React from 'react'
import type { LucideIcon } from 'lucide-react'
import { cn } from '@/lib/utils'

/**
 * Envelope — the unified shell for every chat envelope card.
 *
 * Replaces the ad-hoc `<div className="rounded-sm border border-border-subtle bg-bg-elevated/50 ...">`
 * pattern scattered across envelope components. Every envelope should compose from these pieces.
 *
 * Design tokens (locked):
 *   - radius: 10px (rounded-[10px]) — not rounded-sm (2px) or rounded-full
 *   - surface: bg-bg-elevated  — not bg-bg-elevated/50, /60, /30 etc.
 *   - border:  border-border-subtle
 *   - optional 3px left accent via `accent="success" | "warning" | ...`
 */

export type EnvelopeTone =
  | 'neutral'
  | 'primary'
  | 'success'
  | 'warning'
  | 'danger'
  | 'info'
  | 'brand'

const ACCENT_CLASS: Record<EnvelopeTone, string> = {
  neutral: 'shadow-[inset_3px_0_0_0_var(--color-border)]',
  primary: 'shadow-[inset_3px_0_0_0_var(--color-primary)]',
  success: 'shadow-[inset_3px_0_0_0_var(--color-success)]',
  warning: 'shadow-[inset_3px_0_0_0_var(--color-warning)]',
  danger:  'shadow-[inset_3px_0_0_0_var(--color-danger)]',
  info:    'shadow-[inset_3px_0_0_0_var(--color-info)]',
  brand:   'shadow-[inset_3px_0_0_0_var(--color-brand)]',
}

interface EnvelopeProps extends React.HTMLAttributes<HTMLDivElement> {
  accent?: EnvelopeTone
  /** Use for "applied / rejected / dismissed" terminal states — visually dials back */
  muted?: boolean
}

export function Envelope({
  accent,
  muted,
  className,
  children,
  ...rest
}: EnvelopeProps) {
  return (
    <div
      data-slot="envelope"
      className={cn(
        'relative overflow-hidden rounded-[10px] border border-border-subtle bg-bg-elevated',
        accent && ACCENT_CLASS[accent],
        muted && 'opacity-70',
        className,
      )}
      {...rest}
    >
      {children}
    </div>
  )
}

/* ────────────────────────────────────────────────────────────── */
/*  EnvelopeHeader — mono uppercase label + optional icon + meta   */
/* ────────────────────────────────────────────────────────────── */

const ICON_TONE: Record<EnvelopeTone, string> = {
  neutral: 'text-fg-muted',
  primary: 'text-primary',
  success: 'text-success',
  warning: 'text-warning',
  danger:  'text-danger',
  info:    'text-info',
  brand:   'text-brand',
}

interface EnvelopeHeaderProps {
  icon?: LucideIcon
  label: string
  /** Right-aligned meta — usually a count, timestamp, or id */
  meta?: React.ReactNode
  /** Colors the icon only; use <Envelope accent="..."> for the left rule */
  tone?: EnvelopeTone
  /** Slot for action buttons (rightmost) */
  action?: React.ReactNode
  className?: string
}

export function EnvelopeHeader({
  icon: Icon,
  label,
  meta,
  tone = 'neutral',
  action,
  className,
}: EnvelopeHeaderProps) {
  return (
    <div
      data-slot="envelope-header"
      className={cn(
        'flex items-center justify-between gap-3 px-4 py-2.5',
        className,
      )}
    >
      <div className="flex min-w-0 items-center gap-2">
        {Icon && <Icon className={cn('h-3.5 w-3.5 shrink-0', ICON_TONE[tone])} />}
        <span className="truncate font-mono text-[11px] font-semibold uppercase tracking-wide text-fg-muted">
          {label}
        </span>
      </div>
      <div className="flex items-center gap-2">
        {meta != null && (
          <span className="font-mono text-[11px] text-fg-muted">{meta}</span>
        )}
        {action}
      </div>
    </div>
  )
}

/* ────────────────────────────────────────────────────────────── */
/*  EnvelopeBody — consistent padding, optional title             */
/* ────────────────────────────────────────────────────────────── */

interface EnvelopeBodyProps extends Omit<React.HTMLAttributes<HTMLDivElement>, 'title'> {
  /** Large display title — separate from the mono label in the header */
  title?: React.ReactNode
  /** Short description under the title */
  description?: React.ReactNode
}

export function EnvelopeBody({
  title,
  description,
  className,
  children,
  ...rest
}: EnvelopeBodyProps) {
  return (
    <div
      data-slot="envelope-body"
      className={cn('px-4 py-3', className)}
      {...rest}
    >
      {title && (
        <h3 className="text-[14px] font-semibold leading-snug text-fg">
          {title}
        </h3>
      )}
      {description && (
        <p className="mt-1 text-[13px] leading-relaxed text-fg-secondary">
          {description}
        </p>
      )}
      {(title || description) && children && <div className="mt-3">{children}</div>}
      {!title && !description && children}
    </div>
  )
}

/* ────────────────────────────────────────────────────────────── */
/*  EnvelopeFooter — bordered-top action row                      */
/* ────────────────────────────────────────────────────────────── */

interface EnvelopeFooterProps extends React.HTMLAttributes<HTMLDivElement> {}

export function EnvelopeFooter({ className, children, ...rest }: EnvelopeFooterProps) {
  return (
    <div
      data-slot="envelope-footer"
      className={cn(
        'flex items-center gap-2 px-4 py-2.5',
        className,
      )}
      {...rest}
    >
      {children}
    </div>
  )
}

/* ────────────────────────────────────────────────────────────── */
/*  EnvelopeSection — inline bordered block inside the body       */
/* ────────────────────────────────────────────────────────────── */

export function EnvelopeSection({
  label,
  className,
  children,
  ...rest
}: React.HTMLAttributes<HTMLDivElement> & { label?: React.ReactNode }) {
  return (
    <div
      data-slot="envelope-section"
      className={cn('space-y-1.5', className)}
      {...rest}
    >
      {label && (
        <div className="font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-muted">
          {label}
        </div>
      )}
      {children}
    </div>
  )
}
