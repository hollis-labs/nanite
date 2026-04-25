import { useState } from 'react'
import { BookOpen, CheckCircle, ChevronDown, ChevronRight, Search, Ticket } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { MessageContent } from '../MessageContent'
import { TicketInitFlow } from './TicketInitFlow'
import { Envelope } from './primitives/Envelope'
import { StatusPill, type StatusTone } from './primitives/StatusPill'

interface KBArticle {
  id: string
  title: string
  category: string
  severity: string
  tags?: string[]
  related?: string[]
  body?: string
  rank?: number
  confidence?: string
  source?: string
}

interface KBResultData {
  results?: KBArticle[]
  articles?: KBArticle[]
  query: string
}

interface KBResultCardProps {
  data: KBResultData
  onSendMessage?: (content: string) => void
}

const SEVERITY_TONE: Record<string, StatusTone> = {
  low: 'success',
  medium: 'warning',
  high: 'danger',
  critical: 'danger',
}

function SourceLabel({ source }: { source: string | undefined }) {
  if (source === 'helix') return <StatusPill tone="success">Adtran KB</StatusPill>
  return <StatusPill tone="neutral">Reference</StatusPill>
}

function ArticleCard({
  article,
  defaultExpanded,
}: {
  article: KBArticle
  defaultExpanded: boolean
}) {
  const [expanded, setExpanded] = useState(defaultExpanded)
  const severityTone = SEVERITY_TONE[article.severity] || 'warning'

  return (
    <Envelope accent={article.source === 'helix' ? 'success' : undefined}>
      <button
        type="button"
        aria-expanded={expanded}
        onClick={() => setExpanded(!expanded)}
        className="flex w-full items-start gap-3 px-4 py-3 text-left transition-colors hover:bg-surface"
      >
        <span className="mt-0.5 shrink-0 text-fg-muted">
          {expanded ? (
            <ChevronDown className="h-4 w-4" />
          ) : (
            <ChevronRight className="h-4 w-4" />
          )}
        </span>

        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5">
            <StatusPill tone="info">{article.id}</StatusPill>
            <SourceLabel source={article.source} />
            <StatusPill tone="neutral">{article.category}</StatusPill>
            <StatusPill tone={severityTone}>{article.severity}</StatusPill>
          </div>
          <div className="mt-1.5 text-[14px] font-semibold leading-snug text-fg">
            {article.title}
          </div>
          {article.confidence && !expanded && (
            <div className="mt-1 font-mono text-[11px] text-fg-muted">
              Confidence {article.confidence}
              {article.rank != null && ` · Rank #${article.rank}`}
            </div>
          )}
        </div>
      </button>

      {expanded && article.body && (
        <div className="border-t border-border-subtle px-4 py-3 pl-11 text-[13px] leading-relaxed text-fg-secondary">
          <MessageContent content={article.body} role="assistant" />
        </div>
      )}
    </Envelope>
  )
}

export function KBResultCard({ data, onSendMessage }: KBResultCardProps) {
  const articles = data.results || data.articles || []
  const [feedback, setFeedback] = useState<'none' | 'solved' | 'ticket'>('none')

  if (articles.length === 0) {
    return (
      <div className="space-y-3">
        <Envelope>
          <div className="flex items-center gap-2 px-4 py-3 text-fg-muted">
            <Search className="h-4 w-4" />
            <span className="text-[13px]">No matching articles found for this issue.</span>
          </div>
        </Envelope>
        <TicketInitFlow
          onSendMessage={onSendMessage}
          query={data.query}
          kbCategory={articles[0]?.category}
        />
      </div>
    )
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <BookOpen className="h-3.5 w-3.5 shrink-0 text-fg-muted" />
        <span className="font-mono text-[11px] font-semibold uppercase tracking-wide text-fg-muted">
          Knowledge base · {articles.length} result{articles.length !== 1 ? 's' : ''}
        </span>
      </div>

      {articles.map((article, i) => (
        <ArticleCard key={article.id} article={article} defaultExpanded={i === 0} />
      ))}

      {feedback === 'none' && (
        <Envelope>
          <div className="px-4 py-3">
            <p className="mb-3 text-[13px] text-fg-secondary">
              Did this resolve your issue?
            </p>
            <div className="flex items-center gap-2">
              <Button
                size="sm"
                onClick={() => {
                  setFeedback('solved')
                  onSendMessage?.('The KB article resolved my issue. Thanks!')
                }}
              >
                <CheckCircle className="h-3 w-3" />
                Yes, solved
              </Button>
              <Button size="sm" variant="outline" onClick={() => setFeedback('ticket')}>
                <Ticket className="h-3 w-3" />
                No, open a ticket
              </Button>
            </div>
          </div>
        </Envelope>
      )}

      {feedback === 'solved' && (
        <Envelope accent="success" muted>
          <div className="flex items-center gap-2 px-4 py-2.5">
            <CheckCircle className="h-4 w-4 text-success" />
            <span className="text-[13px] text-fg">Glad that helped!</span>
          </div>
        </Envelope>
      )}

      {feedback === 'ticket' && (
        <TicketInitFlow
          onSendMessage={onSendMessage}
          query={data.query}
          kbCategory={articles[0]?.category}
        />
      )}
    </div>
  )
}
