import { useState, useCallback, useRef, useEffect } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { useChatStore } from '@/stores/useChatStore'
import { useSprintPlanningStore } from '@/stores/useSprintPlanningStore'
import type { Message, StreamEvent, ChatError, ChatErrorCode } from '@/lib/types'

let errorCounter = 0

function makeChatError(
  code: ChatErrorCode,
  message: string,
  details?: Record<string, unknown>,
  timestamp?: string
): ChatError {
  errorCounter += 1
  return {
    id: `err-${Date.now()}-${errorCounter}`,
    code,
    message,
    details,
    timestamp: timestamp || new Date().toISOString(),
  }
}

export function useChat(sessionId: string | null) {
  const [messages, setMessages] = useState<Message[]>([])
  const eventSourceRef = useRef<EventSource | null>(null)

  const queryClient = useQueryClient()

  const isStreaming = useChatStore((s) => s.isStreaming)
  const streamingContent = useChatStore((s) => s.streamingContent)
  const setStreaming = useChatStore((s) => s.setStreaming)
  const setStreamingSessionId = useChatStore((s) => s.setStreamingSessionId)
  const appendStreamContent = useChatStore((s) => s.appendStreamContent)
  const clearStream = useChatStore((s) => s.clearStream)
  const addToolCall = useChatStore((s) => s.addToolCall)
  const updateToolCall = useChatStore((s) => s.updateToolCall)
  const clearToolCalls = useChatStore((s) => s.clearToolCalls)
  const addChatError = useChatStore((s) => s.addChatError)
  const statusMessage = useChatStore((s) => s.statusMessage)
  const setStatusMessage = useChatStore((s) => s.setStatusMessage)
  const circuitOpen = useChatStore((s) => s.circuitOpen)
  const setCircuitOpen = useChatStore((s) => s.setCircuitOpen)

  const loadMessages = useCallback(async () => {
    if (!sessionId) {
      setMessages([])
      return
    }
    try {
      const msgs = await api.getMessages(sessionId)
      setMessages(msgs ?? [])
    } catch (err) {
      console.error('Failed to load messages:', err)
    }
  }, [sessionId])

  // Load messages when sessionId changes
  useEffect(() => {
    void loadMessages()
  }, [loadMessages])

  const sendMessage = useCallback(async (content: string) => {
    if (!sessionId || !content.trim()) return

    // Optimistically add user message
    const tempUserMsg: Message = {
      id: 'temp-' + Date.now(),
      session_id: sessionId,
      agent_id: '',
      role: 'user',
      content,
      envelope: null,
      metadata: '{}',
      created_at: new Date().toISOString(),
    }
    setMessages(prev => [...prev, tempUserMsg])
    setStreaming(true)
    setStreamingSessionId(sessionId)
    clearToolCalls()

    try {
      const { message_id } = await api.sendMessage({ session_id: sessionId, content })

      // Connect to SSE stream
      const es = new EventSource(`/api/stream/${message_id}`)
      eventSourceRef.current = es
      let accumulated = ''

      es.addEventListener('delta', (e: MessageEvent) => {
        const data: StreamEvent = JSON.parse(e.data as string)
        if (data.content) {
          accumulated += data.content
          appendStreamContent(data.content)
          // Clear any transient status message when content starts flowing.
          setStatusMessage(null)
        }
      })

      es.addEventListener('tool_call', (e: MessageEvent) => {
        const data = JSON.parse(e.data as string) as StreamEvent & { tool_id?: string }
        if (data.tool) {
          addToolCall({
            id: data.tool_id || data.message_id || `tc-${Date.now()}`,
            tool: data.tool,
            status: 'running',
          })

          // UI-trigger tools: open frontend modals/panels when the agent calls them.
          if (data.tool === 'conduit_open_sprint_planning') {
            useSprintPlanningStore.getState().openSprintPlanning()
          }
        }
      })

      es.addEventListener('tool_result', (e: MessageEvent) => {
        const data = JSON.parse(e.data as string) as StreamEvent & { tool_id?: string }
        const toolId = data.tool_id || data.message_id
        if (toolId) {
          updateToolCall(toolId, {
            status: data.error ? 'error' : 'done',
            summary: data.summary || data.error,
          })
        }
      })

      es.addEventListener('status', (e: MessageEvent) => {
        const data: StreamEvent = JSON.parse(e.data as string)
        if (data.content) {
          setStatusMessage(data.content)
        }
      })

      es.addEventListener('circuit_open', () => {
        setCircuitOpen(true)
        // Do NOT close the EventSource — keep it open for potential retry.
      })

      es.addEventListener('stream_end', (e: MessageEvent) => {
        const data: StreamEvent = JSON.parse(e.data as string)
        // Add the complete assistant message
        const assistantMsg: Message = {
          id: message_id,
          session_id: sessionId,
          agent_id: data.agent_id || '',
          role: 'assistant',
          content: accumulated,
          envelope: null,
          metadata: JSON.stringify(data.usage || {}),
          created_at: new Date().toISOString(),
        }
        setMessages(prev => [...prev, assistantMsg])
        clearStream()
        es.close()
        eventSourceRef.current = null

        // Refresh widgets that depend on session usage data
        void queryClient.invalidateQueries({ queryKey: ['session-usage', sessionId] })
        void queryClient.invalidateQueries({ queryKey: ['session', sessionId] })
      })

      es.addEventListener('error', (e: MessageEvent) => {
        // Custom SSE error event from the backend (has data).
        if (e.data) {
          try {
            const data: StreamEvent = JSON.parse(e.data as string)

            // Handle structured error from backend
            if (data.structured_error) {
              const se = data.structured_error
              addChatError(makeChatError(se.code, se.message, se.details, se.timestamp))
              // Append a brief user-friendly message to the chat
              const brief = `\n\n_Error: ${se.message}_`
              accumulated += brief
              appendStreamContent(brief)
            } else {
              // Fallback for unstructured errors
              const errMsg = data.error || 'Unknown streaming error'
              console.error('Stream error from backend:', errMsg)
              addChatError(makeChatError('internal_error', errMsg))
              accumulated += `\n\n_Error: ${errMsg}_`
              appendStreamContent(`\n\n_Error: ${errMsg}_`)
            }
          } catch {
            console.error('Stream error (unparseable):', e.data)
          }
        }
        // Finalize the stream with whatever we have.
        if (accumulated) {
          const assistantMsg: Message = {
            id: message_id,
            session_id: sessionId,
            agent_id: '',
            role: 'assistant',
            content: accumulated,
            envelope: null,
            metadata: '{}',
            created_at: new Date().toISOString(),
          }
          setMessages(prev => [...prev, assistantMsg])
        }
        clearStream()
        es.close()
        eventSourceRef.current = null
      })

      // Handle native EventSource connection errors (no data).
      es.onerror = () => {
        // Only handle if the custom error listener above didn't already fire.
        if (eventSourceRef.current) {
          console.error('SSE connection lost')
          if (accumulated) {
            const assistantMsg: Message = {
              id: message_id,
              session_id: sessionId,
              agent_id: '',
              role: 'assistant',
              content: accumulated,
              envelope: null,
              metadata: '{}',
              created_at: new Date().toISOString(),
            }
            setMessages(prev => [...prev, assistantMsg])
          }
          clearStream()
          es.close()
          eventSourceRef.current = null
        }
      }
    } catch (err) {
      console.error('Send failed:', err)
      clearStream()
    }
  }, [sessionId, queryClient, setStreaming, setStreamingSessionId, appendStreamContent, clearStream, addToolCall, updateToolCall, clearToolCalls, addChatError, setStatusMessage, setCircuitOpen])

  const stopStreaming = useCallback(() => {
    if (eventSourceRef.current) {
      eventSourceRef.current.close()
      eventSourceRef.current = null
    }
    clearStream()
  }, [clearStream])

  const retryStream = useCallback(async () => {
    if (!sessionId) return
    setCircuitOpen(false)

    try {
      const { message_id } = await api.retryStream(sessionId)

      // Close old event source if still open.
      if (eventSourceRef.current) {
        eventSourceRef.current.close()
      }

      // Open a new SSE connection for the retry.
      const es = new EventSource(`/api/stream/${message_id}`)
      eventSourceRef.current = es
      setStreaming(true)

      es.addEventListener('delta', (e: MessageEvent) => {
        const data: StreamEvent = JSON.parse(e.data as string)
        if (data.content) {
          appendStreamContent(data.content)
          setStatusMessage(null)
        }
      })

      es.addEventListener('stream_end', (e: MessageEvent) => {
        const data: StreamEvent = JSON.parse(e.data as string)
        const assistantMsg: Message = {
          id: message_id,
          session_id: sessionId,
          agent_id: data.agent_id || '',
          role: 'assistant',
          content: useChatStore.getState().streamingContent,
          envelope: null,
          metadata: JSON.stringify(data.usage || {}),
          created_at: new Date().toISOString(),
        }
        setMessages(prev => [...prev, assistantMsg])
        clearStream()
        es.close()
        eventSourceRef.current = null
      })

      es.addEventListener('circuit_open', () => {
        setCircuitOpen(true)
      })

      es.addEventListener('error', () => {
        clearStream()
        es.close()
        eventSourceRef.current = null
      })

      es.onerror = () => {
        if (eventSourceRef.current) {
          clearStream()
          es.close()
          eventSourceRef.current = null
        }
      }
    } catch (err) {
      console.error('Retry failed:', err)
      clearStream()
    }
  }, [sessionId, setCircuitOpen, setStreaming, appendStreamContent, setStatusMessage, clearStream])

  const dismissCircuit = useCallback(() => {
    setCircuitOpen(false)
    if (eventSourceRef.current) {
      eventSourceRef.current.close()
      eventSourceRef.current = null
    }
    // Save partial content with interruption note.
    const partial = useChatStore.getState().streamingContent
    if (partial && sessionId) {
      const msg: Message = {
        id: `partial-${Date.now()}`,
        session_id: sessionId,
        agent_id: '',
        role: 'assistant',
        content: partial + '\n\n_(Response interrupted: provider rate limited)_',
        envelope: null,
        metadata: '{}',
        created_at: new Date().toISOString(),
      }
      setMessages(prev => [...prev, msg])
    }
    clearStream()
  }, [sessionId, setCircuitOpen, clearStream])

  return { messages, isStreaming, streamingContent, statusMessage, circuitOpen, sendMessage, loadMessages, stopStreaming, retryStream, dismissCircuit }
}
