import { useState } from 'react'
import { Search, BookOpen, ChevronDown, ChevronRight, CheckCircle, Ticket } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { MessageContent } from '../MessageContent'
import { TicketInitFlow } from './TicketInitFlow'

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

const SEVERITY_DOT: Record<string, string> = {
  low: 'bg-green-400',
  medium: 'bg-amber-400',
  high: 'bg-red-400',
  critical: 'bg-red-500',
}

function SourceBadge({ source }: { source: string | undefined }) {
  if (source === 'helix') {
    return (
      <span className="inline-block rounded bg-emerald-500/20 px-1.5 py-0.5 text-xs font-medium text-emerald-400 border border-emerald-500/25">
        Adtran KB
      </span>
    )
  }
  return (
    <span className="inline-block rounded bg-surface-hover/50 px-1.5 py-0.5 text-xs font-medium text-fg-muted border border-border-subtle">
      Reference
    </span>
  )
}

function ArticleCard({ article, defaultExpanded }: { article: KBArticle; defaultExpanded: boolean }) {
  const [expanded, setExpanded] = useState(defaultExpanded)
  const dotColor = SEVERITY_DOT[article.severity] || SEVERITY_DOT['medium']

  return (
    <div className={`rounded-lg border overflow-hidden ${
      article.source === 'helix'
        ? 'border-emerald-500/20 bg-bg-elevated/50'
        : 'border-border-subtle bg-bg-elevated/50'
    }`}>
      {/* Header */}
      <button
        type="button"
        className="flex w-full items-start gap-3 text-left p-4"
        onClick={() => setExpanded(!expanded)}
      >
        <div className="mt-0.5 shrink-0">
          {expanded ? (
            <ChevronDown className="h-4 w-4 text-fg-secondary" />
          ) : (
            <ChevronRight className="h-4 w-4 text-fg-secondary" />
          )}
        </div>

        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="inline-block rounded bg-blue-500/20 px-1.5 py-0.5 text-xs font-medium text-blue-400 border border-blue-500/25">
              {article.id}
            </span>
            <SourceBadge source={article.source} />
            <span className="text-sm font-medium text-fg">{article.title}</span>
          </div>
          <div className="mt-1 flex items-center gap-2">
            <span className="inline-block rounded-full bg-surface px-2 py-0.5 text-xs text-fg-secondary">
              {article.category}
            </span>
            <span className={`inline-block h-2 w-2 rounded-full ${dotColor}`} title={article.severity} />
            <span className="text-xs text-fg-muted">{article.severity}</span>
          </div>
        </div>
      </button>

      {/* Expanded body */}
      {expanded && article.body && (
        <div className="px-4 pb-4 ml-7 border-t border-border pt-3">
          <MessageContent content={article.body} role="assistant" />
        </div>
      )}
    </div>
  )
}

export function KBResultCard({ data, onSendMessage }: KBResultCardProps) {
  const articles = data.results || data.articles || []
  const [feedback, setFeedback] = useState<'none' | 'solved' | 'ticket'>('none')

  if (articles.length === 0) {
    return (
      <div className="space-y-3">
        <div className="rounded-lg border border-border-subtle bg-bg-elevated/50 p-4">
          <div className="flex items-center gap-2 text-fg-muted">
            <Search className="h-4 w-4" />
            <span className="text-sm">No matching articles found for this issue.</span>
          </div>
        </div>
        {/* No results → go straight to ticket flow */}
        <TicketInitFlow onSendMessage={onSendMessage} query={data.query} kbCategory={articles[0]?.category} />
      </div>
    )
  }

  return (
    <div className="space-y-3">
      {/* Header */}
      <div className="flex items-center gap-2 text-fg-secondary">
        <BookOpen className="h-4 w-4 shrink-0" />
        <span className="text-xs font-medium">
          Knowledge Base — {articles.length} article{articles.length !== 1 ? 's' : ''} found
        </span>
      </div>

      {/* Article list — first one expanded */}
      {articles.map((article, i) => (
        <ArticleCard key={article.id} article={article} defaultExpanded={i === 0} />
      ))}

      {/* Did this help? */}
      {feedback === 'none' && (
        <div className="rounded-lg border border-border-subtle bg-bg-elevated/50 p-4">
          <p className="text-sm text-fg-secondary mb-3">Did this resolve your issue?</p>
          <div className="flex items-center gap-2">
            <Button
              size="sm"
              className="bg-green-600 hover:bg-green-500 text-white text-xs px-3 py-1 h-7"
              onClick={() => {
                setFeedback('solved')
                if (onSendMessage) {
                  onSendMessage('The KB article resolved my issue. Thanks!')
                }
              }}
            >
              <CheckCircle className="mr-1.5 h-3 w-3" />
              Yes, solved
            </Button>
            <Button
              size="sm"
              className="bg-surface-hover hover:bg-surface-hover text-fg text-xs px-3 py-1 h-7"
              onClick={() => setFeedback('ticket')}
            >
              <Ticket className="mr-1.5 h-3 w-3" />
              No, open a ticket
            </Button>
          </div>
        </div>
      )}

      {feedback === 'solved' && (
        <div className="rounded-lg border border-green-500/30 bg-green-500/5 p-3">
          <div className="flex items-center gap-2">
            <CheckCircle className="h-4 w-4 text-success" />
            <span className="text-sm text-success">Glad that helped!</span>
          </div>
        </div>
      )}

      {feedback === 'ticket' && (
        <TicketInitFlow onSendMessage={onSendMessage} query={data.query} kbCategory={articles[0]?.category} />
      )}
    </div>
  )
}
