import { Bot, AlertTriangle, RefreshCw, X } from 'lucide-react'
import { ChatHeader } from './ChatHeader'
import { ChatTranscript } from './ChatTranscript'
import { ChatComposer } from './ChatComposer'
import { useChat } from '@/hooks/useChat'
import { useAppStore } from '@/stores/useAppStore'

interface ChatMainProps {
  onEditorReady?: (focus: () => void) => void
}

export function ChatMain({ onEditorReady }: ChatMainProps) {
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const { messages, isStreaming, streamingContent, statusMessage, circuitOpen, sendMessage, stopStreaming, retryStream, dismissCircuit } =
    useChat(activeSessionId)

  if (!activeSessionId) {
    return (
      <main className="flex-1 flex flex-col items-center justify-center min-w-0 bg-zinc-950">
        <Bot className="w-16 h-16 text-zinc-800 mb-4" />
        <h2 className="text-lg font-medium text-zinc-400 mb-1">Create your first chat</h2>
        <p className="text-sm text-zinc-600">Select a session or press Cmd+N to begin</p>
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
      {circuitOpen && (
        <div className="mx-4 mb-2 rounded-lg border border-amber-500/30 bg-amber-500/10 p-4">
          <div className="flex items-start gap-3">
            <AlertTriangle className="w-5 h-5 text-amber-400 mt-0.5 flex-shrink-0" />
            <div className="flex-1 min-w-0">
              <p className="text-sm font-medium text-amber-200">
                Provider rate limited after multiple retries
              </p>
              <p className="text-xs text-amber-300/70 mt-1">
                The API provider has been returning rate limit errors. You can retry or dismiss to keep the partial response.
              </p>
              <div className="flex gap-2 mt-3">
                <button
                  onClick={() => void retryStream()}
                  className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium bg-amber-500/20 text-amber-200 hover:bg-amber-500/30 transition-colors"
                >
                  <RefreshCw className="w-3.5 h-3.5" />
                  Retry
                </button>
                <button
                  onClick={dismissCircuit}
                  className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium bg-zinc-700/50 text-zinc-300 hover:bg-zinc-700 transition-colors"
                >
                  <X className="w-3.5 h-3.5" />
                  Dismiss
                </button>
              </div>
            </div>
          </div>
        </div>
      )}
      {statusMessage && (
        <div className="px-4 py-1.5 text-xs text-amber-400 bg-amber-950/30 border-t border-amber-900/40 animate-pulse">
          {statusMessage}
        </div>
      )}
      <ChatComposer
        onSend={sendMessage}
        isStreaming={isStreaming}
        onStop={stopStreaming}
        onEditorReady={onEditorReady}
      />
    </main>
  )
}
