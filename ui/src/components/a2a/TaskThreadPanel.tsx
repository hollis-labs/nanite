import { useState, useRef, useEffect, useCallback } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { MessageSquare, Send, X, ChevronRight } from 'lucide-react'
import { api } from '@/lib/api'
import type { A2AMessage } from '@/lib/types'

interface TaskThreadPanelProps {
  taskId: string
  open: boolean
  onToggle: () => void
}

/**
 * TaskThreadPanel renders an A2A message thread for a given Engine task.
 * It appears as a narrow side panel beside the chat when the session is
 * task-scoped (context_type === 'task').
 */
export function TaskThreadPanel({ taskId, open, onToggle }: TaskThreadPanelProps) {
  const [body, setBody] = useState('')
  const [sending, setSending] = useState(false)
  const scrollRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)
  const queryClient = useQueryClient()

  const { data: messages = [], isLoading } = useQuery({
    queryKey: ['a2a-thread', taskId],
    queryFn: () => api.getA2AThread(taskId),
    refetchInterval: 10_000,
    enabled: open,
  })

  // Scroll to bottom when messages change
  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
  }, [messages])

  const handleSend = useCallback(async () => {
    const trimmed = body.trim()
    if (!trimmed || sending) return

    setSending(true)
    try {
      await api.sendA2AMessage({
        from_agent: 'user',
        to_agent: 'thread',
        thread_id: taskId,
        type: 'message',
        body: trimmed,
      })
      setBody('')
      // Refetch thread immediately after send
      await queryClient.invalidateQueries({ queryKey: ['a2a-thread', taskId] })
    } catch (err) {
      console.error('Failed to send A2A message:', err)
    } finally {
      setSending(false)
    }
  }, [body, sending, taskId, queryClient])

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault()
        void handleSend()
      }
    },
    [handleSend],
  )

  // Collapsed state: show a thin tab on the right edge
  if (!open) {
    return (
      <button
        onClick={onToggle}
        className="flex items-center gap-1 px-1.5 py-3 bg-zinc-900 border-l border-zinc-800 hover:bg-zinc-800 transition-colors cursor-pointer"
        title="Open task thread"
      >
        <ChevronRight className="w-3.5 h-3.5 text-zinc-500 rotate-180" />
        <MessageSquare className="w-4 h-4 text-zinc-500" />
      </button>
    )
  }

  return (
    <div className="flex flex-col w-[320px] min-w-[320px] border-l border-zinc-800 bg-zinc-900">
      {/* Header */}
      <div className="flex items-center justify-between px-3 py-2.5 border-b border-zinc-800">
        <div className="flex items-center gap-2 min-w-0">
          <MessageSquare className="w-4 h-4 text-zinc-400 flex-shrink-0" />
          <span className="text-sm font-medium text-zinc-300 truncate">
            Task Thread
          </span>
          <span className="text-xs text-zinc-600 truncate" title={taskId}>
            {taskId.length > 16 ? `${taskId.slice(0, 16)}...` : taskId}
          </span>
        </div>
        <button
          onClick={onToggle}
          className="p-1 rounded hover:bg-zinc-800 transition-colors"
          title="Close thread panel"
        >
          <X className="w-4 h-4 text-zinc-500" />
        </button>
      </div>

      {/* Messages */}
      <div ref={scrollRef} className="flex-1 overflow-y-auto px-3 py-2 space-y-3">
        {isLoading && (
          <div className="text-xs text-zinc-600 text-center py-8">
            Loading thread...
          </div>
        )}
        {!isLoading && messages.length === 0 && (
          <div className="text-xs text-zinc-600 text-center py-8">
            No messages yet. Start the conversation.
          </div>
        )}
        {messages.map((msg) => (
          <ThreadMessage key={msg.id} message={msg} />
        ))}
      </div>

      {/* Compose */}
      <div className="border-t border-zinc-800 p-2">
        <div className="flex items-end gap-2">
          <textarea
            ref={inputRef}
            value={body}
            onChange={(e) => setBody(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="Message this thread..."
            rows={1}
            className="flex-1 resize-none rounded-md bg-zinc-800 border border-zinc-700 px-2.5 py-1.5 text-sm text-zinc-200 placeholder-zinc-600 focus:outline-none focus:border-zinc-600 focus:ring-1 focus:ring-zinc-600"
          />
          <button
            onClick={() => void handleSend()}
            disabled={!body.trim() || sending}
            className="p-1.5 rounded-md bg-blue-600 hover:bg-blue-500 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
            title="Send message"
          >
            <Send className="w-4 h-4 text-white" />
          </button>
        </div>
      </div>
    </div>
  )
}

// --- Individual message bubble ---

function ThreadMessage({ message }: { message: A2AMessage }) {
  const ts = formatTimestamp(message.created_at)

  return (
    <div className="group">
      <div className="flex items-baseline gap-2 mb-0.5">
        <span className="text-xs font-medium text-blue-400 truncate">
          {message.from_agent || 'unknown'}
        </span>
        <span className="text-[10px] text-zinc-600 flex-shrink-0">{ts}</span>
      </div>
      <div className="text-sm text-zinc-300 leading-relaxed whitespace-pre-wrap break-words">
        {message.body}
      </div>
    </div>
  )
}

// --- Helpers ---

function formatTimestamp(iso: string): string {
  try {
    const d = new Date(iso)
    const now = new Date()
    const diffMs = now.getTime() - d.getTime()
    const diffMin = Math.floor(diffMs / 60_000)

    if (diffMin < 1) return 'just now'
    if (diffMin < 60) return `${String(diffMin)}m ago`

    const diffHr = Math.floor(diffMin / 60)
    if (diffHr < 24) return `${String(diffHr)}h ago`

    return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
  } catch {
    return iso
  }
}
