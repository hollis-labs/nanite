interface TagPillsProps {
  tags: string
  max?: number
  className?: string
}

export function TagPills({ tags, max = 3, className = '' }: TagPillsProps) {
  let parsed: string[]
  try {
    parsed = JSON.parse(tags || '[]')
    if (!Array.isArray(parsed)) return null
  } catch {
    return null
  }
  if (parsed.length === 0) return null

  return (
    <div className={`flex gap-1 flex-wrap ${className}`}>
      {parsed.slice(0, max).map((tag) => (
        <span
          key={tag}
          className="text-[10px] px-1.5 py-0.5 rounded-md bg-bg-elevated border border-border-subtle text-fg-muted leading-none"
        >
          {tag}
        </span>
      ))}
    </div>
  )
}
