import { useCallback, useEffect, useRef, useState } from 'react'
import { Shield, ShieldCheck, ShieldX, Clock } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Tooltip } from '@/components/ui/tooltip'
import { api } from '@/lib/api'
import type { ApprovalDecision, ApprovalScope, PendingApproval } from '@/lib/types'
import { useAppStore } from '@/stores/useAppStore'
import { useChatStore } from '@/stores/useChatStore'

const AUTO_DENY_SECONDS = 60

interface ApprovalCardProps {
  approval: PendingApproval
}

export function ApprovalCard({ approval }: ApprovalCardProps) {
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const resolvePendingApproval = useChatStore((s) => s.resolvePendingApproval)
  const [submitting, setSubmitting] = useState(false)
  const [secondsLeft, setSecondsLeft] = useState(() => {
    const elapsed = Math.floor((Date.now() - approval.receivedAt) / 1000)
    return Math.max(0, AUTO_DENY_SECONDS - elapsed)
  })
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null)

  const handleDecision = useCallback(async (
    decision: ApprovalDecision,
    scope?: ApprovalScope,
  ) => {
    if (!activeSessionId || submitting || approval.resolved) return
    setSubmitting(true)
    try {
      await api.respondToApproval(activeSessionId, approval.request_id, decision, scope)
      resolvePendingApproval(approval.request_id, { decision, scope })
    } catch (err) {
      console.error('[ApprovalCard] Failed to respond:', err)
    } finally {
      setSubmitting(false)
    }
  }, [activeSessionId, approval.request_id, approval.resolved, submitting, resolvePendingApproval])

  // Countdown timer
  useEffect(() => {
    if (approval.resolved) return

    timerRef.current = setInterval(() => {
      setSecondsLeft((prev) => {
        if (prev <= 1) {
          if (timerRef.current) clearInterval(timerRef.current)
          return 0
        }
        return prev - 1
      })
    }, 1000)

    return () => {
      if (timerRef.current) clearInterval(timerRef.current)
    }
  }, [approval.resolved])

  // Auto-deny on timeout — fires once via ref guard to prevent repeated calls
  const autoDeniedRef = useRef(false)
  useEffect(() => {
    if (secondsLeft === 0 && !approval.resolved && !autoDeniedRef.current) {
      autoDeniedRef.current = true
      void handleDecision('deny')
    }
  }, [secondsLeft, approval.resolved, handleDecision])

  // Summarize tool input (first 2 keys, truncated)
  const inputSummary = summarizeInput(approval.input)

  // --- Resolved state: collapsed one-liner ---
  if (approval.resolved) {
    const isAllowed = approval.resolved.decision === 'allow'
    return (
      <div className="flex items-center gap-2 text-[10px] px-1.5 py-0.5 rounded-[6px] bg-bg-elevated border border-border-subtle text-fg-muted">
        {isAllowed ? (
          <ShieldCheck className="w-3.5 h-3.5 text-success shrink-0" />
        ) : (
          <ShieldX className="w-3.5 h-3.5 text-primary shrink-0" />
        )}
        <span className="text-xs text-fg-secondary">
          <span className="font-medium text-fg">{approval.tool}</span>
          {' — '}
          {isAllowed
            ? `Allowed${approval.resolved.scope ? ` for ${approval.resolved.scope}` : ''}`
            : 'Denied'}
        </span>
      </div>
    )
  }

  // --- Pending state: full approval card ---
  const urgency = secondsLeft <= 15
  const timerColor = urgency ? 'text-primary' : 'text-fg-muted'

  return (
    <div className="rounded-[10px] border border-border-subtle bg-bg-elevated overflow-hidden">
      {/* Header */}
      <div className="flex items-center gap-2.5 px-3.5 py-2.5">
        <div className="w-8 h-8 rounded-lg bg-warning/15 flex items-center justify-center shrink-0">
          <Shield className="w-4 h-4 text-warning" />
        </div>
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-xs font-medium text-fg truncate">{approval.tool}</span>
            <span className="text-[10px] text-fg-muted">wants permission</span>
          </div>
          {approval.reason && (
            <p className="text-[11px] text-fg-secondary mt-0.5 leading-snug">{approval.reason}</p>
          )}
        </div>
        <Tooltip content={`Auto-deny in ${secondsLeft}s`} side="left">
          <div className={`flex items-center gap-1 text-[10px] font-mono tabular-nums ${timerColor}`}>
            <Clock className="w-3 h-3" />
            {secondsLeft}s
          </div>
        </Tooltip>
      </div>

      {/* Input summary */}
      {inputSummary && (
        <div className="px-3.5 py-2 border-t border-divider bg-surface">
          <pre className="text-[10px] text-fg-muted font-mono leading-relaxed whitespace-pre-wrap break-all max-h-20 overflow-y-auto">
            {inputSummary}
          </pre>
        </div>
      )}

      {/* Actions */}
      <div className="flex items-center gap-1.5 px-3.5 py-2 border-t border-divider bg-surface">
        <Button
          variant="default"
          size="sm"
          disabled={submitting}
          onClick={() => handleDecision('allow', 'once')}
          className="text-[11px] h-6 px-2"
        >
          Allow Once
        </Button>
        <Button
          variant="ghost"
          size="sm"
          disabled={submitting}
          onClick={() => handleDecision('allow', 'session')}
          className="text-[11px] h-6 px-2 text-fg-secondary hover:text-fg"
        >
          Allow for Session
        </Button>
        <div className="flex-1" />
        <Button
          variant="ghost"
          size="sm"
          disabled={submitting}
          onClick={() => handleDecision('deny')}
          className="text-[11px] h-6 px-2 text-primary hover:text-primary-hover hover:bg-primary/10"
        >
          Deny
        </Button>
      </div>
    </div>
  )
}

/** Summarize tool input as key=value lines, truncated to ~120 chars total */
function summarizeInput(input: Record<string, unknown>): string | null {
  const entries = Object.entries(input)
  if (entries.length === 0) return null

  const lines: string[] = []
  let total = 0
  for (const [key, value] of entries.slice(0, 4)) {
    const val = typeof value === 'string' ? value : JSON.stringify(value)
    const truncated = val.length > 80 ? val.slice(0, 77) + '...' : val
    const line = `${key}: ${truncated}`
    lines.push(line)
    total += line.length
    if (total > 200) break
  }
  if (entries.length > 4) {
    lines.push(`... +${entries.length - 4} more`)
  }
  return lines.join('\n')
}
