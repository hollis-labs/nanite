import { useState } from 'react'
import { ExternalLink, Play, Image, Link2 } from 'lucide-react'

interface OEmbedData {
  title: string
  description: string
  thumbnail_url: string
  provider_name: string
  provider_url: string
  type: string // "video", "photo", "rich", "link"
  url: string
  author_name: string
  html: string
}

interface OEmbedCardProps {
  data: OEmbedData
  onSendMessage?: (content: string) => void
}

function TypeIcon({ type }: { type: string }) {
  if (type === 'video') return <Play className="h-3 w-3" />
  if (type === 'photo') return <Image className="h-3 w-3" />
  return <Link2 className="h-3 w-3" />
}

export function OEmbedCard({ data }: OEmbedCardProps) {
  const [imgLoaded, setImgLoaded] = useState(false)
  const [imgError, setImgError] = useState(false)

  const providerColor = getProviderColor(data.provider_name)

  return (
    <div className="animate-in fade-in zoom-in-95 duration-500 ease-out">
      <div className={`rounded-lg border ${providerColor.border} bg-bg-elevated/50 overflow-hidden max-w-md`}>
        {/* Thumbnail */}
        {data.thumbnail_url && !imgError && (
          <div className="relative">
            {!imgLoaded && (
              <div className="w-full aspect-video bg-surface animate-pulse flex items-center justify-center">
                <TypeIcon type={data.type} />
              </div>
            )}
            <a href={data.url} target="_blank" rel="noopener noreferrer" className="block">
              <img
                src={data.thumbnail_url}
                alt={data.title || 'Preview'}
                className={`w-full aspect-video object-cover transition-opacity duration-300 ${imgLoaded ? 'opacity-100' : 'opacity-0 absolute inset-0'}`}
                onLoad={() => setImgLoaded(true)}
                onError={() => setImgError(true)}
              />
              {/* Play overlay for video type */}
              {data.type === 'video' && imgLoaded && (
                <div className="absolute inset-0 flex items-center justify-center bg-black/30 hover:bg-black/20 transition-colors">
                  <div className="w-12 h-12 rounded-full bg-white/90 flex items-center justify-center">
                    <Play className="h-5 w-5 text-bg ml-0.5" />
                  </div>
                </div>
              )}
            </a>
          </div>
        )}

        {/* Content */}
        <div className="px-4 py-3 space-y-1.5">
          {/* Provider badge + type */}
          <div className="flex items-center gap-2">
            <span className={`inline-block rounded px-1.5 py-0.5 text-[10px] font-medium border ${providerColor.badge}`}>
              {data.provider_name || 'Link'}
            </span>
            <span className="flex items-center gap-1 text-[10px] text-fg-faint">
              <TypeIcon type={data.type} />
              {data.type}
            </span>
          </div>

          {/* Title */}
          {data.title && (
            <a
              href={data.url}
              target="_blank"
              rel="noopener noreferrer"
              className="block text-sm font-medium text-fg hover:text-white transition-colors line-clamp-2"
            >
              {data.title}
            </a>
          )}

          {/* Author */}
          {data.author_name && (
            <p className="text-xs text-fg-muted">{data.author_name}</p>
          )}

          {/* Description (for rich/link types without thumbnail) */}
          {data.description && (!data.thumbnail_url || imgError) && (
            <p className="text-xs text-fg-secondary line-clamp-3">{data.description}</p>
          )}
        </div>

        {/* Footer */}
        <div className="px-4 pb-3 flex items-center justify-between">
          <span className="text-[10px] text-fg-faint truncate max-w-[200px]">
            {data.url}
          </span>
          <a
            href={data.url}
            target="_blank"
            rel="noopener noreferrer"
            className="flex items-center gap-1 text-[10px] text-fg-muted hover:text-fg-secondary transition-colors shrink-0"
          >
            <ExternalLink className="h-3 w-3" />
            Open
          </a>
        </div>
      </div>
    </div>
  )
}

function getProviderColor(name: string): { border: string; badge: string } {
  const lower = (name || '').toLowerCase()

  if (lower.includes('youtube')) {
    return {
      border: 'border-red-500/20',
      badge: 'bg-red-500/20 text-red-400 border-red-500/25',
    }
  }
  if (lower.includes('spotify')) {
    return {
      border: 'border-green-500/20',
      badge: 'bg-green-500/20 text-green-400 border-green-500/25',
    }
  }
  if (lower.includes('vimeo')) {
    return {
      border: 'border-cyan-500/20',
      badge: 'bg-cyan-500/20 text-cyan-400 border-cyan-500/25',
    }
  }
  if (lower.includes('soundcloud')) {
    return {
      border: 'border-orange-500/20',
      badge: 'bg-orange-500/20 text-orange-400 border-orange-500/25',
    }
  }
  return {
    border: 'border-border-subtle',
    badge: 'bg-surface-hover/50 text-fg-secondary border-border-subtle',
  }
}
