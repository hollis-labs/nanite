import { useState, useCallback, useRef, useEffect } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { useChatStore } from '@/stores/useChatStore'
import type { Message, StreamEvent } from '@/lib/types'

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
            const errMsg = data.error || 'Unknown streaming error'
            console.error('Stream error from backend:', errMsg)
            accumulated += `\n\n**Error:** ${errMsg}`
            appendStreamContent(`\n\n**Error:** ${errMsg}`)
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
  }, [sessionId, queryClient, setStreaming, setStreamingSessionId, appendStreamContent, clearStream, addToolCall, updateToolCall, clearToolCalls])

  const stopStreaming = useCallback(() => {
    if (eventSourceRef.current) {
      eventSourceRef.current.close()
      eventSourceRef.current = null
    }
    clearStream()
  }, [clearStream])

  return { messages, isStreaming, streamingContent, sendMessage, loadMessages, stopStreaming }
}
