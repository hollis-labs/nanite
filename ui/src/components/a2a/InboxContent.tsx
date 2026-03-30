import { useState, useEffect, useCallback } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Mail, Reply, Check, CheckCheck, Clock, AlertTriangle, ArrowRight, Send } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { ScrollArea } from '@/components/ui/scroll-area'
import { api } from '@/lib/api'
import type { A2AMessage, A2AMessageType } from '@/lib/types'

const TYPE_LABELS: Record<A2AMessageType, string> = {
  message: 'Message',
  help_request: 'Help Request',
  directive: 'Directive',
  status_update: 'Status',
  handoff: 'Handoff',
}

const TYPE_COLORS: Record<A2AMessageType, string> = {
  message: 'bg-surface text-fg-secondary',
  help_request: 'bg-amber-900/60 text-amber-300',
  directive: 'bg-accent-muted text-accent-hover',
  status_update: 'bg-success-muted text-success',
  handoff: 'bg-accent-muted text-accent-hover',
}

const STATUS_ICONS = {
  unread: Mail,
  read: Check,
  acknowledged: CheckCheck,
  resolved: CheckCheck,
}

interface Toast {
  id: number
  message: string
}

let toastId = 0

type InboxTab = 'user' | 'agent'

interface InboxContentProps {
  agentId: string
}

export function InboxContent({ agentId }: InboxContentProps) {
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

  const effectiveAgentId = activeTab === 'user' ? 'user' : agentId

  const { data: agents = [] } = useQuery({
    queryKey: ['agents'],
    queryFn: api.listAgents,
  })

  const agentNameMap = new Map<string, string>()
  agents.forEach((a) => {
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

  const { data: messages = [], isLoading } = useQuery({
    queryKey: ['a2a-inbox', effectiveAgentId, filter],
    queryFn: () => api.getA2AInbox(effectiveAgentId, filter || undefined),
    enabled: !!effectiveAgentId,
    refetchInterval: 30000,
  })

  const { data: threadMessages = [] } = useQuery({
    queryKey: ['a2a-thread', threadView],
    queryFn: () => api.getA2AThread(threadView!),
    enabled: !!threadView,
  })

  const ackMutation = useMutation({
    mutationFn: api.ackA2AMessage,
    onSuccess: () => {
      addToast('Marked as read')
      void queryClient.invalidateQueries({ queryKey: ['a2a-inbox'] })
      void queryClient.invalidateQueries({ queryKey: ['a2a-unread'] })
    },
  })

  const resolveMutation = useMutation({
    mutationFn: api.resolveA2AMessage,
    onSuccess: () => {
      addToast('Message resolved')
      void queryClient.invalidateQueries({ queryKey: ['a2a-inbox'] })
      void queryClient.invalidateQueries({ queryKey: ['a2a-unread'] })
    },
  })

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

  // Reset state when switching away
  useEffect(() => {
    return () => {
      setThreadView(null)
      setExpandedId(null)
      setReplyTo(null)
    }
  }, [])

  const displayMessages = threadView ? threadMessages : messages

  return (
    <>
      {/* Tab bar */}
      {!threadView && (
        <div className="flex border-b border-border shrink-0">
          <button
            onClick={() => handleTabSwitch('user')}
            className={`flex-1 px-4 py-2 text-xs font-medium transition-colors ${
              activeTab === 'user'
                ? 'text-success border-b-2 border-success'
                : 'text-fg-muted hover:text-fg-secondary border-b-2 border-transparent'
            }`}
          >
            My Inbox
          </button>
          <button
            onClick={() => handleTabSwitch('agent')}
            className={`flex-1 px-4 py-2 text-xs font-medium transition-colors ${
              activeTab === 'agent'
                ? 'text-success border-b-2 border-success'
                : 'text-fg-muted hover:text-fg-secondary border-b-2 border-transparent'
            }`}
          >
            Agent Inbox
          </button>
        </div>
      )}

      {/* Thread back button */}
      {threadView && (
        <div className="px-3 py-2 border-b border-border shrink-0">
          <button
            onClick={() => setThreadView(null)}
            className="text-xs text-fg-secondary hover:text-fg transition-colors"
          >
            &larr; Back to inbox
          </button>
        </div>
      )}

      {/* Filter bar */}
      {!threadView && (
        <div className="flex gap-1 px-4 py-2 border-b border-border/50 shrink-0">
          {['', 'unread', 'read', 'resolved'].map((s) => (
            <button
              key={s}
              onClick={() => setFilter(s)}
              className={`px-2 py-1 text-xs rounded transition-colors ${
                filter === s
                  ? 'bg-surface text-fg'
                  : 'text-fg-muted hover:text-fg-secondary'
              }`}
            >
              {s || 'All'}
            </button>
          ))}
        </div>
      )}

      {/* Messages */}
      <ScrollArea className="flex-1 min-h-0">
        {isLoading ? (
          <div className="divide-y divide-border/50">
            {Array.from({ length: 4 }).map((_, i) => (
              <div key={i} className="px-4 py-3">
                <div className="flex items-start justify-between gap-2">
                  <div className="flex items-center gap-2">
                    <Skeleton className="size-3.5 rounded-sm" />
                    <Skeleton className="h-3.5 w-20" />
                    <Skeleton className="h-3 w-3" />
                    <Skeleton className="h-3.5 w-16" />
                  </div>
                  <Skeleton className="h-4 w-14 rounded" />
                </div>
                <div className="ml-5 mt-2 space-y-1.5">
                  <Skeleton className="h-2.5 w-16" />
                  <Skeleton className="h-3 w-full" />
                  <Skeleton className="h-3 w-3/4" />
                </div>
              </div>
            ))}
          </div>
        ) : displayMessages.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-12 text-fg-muted text-sm gap-2">
            <Mail className="w-8 h-8 text-fg-faint" />
            <span>No messages</span>
          </div>
        ) : (
          <div className="divide-y divide-border/50">
            {displayMessages.map((msg) => {
              const expanded = expandedId === msg.id
              const StatusIcon = STATUS_ICONS[msg.status as keyof typeof STATUS_ICONS] || Mail
              const isUnread = msg.status === 'unread'

              return (
                <div
                  key={msg.id}
                  className={`px-4 py-3 transition-colors ${
                    isUnread ? 'bg-bg-elevated/80' : ''
                  }`}
                >
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
                            isUnread ? 'text-accent' : 'text-fg-faint'
                          }`}
                        />
                        <span className="text-sm font-medium text-fg truncate">
                          {getAgentName(msg.from_agent)}
                        </span>
                        <ArrowRight className="w-3 h-3 text-fg-faint shrink-0" />
                        <span className="text-sm text-fg-secondary truncate">
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
                      <p className="text-xs font-medium text-fg-secondary mt-1 ml-5">
                        {msg.subject}
                      </p>
                    )}

                    <div className="flex items-center gap-2 mt-1 ml-5">
                      <span className="text-[11px] text-fg-faint">
                        {formatTime(msg.created_at)}
                      </span>
                      {msg.priority === 1 && (
                        <AlertTriangle className="w-3 h-3 text-accent" />
                      )}
                      {msg.thread_id && msg.thread_id !== msg.id && !threadView && (
                        <button
                          className="text-[11px] text-accent hover:text-accent-hover"
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
                      <p className="text-xs text-fg-muted mt-1 ml-5 line-clamp-2">
                        {msg.body}
                      </p>
                    )}
                  </button>

                  {expanded && (
                    <div className="mt-2 ml-5">
                      <pre className="text-xs text-fg-secondary whitespace-pre-wrap font-sans leading-relaxed">
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

                      {replyTo === msg.id && (
                        <div className="mt-2 flex flex-col gap-2">
                          <textarea
                            className="w-full bg-bg-elevated border border-border-subtle rounded px-3 py-2 text-xs text-fg resize-none focus:outline-none focus:border-accent"
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
        <div className="absolute bottom-4 right-4 z-10 flex flex-col gap-2">
          {toasts.map((toast) => (
            <div
              key={toast.id}
              className="px-4 py-3 bg-surface border border-border-subtle rounded-sm shadow-xl text-sm text-fg max-w-sm animate-in fade-in slide-in-from-bottom-2"
            >
              {toast.message}
            </div>
          ))}
        </div>
      )}
    </>
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
