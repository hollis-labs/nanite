import { Bot, User, BookmarkCheck } from 'lucide-react'
import { useState, useMemo } from 'react'
import type { Message, AgentMode, Envelope } from '@/lib/types'
import { MessageContent } from './MessageContent'
import { EnvelopeRenderer } from './envelopes/EnvelopeRenderer'
import { ContentActions } from './ContentActions'
import { useChatStore } from '@/stores/useChatStore'

interface StructuredMessage {
  v: number
  text: string
  tier: string
  hash?: string
  envelopes?: Array<{ type: string; data: unknown }>
  tool_calls?: Array<{ id: string; name: string; status: string; has_envelope?: boolean }>
  flags: {
    truncated?: boolean
    has_error?: boolean
    provisional?: boolean
  }
}

function parseStructuredContent(content: string): { text: string; structured?: StructuredMessage } {
  try {
    const parsed = JSON.parse(content)
    if (parsed && typeof parsed === 'object' && parsed.v === 1) {
      return { text: parsed.text, structured: parsed as StructuredMessage }
    }
  } catch {
    // Not JSON — legacy raw text
  }
  return { text: content }
}

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

// Agent colors for multi-agent sessions — deterministic by agent_id
const AGENT_COLORS = [
  { border: 'ring-indigo-500', badge: 'bg-indigo-500/15 text-indigo-400' },
  { border: 'ring-emerald-500', badge: 'bg-emerald-500/15 text-emerald-400' },
  { border: 'ring-orange-500', badge: 'bg-orange-500/15 text-orange-400' },
  { border: 'ring-pink-500', badge: 'bg-pink-500/15 text-pink-400' },
  { border: 'ring-cyan-500', badge: 'bg-cyan-500/15 text-cyan-400' },
]

function agentColorIndex(agentId: string): number {
  let hash = 0
  for (let i = 0; i < agentId.length; i++) {
    hash = (hash * 31 + agentId.charCodeAt(i)) | 0
  }
  return Math.abs(hash) % AGENT_COLORS.length
}

interface ChatMessageProps {
  message: Message
  isBookmarked?: boolean
  onToggleBookmark?: (messageId: string) => void
  onSendMessage?: (content: string) => void
  agentName?: string
  isMultiAgent?: boolean
}

export function ChatMessage({ message, isBookmarked = false, onToggleBookmark, onSendMessage, agentName, isMultiAgent = false }: ChatMessageProps) {
  const [hovered, setHovered] = useState(false)
  const activeMode = useChatStore((s) => s.activeMode)

  // Parse structured message format (v=1) or fall through to legacy raw text.
  const { text: displayText, structured } = useMemo(
    () => parseStructuredContent(message.content),
    [message.content]
  )

  const isUser = message.role === 'user'
  const avatarStyle = isUser
    ? { bg: 'bg-zinc-800', text: 'text-zinc-400' }
    : MODE_AVATAR_STYLES[activeMode]

  // Parse envelope — from saved envelope field or from streaming content.
  const envelope = useMemo<Envelope | null>(() => {
    // Helper: merge an array of envelopes into one.
    const mergeEnvelopes = (arr: Envelope[]): Envelope => {
      const merged: Envelope = { kind: arr[0].kind, version: arr[0].version, type: arr[0].type }
      for (const env of arr) {
        if (env.proposals) merged.proposals = [...(merged.proposals ?? []), ...env.proposals]
        if (env.questions) merged.questions = [...(merged.questions ?? []), ...env.questions]
        if (env.approval && !merged.approval) merged.approval = env.approval
        if (env.status && !merged.status) merged.status = env.status
        if (env.data && !merged.data) { merged.data = env.data; merged.type = env.type }
      }
      return merged
    }

    // 1. Try the saved envelope field (set after message is persisted).
    if (message.envelope) {
      try {
        const raw = typeof message.envelope === 'string'
          ? JSON.parse(message.envelope)
          : message.envelope
        if (Array.isArray(raw) && raw.length > 0) return mergeEnvelopes(raw)
        if (raw && typeof raw === 'object' && !Array.isArray(raw)) return raw as Envelope
      } catch { /* ignore */ }
    }

    // 2. During streaming, extract from content (envelope field not set yet).
    if (message.content) {
      const pattern = /```(?:volon-envelope|conduit-envelope)\s*\n([\s\S]*?)```/g
      const envelopes: Envelope[] = []
      let match
      while ((match = pattern.exec(message.content)) !== null) {
        try {
          envelopes.push(JSON.parse(match[1].trim()))
        } catch { /* incomplete JSON during streaming — skip */ }
      }
      if (envelopes.length > 0) return mergeEnvelopes(envelopes)
    }

    return null
  }, [message.envelope, message.content])

  return (
    <div
      className={`flex gap-3 group ${isUser ? 'flex-row-reverse' : ''}`}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      data-message-id={message.id}
    >
      {/* Avatar */}
      <div
        className={`w-7 h-7 rounded-md flex items-center justify-center shrink-0 mt-0.5 ${avatarStyle.bg} ${avatarStyle.text} ${
          isMultiAgent && !isUser && message.agent_id
            ? `ring-2 ${AGENT_COLORS[agentColorIndex(message.agent_id)].border}`
            : ''
        }`}
      >
        {isUser ? <User className="w-4 h-4" /> : <Bot className="w-4 h-4" />}
      </div>

      {/* Content */}
      <div className={`flex-1 min-w-0 ${isUser ? 'flex flex-col items-end' : ''}`}>
        <div className="flex items-center gap-2 mb-1">
          <span className="text-xs font-medium text-zinc-500">
            {isUser ? 'You' : (agentName || 'Conduit')}
          </span>
          {isMultiAgent && !isUser && message.agent_id && (
            <span className={`text-xs px-1.5 py-0.5 rounded-full ${AGENT_COLORS[agentColorIndex(message.agent_id)].badge}`}>
              agent
            </span>
          )}
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
          {/* Persistent bookmark indicator */}
          {isBookmarked && !hovered && (
            <BookmarkCheck className="w-3.5 h-3.5 text-amber-500" />
          )}
        </div>
        <div
          className={`${
            isUser
              ? 'bg-zinc-800 rounded-2xl rounded-tr-sm px-4 py-2.5 max-w-[80%]'
              : 'max-w-full'
          }`}
        >
          <MessageContent content={displayText} role={message.role} />
        </div>

        {/* Truncation banner for structured messages */}
        {structured?.flags?.truncated && (
          <div className="mt-2 px-3 py-1.5 text-xs text-amber-400 border border-amber-700/50 rounded bg-amber-900/20">
            Response was cut short due to length limits
          </div>
        )}

        {/* Envelope rendering */}
        {envelope && !isUser && (
          <div className="mt-3">
            <EnvelopeRenderer envelope={envelope} onSendMessage={onSendMessage} />
          </div>
        )}

        {/* Actions — always rendered to avoid layout shift, opacity toggles on hover */}
        {!isUser && (
          <ContentActions
            content={displayText}
            messageId={message.id}
            isBookmarked={isBookmarked}
            onToggleBookmark={onToggleBookmark}
            visible={hovered}
            className="mt-1"
          />
        )}
      </div>
    </div>
  )
}
