import { Inbox, Mail, MailOpen, Search, Info } from 'lucide-react'

interface EmailMessage {
  id: string
  from: string
  subject: string
  snippet: string
  date: string
  labels?: string[]
  unread?: boolean
}

interface EmailInboxData {
  query: string
  messages: EmailMessage[]
  total: number
  demo_mode?: boolean
}

interface EmailInboxCardProps {
  data: EmailInboxData
  onSendMessage?: (content: string) => void
}

export function EmailInboxCard({ data, onSendMessage }: EmailInboxCardProps) {
  const handleClickMessage = (messageId: string) => {
    if (onSendMessage) {
      onSendMessage(`Read email ${messageId}`)
    }
  }

  return (
    <div className="animate-in fade-in zoom-in-95 duration-500 ease-out">
      <div className="rounded-lg border border-blue-500/20 bg-zinc-900/50 overflow-hidden max-w-2xl">
        {/* Header */}
        <div className="flex items-center justify-between bg-blue-500/5 px-4 py-3 border-b border-blue-500/20">
          <div className="flex items-center gap-2">
            <Inbox className="h-4 w-4 text-blue-400" />
            <span className="text-sm font-medium text-zinc-200">
              Inbox ({data.total} message{data.total !== 1 ? 's' : ''})
            </span>
          </div>
          <div className="flex items-center gap-1.5 text-xs text-zinc-500">
            <Search className="h-3 w-3" />
            <span className="font-mono">{data.query}</span>
          </div>
        </div>

        {/* Message list */}
        <div className="divide-y divide-zinc-800">
          {data.messages.length === 0 ? (
            <div className="px-4 py-6 text-center text-sm text-zinc-500">
              No messages found
            </div>
          ) : (
            data.messages.map((msg) => (
              <button
                key={msg.id}
                type="button"
                onClick={() => handleClickMessage(msg.id)}
                className="w-full text-left px-4 py-3 hover:bg-zinc-800/50 transition-colors cursor-pointer group"
              >
                <div className="flex items-start gap-3">
                  {/* Unread indicator */}
                  <div className="mt-1 shrink-0">
                    {msg.unread ? (
                      <Mail className="h-4 w-4 text-blue-400" />
                    ) : (
                      <MailOpen className="h-4 w-4 text-zinc-600" />
                    )}
                  </div>

                  {/* Content */}
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center justify-between gap-2">
                      <span className={`text-sm truncate ${msg.unread ? 'font-semibold text-zinc-100' : 'text-zinc-300'}`}>
                        {msg.from?.split('<')[0]?.trim() || msg.from}
                      </span>
                      <span className="text-xs text-zinc-500 shrink-0">
                        {formatDate(msg.date)}
                      </span>
                    </div>
                    <div className={`text-sm truncate ${msg.unread ? 'font-medium text-zinc-200' : 'text-zinc-400'}`}>
                      {msg.subject}
                    </div>
                    <div className="text-xs text-zinc-500 truncate mt-0.5">
                      {msg.snippet}
                    </div>
                  </div>

                  {/* Unread dot */}
                  {msg.unread && (
                    <div className="mt-2 shrink-0">
                      <div className="h-2 w-2 rounded-full bg-blue-400" />
                    </div>
                  )}
                </div>
              </button>
            ))
          )}
        </div>

        {/* Demo mode note */}
        {data.demo_mode && (
          <div className="px-4 py-3 border-t border-zinc-800">
            <div className="flex items-start gap-2 rounded-md border border-zinc-700 bg-zinc-800/50 px-3 py-2">
              <Info className="mt-0.5 h-3 w-3 shrink-0 text-zinc-500" />
              <span className="text-xs text-zinc-500">
                Demo mode — showing simulated email data. Configure Gmail credentials for real inbox access.
              </span>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

function formatDate(dateStr: string): string {
  try {
    const date = new Date(dateStr)
    const now = new Date()
    const diffMs = now.getTime() - date.getTime()
    const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24))

    if (diffDays === 0) {
      return date.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
    }
    if (diffDays === 1) {
      return 'Yesterday'
    }
    if (diffDays < 7) {
      return date.toLocaleDateString(undefined, { weekday: 'short' })
    }
    return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
  } catch {
    return dateStr
  }
}
