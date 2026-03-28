import { useState, useCallback } from 'react'
import { Copy, Check, Bookmark, BookmarkCheck } from 'lucide-react'

interface ContentActionsProps {
  content: string
  messageId?: string
  isBookmarked?: boolean
  onToggleBookmark?: (messageId: string) => void
  visible: boolean
  className?: string
}

export function ContentActions({
  content,
  messageId,
  isBookmarked = false,
  onToggleBookmark,
  visible,
  className = '',
}: ContentActionsProps) {
  const [copied, setCopied] = useState(false)

  const handleCopy = useCallback(() => {
    void navigator.clipboard.writeText(content)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }, [content])

  const handleBookmark = useCallback(() => {
    if (messageId && onToggleBookmark) {
      onToggleBookmark(messageId)
    }
  }, [messageId, onToggleBookmark])

  return (
    <div className={`flex items-center gap-1 transition-opacity duration-150 ${visible ? 'opacity-100' : 'opacity-0'} ${className}`}>
      <button
        onClick={handleCopy}
        className="p-1 rounded text-zinc-600 hover:text-zinc-300 hover:bg-zinc-800 transition-colors"
        aria-label="Copy"
        tabIndex={visible ? 0 : -1}
      >
        {copied ? <Check className="w-3.5 h-3.5" /> : <Copy className="w-3.5 h-3.5" />}
      </button>
      {messageId && onToggleBookmark && (
        <button
          onClick={handleBookmark}
          className={`p-1 rounded transition-colors ${
            isBookmarked
              ? 'text-amber-500 hover:text-amber-400 hover:bg-zinc-800'
              : 'text-zinc-600 hover:text-zinc-300 hover:bg-zinc-800'
          }`}
          aria-label={isBookmarked ? 'Remove bookmark' : 'Bookmark'}
          tabIndex={visible ? 0 : -1}
        >
          {isBookmarked ? (
            <BookmarkCheck className="w-3.5 h-3.5" />
          ) : (
            <Bookmark className="w-3.5 h-3.5" />
          )}
        </button>
      )}
    </div>
  )
}
