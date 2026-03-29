import { CheckCircle, Mail, Clock, Info } from 'lucide-react'

interface EmailComposeData {
  to: string
  cc?: string
  subject: string
  body: string
  is_html?: boolean
  sent_at: string
  demo_mode?: boolean
}

interface EmailComposeCardProps {
  data: EmailComposeData
  onSendMessage?: (content: string) => void
}

export function EmailComposeCard({ data }: EmailComposeCardProps) {
  return (
    <div className="animate-in fade-in zoom-in-95 duration-500 ease-out">
      <div className="rounded-lg border border-green-500/30 bg-bg-elevated/50 overflow-hidden">
        {/* Header */}
        <div className="flex items-center justify-between bg-green-500/5 px-4 py-3 border-b border-green-500/20">
          <div className="flex items-center gap-2">
            <CheckCircle className="h-4 w-4 text-success" />
            <span className="text-sm font-medium text-success">Email Sent</span>
          </div>
          <div className="flex items-center gap-1.5 text-xs text-fg-muted">
            <Clock className="h-3 w-3" />
            <span>{new Date(data.sent_at).toLocaleString()}</span>
          </div>
        </div>

        {/* Details */}
        <div className="px-4 py-3 space-y-2">
          <div className="flex items-start gap-2">
            <Mail className="h-4 w-4 text-fg-muted mt-0.5 shrink-0" />
            <div className="space-y-1 min-w-0">
              <div className="flex items-center gap-2">
                <span className="text-xs font-medium text-fg-secondary w-12 shrink-0">To:</span>
                <span className="text-sm text-fg truncate">{data.to}</span>
              </div>
              {data.cc && (
                <div className="flex items-center gap-2">
                  <span className="text-xs font-medium text-fg-secondary w-12 shrink-0">CC:</span>
                  <span className="text-sm text-fg-secondary truncate">{data.cc}</span>
                </div>
              )}
              <div className="flex items-center gap-2">
                <span className="text-xs font-medium text-fg-secondary w-12 shrink-0">Subject:</span>
                <span className="text-sm text-fg font-medium truncate">{data.subject}</span>
              </div>
            </div>
          </div>

          {/* Body preview */}
          <div className="mt-2 rounded-md border border-border-subtle bg-surface/50 px-3 py-2">
            <p className="text-sm text-fg-secondary whitespace-pre-wrap line-clamp-4">{data.body}</p>
          </div>
        </div>

        {/* Demo mode note */}
        {data.demo_mode && (
          <div className="px-4 pb-3">
            <div className="flex items-start gap-2 rounded-md border border-border-subtle bg-surface/50 px-3 py-2">
              <Info className="mt-0.5 h-3 w-3 shrink-0 text-fg-muted" />
              <span className="text-xs text-fg-muted">
                Demo mode — email was not actually sent. Configure Gmail credentials to enable real sending.
              </span>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
