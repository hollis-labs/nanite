interface SourceBadgeProps {
  source: string
  className?: string
}

export const SOURCE_LABELS: Record<string, string> = {
  seed: 'system',
  api: 'api',
  agentrc: 'agentrc',
}

export function SourceBadge({ source, className = '' }: SourceBadgeProps) {
  if (!source) return null
  const label = SOURCE_LABELS[source] ?? source
  return (
    <span
      className={`text-[10px] px-1.5 py-0.5 rounded-md bg-bg-elevated border border-border-subtle text-fg-muted leading-none shrink-0 ${className}`}
    >
      {label}
    </span>
  )
}
