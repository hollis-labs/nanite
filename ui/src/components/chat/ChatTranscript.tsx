import { useEffect, useRef, useMemo, useCallback } from 'react'
import { Bot } from 'lucide-react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { ScrollArea } from '@/components/ui/ScrollArea'
import { ChatMessage } from './ChatMessage'
import { MessageContent } from './MessageContent'
import { ToolCallIndicator } from './ToolCallIndicator'
import { ErrorBanner } from './ErrorBanner'
import { useChatStore } from '@/stores/useChatStore'
import { useAppStore } from '@/stores/useAppStore'
import { api } from '@/lib/api'
import type { Message, AgentMode } from '@/lib/types'

const MODE_AVATAR_STYLES: Record<AgentMode, { bg: string; text: string }> = {
  default: { bg: 'bg-blue-500/15', text: 'text-blue-400' },
  architect: { bg: 'bg-purple-500/15', text: 'text-purple-400' },
  planner: { bg: 'bg-green-500/15', text: 'text-green-400' },
  writer: { bg: 'bg-amber-500/15', text: 'text-amber-400' },
}

interface ChatTranscriptProps {
  messages: Message[]
  isStreaming: boolean
  streamingContent: string
  onSendMessage?: (content: string) => void
}

export function ChatTranscript({ messages, isStreaming, streamingContent, onSendMessage }: ChatTranscriptProps) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const bottomRef = useRef<HTMLDivElement>(null)
  const activeMode = useChatStore((s) => s.activeMode)
  const toolCalls = useChatStore((s) => s.toolCalls)
  const chatErrors = useChatStore((s) => s.chatErrors)
  const dismissChatError = useChatStore((s) => s.dismissChatError)
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const queryClient = useQueryClient()

  const avatarStyle = MODE_AVATAR_STYLES[activeMode]

  // Fetch bookmarks for the active session
  const { data: bookmarks = [] } = useQuery({
    queryKey: ['bookmarks', activeSessionId],
    queryFn: () => api.listBookmarks(activeSessionId!),
    enabled: !!activeSessionId,
  })

  const bookmarkedMessageIds = useMemo(
    () => new Set(bookmarks.map((b) => b.message_id)),
    [bookmarks]
  )

  const toggleBookmarkMutation = useMutation({
    mutationFn: (messageId: string) => api.toggleBookmark(messageId, activeSessionId!),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['bookmarks', activeSessionId] })
    },
  })

  const handleToggleBookmark = useCallback(
    (messageId: string) => {
      if (!activeSessionId) return
      toggleBookmarkMutation.mutate(messageId)
    },
    [activeSessionId, toggleBookmarkMutation]
  )

  // Auto-scroll to bottom on new messages or streaming updates
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages.length, streamingContent, toolCalls.length, chatErrors.length])

  if (messages.length === 0 && !isStreaming) {
    return (
      <div className="flex-1 flex items-center justify-center">
        <div className="text-center">
          <Bot className="w-16 h-16 text-zinc-800 mx-auto mb-4" />
          <h2 className="text-lg font-medium text-zinc-400 mb-1">Start a conversation with Conduit</h2>
          <p className="text-xs text-zinc-600 mt-1">Type a message below to begin</p>
        </div>
      </div>
    )
  }

  return (
    <ScrollArea className="flex-1 px-4 py-6" ref={scrollRef}>
      <div className="max-w-3xl mx-auto space-y-6">
        {messages.map((msg) => (
          <ChatMessage
            key={msg.id}
            message={msg}
            isBookmarked={bookmarkedMessageIds.has(msg.id)}
            onToggleBookmark={handleToggleBookmark}
            onSendMessage={onSendMessage}
          />
        ))}

        {/* Tool call indicators during streaming */}
        {isStreaming && toolCalls.length > 0 && (
          <div className="flex gap-3">
            <div className={`w-7 h-7 rounded-md flex items-center justify-center shrink-0 mt-0.5 ${avatarStyle.bg} ${avatarStyle.text}`}>
              <Bot className="w-4 h-4" />
            </div>
            <div className="flex-1 min-w-0 space-y-1">
              {toolCalls.map((tc) => (
                <ToolCallIndicator key={tc.id} toolCall={tc} />
              ))}
            </div>
          </div>
        )}

        {/* Streaming message */}
        {isStreaming && streamingContent && (
          <div className="flex gap-3">
            <div className={`w-7 h-7 rounded-md flex items-center justify-center shrink-0 mt-0.5 ${avatarStyle.bg} ${avatarStyle.text}`}>
              <Bot className="w-4 h-4" />
            </div>
            <div className="flex-1 min-w-0">
              <div className="text-xs font-medium text-zinc-500 mb-1">Conduit</div>
              <MessageContent content={streamingContent} role="assistant" />
            </div>
          </div>
        )}

        {/* Streaming indicator (before any content arrives) */}
        {isStreaming && !streamingContent && toolCalls.length === 0 && (
          <div className="flex gap-3">
            <div className={`w-7 h-7 rounded-md flex items-center justify-center shrink-0 mt-0.5 ${avatarStyle.bg} ${avatarStyle.text}`}>
              <Bot className="w-4 h-4" />
            </div>
            <div className="flex-1 min-w-0">
              <div className="text-xs font-medium text-zinc-500 mb-1">Conduit</div>
              <div className="flex items-center gap-1 py-2">
                <div className="w-1.5 h-1.5 rounded-full bg-zinc-500 animate-pulse" />
                <div className="w-1.5 h-1.5 rounded-full bg-zinc-500 animate-pulse [animation-delay:150ms]" />
                <div className="w-1.5 h-1.5 rounded-full bg-zinc-500 animate-pulse [animation-delay:300ms]" />
              </div>
            </div>
          </div>
        )}

        {/* Error banners */}
        {chatErrors.filter((e) => !e.dismissed).map((error) => (
          <ErrorBanner key={error.id} error={error} onDismiss={dismissChatError} />
        ))}

        <div ref={bottomRef} />
      </div>
    </ScrollArea>
  )
}
