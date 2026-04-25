import { GitCompare } from 'lucide-react'
import { Envelope, EnvelopeHeader } from './Envelope'

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
    ? 'whitespace-pre-wrap p-3 font-mono text-[12px] leading-relaxed text-fg-secondary'
    : 'whitespace-pre-wrap p-3 text-[13px] leading-relaxed text-fg-secondary'

  return (
    <Envelope>
      <EnvelopeHeader icon={GitCompare} label={data.title || 'Diff'} />
      <div className="grid grid-cols-2 divide-x divide-border-subtle">
        {/* Before */}
        <div>
          <div className="flex items-center gap-2 border-b border-border-subtle bg-danger/5 px-3 py-1.5">
            <span className="h-1.5 w-1.5 rounded-full bg-danger" />
            <span className="font-mono text-[10px] font-semibold uppercase tracking-wide text-danger">
              {data.before.label}
            </span>
          </div>
          <div className={contentClass}>{data.before.content}</div>
        </div>
        {/* After */}
        <div>
          <div className="flex items-center gap-2 border-b border-border-subtle bg-success/5 px-3 py-1.5">
            <span className="h-1.5 w-1.5 rounded-full bg-success" />
            <span className="font-mono text-[10px] font-semibold uppercase tracking-wide text-success">
              {data.after.label}
            </span>
          </div>
          <div className={contentClass}>{data.after.content}</div>
        </div>
      </div>
    </Envelope>
  )
}
