import { Bot, User, Copy, Check } from 'lucide-react'
import { useState, useCallback } from 'react'
import type { Message, AgentMode, Envelope } from '@/lib/types'
import { MessageContent } from './MessageContent'
import { EnvelopeRenderer } from './envelopes/EnvelopeRenderer'
import { useChatStore } from '@/stores/useChatStore'

function formatRelativeTime(dateStr: string): string {
  const date = new Date(dateStr)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffSecs = Math.floor(diffMs / 1000)
  const diffMins = Math.floor(diffSecs / 60)
  const diffHours = Math.floor(diffMins / 60)
  const diffDays = Math.floor(diffHours / 24)

  if (diffSecs < 60) return 'just now'
  if (diffMins < 60) return `${diffMins}m ago`
  if (diffHours < 24) return `${diffHours}h ago`
  if (diffDays < 7) return `${diffDays}d ago`
  return date.toLocaleDateString()
}

const MODE_AVATAR_STYLES: Record<AgentMode, { bg: string; text: string }> = {
  default: { bg: 'bg-blue-500/15', text: 'text-blue-400' },
  architect: { bg: 'bg-purple-500/15', text: 'text-purple-400' },
  planner: { bg: 'bg-green-500/15', text: 'text-green-400' },
  writer: { bg: 'bg-amber-500/15', text: 'text-amber-400' },
}

const MODE_LABEL_STYLES: Record<AgentMode, string> = {
  default: 'text-blue-400',
  architect: 'text-purple-400',
  planner: 'text-green-400',
  writer: 'text-amber-400',
}

interface ChatMessageProps {
  message: Message
}

export function ChatMessage({ message }: ChatMessageProps) {
  const [copied, setCopied] = useState(false)
  const [hovered, setHovered] = useState(false)
  const activeMode = useChatStore((s) => s.activeMode)

  const handleCopy = useCallback(() => {
    void navigator.clipboard.writeText(message.content)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }, [message.content])

  const isUser = message.role === 'user'
  const avatarStyle = isUser
    ? { bg: 'bg-zinc-800', text: 'text-zinc-400' }
    : MODE_AVATAR_STYLES[activeMode]

  // Parse envelope if present
  let envelope: Envelope | null = null
  if (message.envelope) {
    try {
      envelope = typeof message.envelope === 'string'
        ? JSON.parse(message.envelope) as Envelope
        : message.envelope as unknown as Envelope
    } catch {
      // ignore parse errors
    }
  }

  return (
    <div
      className={`flex gap-3 group ${isUser ? 'flex-row-reverse' : ''}`}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
      {/* Avatar */}
      <div
        className={`w-7 h-7 rounded-md flex items-center justify-center shrink-0 mt-0.5 ${avatarStyle.bg} ${avatarStyle.text}`}
      >
        {isUser ? <User className="w-4 h-4" /> : <Bot className="w-4 h-4" />}
      </div>

      {/* Content */}
      <div className={`flex-1 min-w-0 ${isUser ? 'flex flex-col items-end' : ''}`}>
        <div className="flex items-center gap-2 mb-1">
          <span className="text-xs font-medium text-zinc-500">
            {isUser ? 'You' : 'Mentat'}
          </span>
          {!isUser && activeMode !== 'default' && (
            <span className={`text-xs font-medium ${MODE_LABEL_STYLES[activeMode]}`}>
              · {activeMode}
            </span>
          )}
          {hovered && (
            <span className="text-xs text-zinc-600">
              {formatRelativeTime(message.created_at)}
            </span>
          )}
        </div>
        <div
          className={`${
            isUser
              ? 'bg-zinc-800 rounded-2xl rounded-tr-sm px-4 py-2.5 max-w-[80%]'
              : 'max-w-full'
          }`}
        >
          <MessageContent content={message.content} role={message.role} />
        </div>

        {/* Envelope rendering */}
        {envelope && !isUser && (
          <div className="mt-3">
            <EnvelopeRenderer envelope={envelope} />
          </div>
        )}

        {/* Actions */}
        {hovered && !isUser && (
          <div className="flex items-center gap-1 mt-1">
            <button
              onClick={handleCopy}
              className="p-1 rounded text-zinc-600 hover:text-zinc-300 hover:bg-zinc-800 transition-colors"
              aria-label="Copy message"
            >
              {copied ? <Check className="w-3.5 h-3.5" /> : <Copy className="w-3.5 h-3.5" />}
            </button>
          </div>
        )}
      </div>
    </div>
  )
}
