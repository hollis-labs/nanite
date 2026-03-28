interface SourceBadgeProps {
  source: string
  className?: string
}

export function SourceBadge({ source, className = '' }: SourceBadgeProps) {
  if (!source) return null
  return (
    <span
      className={`text-[10px] px-1.5 py-0 rounded-full bg-zinc-800 text-zinc-500 leading-relaxed shrink-0 ${className}`}
    >
      {source}
    </span>
  )
}
