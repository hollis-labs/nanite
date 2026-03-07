import { Bookmark } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { Widget } from './Widget'
import { useAppStore } from '@/stores/useAppStore'
import { api } from '@/lib/api'

function formatTime(dateStr: string): string {
  return new Date(dateStr).toLocaleTimeString(undefined, {
    hour: '2-digit',
    minute: '2-digit',
  })
}

function scrollToMessage(messageId: string) {
  const el = document.querySelector(`[data-message-id="${messageId}"]`)
  if (el) {
    el.scrollIntoView({ behavior: 'smooth', block: 'center' })
    el.classList.add('ring-1', 'ring-amber-500/50', 'rounded-lg')
    setTimeout(() => {
      el.classList.remove('ring-1', 'ring-amber-500/50', 'rounded-lg')
    }, 2000)
  }
}

export function BookmarksWidget() {
  const activeSessionId = useAppStore((s) => s.activeSessionId)

  const { data: bookmarks = [] } = useQuery({
    queryKey: ['bookmarks', activeSessionId],
    queryFn: () => api.listBookmarks(activeSessionId!),
    enabled: !!activeSessionId,
  })

  return (
    <Widget id="bookmarks" title="Bookmarks" icon={Bookmark}>
      {bookmarks.length === 0 ? (
        <p className="text-xs text-zinc-500 italic">
          No bookmarks yet. Bookmark messages with Cmd+D.
        </p>
      ) : (
        <div className="space-y-2">
          {bookmarks.map((bm) => (
            <button
              key={bm.id}
              onClick={() => scrollToMessage(bm.message_id)}
              className="w-full text-left p-2 rounded-md bg-zinc-800/50 hover:bg-zinc-800 transition-colors group"
            >
              <p className="text-xs text-zinc-300 truncate group-hover:text-zinc-100">
                {bm.note || 'Bookmarked message'}
              </p>
              <p className="text-xs text-zinc-600 mt-0.5">{formatTime(bm.created_at)}</p>
            </button>
          ))}
        </div>
      )}
    </Widget>
  )
}
