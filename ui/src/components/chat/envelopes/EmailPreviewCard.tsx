import { Mail, User, Calendar, Tag, Info } from 'lucide-react'

interface EmailPreviewData {
  id: string
  from: string
  to: string
  subject: string
  date: string
  body: string
  labels?: string[]
  demo_mode?: boolean
}

interface EmailPreviewCardProps {
  data: EmailPreviewData
  onSendMessage?: (content: string) => void
}

const LABEL_STYLES: Record<string, string> = {
  INBOX: 'bg-blue-500/15 text-blue-400 border-blue-500/25',
  UNREAD: 'bg-amber-500/15 text-amber-400 border-amber-500/25',
  IMPORTANT: 'bg-red-500/15 text-red-400 border-red-500/25',
  STARRED: 'bg-yellow-500/15 text-yellow-400 border-yellow-500/25',
  SENT: 'bg-green-500/15 text-green-400 border-green-500/25',
  DRAFT: 'bg-zinc-500/15 text-zinc-400 border-zinc-500/25',
}

function getLabelStyle(label: string): string {
  return LABEL_STYLES[label] || 'bg-zinc-500/15 text-zinc-400 border-zinc-500/25'
}

export function EmailPreviewCard({ data }: EmailPreviewCardProps) {
  // Detect if body looks like HTML
  const isHTML = data.body?.includes('<') && data.body?.includes('>')

  const visibleLabels = (data.labels || []).filter(
    l => !['CATEGORY_UPDATES', 'CATEGORY_PROMOTIONS', 'CATEGORY_SOCIAL', 'CATEGORY_FORUMS'].includes(l)
  )

  return (
    <div className="animate-in fade-in zoom-in-95 duration-500 ease-out">
      <div className="rounded-lg border border-violet-500/20 bg-zinc-900/50 overflow-hidden max-w-2xl">
        {/* Header */}
        <div className="bg-violet-500/5 px-4 py-3 border-b border-violet-500/20">
          <div className="flex items-start justify-between gap-2">
            <div className="flex items-center gap-2 min-w-0">
              <Mail className="h-4 w-4 text-violet-400 shrink-0" />
              <span className="text-sm font-medium text-zinc-200 truncate">{data.subject}</span>
            </div>
            {visibleLabels.length > 0 && (
              <div className="flex items-center gap-1 shrink-0">
                {visibleLabels.slice(0, 3).map(label => (
                  <span
                    key={label}
                    className={`inline-block rounded-full border px-1.5 py-0.5 text-[10px] uppercase ${getLabelStyle(label)}`}
                  >
                    {label}
                  </span>
                ))}
              </div>
            )}
          </div>
        </div>

        {/* Metadata */}
        <div className="px-4 py-3 space-y-2 border-b border-zinc-800">
          <div className="grid grid-cols-2 gap-2">
            <div className="flex items-center gap-2">
              <User className="h-3.5 w-3.5 text-zinc-500 shrink-0" />
              <div>
                <span className="text-xs text-zinc-500 mr-1.5">From:</span>
                <span className="text-sm text-zinc-200">{data.from}</span>
              </div>
            </div>
            <div className="flex items-center gap-2">
              <Calendar className="h-3.5 w-3.5 text-zinc-500 shrink-0" />
              <span className="text-sm text-zinc-300">
                {new Date(data.date).toLocaleString()}
              </span>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <Tag className="h-3.5 w-3.5 text-zinc-500 shrink-0" />
            <div>
              <span className="text-xs text-zinc-500 mr-1.5">To:</span>
              <span className="text-sm text-zinc-300">{data.to}</span>
            </div>
          </div>
        </div>

        {/* Body */}
        <div className="px-4 py-3">
          {isHTML ? (
            <div
              className="prose prose-invert prose-sm max-w-none text-zinc-300"
              dangerouslySetInnerHTML={{ __html: data.body }}
            />
          ) : (
            <p className="text-sm text-zinc-300 whitespace-pre-wrap">{data.body}</p>
          )}
        </div>

        {/* Demo mode note */}
        {data.demo_mode && (
          <div className="px-4 pb-3">
            <div className="flex items-start gap-2 rounded-md border border-zinc-700 bg-zinc-800/50 px-3 py-2">
              <Info className="mt-0.5 h-3 w-3 shrink-0 text-zinc-500" />
              <span className="text-xs text-zinc-500">
                Demo mode — showing simulated email data
              </span>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
