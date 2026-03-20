import { useEffect, useRef, useMemo, useCallback, useState, useLayoutEffect } from 'react'
import { Bot, ArrowDown, Info } from 'lucide-react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { ScrollArea } from '@/components/ui/ScrollArea'
import { ChatMessage } from './ChatMessage'
import { MessageContent } from './MessageContent'
import { ToolCallIndicator } from './ToolCallIndicator'
import { ToolWarningBanner } from './ToolWarningBanner'
import { ThinkingIndicator } from './ThinkingIndicator'
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
  const toolWarnings = useChatStore((s) => s.toolWarnings)
  const textOnlyMode = useChatStore((s) => s.textOnlyMode)
  const chatErrors = useChatStore((s) => s.chatErrors)
  const dismissChatError = useChatStore((s) => s.dismissChatError)
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const queryClient = useQueryClient()

  // Track if user is scrolled to bottom - auto-scroll only when at bottom
  const [isAtBottom, setIsAtBottom] = useState(true)
  const [userHasScrolled, setUserHasScrolled] = useState(false)

  // Detect stalled stream — content stopped flowing but still streaming (tool calls, LLM thinking)
  const [streamStalled, setStreamStalled] = useState(false)
  const lastContentRef = useRef(streamingContent)
  const stallTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    if (!isStreaming) {
      setStreamStalled(false)
      if (stallTimerRef.current) { clearTimeout(stallTimerRef.current); stallTimerRef.current = null }
      lastContentRef.current = ''
      return
    }
    // Content changed — reset stall detection
    if (streamingContent !== lastContentRef.current) {
      lastContentRef.current = streamingContent
      setStreamStalled(false)
      if (stallTimerRef.current) clearTimeout(stallTimerRef.current)
      // Start new stall timer — if no new content for 2s, show indicator
      if (streamingContent) {
        stallTimerRef.current = setTimeout(() => setStreamStalled(true), 2000)
      }
    }
  }, [isStreaming, streamingContent])

  const avatarStyle = MODE_AVATAR_STYLES[activeMode]

  // Check if user is near the bottom of the scroll area
  const checkScrollPosition = useCallback(() => {
    const scrollElement = scrollRef.current
    if (!scrollElement) return

    const { scrollTop, scrollHeight, clientHeight } = scrollElement
    const distanceFromBottom = scrollHeight - scrollTop - clientHeight

    // Use threshold: >100px = pause auto-scroll, <50px = resume auto-scroll
    if (distanceFromBottom > 100) {
      setIsAtBottom(false)
      setUserHasScrolled(true)
    } else if (distanceFromBottom < 50) {
      setIsAtBottom(true)
      setUserHasScrolled(false)
    }
  }, [])

  // Add scroll event listener
  useEffect(() => {
    const scrollElement = scrollRef.current
    if (!scrollElement) return

    scrollElement.addEventListener('scroll', checkScrollPosition)
    return () => {
      scrollElement.removeEventListener('scroll', checkScrollPosition)
    }
  }, [checkScrollPosition])

  // Also check scroll position when content changes (not just user scroll)
  useLayoutEffect(() => {
    checkScrollPosition()
  }, [messages.length, streamingContent, toolCalls.length, toolWarnings.length, chatErrors.length, checkScrollPosition])

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

  // Scroll to bottom function
  const scrollToBottom = useCallback(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
    setIsAtBottom(true)
    setUserHasScrolled(false)
  }, [])

  // Auto-scroll to bottom on new messages or streaming updates
  // Only when user is at bottom AND hasn't manually scrolled up
  useEffect(() => {
    if (isAtBottom && !userHasScrolled) {
      bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
    }
  }, [messages.length, streamingContent, toolCalls.length, toolWarnings.length, chatErrors.length, isAtBottom, userHasScrolled])

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
    <ScrollArea className="flex-1 px-4 py-6 relative" ref={scrollRef}>
      <div className="max-w-3xl mx-auto space-y-6">
        {/* Persistent text-only mode banner (agent has 0 MCP tools) */}
        {textOnlyMode && (
          <div className="flex items-center gap-2 px-3 py-2 rounded-md text-xs bg-zinc-800 border border-zinc-700 text-zinc-400">
            <Info className="w-3.5 h-3.5 shrink-0" />
            <span>This agent has no tools configured — responses are text-only</span>
          </div>
        )}

        {messages.map((msg) => (
          <ChatMessage
            key={msg.id}
            message={msg}
            isBookmarked={bookmarkedMessageIds.has(msg.id)}
            onToggleBookmark={handleToggleBookmark}
            {...(onSendMessage && { onSendMessage })}
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

        {/* Tool warning banner during streaming */}
        {isStreaming && toolWarnings.length > 0 && (
          <ToolWarningBanner warnings={toolWarnings} />
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
              {streamStalled && <ThinkingIndicator />}
            </div>
          </div>
        )}

        {/* Thinking indicator — shown while streaming, before content arrives */}
        {isStreaming && !streamingContent && (
          <div className="flex gap-3">
            <div className={`w-7 h-7 rounded-md flex items-center justify-center shrink-0 mt-0.5 ${avatarStyle.bg} ${avatarStyle.text}`}>
              <Bot className="w-4 h-4" />
            </div>
            <div className="flex-1 min-w-0">
              <div className="text-xs font-medium text-zinc-500 mb-1">Conduit</div>
              <ThinkingIndicator />
            </div>
          </div>
        )}

        {/* Error banners */}
        {chatErrors.filter((e) => !e.dismissed).map((error) => (
          <ErrorBanner key={error.id} error={error} onDismiss={dismissChatError} />
        ))}

        <div ref={bottomRef} />
      </div>

      {/* Scroll to bottom button - shown when user has scrolled up */}
      {userHasScrolled && !isAtBottom && (
        <button
          onClick={scrollToBottom}
          className="absolute bottom-4 right-4 bg-blue-500 hover:bg-blue-600 text-white rounded-full p-3 shadow-lg transition-all duration-200 hover:scale-105"
          aria-label="Scroll to bottom"
        >
          <ArrowDown className="w-5 h-5" />
        </button>
      )}
    </ScrollArea>
  )
}
