interface SourceBadgeProps {
  source: string
  className?: string
}

export function SourceBadge({ source, className = '' }: SourceBadgeProps) {
  if (!source) return null
  return (
    <span
      className={`text-[10px] px-1.5 py-0.5 rounded-md bg-bg-elevated border border-border-subtle text-fg-muted leading-none shrink-0 ${className}`}
    >
      {source}
    </span>
  )
}
