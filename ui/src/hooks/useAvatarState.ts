import { useState, useEffect, useRef } from 'react'
import { useChatStore } from '@/stores/useChatStore'
import { useAppStore } from '@/stores/useAppStore'

export type AvatarState =
  | 'idle'
  | 'thinking'
  | 'tool_active'
  | 'listening'
  | 'celebrating'
  | 'error'
  | 'confused'

// Derives a deterministic avatar display state from the chat store.
// Priority: error > confused > celebrating > tool_active > thinking > listening > idle
export function useAvatarState(): AvatarState {
  const isStreaming = useChatStore((s) => s.isStreaming)
  const toolCalls = useChatStore((s) => s.toolCalls)
  const circuitOpen = useChatStore((s) => s.circuitOpen)
  const streamStalled = useChatStore((s) => s.streamStalled)
  const sessionTakeover = useChatStore((s) => s.sessionTakeover)
  const chatErrors = useChatStore((s) => s.chatErrors)
  const pendingTools = useChatStore((s) => s.pendingTools)
  const activeSessionId = useAppStore((s) => s.activeSessionId)

  const [celebrating, setCelebrating] = useState(false)
  const wasStreamingRef = useRef(false)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // Trigger a brief celebration burst when a stream completes
  useEffect(() => {
    if (wasStreamingRef.current && !isStreaming) {
      setCelebrating(true)
      if (timerRef.current) clearTimeout(timerRef.current)
      timerRef.current = setTimeout(() => setCelebrating(false), 2500)
    }
    wasStreamingRef.current = isStreaming
  }, [isStreaming])

  useEffect(
    () => () => {
      if (timerRef.current) clearTimeout(timerRef.current)
    },
    [],
  )

  const hasErrors = circuitOpen || chatErrors.filter((e) => !e.dismissed).length > 0
  const hasPendingTools = activeSessionId ? pendingTools.has(activeSessionId) : false
  const hasRunningTools = toolCalls.some((tc) => tc.status === 'running')

  if (hasErrors) return 'error'
  if (streamStalled || sessionTakeover) return 'confused'
  if (celebrating && !isStreaming) return 'celebrating'
  if (isStreaming && hasRunningTools) return 'tool_active'
  if (isStreaming) return 'thinking'
  if (hasPendingTools) return 'listening'
  return 'idle'
}
