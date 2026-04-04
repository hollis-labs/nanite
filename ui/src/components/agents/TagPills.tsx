interface TagPillsProps {
  tags: string
  max?: number
  className?: string
}

// Deterministic color from tag string — consistent across renders
const TAG_COLORS = [
  { bg: 'bg-info/15', border: 'border-info/30', text: 'text-info' },
  { bg: 'bg-purple-500/10', border: 'border-purple-500/20', text: 'text-purple-400' },
  { bg: 'bg-warning/10', border: 'border-warning/20', text: 'text-warning' },
  { bg: 'bg-cyan-500/10', border: 'border-cyan-500/20', text: 'text-cyan-400' },
  { bg: 'bg-pink-500/10', border: 'border-pink-500/20', text: 'text-pink-400' },
  { bg: 'bg-success/10', border: 'border-success/20', text: 'text-success' },
  { bg: 'bg-warning/10', border: 'border-orange-500/20', text: 'text-warning' },
  { bg: 'bg-indigo-500/10', border: 'border-indigo-500/20', text: 'text-indigo-400' },
]

function hashTag(tag: string): number {
  let hash = 0
  for (let i = 0; i < tag.length; i++) {
    hash = ((hash << 5) - hash + tag.charCodeAt(i)) | 0
  }
  return Math.abs(hash) % TAG_COLORS.length
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

  const visible = parsed.slice(0, max)
  const remaining = parsed.length - visible.length

  return (
    <div className={`flex gap-1 flex-wrap ${className}`}>
      {visible.map((tag) => {
        const color = TAG_COLORS[hashTag(tag)]!
        return (
          <span
            key={tag}
            className={`text-[10px] px-1.5 py-0.5 rounded-md border leading-none ${color.bg} ${color.border} ${color.text}`}
          >
            {tag}
          </span>
        )
      })}
      {remaining > 0 && (
        <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-bg-elevated border border-border-subtle text-fg-muted leading-none">
          +{remaining}
        </span>
      )}
    </div>
  )
}
