import { useState } from 'react'
import { Shield, BookOpen, Tv, Film } from 'lucide-react'

interface MarvelCharacterData {
  name: string
  description: string
  image_url: string
  comic_count: number
  series_count: number
  movies: string[] | null
  source: string
}

interface MarvelCharacterCardProps {
  data: MarvelCharacterData
  onSendMessage?: (content: string) => void
}

export function MarvelCharacterCard({ data, onSendMessage }: MarvelCharacterCardProps) {
  const [imgLoaded, setImgLoaded] = useState(false)
  const [imgError, setImgError] = useState(false)

  return (
    <div className="animate-in fade-in zoom-in-95 duration-500 ease-out">
      <div className="rounded-sm border border-accent/20 bg-bg-elevated/50 overflow-hidden max-w-md">
        {/* Header */}
        <div className="flex items-center gap-2 px-4 pt-3 pb-2 border-b border-border">
          <Shield className="h-4 w-4 text-red-400 shrink-0" />
          <span className="text-sm font-semibold text-fg">{data.name}</span>
          <span className="ml-auto text-[10px] text-fg-faint uppercase tracking-wider">
            {data.source || 'Marvel'}
          </span>
        </div>

        <div className="flex gap-3 p-4">
          {/* Character image */}
          {data.image_url && !imgError && (
            <div className="shrink-0 w-24 h-36 rounded-md overflow-hidden bg-surface">
              {!imgLoaded && (
                <div className="w-full h-full animate-pulse bg-surface flex items-center justify-center">
                  <Shield className="h-6 w-6 text-fg-faint" />
                </div>
              )}
              <img
                src={data.image_url}
                alt={data.name}
                className={`w-full h-full object-cover transition-opacity duration-300 ${imgLoaded ? 'opacity-100' : 'opacity-0'}`}
                onLoad={() => setImgLoaded(true)}
                onError={() => setImgError(true)}
              />
            </div>
          )}

          {/* Info */}
          <div className="min-w-0 flex-1 space-y-2">
            {/* Description */}
            <p className="text-xs text-fg-secondary line-clamp-4">
              {data.description || 'No description available.'}
            </p>

            {/* Stats */}
            <div className="flex flex-wrap gap-3 text-xs">
              <div className="flex items-center gap-1 text-amber-400">
                <BookOpen className="h-3 w-3" />
                <span>{data.comic_count.toLocaleString()} comics</span>
              </div>
              <div className="flex items-center gap-1 text-blue-400">
                <Tv className="h-3 w-3" />
                <span>{data.series_count.toLocaleString()} series</span>
              </div>
            </div>
          </div>
        </div>

        {/* Movie appearances */}
        {data.movies && data.movies.length > 0 && (
          <div className="px-4 pb-3 border-t border-border pt-3">
            <div className="flex items-center gap-1.5 mb-2">
              <Film className="h-3 w-3 text-fg-muted" />
              <span className="text-[10px] text-fg-muted uppercase tracking-wider font-medium">Movie Appearances</span>
            </div>
            <div className="flex flex-wrap gap-1.5">
              {data.movies.map((movie) => (
                <span
                  key={movie}
                  className="inline-block rounded bg-red-500/10 px-2 py-0.5 text-[11px] text-red-300 border border-red-500/20"
                >
                  {movie}
                </span>
              ))}
            </div>
          </div>
        )}

        {/* Action: search movie */}
        {onSendMessage && (
          <div className="px-4 pb-3">
            <button
              type="button"
              onClick={() => onSendMessage(`!movie ${data.name}`)}
              className="text-[11px] text-red-400 hover:text-red-300 transition-colors"
            >
              Search movies for {data.name} &rarr;
            </button>
          </div>
        )}
      </div>
    </div>
  )
}
