import { MessageSquarePlus } from 'lucide-react'
import { ChatHeader } from './ChatHeader'
import { ChatTranscript } from './ChatTranscript'
import { ChatComposer } from './ChatComposer'
import { useChat } from '@/hooks/useChat'
import { useAppStore } from '@/stores/useAppStore'

export function ChatMain() {
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const { messages, isStreaming, streamingContent, sendMessage, stopStreaming } =
    useChat(activeSessionId)

  if (!activeSessionId) {
    return (
      <main className="flex-1 flex flex-col items-center justify-center min-w-0 bg-zinc-950">
        <MessageSquarePlus className="w-16 h-16 text-zinc-800 mb-4" />
        <h2 className="text-lg font-medium text-zinc-400 mb-1">No chat selected</h2>
        <p className="text-sm text-zinc-600">Select a session or create a new chat to begin</p>
      </main>
    )
  }

  return (
    <main className="flex-1 flex flex-col min-w-0 bg-zinc-950">
      <ChatHeader />
      <ChatTranscript
        messages={messages}
        isStreaming={isStreaming}
        streamingContent={streamingContent}
      />
      <ChatComposer
        onSend={sendMessage}
        isStreaming={isStreaming}
        onStop={stopStreaming}
      />
    </main>
  )
}
