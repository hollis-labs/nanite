import { ChevronDown, Monitor, Moon, Sun } from "lucide-react"
import type { LucideIcon } from "lucide-react"
import { cn } from "@/lib/utils"

// --- PanelHeader ---

export function PanelHeader({ title, description }: { title: string; description?: string }) {
  return (
    <div className="mb-6">
      <h2 className="text-[20px] font-semibold text-fg">{title}</h2>
      {description && (
        <p className="text-[13px] text-fg-muted mt-1.5 leading-relaxed">{description}</p>
      )}
    </div>
  )
}

// --- SCard ---

type SCardAccent = 'brand' | 'success' | 'warning' | 'danger' | 'info' | 'primary'

const SCARD_ACCENT_CLASSES = {
  brand:   'border-l-2 border-l-brand',
  success: 'border-l-2 border-l-status-ok',
  warning: 'border-l-2 border-l-status-warn',
  danger:  'border-l-2 border-l-status-danger',
  info:    'border-l-2 border-l-fg-secondary',
  primary: 'border-l-2 border-l-fg',
} satisfies Record<SCardAccent, string>

export function SCard({
  title,
  meta,
  description,
  children,
  className,
  accent,
}: {
  title: string
  meta?: string
  description?: string
  children: React.ReactNode
  className?: string
  accent?: SCardAccent
}) {
  return (
    <div
      className={cn(
        "bg-bg-elevated border border-border-subtle rounded-[10px] overflow-hidden mb-4",
        accent && SCARD_ACCENT_CLASSES[accent],
        className,
      )}
    >
      <div className="px-4 py-3 border-b border-border-subtle">
        <div className="flex items-center gap-2">
          <span className="font-mono text-[11px] font-semibold uppercase tracking-[0.04em] text-fg-secondary">
            {title}
          </span>
          {meta && <span className="font-mono text-[11px] text-fg-muted ml-auto">{meta}</span>}
        </div>
        {description && <p className="text-xs text-fg-muted mt-1">{description}</p>}
      </div>
      {children}
    </div>
  )
}

// --- SRow ---

export function SRow({
  label,
  description,
  children,
  vertical,
  className,
  onClick,
}: {
  label: string
  description?: string
  children: React.ReactNode
  vertical?: boolean
  className?: string
  onClick?: () => void
}) {
  if (vertical) {
    return (
      <div
        className={cn(
          "px-4 py-3 space-y-2 border-b border-border-subtle [&:last-child]:border-b-0",
          className,
        )}
        onClick={onClick}
      >
        <div className="min-w-0">
          <div className="text-[13px] text-fg">{label}</div>
          {description && <div className="text-xs text-fg-muted mt-0.5">{description}</div>}
        </div>
        <div>{children}</div>
      </div>
    )
  }
  return (
    <div
      className={cn(
        "flex items-center justify-between gap-4 px-4 py-3 border-b border-border-subtle [&:last-child]:border-b-0",
        className,
      )}
      onClick={onClick}
    >
      <div className="min-w-0">
        <div className="text-[13px] text-fg">{label}</div>
        {description && <div className="text-xs text-fg-muted mt-0.5">{description}</div>}
      </div>
      <div className="shrink-0">{children}</div>
    </div>
  )
}

// --- SToggle ---

export function SToggle({
  checked,
  onChange,
  variant = "default",
}: {
  checked: boolean
  onChange: (checked: boolean) => void
  variant?: "default" | "warning"
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      onClick={() => onChange(!checked)}
      className={cn(
        "relative inline-flex h-5 w-9 rounded-full transition-[background-color] duration-[160ms] shrink-0",
        checked ? (variant === "warning" ? "bg-warning" : "bg-primary") : "bg-surface",
      )}
    >
      <span
        className={cn(
          "absolute top-0.5 h-4 w-4 rounded-full bg-white shadow-sm transition-[left] duration-[160ms]",
          checked ? "left-[18px]" : "left-0.5",
        )}
      />
    </button>
  )
}

// --- SSelect ---

export function SSelect({
  value,
  options,
  onChange,
  allowNone = false,
  disabled,
  width = "w-48",
  className,
}: {
  value: string
  options: { value: string; label: string }[]
  onChange: (value: string) => void
  allowNone?: boolean
  disabled?: boolean
  width?: string
  className?: string
}) {
  return (
    <div className={cn("relative", width, className)}>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        className="appearance-none w-full bg-surface border border-border-subtle rounded-[6px] py-[7px] pl-3 pr-8 text-[13px] text-fg focus:outline-none focus:ring-1 focus:ring-primary focus:border-primary disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
      >
        {allowNone && <option value="">None</option>}
        {options.map((opt) => (
          <option key={opt.value} value={opt.value}>
            {opt.label}
          </option>
        ))}
      </select>
      <ChevronDown className="absolute right-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-fg-faint pointer-events-none" />
    </div>
  )
}

// --- STextField ---

export function STextField({
  value,
  onChange,
  placeholder,
  type = "text",
  width = "w-60",
  className,
}: {
  value: string
  onChange: (value: string) => void
  placeholder?: string
  type?: string
  width?: string
  className?: string
}) {
  return (
    <input
      type={type}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      placeholder={placeholder}
      className={cn(
        "bg-surface border border-border-subtle rounded-[6px] py-[7px] px-3 text-[13px] text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-primary focus:border-primary",
        width,
        className,
      )}
    />
  )
}

// --- ThemeSeg ---

export type ThemeOption = "system" | "light" | "dark"

const THEME_SEG_OPTIONS: { value: ThemeOption; label: string; icon: LucideIcon }[] = [
  { value: "light", label: "Light", icon: Sun },
  { value: "dark", label: "Dark", icon: Moon },
  { value: "system", label: "System", icon: Monitor },
]

export function ThemeSeg({
  value,
  onChange,
}: {
  value: ThemeOption
  onChange: (value: ThemeOption) => void
}) {
  return (
    <div className="inline-flex p-[3px] gap-[2px] bg-surface border border-border-subtle rounded-[7px]">
      {THEME_SEG_OPTIONS.map((opt) => {
        const Icon = opt.icon
        const isActive = value === opt.value
        return (
          <button
            key={opt.value}
            onClick={() => onChange(opt.value)}
            className={cn(
              "flex items-center gap-1.5 px-3 py-[5px] rounded-[5px] text-xs transition-colors",
              isActive
                ? "bg-bg-elevated text-fg font-medium shadow-sm"
                : "text-fg-muted hover:text-fg-secondary",
            )}
          >
            <Icon className="size-3" />
            {opt.label}
          </button>
        )
      })}
    </div>
  )
}

// --- Kbd / KbdGroup ---

export function Kbd({
  children,
  active,
  className,
}: {
  children: React.ReactNode
  active?: boolean
  className?: string
}) {
  return (
    <kbd
      className={cn(
        "inline-flex items-center justify-center min-w-[22px] h-[22px] px-1.5 rounded-[4px] font-mono text-[11px] font-medium",
        active
          ? "bg-primary-muted border border-transparent text-primary"
          : "bg-surface border border-border-subtle text-fg-secondary shadow-[0_1px_0_0] shadow-border-subtle",
        className,
      )}
    >
      {children}
    </kbd>
  )
}

export function KbdGroup({
  children,
  className,
}: {
  children: React.ReactNode
  className?: string
}) {
  return <div className={cn("inline-flex items-center gap-[3px]", className)}>{children}</div>
}
