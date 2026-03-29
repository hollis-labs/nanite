import { useState, useEffect } from 'react'
import { AlertTriangle, Copy, Check } from 'lucide-react'

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

const CODE_LABELS: Record<string, { label: string; color: string }> = {
  rate_limit: { label: 'RATE_LIMIT', color: 'bg-amber-500/20 text-amber-400 border-amber-500/30' },
  tool_error: { label: 'TOOL_ERROR', color: 'bg-orange-500/20 text-orange-400 border-orange-500/30' },
  provider_error: { label: 'PROVIDER_ERROR', color: 'bg-red-500/20 text-red-400 border-red-500/30' },
  internal_error: { label: 'INTERNAL_ERROR', color: 'bg-red-500/20 text-red-300 border-red-500/30' },
}

// Giphy API search URL builder. Uses the GIPHY_API_KEY env var via the backend,
// but for the frontend card we use the public beta key for search previews.
const GIPHY_BETA_KEY = 'dc6zaTOxFJmzC'

export function ErrorCard({ data }: ErrorCardProps) {
  const [gifUrl, setGifUrl] = useState<string | null>(null)
  const [gifLoaded, setGifLoaded] = useState(false)
  const [copied, setCopied] = useState(false)

  const codeInfo = CODE_LABELS[data.code] ?? {
    label: data.code.toUpperCase(),
    color: 'bg-bg-elevated text-fg-secondary border-border-subtle',
  }

  // Fetch a giphy image based on the query
  useEffect(() => {
    if (!data.giphy_query) return
    const controller = new AbortController()

    void (async () => {
      try {
        const url = `https://api.giphy.com/v1/gifs/search?api_key=${GIPHY_BETA_KEY}&q=${encodeURIComponent(data.giphy_query)}&limit=5&rating=g`
        const resp = await fetch(url, { signal: controller.signal })
        if (!resp.ok) return
        const json = await resp.json() as { data: Array<{ images: { fixed_width: { url: string } } }> }
        if (json.data && json.data.length > 0) {
          // Pick a random one from the top 5 results
          const idx = Math.floor(Math.random() * Math.min(json.data.length, 5))
          setGifUrl(json.data[idx].images.fixed_width.url)
        }
      } catch {
        // Giphy fetch failed — card still works without the gif
      }
    })()

    return () => controller.abort()
  }, [data.giphy_query])

  const handleCopy = () => {
    const errorPayload = JSON.stringify({
      code: data.code,
      message: data.message,
      details: data.details,
      timestamp: data.timestamp,
    }, null, 2)
    void navigator.clipboard.writeText(errorPayload).then(() => {
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
      <div className="rounded-sm border border-red-500/30 bg-bg-elevated/80 overflow-hidden max-w-lg">
        <div className="flex">
          {/* Left side: Giphy image (30%) */}
          <div className="w-[30%] shrink-0 bg-bg/50 flex items-center justify-center p-2">
            {gifUrl ? (
              <div className="relative w-full">
                {!gifLoaded && (
                  <div className="w-full aspect-square rounded-md bg-surface animate-pulse flex items-center justify-center">
                    <AlertTriangle className="h-6 w-6 text-fg-faint" />
                  </div>
                )}
                <img
                  src={gifUrl}
                  alt={data.giphy_query}
                  className={`w-full rounded-md transition-opacity duration-300 ${gifLoaded ? 'opacity-100' : 'opacity-0 absolute inset-0'}`}
                  onLoad={() => setGifLoaded(true)}
                />
                <span className="block text-[8px] text-fg-faint text-center mt-1 uppercase tracking-wider">
                  GIPHY
                </span>
              </div>
            ) : (
              <div className="w-full aspect-square rounded-md bg-surface/50 flex items-center justify-center">
                <AlertTriangle className="h-8 w-8 text-red-500/40" />
              </div>
            )}
          </div>

          {/* Right side: Error info (70%) */}
          <div className="flex-1 p-3 flex flex-col gap-2 min-w-0">
            {/* Error code badge */}
            <div className="flex items-center gap-2">
              <span className={`text-[10px] font-mono font-semibold px-1.5 py-0.5 rounded border ${codeInfo.color}`}>
                {codeInfo.label}
              </span>
              <span className="text-[10px] text-fg-faint ml-auto">
                {formattedTime}
              </span>
            </div>

            {/* Error message */}
            <p className="text-sm text-fg-secondary leading-snug">
              {data.message}
            </p>

            {/* Details preview (if present) */}
            {data.details && data.details.raw != null && (
              <p className="text-xs text-fg-muted font-mono truncate" title={String(data.details.raw)}>
                {String(data.details.raw).slice(0, 120)}
              </p>
            )}

            {/* Copy button */}
            <button
              type="button"
              onClick={handleCopy}
              className="flex items-center gap-1.5 text-xs text-fg-secondary hover:text-fg transition-colors self-start mt-1 px-2 py-1 rounded bg-surface/50 hover:bg-surface border border-border-subtle/50"
            >
              {copied ? (
                <>
                  <Check className="h-3 w-3 text-success" />
                  <span className="text-success">Copied</span>
                </>
              ) : (
                <>
                  <Copy className="h-3 w-3" />
                  <span>Copy Error</span>
                </>
              )}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
