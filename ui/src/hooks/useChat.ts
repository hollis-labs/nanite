import { useState, useCallback, useRef, useEffect } from 'react'
import { api } from '@/lib/api'
import type { Message, StreamEvent } from '@/lib/types'

export function useChat(sessionId: string | null) {
  const [messages, setMessages] = useState<Message[]>([])
  const [isStreaming, setIsStreaming] = useState(false)
  const [streamingContent, setStreamingContent] = useState('')
  const eventSourceRef = useRef<EventSource | null>(null)

  const loadMessages = useCallback(async () => {
    if (!sessionId) {
      setMessages([])
      return
    }
    try {
      const msgs = await api.getMessages(sessionId)
      setMessages(msgs)
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
    setIsStreaming(true)
    setStreamingContent('')

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
          setStreamingContent(accumulated)
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
        setStreamingContent('')
        setIsStreaming(false)
        es.close()
        eventSourceRef.current = null
      })

      es.addEventListener('error', () => {
        setIsStreaming(false)
        es.close()
        eventSourceRef.current = null
      })

      // Also handle native EventSource errors
      es.onerror = () => {
        setIsStreaming(false)
        es.close()
        eventSourceRef.current = null
      }
    } catch (err) {
      console.error('Send failed:', err)
      setIsStreaming(false)
    }
  }, [sessionId])

  const stopStreaming = useCallback(() => {
    if (eventSourceRef.current) {
      eventSourceRef.current.close()
      eventSourceRef.current = null
    }
    setIsStreaming(false)
  }, [])

  return { messages, isStreaming, streamingContent, sendMessage, loadMessages, stopStreaming }
}
