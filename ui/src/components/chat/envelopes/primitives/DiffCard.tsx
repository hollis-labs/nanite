interface DiffCardData {
  title?: string
  before: { label: string; content: string }
  after: { label: string; content: string }
  format?: 'text' | 'code'
}

interface DiffCardProps {
  data: DiffCardData
}

export function DiffCard({ data }: DiffCardProps) {
  const isCode = data.format === 'code'
  const contentClass = isCode
    ? 'font-mono text-xs whitespace-pre-wrap bg-surface/50 p-3 rounded-sm'
    : 'text-sm whitespace-pre-wrap p-3'

  return (
    <div className="rounded-sm border border-border-subtle bg-bg-elevated/50 overflow-hidden">
      {data.title && (
        <div className="px-4 py-3 border-b border-border">
          <h4 className="text-sm font-medium text-fg">{data.title}</h4>
        </div>
      )}
      <div className="grid grid-cols-2 divide-x divide-border">
        {/* Before */}
        <div>
          <div className="px-3 py-2 border-b border-border/50 bg-danger/5">
            <span className="text-xs font-medium text-danger">{data.before.label}</span>
          </div>
          <div className={`${contentClass} text-fg-secondary`}>
            {data.before.content}
          </div>
        </div>
        {/* After */}
        <div>
          <div className="px-3 py-2 border-b border-border/50 bg-success/5">
            <span className="text-xs font-medium text-success">{data.after.label}</span>
          </div>
          <div className={`${contentClass} text-fg-secondary`}>
            {data.after.content}
          </div>
        </div>
      </div>
    </div>
  )
}
