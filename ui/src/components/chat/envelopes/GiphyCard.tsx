import { useState, useCallback } from 'react'
import { Sparkles, Copy, Check } from 'lucide-react'

interface GiphyCardData {
  title: string
  gif_url: string
  source: string
  query: string
}

interface GiphyCardProps {
  data: GiphyCardData
  onSendMessage?: (content: string) => void
}

export function GiphyCard({ data }: GiphyCardProps) {
  const [loaded, setLoaded] = useState(false)
  const [copied, setCopied] = useState(false)

  const handleCopyURL = useCallback(() => {
    if (!data.gif_url) return
    navigator.clipboard.writeText(data.gif_url).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    })
  }, [data.gif_url])

  return (
    <div className="animate-in fade-in zoom-in-95 duration-500 ease-out">
      <div className="rounded-lg border border-violet-500/20 bg-zinc-900/50 overflow-hidden max-w-sm">
        {/* Title */}
        <div className="flex items-center gap-2 px-4 pt-3 pb-2">
          <Sparkles className="h-4 w-4 text-violet-400 shrink-0" />
          <span className="text-sm font-medium text-zinc-200">{data.title}</span>
        </div>

        {/* GIF container */}
        <div className="relative px-3 pb-2">
          {/* Skeleton loader */}
          {!loaded && (
            <div className="w-full aspect-video rounded-md bg-zinc-800 animate-pulse flex items-center justify-center">
              <Sparkles className="h-8 w-8 text-zinc-700" />
            </div>
          )}
          <img
            src={data.gif_url}
            alt={data.query}
            className={`w-full rounded-md transition-opacity duration-300 ${loaded ? 'opacity-100' : 'opacity-0 absolute inset-0 px-3'}`}
            onLoad={() => setLoaded(true)}
          />
        </div>

        {/* Footer: attribution + copy URL */}
        <div className="px-4 pb-3 flex items-center justify-between">
          <span className="text-[10px] text-zinc-600 uppercase tracking-wider">
            Powered by {data.source || 'GIPHY'}
          </span>
          <div className="flex items-center gap-2">
            <span className="text-[10px] text-zinc-600">
              &ldquo;{data.query}&rdquo;
            </span>
            <button
              type="button"
              onClick={handleCopyURL}
              className="flex items-center gap-1 text-[10px] text-zinc-500 hover:text-violet-400 transition-colors"
              title="Copy GIF URL"
            >
              {copied ? (
                <Check className="h-3 w-3 text-green-400" />
              ) : (
                <Copy className="h-3 w-3" />
              )}
              {copied ? 'Copied' : 'Copy URL'}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
