import { CheckCircle, ExternalLink, MessageSquare } from 'lucide-react'

interface TeamsMessageData {
  title: string
  message: string
  facts?: Record<string, string>
  link_url?: string
  sent_at?: string
}

interface TeamsMessageCardProps {
  data: TeamsMessageData
}

export function TeamsMessageCard({ data }: TeamsMessageCardProps) {
  const { title, message, facts, link_url } = data

  return (
    <div className="rounded-lg border border-indigo-500/30 bg-zinc-900/50 overflow-hidden">
      {/* Header */}
      <div className="flex items-center justify-between bg-indigo-500/5 px-4 py-3 border-b border-indigo-500/20">
        <div className="flex items-center gap-2">
          <MessageSquare className="h-4 w-4 text-indigo-400" />
          <span className="text-sm font-medium text-zinc-200">Teams Message</span>
        </div>
        <span className="inline-flex items-center gap-1 rounded-full bg-green-500/15 border border-green-500/25 px-2 py-0.5 text-xs text-green-400">
          <CheckCircle className="h-3 w-3" />
          Sent to Teams
        </span>
      </div>

      {/* Card content */}
      <div className="px-4 py-3 space-y-3">
        {/* Title */}
        <h3 className="text-sm font-semibold text-zinc-100">{title}</h3>

        {/* Message */}
        <p className="text-sm text-zinc-300 whitespace-pre-wrap">{message}</p>

        {/* Facts */}
        {facts && Object.keys(facts).length > 0 && (
          <div className="rounded-md border border-zinc-700 bg-zinc-800/50 px-3 py-2">
            <div className="grid grid-cols-2 gap-x-4 gap-y-1.5">
              {Object.entries(facts).map(([key, value]) => (
                <div key={key} className="flex items-baseline gap-2">
                  <span className="text-xs font-medium text-zinc-400 shrink-0">{key}:</span>
                  <span className="text-xs text-zinc-200">{value}</span>
                </div>
              ))}
            </div>
          </div>
        )}

        {/* Link */}
        {link_url && (
          <a
            href={link_url}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1.5 text-xs text-indigo-400 hover:text-indigo-300 transition-colors"
          >
            <ExternalLink className="h-3 w-3" />
            View Details
          </a>
        )}
      </div>
    </div>
  )
}
