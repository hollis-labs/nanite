import { useState } from 'react'
import { Sparkles } from 'lucide-react'

interface GiphyModalData {
  title: string
  gif_url: string
  source: string
  query: string
}

interface GiphyModalCardProps {
  data: GiphyModalData
  onSendMessage?: (content: string) => void
}

export function GiphyModalCard({ data }: GiphyModalCardProps) {
  const [loaded, setLoaded] = useState(false)

  return (
    <div className="animate-in fade-in zoom-in-95 duration-500 ease-out">
      <div className="rounded-lg border border-violet-500/20 bg-bg-elevated/50 overflow-hidden max-w-sm">
        {/* Title */}
        <div className="flex items-center gap-2 px-4 pt-3 pb-2">
          <Sparkles className="h-4 w-4 text-violet-400 shrink-0" />
          <span className="text-sm font-medium text-fg">{data.title}</span>
        </div>

        {/* GIF container */}
        <div className="relative px-3 pb-2">
          {/* Skeleton loader */}
          {!loaded && (
            <div className="w-full aspect-video rounded-md bg-surface animate-pulse flex items-center justify-center">
              <Sparkles className="h-8 w-8 text-fg-faint" />
            </div>
          )}
          <img
            src={data.gif_url}
            alt={data.query}
            className={`w-full rounded-md transition-opacity duration-300 ${loaded ? 'opacity-100' : 'opacity-0 absolute inset-0 px-3'}`}
            onLoad={() => setLoaded(true)}
          />
        </div>

        {/* Attribution — required by Giphy TOS */}
        <div className="px-4 pb-3 flex items-center justify-between">
          <span className="text-[10px] text-fg-faint uppercase tracking-wider">
            Powered by {data.source || 'GIPHY'}
          </span>
          <span className="text-[10px] text-fg-faint">
            &ldquo;{data.query}&rdquo;
          </span>
        </div>
      </div>
    </div>
  )
}
