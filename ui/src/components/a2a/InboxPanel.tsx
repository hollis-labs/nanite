import { useState, useEffect, useCallback } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { X, Mail, Reply, Check, CheckCheck, Clock, AlertTriangle, ArrowRight, Send } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { ScrollArea } from '@/components/ui/ScrollArea'
import { api } from '@/lib/api'
import type { A2AMessage, A2AMessageType, Agent } from '@/lib/types'

const TYPE_LABELS: Record<A2AMessageType, string> = {
  message: 'Message',
  help_request: 'Help Request',
  directive: 'Directive',
  status_update: 'Status',
  handoff: 'Handoff',
}

const TYPE_COLORS: Record<A2AMessageType, string> = {
  message: 'bg-zinc-700 text-zinc-300',
  help_request: 'bg-amber-900/60 text-amber-300',
  directive: 'bg-indigo-900/60 text-indigo-300',
  status_update: 'bg-emerald-900/60 text-emerald-300',
  handoff: 'bg-purple-900/60 text-purple-300',
}

const STATUS_ICONS = {
  unread: Mail,
  read: Check,
  acknowledged: CheckCheck,
  resolved: CheckCheck,
}

// --- Toast notification ---

interface Toast {
  id: number
  message: string
}

let toastId = 0

type InboxTab = 'user' | 'agent'

interface InboxPanelProps {
  agentId: string
  open: boolean
  onClose: () => void
}

export function InboxPanel({ agentId, open, onClose }: InboxPanelProps) {
  const [activeTab, setActiveTab] = useState<InboxTab>('user')
  const [filter, setFilter] = useState<string>('')
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const [threadView, setThreadView] = useState<string | null>(null)
  const [replyTo, setReplyTo] = useState<string | null>(null)
  const [replyBody, setReplyBody] = useState('')
  const [toasts, setToasts] = useState<Toast[]>([])
  const queryClient = useQueryClient()

  const addToast = useCallback((message: string) => {
    const id = ++toastId
    setToasts((prev) => [...prev, { id, message }])
    setTimeout(() => {
      setToasts((prev) => prev.filter((t) => t.id !== id))
    }, 3000)
  }, [])

  // The effective agent ID depends on which tab is active
  const effectiveAgentId = activeTab === 'user' ? 'user' : agentId

  // Fetch agents for name lookup
  const { data: agents = [] } = useQuery({
    queryKey: ['agents'],
    queryFn: api.listAgents,
  })

  const agentNameMap = new Map<string, string>()
  agents.forEach((a: Agent) => {
    agentNameMap.set(a.id, a.name)
    agentNameMap.set(a.slug, a.name)
  })

  const getAgentName = useCallback(
    (id: string) => {
      if (id === 'user') return 'You'
      return agentNameMap.get(id) || id
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [agents],
  )

  // Inbox query
  const { data: messages = [], isLoading } = useQuery({
    queryKey: ['a2a-inbox', effectiveAgentId, filter],
    queryFn: () => api.getA2AInbox(effectiveAgentId, filter || undefined),
    enabled: open && !!effectiveAgentId,
    refetchInterval: 30000,
  })

  // Thread query
  const { data: threadMessages = [] } = useQuery({
    queryKey: ['a2a-thread', threadView],
    queryFn: () => api.getA2AThread(threadView!),
    enabled: !!threadView,
  })

  // Ack mutation
  const ackMutation = useMutation({
    mutationFn: api.ackA2AMessage,
    onSuccess: () => {
      addToast('Marked as read')
      void queryClient.invalidateQueries({ queryKey: ['a2a-inbox'] })
      void queryClient.invalidateQueries({ queryKey: ['a2a-unread'] })
    },
  })

  // Resolve mutation
  const resolveMutation = useMutation({
    mutationFn: api.resolveA2AMessage,
    onSuccess: () => {
      addToast('Message resolved')
      void queryClient.invalidateQueries({ queryKey: ['a2a-inbox'] })
      void queryClient.invalidateQueries({ queryKey: ['a2a-unread'] })
    },
  })

  // Send reply mutation
  const sendMutation = useMutation({
    mutationFn: api.sendA2AMessage,
    onSuccess: () => {
      setReplyBody('')
      setReplyTo(null)
      addToast('Message sent')
      void queryClient.invalidateQueries({ queryKey: ['a2a-inbox'] })
      void queryClient.invalidateQueries({ queryKey: ['a2a-thread'] })
    },
  })

  // Close thread view when panel closes
  useEffect(() => {
    if (!open) {
      setThreadView(null)
      setExpandedId(null)
      setReplyTo(null)
    }
  }, [open])

  const handleTabSwitch = (tab: InboxTab) => {
    setActiveTab(tab)
    setFilter('')
    setExpandedId(null)
    setThreadView(null)
    setReplyTo(null)
  }

  const handleReply = (msg: A2AMessage) => {
    setReplyTo(msg.id)
    setReplyBody('')
  }

  const submitReply = (msg: A2AMessage) => {
    if (!replyBody.trim()) return
    const fromAgent = activeTab === 'user' ? 'user' : agentId
    sendMutation.mutate({
      from_agent: fromAgent,
      to_agent: msg.from_agent,
      body: replyBody.trim(),
      subject: msg.subject ? `Re: ${msg.subject}` : undefined,
      thread_id: msg.thread_id || msg.id,
      reply_to: msg.id,
      type: 'message',
    })
  }

  if (!open) return null

  const displayMessages = threadView ? threadMessages : messages

  return (
    <div className="fixed inset-y-0 right-0 w-[420px] bg-zinc-950 border-l border-zinc-800 z-50 flex flex-col shadow-2xl">
      {/* Header */}
      <div className="flex items-center justify-between px-4 py-3 border-b border-zinc-800">
        <div className="flex items-center gap-2">
          <Mail className="w-4 h-4 text-zinc-400" />
          <h2 className="text-sm font-semibold text-zinc-100">
            {threadView ? 'Thread' : 'Inbox'}
          </h2>
          {!threadView && messages.length > 0 && (
            <span className="text-xs text-zinc-500">({messages.length})</span>
          )}
        </div>
        <div className="flex items-center gap-1">
          {threadView && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setThreadView(null)}
              className="text-xs text-zinc-400"
            >
              Back to inbox
            </Button>
          )}
          <Button variant="ghost" size="icon" onClick={onClose}>
            <X className="w-4 h-4" />
          </Button>
        </div>
      </div>

      {/* Tab bar */}
      {!threadView && (
        <div className="flex border-b border-zinc-800">
          <button
            onClick={() => handleTabSwitch('user')}
            className={`flex-1 px-4 py-2 text-xs font-medium transition-colors ${
              activeTab === 'user'
                ? 'text-blue-400 border-b-2 border-blue-500'
                : 'text-zinc-500 hover:text-zinc-300 border-b-2 border-transparent'
            }`}
          >
            My Inbox
          </button>
          <button
            onClick={() => handleTabSwitch('agent')}
            className={`flex-1 px-4 py-2 text-xs font-medium transition-colors ${
              activeTab === 'agent'
                ? 'text-blue-400 border-b-2 border-blue-500'
                : 'text-zinc-500 hover:text-zinc-300 border-b-2 border-transparent'
            }`}
          >
            Agent Inbox
          </button>
        </div>
      )}

      {/* Filter bar (inbox only) */}
      {!threadView && (
        <div className="flex gap-1 px-4 py-2 border-b border-zinc-800/50">
          {['', 'unread', 'read', 'resolved'].map((s) => (
            <button
              key={s}
              onClick={() => setFilter(s)}
              className={`px-2 py-1 text-xs rounded transition-colors ${
                filter === s
                  ? 'bg-zinc-800 text-zinc-100'
                  : 'text-zinc-500 hover:text-zinc-300'
              }`}
            >
              {s || 'All'}
            </button>
          ))}
        </div>
      )}

      {/* Messages */}
      <ScrollArea className="flex-1">
        {isLoading ? (
          <div className="flex items-center justify-center py-12 text-zinc-500 text-sm">
            Loading...
          </div>
        ) : displayMessages.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-12 text-zinc-500 text-sm gap-2">
            <Mail className="w-8 h-8 text-zinc-700" />
            <span>No messages</span>
          </div>
        ) : (
          <div className="divide-y divide-zinc-800/50">
            {displayMessages.map((msg) => {
              const expanded = expandedId === msg.id
              const StatusIcon = STATUS_ICONS[msg.status as keyof typeof STATUS_ICONS] || Mail
              const isUnread = msg.status === 'unread'

              return (
                <div
                  key={msg.id}
                  className={`px-4 py-3 transition-colors ${
                    isUnread ? 'bg-zinc-900/80' : ''
                  }`}
                >
                  {/* Message header */}
                  <button
                    className="w-full text-left"
                    onClick={() => {
                      setExpandedId(expanded ? null : msg.id)
                      if (isUnread) ackMutation.mutate(msg.id)
                    }}
                  >
                    <div className="flex items-start justify-between gap-2">
                      <div className="flex items-center gap-2 min-w-0">
                        <StatusIcon
                          className={`w-3.5 h-3.5 shrink-0 ${
                            isUnread ? 'text-indigo-400' : 'text-zinc-600'
                          }`}
                        />
                        <span className="text-sm font-medium text-zinc-200 truncate">
                          {getAgentName(msg.from_agent)}
                        </span>
                        <ArrowRight className="w-3 h-3 text-zinc-600 shrink-0" />
                        <span className="text-sm text-zinc-400 truncate">
                          {getAgentName(msg.to_agent)}
                        </span>
                      </div>
                      <span
                        className={`text-[10px] px-1.5 py-0.5 rounded shrink-0 ${
                          TYPE_COLORS[msg.type as A2AMessageType] || TYPE_COLORS.message
                        }`}
                      >
                        {TYPE_LABELS[msg.type as A2AMessageType] || msg.type}
                      </span>
                    </div>

                    {msg.subject && (
                      <p className="text-xs font-medium text-zinc-300 mt-1 ml-5">
                        {msg.subject}
                      </p>
                    )}

                    <div className="flex items-center gap-2 mt-1 ml-5">
                      <span className="text-[11px] text-zinc-600">
                        {formatTime(msg.created_at)}
                      </span>
                      {msg.priority === 1 && (
                        <AlertTriangle className="w-3 h-3 text-red-400" />
                      )}
                      {msg.thread_id && msg.thread_id !== msg.id && !threadView && (
                        <button
                          className="text-[11px] text-indigo-400 hover:text-indigo-300"
                          onClick={(e) => {
                            e.stopPropagation()
                            setThreadView(msg.thread_id)
                          }}
                        >
                          View thread
                        </button>
                      )}
                    </div>

                    {!expanded && (
                      <p className="text-xs text-zinc-500 mt-1 ml-5 line-clamp-2">
                        {msg.body}
                      </p>
                    )}
                  </button>

                  {/* Expanded body */}
                  {expanded && (
                    <div className="mt-2 ml-5">
                      <pre className="text-xs text-zinc-300 whitespace-pre-wrap font-sans leading-relaxed">
                        {msg.body}
                      </pre>

                      <div className="flex items-center gap-2 mt-3">
                        <Button
                          variant="ghost"
                          size="sm"
                          className="text-xs gap-1"
                          onClick={() => handleReply(msg)}
                        >
                          <Reply className="w-3 h-3" /> Reply
                        </Button>
                        {msg.status !== 'resolved' && (
                          <Button
                            variant="ghost"
                            size="sm"
                            className="text-xs gap-1"
                            onClick={() => resolveMutation.mutate(msg.id)}
                          >
                            <CheckCheck className="w-3 h-3" /> Resolve
                          </Button>
                        )}
                        {msg.thread_id && !threadView && (
                          <Button
                            variant="ghost"
                            size="sm"
                            className="text-xs gap-1"
                            onClick={() => setThreadView(msg.thread_id)}
                          >
                            <Clock className="w-3 h-3" /> Thread
                          </Button>
                        )}
                      </div>

                      {/* Inline reply */}
                      {replyTo === msg.id && (
                        <div className="mt-2 flex flex-col gap-2">
                          <textarea
                            className="w-full bg-zinc-900 border border-zinc-700 rounded px-3 py-2 text-xs text-zinc-200 resize-none focus:outline-none focus:border-indigo-500"
                            rows={3}
                            placeholder="Type your reply..."
                            value={replyBody}
                            onChange={(e) => setReplyBody(e.target.value)}
                            onKeyDown={(e) => {
                              if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
                                submitReply(msg)
                              }
                            }}
                          />
                          <div className="flex items-center gap-2">
                            <Button
                              variant="secondary"
                              size="sm"
                              className="text-xs gap-1"
                              disabled={!replyBody.trim() || sendMutation.isPending}
                              onClick={() => submitReply(msg)}
                            >
                              <Send className="w-3 h-3" /> Send
                            </Button>
                            <Button
                              variant="ghost"
                              size="sm"
                              className="text-xs"
                              onClick={() => setReplyTo(null)}
                            >
                              Cancel
                            </Button>
                          </div>
                        </div>
                      )}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </ScrollArea>

      {/* Toast container */}
      {toasts.length > 0 && (
        <div className="fixed bottom-4 right-4 z-[60] flex flex-col gap-2">
          {toasts.map((toast) => (
            <div
              key={toast.id}
              className="px-4 py-3 bg-zinc-800 border border-zinc-700 rounded-lg shadow-xl text-sm text-zinc-200 max-w-sm animate-in fade-in slide-in-from-bottom-2"
            >
              {toast.message}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function formatTime(iso: string): string {
  try {
    const d = new Date(iso)
    const now = new Date()
    const diff = now.getTime() - d.getTime()

    if (diff < 60000) return 'just now'
    if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`
    if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`

    return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
  } catch {
    return iso
  }
}
