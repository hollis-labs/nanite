import { useState, useCallback, useEffect, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { X, ChevronDown, ChevronRight, Copy, Check, Cpu, MessageSquare, Wrench, BarChart3 } from 'lucide-react'
import { api } from '@/lib/api'
import type { ContextBreakdown, MessageTokenDetail, ToolTokenDetail } from '@/lib/types'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatTokens(n: number): string {
  if (n >= 1000) return `${Math.round(n / 1000)}K`
  return String(n)
}

function formatCost(usd: number): string {
  if (usd < 0.01) return '<$0.01'
  return `$${usd.toFixed(2)}`
}

function getPctColor(pct: number): string {
  if (pct < 50) return 'text-green-400'
  if (pct < 75) return 'text-amber-400'
  return 'text-red-400'
}

function getBarColor(pct: number): string {
  if (pct < 50) return 'bg-green-500'
  if (pct < 75) return 'bg-amber-500'
  return 'bg-red-500'
}

function getBadgeColor(pct: number): string {
  if (pct < 50) return 'bg-green-500/15 text-green-400 border-green-500/30'
  if (pct < 75) return 'bg-amber-500/15 text-amber-400 border-amber-500/30'
  return 'bg-red-500/15 text-red-400 border-red-500/30'
}

// ---------------------------------------------------------------------------
// CopyButton
// ---------------------------------------------------------------------------

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false)

  const handleCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      // clipboard not available
    }
  }, [text])

  return (
    <button
      onClick={(e) => { e.stopPropagation(); handleCopy() }}
      className="p-1 rounded hover:bg-zinc-700 transition-colors shrink-0"
      title="Copy to clipboard"
    >
      {copied ? (
        <Check className="w-3 h-3 text-green-400" />
      ) : (
        <Copy className="w-3 h-3 text-zinc-500" />
      )}
    </button>
  )
}

// ---------------------------------------------------------------------------
// AccordionSection
// ---------------------------------------------------------------------------

function AccordionSection({
  title,
  icon,
  tokens,
  totalCeiling,
  children,
  defaultOpen = false,
}: {
  title: string
  icon: ReactNode
  tokens: number
  totalCeiling: number
  children: ReactNode
  defaultOpen?: boolean
}) {
  const [open, setOpen] = useState(defaultOpen)
  const pct = totalCeiling > 0 ? (tokens / totalCeiling) * 100 : 0

  return (
    <div className="border border-zinc-800 rounded-lg overflow-hidden">
      <button
        onClick={() => setOpen((o) => !o)}
        className="flex items-center gap-2 w-full px-3 py-2.5 hover:bg-zinc-800/50 transition-colors text-left"
      >
        {open ? (
          <ChevronDown className="w-3.5 h-3.5 text-zinc-500 shrink-0" />
        ) : (
          <ChevronRight className="w-3.5 h-3.5 text-zinc-500 shrink-0" />
        )}
        <span className="shrink-0">{icon}</span>
        <span className="text-sm text-zinc-200 flex-1">{title}</span>
        <span className={`text-xs font-mono px-1.5 py-0.5 rounded border ${getBadgeColor(pct)}`}>
          {formatTokens(tokens)}
        </span>
      </button>
      {open && (
        <div className="px-3 pb-3 pt-1 border-t border-zinc-800/50">
          {children}
        </div>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// MessageRow
// ---------------------------------------------------------------------------

function MessageRow({ msg }: { msg: MessageTokenDetail }) {
  const roleColors: Record<string, string> = {
    user: 'text-blue-400',
    assistant: 'text-accent',
    system: 'text-amber-400',
    tool: 'text-green-400',
  }

  return (
    <div className="flex items-start gap-2 py-1.5 border-b border-zinc-800/50 last:border-b-0">
      <span className={`text-xs font-mono w-16 shrink-0 pt-0.5 ${roleColors[msg.role] ?? 'text-zinc-400'}`}>
        {msg.role}
      </span>
      <span className="text-xs text-zinc-400 flex-1 break-all line-clamp-2">
        {msg.is_compacted ? '[compacted]' : msg.content_preview}
      </span>
      <span className="text-xs font-mono text-zinc-500 shrink-0 pt-0.5">
        {formatTokens(msg.tokens)}
      </span>
      <CopyButton text={msg.content_preview} />
    </div>
  )
}

// ---------------------------------------------------------------------------
// ToolRow
// ---------------------------------------------------------------------------

function ToolRow({ tool }: { tool: ToolTokenDetail }) {
  return (
    <div className="flex items-center gap-2 py-1.5 border-b border-zinc-800/50 last:border-b-0">
      <span className="text-xs text-zinc-300 flex-1 font-mono truncate">
        {tool.name}
      </span>
      <span className="text-xs font-mono text-zinc-500 shrink-0">
        {formatTokens(tool.tokens)}
      </span>
      <CopyButton text={tool.name} />
    </div>
  )
}

// ---------------------------------------------------------------------------
// ContextInspectorModal
// ---------------------------------------------------------------------------

interface ContextInspectorModalProps {
  sessionId: string
  open: boolean
  onClose: () => void
}

export function ContextInspectorModal({ sessionId, open, onClose }: ContextInspectorModalProps) {
  const { data, isLoading, error } = useQuery({
    queryKey: ['context-breakdown', sessionId],
    queryFn: () => api.getContextBreakdown(sessionId),
    enabled: open && !!sessionId,
    staleTime: 10_000,
  })

  // Close on Escape key
  useEffect(() => {
    if (!open) return
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [open, onClose])

  if (!open) return null

  const breakdown: ContextBreakdown | undefined = data
  const pct = breakdown ? Math.min((breakdown.total / breakdown.ceiling) * 100, 100) : 0

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      {/* Backdrop */}
      <div
        className="absolute inset-0 bg-black/60 backdrop-blur-sm"
        onClick={onClose}
      />

      {/* Modal */}
      <div className="relative bg-zinc-900 border border-zinc-700 rounded-xl shadow-2xl w-full max-w-lg max-h-[80vh] flex flex-col mx-4">
        {/* Header */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-zinc-800">
          <h2 className="text-sm font-semibold text-zinc-100">Context Inspector</h2>
          <button
            onClick={onClose}
            className="p-1 rounded hover:bg-zinc-800 transition-colors"
          >
            <X className="w-4 h-4 text-zinc-400" />
          </button>
        </div>

        {/* Body */}
        <div className="flex-1 overflow-y-auto px-4 py-3 space-y-3">
          {isLoading && (
            <div className="text-sm text-zinc-500 text-center py-8">Loading breakdown...</div>
          )}

          {error && (
            <div className="text-sm text-red-400 text-center py-8">
              Failed to load context breakdown.
            </div>
          )}

          {breakdown && (
            <>
              {/* Overall progress bar */}
              <div className="space-y-1.5">
                <div className="flex justify-between text-xs">
                  <span className="text-zinc-400">Total context usage</span>
                  <span className={getPctColor(pct)}>
                    {formatTokens(breakdown.total)} / {formatTokens(breakdown.ceiling)} ({Math.round(pct)}%)
                  </span>
                </div>
                <div className="w-full bg-zinc-800 rounded-full h-2">
                  <div
                    className={`${getBarColor(pct)} h-2 rounded-full transition-all duration-500`}
                    style={{ width: `${Math.max(pct, 1)}%` }}
                  />
                </div>
              </div>

              {/* System Prompt */}
              <AccordionSection
                title="System Prompt"
                icon={<Cpu className="w-3.5 h-3.5 text-amber-400" />}
                tokens={breakdown.system_prompt_tokens}
                totalCeiling={breakdown.ceiling}
              >
                <div className="space-y-2">
                  {breakdown.system_prompt_preview ? (
                    <>
                      <div className="max-h-40 overflow-y-auto">
                        <pre className="text-xs text-zinc-400 whitespace-pre-wrap break-words font-mono leading-relaxed">
                          {breakdown.system_prompt_preview}
                        </pre>
                      </div>
                      <div className="flex items-center justify-between pt-1 border-t border-zinc-800/50">
                        <span className="text-xs text-zinc-500">
                          {breakdown.system_prompt_tokens} tokens
                          {breakdown.system_prompt_preview.endsWith('...') && ' (preview truncated)'}
                        </span>
                        <CopyButton text={breakdown.system_prompt_preview} />
                      </div>
                    </>
                  ) : (
                    <div className="flex items-center justify-between">
                      <span className="text-xs text-zinc-500">
                        {breakdown.system_prompt_tokens} tokens (no preview available)
                      </span>
                    </div>
                  )}
                </div>
              </AccordionSection>

              {/* Messages */}
              <AccordionSection
                title={`Messages (${breakdown.messages.length})`}
                icon={<MessageSquare className="w-3.5 h-3.5 text-blue-400" />}
                tokens={breakdown.message_tokens_total}
                totalCeiling={breakdown.ceiling}
                defaultOpen={true}
              >
                <div className="max-h-48 overflow-y-auto">
                  {breakdown.messages.length === 0 ? (
                    <span className="text-xs text-zinc-600">No messages yet</span>
                  ) : (
                    breakdown.messages.map((msg) => (
                      <MessageRow key={msg.id} msg={msg} />
                    ))
                  )}
                </div>
              </AccordionSection>

              {/* Tool Calls */}
              <AccordionSection
                title={`Tool Calls (${breakdown.tools.length})`}
                icon={<Wrench className="w-3.5 h-3.5 text-green-400" />}
                tokens={breakdown.tool_tokens_total}
                totalCeiling={breakdown.ceiling}
              >
                <div className="space-y-1">
                  <div className="text-xs text-zinc-500 pb-1">
                    {breakdown.tools_available} tools available
                  </div>
                  <div className="max-h-48 overflow-y-auto">
                    {breakdown.tools.length === 0 ? (
                      <span className="text-xs text-zinc-600">No tool calls in this session</span>
                    ) : (
                      breakdown.tools.map((tool, i) => (
                        <ToolRow key={`${tool.name}-${i}`} tool={tool} />
                      ))
                    )}
                  </div>
                </div>
              </AccordionSection>

              {/* Summary */}
              <div className="border border-zinc-800 rounded-lg px-3 py-2.5 space-y-1.5">
                <div className="flex items-center gap-2 mb-1">
                  <BarChart3 className="w-3.5 h-3.5 text-zinc-400" />
                  <span className="text-sm font-medium text-zinc-200">Summary</span>
                </div>
                <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs">
                  <span className="text-zinc-500">System prompt</span>
                  <span className="text-zinc-300 text-right font-mono">{formatTokens(breakdown.system_prompt_tokens)}</span>

                  <span className="text-zinc-500">Messages</span>
                  <span className="text-zinc-300 text-right font-mono">{formatTokens(breakdown.message_tokens_total)}</span>

                  <span className="text-zinc-500">Tool calls</span>
                  <span className="text-zinc-300 text-right font-mono">{formatTokens(breakdown.tool_tokens_total)}</span>

                  <span className="text-zinc-500 font-medium pt-1 border-t border-zinc-800">Total</span>
                  <span className={`text-right font-mono font-medium pt-1 border-t border-zinc-800 ${getPctColor(pct)}`}>
                    {formatTokens(breakdown.total)}
                  </span>

                  <span className="text-zinc-500">Ceiling</span>
                  <span className="text-zinc-300 text-right font-mono">{formatTokens(breakdown.ceiling)}</span>

                  <span className="text-zinc-500">Estimated cost</span>
                  <span className="text-zinc-300 text-right font-mono">{formatCost(breakdown.estimated_cost_usd)}</span>
                </div>
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
