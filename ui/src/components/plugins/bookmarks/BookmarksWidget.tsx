import { useEffect, useRef } from 'react'
import { Bookmark } from 'lucide-react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Widget } from '@/components/widgets/Widget'
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
    el.classList.add('ring-1', 'ring-status-warn/50', 'rounded-lg')
    setTimeout(() => {
      el.classList.remove('ring-1', 'ring-status-warn/50', 'rounded-lg')
    }, 2000)
  }
}

export function BookmarksWidget() {
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const queryClient = useQueryClient()
  const autotitledIds = useRef(new Set<string>())

  const { data: bookmarks = [] } = useQuery({
    queryKey: ['bookmarks', activeSessionId],
    queryFn: () => api.listBookmarks(activeSessionId!),
    enabled: !!activeSessionId,
  })

  // Auto-title bookmarks that have no custom note
  useEffect(() => {
    for (const bm of bookmarks) {
      const needsTitle = !bm.note || bm.note === 'Bookmarked message' || bm.note === 'bookmark'
      if (needsTitle && !autotitledIds.current.has(bm.id)) {
        autotitledIds.current.add(bm.id)
        api.autotitleBookmark(bm.id)
          .then(() => {
            void queryClient.invalidateQueries({ queryKey: ['bookmarks', activeSessionId] })
          })
          .catch(() => {
            // Endpoint may not exist yet — silently ignore
            autotitledIds.current.delete(bm.id)
          })
      }
    }
  }, [bookmarks, activeSessionId, queryClient])

  return (
    <Widget id="bookmarks" title="Bookmarks" icon={Bookmark}>
      {bookmarks.length === 0 ? (
        <p className="text-xs text-fg-muted italic">
          No bookmarks yet. Bookmark messages with Cmd+D.
        </p>
      ) : (
        <div className="space-y-2">
          {bookmarks.map((bm) => (
            <button
              key={bm.id}
              onClick={() => scrollToMessage(bm.message_id)}
              className="w-full text-left p-2 rounded-md bg-surface/50 hover:bg-surface transition-colors group"
            >
              <p className="text-xs text-fg-secondary truncate group-hover:text-fg">
                {bm.note || 'Bookmarked message'}
              </p>
              <p className="text-xs text-fg-faint mt-0.5">{formatTime(bm.created_at)}</p>
            </button>
          ))}
        </div>
      )}
    </Widget>
  )
}
