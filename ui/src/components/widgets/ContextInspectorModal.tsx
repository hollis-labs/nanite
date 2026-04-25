import { useState, useCallback, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ChevronDown, ChevronRight, Copy, Check, Cpu, MessageSquare, Wrench, BarChart3 } from 'lucide-react'
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription
} from '@/components/ui/dialog'
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
  if (pct < 50) return 'text-success'
  if (pct < 75) return 'text-warning'
  return 'text-danger'
}

function getBarColor(pct: number): string {
  if (pct < 50) return 'bg-success'
  if (pct < 75) return 'bg-warning'
  return 'bg-danger'
}

function getBadgeColor(pct: number): string {
  if (pct < 50) return 'bg-success-muted text-success border-transparent'
  if (pct < 75) return 'bg-warning-muted text-warning border-transparent'
  return 'bg-danger-muted text-danger border-transparent'
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
      className="p-1 rounded hover:bg-surface-hover transition-colors shrink-0"
      title="Copy to clipboard"
    >
      {copied ? (
        <Check className="w-3 h-3 text-success" />
      ) : (
        <Copy className="w-3 h-3 text-fg-muted" />
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
    <div className="border border-border-subtle rounded-[8px] overflow-hidden">
      <button
        onClick={() => setOpen((o) => !o)}
        className="flex items-center gap-2 w-full px-3 py-2.5 hover:bg-surface transition-colors text-left"
      >
        {open ? (
          <ChevronDown className="w-3.5 h-3.5 text-fg-muted shrink-0" />
        ) : (
          <ChevronRight className="w-3.5 h-3.5 text-fg-muted shrink-0" />
        )}
        <span className="shrink-0">{icon}</span>
        <span className="text-sm text-fg flex-1">{title}</span>
        <span className={`text-xs font-mono px-1.5 py-0.5 rounded border ${getBadgeColor(pct)}`}>
          {formatTokens(tokens)}
        </span>
      </button>
      {open && (
        <div className="px-3 pb-3 pt-1 border-t border-divider">
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
    user: 'text-info',
    assistant: 'text-brand',
    system: 'text-warning',
    tool: 'text-success',
  }

  return (
    <div className="flex items-start gap-2 py-1.5 border-b border-divider last:border-b-0">
      <span className={`text-xs font-mono w-16 shrink-0 pt-0.5 ${roleColors[msg.role] ?? 'text-fg-secondary'}`}>
        {msg.role}
      </span>
      <span className="text-xs text-fg-secondary flex-1 break-all line-clamp-2">
        {msg.is_compacted ? '[compacted]' : msg.content_preview}
      </span>
      <span className="text-xs font-mono text-fg-muted shrink-0 pt-0.5">
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
    <div className="flex items-center gap-2 py-1.5 border-b border-divider last:border-b-0">
      <span className="text-xs text-fg-secondary flex-1 font-mono truncate">
        {tool.name}
      </span>
      <span className="text-xs font-mono text-fg-muted shrink-0">
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

  const breakdown: ContextBreakdown | undefined = data
  const pct = breakdown ? Math.min((breakdown.total / breakdown.ceiling) * 100, 100) : 0

  return (
    <Dialog open={open} onOpenChange={() => onClose()}>
      <DialogContent className="sm:max-w-2xl max-h-[80vh] flex flex-col">
        <DialogHeader className="px-4 pt-4 pb-3">
          <DialogTitle>Context Inspector</DialogTitle>
          <DialogDescription className="sr-only">Token usage breakdown for this session</DialogDescription>
        </DialogHeader>

        {/* Body */}
        <div className="flex-1 overflow-y-auto px-4 py-3 space-y-3">
          {isLoading && (
            <div className="text-sm text-fg-muted text-center py-8">Loading breakdown...</div>
          )}

          {error && (
            <div className="text-sm text-danger text-center py-8">
              Failed to load context breakdown.
            </div>
          )}

          {breakdown && (
            <>
              {/* Overall progress bar */}
              <div className="space-y-1.5">
                <div className="flex justify-between text-xs">
                  <span className="text-fg-secondary">Total context usage</span>
                  <span className={getPctColor(pct)}>
                    {formatTokens(breakdown.total)} / {formatTokens(breakdown.ceiling)} ({Math.round(pct)}%)
                  </span>
                </div>
                <div className="w-full bg-surface rounded-[3px] h-[4px]">
                  <div
                    className={`${getBarColor(pct)} h-full rounded-[3px] transition-[width] duration-500`}
                    style={{ width: `${Math.max(pct, 1)}%` }}
                  />
                </div>
              </div>

              {/* System Prompt */}
              <AccordionSection
                title="System Prompt"
                icon={<Cpu className="w-3.5 h-3.5 text-warning" />}
                tokens={breakdown.system_prompt_tokens}
                totalCeiling={breakdown.ceiling}
              >
                <div className="space-y-2">
                  {breakdown.system_prompt_preview ? (
                    <>
                      <div className="max-h-40 overflow-y-auto">
                        <pre className="text-xs text-fg-secondary whitespace-pre-wrap break-words font-mono leading-relaxed">
                          {breakdown.system_prompt_preview}
                        </pre>
                      </div>
                      <div className="flex items-center justify-between pt-1 border-t border-divider">
                        <span className="text-xs text-fg-muted">
                          {breakdown.system_prompt_tokens} tokens
                          {breakdown.system_prompt_preview.endsWith('...') && ' (preview truncated)'}
                        </span>
                        <CopyButton text={breakdown.system_prompt_preview} />
                      </div>
                    </>
                  ) : (
                    <div className="flex items-center justify-between">
                      <span className="text-xs text-fg-muted">
                        {breakdown.system_prompt_tokens} tokens (no preview available)
                      </span>
                    </div>
                  )}
                </div>
              </AccordionSection>

              {/* Messages */}
              <AccordionSection
                title={`Messages (${breakdown.messages.length})`}
                icon={<MessageSquare className="w-3.5 h-3.5 text-info" />}
                tokens={breakdown.message_tokens_total}
                totalCeiling={breakdown.ceiling}
                defaultOpen={true}
              >
                <div className="max-h-48 overflow-y-auto">
                  {breakdown.messages.length === 0 ? (
                    <span className="text-xs text-fg-faint">No messages yet</span>
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
                icon={<Wrench className="w-3.5 h-3.5 text-success" />}
                tokens={breakdown.tool_tokens_total}
                totalCeiling={breakdown.ceiling}
              >
                <div className="space-y-1">
                  <div className="text-xs text-fg-muted pb-1">
                    {breakdown.tools_available} tools available
                  </div>
                  <div className="max-h-48 overflow-y-auto">
                    {breakdown.tools.length === 0 ? (
                      <span className="text-xs text-fg-faint">No tool calls in this session</span>
                    ) : (
                      breakdown.tools.map((tool, i) => (
                        <ToolRow key={`${tool.name}-${i}`} tool={tool} />
                      ))
                    )}
                  </div>
                </div>
              </AccordionSection>

              {/* Summary */}
              <div className="border border-border-subtle rounded-[8px] px-3 py-2.5 space-y-1.5">
                <div className="flex items-center gap-2 mb-1">
                  <BarChart3 className="w-3.5 h-3.5 text-fg-secondary" />
                  <span className="text-sm font-medium text-fg">Summary</span>
                </div>
                <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs">
                  <span className="text-fg-muted">System prompt</span>
                  <span className="text-fg-secondary text-right font-mono">{formatTokens(breakdown.system_prompt_tokens)}</span>

                  <span className="text-fg-muted">Messages</span>
                  <span className="text-fg-secondary text-right font-mono">{formatTokens(breakdown.message_tokens_total)}</span>

                  <span className="text-fg-muted">Tool calls</span>
                  <span className="text-fg-secondary text-right font-mono">{formatTokens(breakdown.tool_tokens_total)}</span>

                  <span className="text-fg-muted font-medium pt-1 border-t border-divider">Total</span>
                  <span className={`text-right font-mono font-medium pt-1 border-t border-divider ${getPctColor(pct)}`}>
                    {formatTokens(breakdown.total)}
                  </span>

                  <span className="text-fg-muted">Ceiling</span>
                  <span className="text-fg-secondary text-right font-mono">{formatTokens(breakdown.ceiling)}</span>

                  <span className="text-fg-muted">Estimated cost</span>
                  <span className="text-fg-secondary text-right font-mono">{formatCost(breakdown.estimated_cost_usd)}</span>
                </div>
              </div>
            </>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
