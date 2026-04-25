import { useState, useEffect } from 'react'
import { AlertTriangle, Copy, Check, Info } from 'lucide-react'
import { Envelope } from './primitives/Envelope'
import { StatusPill, type StatusTone } from './primitives/StatusPill'
import { ErrorDetailModal } from '@/components/chat/ErrorDetailModal'
import type { ChatError } from '@/lib/types'

interface ErrorReportData {
  code: string
  message: string
  details?: Record<string, unknown>
  giphy_query: string
  timestamp: string
}

interface ErrorCardProps {
  data: ErrorReportData
  onSendMessage?: (content: string) => void
}

const CODE_TONE: Record<string, StatusTone> = {
  rate_limit: 'warning',
  tool_error: 'warning',
  provider_error: 'danger',
  internal_error: 'danger',
}

const GIPHY_BETA_KEY = 'dc6zaTOxFJmzC'

export function ErrorCard({ data }: ErrorCardProps) {
  const [gifUrl, setGifUrl] = useState<string | null>(null)
  const [gifLoaded, setGifLoaded] = useState(false)
  const [copied, setCopied] = useState(false)

  const [showDetails, setShowDetails] = useState(false)
  const tone = CODE_TONE[data.code] ?? 'neutral'
  const codeLabel = data.code.toUpperCase().replace(/_/g, ' ')
  const accent = tone === 'danger' ? 'danger' : tone === 'warning' ? 'warning' : undefined

  useEffect(() => {
    if (!data.giphy_query) return
    const controller = new AbortController()
    void (async () => {
      try {
        const url = `https://api.giphy.com/v1/gifs/search?api_key=${GIPHY_BETA_KEY}&q=${encodeURIComponent(data.giphy_query)}&limit=5&rating=g`
        const resp = await fetch(url, { signal: controller.signal })
        if (!resp.ok) return
        const json = (await resp.json()) as {
          data: Array<{ images: { fixed_width: { url: string } } }>
        }
        if (json.data && json.data.length > 0) {
          const idx = Math.floor(Math.random() * Math.min(json.data.length, 5))
          setGifUrl(json.data[idx].images.fixed_width.url)
        }
      } catch {
        // noop
      }
    })()
    return () => controller.abort()
  }, [data.giphy_query])

  const handleCopy = () => {
    const payload = JSON.stringify(
      {
        code: data.code,
        message: data.message,
        details: data.details,
        timestamp: data.timestamp,
      },
      null,
      2,
    )
    void navigator.clipboard.writeText(payload).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    })
  }

  const formattedTime = (() => {
    try {
      return new Date(data.timestamp).toLocaleTimeString([], {
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
      })
    } catch {
      return data.timestamp
    }
  })()

  return (
    <div className="animate-in fade-in slide-in-from-bottom-2 duration-300">
      <Envelope accent={accent} className="max-w-lg">
        <div className="flex">
          <div className="flex w-[30%] shrink-0 items-center justify-center border-r border-border-subtle bg-surface p-2">
            {gifUrl ? (
              <div className="relative w-full">
                {!gifLoaded && (
                  <div className="flex aspect-square w-full animate-pulse items-center justify-center rounded-[6px] bg-surface">
                    <AlertTriangle className="h-6 w-6 text-fg-faint" />
                  </div>
                )}
                <img
                  src={gifUrl}
                  alt={data.giphy_query}
                  className={`w-full rounded-[6px] transition-opacity duration-300 ${
                    gifLoaded ? 'opacity-100' : 'absolute inset-0 opacity-0'
                  }`}
                  onLoad={() => setGifLoaded(true)}
                />
                <span className="mt-1 block text-center font-mono text-[9px] uppercase tracking-wider text-fg-faint">
                  Giphy
                </span>
              </div>
            ) : (
              <div className="flex aspect-square w-full items-center justify-center rounded-[6px] bg-surface">
                <AlertTriangle className="h-8 w-8 text-danger" />
              </div>
            )}
          </div>

          <div className="flex min-w-0 flex-1 flex-col gap-2 p-4">
            <div className="flex items-center gap-2">
              <StatusPill tone={tone}>{codeLabel}</StatusPill>
              <span className="ml-auto font-mono text-[11px] text-fg-muted">{formattedTime}</span>
            </div>

            <p className="text-[13px] leading-snug text-fg">{data.message}</p>

            {data.details && data.details.raw != null && (
              <p
                className="truncate font-mono text-[11px] text-fg-muted"
                title={String(data.details.raw)}
              >
                {String(data.details.raw).slice(0, 120)}
              </p>
            )}

            <div className="mt-1 flex items-center gap-1.5">
              <button
                type="button"
                onClick={handleCopy}
                className="flex items-center gap-1.5 self-start rounded-[4px] border border-border-subtle bg-surface px-2 py-1 text-[11px] text-fg-secondary transition-colors hover:border-border hover:text-fg"
              >
                {copied ? (
                  <>
                    <Check className="h-3 w-3 text-success" />
                    <span className="text-success">Copied</span>
                  </>
                ) : (
                  <>
                    <Copy className="h-3 w-3" />
                    <span>Copy error</span>
                  </>
                )}
              </button>
              <button
                type="button"
                onClick={() => setShowDetails(true)}
                className="flex items-center gap-1.5 self-start rounded-[4px] border border-border-subtle bg-surface px-2 py-1 text-[11px] text-fg-secondary transition-colors hover:border-border hover:text-fg"
              >
                <Info className="h-3 w-3" />
                <span>Details</span>
              </button>
            </div>
          </div>
        </div>
      </Envelope>
      {showDetails && (
        <ErrorDetailModal
          error={data as unknown as ChatError}
          onClose={() => setShowDetails(false)}
        />
      )}
    </div>
  )
}
