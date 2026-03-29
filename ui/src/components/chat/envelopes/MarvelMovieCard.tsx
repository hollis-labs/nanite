import { useState } from 'react'
import { Star, Calendar, Users, Film } from 'lucide-react'

interface MarvelMovieData {
  title: string
  overview: string
  poster_url: string
  release_date: string
  vote_average: number
  vote_count: number
  source: string
}

interface MarvelMovieCardProps {
  data: MarvelMovieData
  onSendMessage?: (content: string) => void
}

export function MarvelMovieCard({ data }: MarvelMovieCardProps) {
  const [imgLoaded, setImgLoaded] = useState(false)
  const [imgError, setImgError] = useState(false)

  const rating = data.vote_average ? data.vote_average.toFixed(1) : 'N/A'
  const ratingColor = data.vote_average >= 7 ? 'text-green-400' : data.vote_average >= 5 ? 'text-amber-400' : 'text-red-400'

  return (
    <div className="animate-in fade-in zoom-in-95 duration-500 ease-out">
      <div className="rounded-lg border border-blue-500/20 bg-bg-elevated/50 overflow-hidden max-w-md">
        {/* Header */}
        <div className="flex items-center gap-2 px-4 pt-3 pb-2 border-b border-border">
          <Film className="h-4 w-4 text-blue-400 shrink-0" />
          <span className="text-sm font-semibold text-fg">{data.title}</span>
          <span className="ml-auto text-[10px] text-fg-faint uppercase tracking-wider">
            {data.source || 'TMDB'}
          </span>
        </div>

        <div className="flex gap-3 p-4">
          {/* Poster */}
          {data.poster_url && !imgError && (
            <div className="shrink-0 w-24 h-36 rounded-md overflow-hidden bg-surface">
              {!imgLoaded && (
                <div className="w-full h-full animate-pulse bg-surface flex items-center justify-center">
                  <Film className="h-6 w-6 text-fg-faint" />
                </div>
              )}
              <img
                src={data.poster_url}
                alt={data.title}
                className={`w-full h-full object-cover transition-opacity duration-300 ${imgLoaded ? 'opacity-100' : 'opacity-0'}`}
                onLoad={() => setImgLoaded(true)}
                onError={() => setImgError(true)}
              />
            </div>
          )}

          {/* Info */}
          <div className="min-w-0 flex-1 space-y-2">
            <p className="text-xs text-fg-secondary line-clamp-4">
              {data.overview || 'No overview available.'}
            </p>

            {/* Stats row */}
            <div className="flex flex-wrap gap-3 text-xs">
              <div className={`flex items-center gap-1 ${ratingColor}`}>
                <Star className="h-3 w-3" />
                <span>{rating}</span>
              </div>
              {data.release_date && data.release_date !== 'N/A' && (
                <div className="flex items-center gap-1 text-fg-muted">
                  <Calendar className="h-3 w-3" />
                  <span>{data.release_date}</span>
                </div>
              )}
              {data.vote_count > 0 && (
                <div className="flex items-center gap-1 text-fg-muted">
                  <Users className="h-3 w-3" />
                  <span>{data.vote_count.toLocaleString()} votes</span>
                </div>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
