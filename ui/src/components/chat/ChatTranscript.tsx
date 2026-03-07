import { useEffect, useRef } from 'react'
import { MessageSquare, Bot } from 'lucide-react'
import { ScrollArea } from '@/components/ui/ScrollArea'
import { ChatMessage } from './ChatMessage'
import { MessageContent } from './MessageContent'
import type { Message } from '@/lib/types'

interface ChatTranscriptProps {
  messages: Message[]
  isStreaming: boolean
  streamingContent: string
}

export function ChatTranscript({ messages, isStreaming, streamingContent }: ChatTranscriptProps) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const bottomRef = useRef<HTMLDivElement>(null)

  // Auto-scroll to bottom on new messages or streaming updates
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages.length, streamingContent])

  if (messages.length === 0 && !isStreaming) {
    return (
      <div className="flex-1 flex items-center justify-center">
        <div className="text-center">
          <MessageSquare className="w-12 h-12 text-zinc-700 mx-auto mb-3" />
          <p className="text-sm text-zinc-500">Start a conversation</p>
          <p className="text-xs text-zinc-600 mt-1">Type a message below to begin</p>
        </div>
      </div>
    )
  }

  return (
    <ScrollArea className="flex-1 px-4 py-6" ref={scrollRef}>
      <div className="max-w-3xl mx-auto space-y-6">
        {messages.map((msg) => (
          <ChatMessage key={msg.id} message={msg} />
        ))}

        {/* Streaming message */}
        {isStreaming && streamingContent && (
          <div className="flex gap-3">
            <div className="w-7 h-7 rounded-md flex items-center justify-center shrink-0 mt-0.5 bg-indigo-500/15 text-indigo-400">
              <Bot className="w-4 h-4" />
            </div>
            <div className="flex-1 min-w-0">
              <div className="text-xs font-medium text-zinc-500 mb-1">Mentat</div>
              <MessageContent content={streamingContent} role="assistant" />
            </div>
          </div>
        )}

        {/* Streaming indicator (before any content arrives) */}
        {isStreaming && !streamingContent && (
          <div className="flex gap-3">
            <div className="w-7 h-7 rounded-md flex items-center justify-center shrink-0 mt-0.5 bg-indigo-500/15 text-indigo-400">
              <Bot className="w-4 h-4" />
            </div>
            <div className="flex-1 min-w-0">
              <div className="text-xs font-medium text-zinc-500 mb-1">Mentat</div>
              <div className="flex items-center gap-1 py-2">
                <div className="w-1.5 h-1.5 rounded-full bg-zinc-500 animate-pulse" />
                <div className="w-1.5 h-1.5 rounded-full bg-zinc-500 animate-pulse [animation-delay:150ms]" />
                <div className="w-1.5 h-1.5 rounded-full bg-zinc-500 animate-pulse [animation-delay:300ms]" />
              </div>
            </div>
          </div>
        )}

        <div ref={bottomRef} />
      </div>
    </ScrollArea>
  )
}
