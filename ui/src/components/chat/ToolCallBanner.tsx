import { Loader2, CheckCircle2, Cpu } from 'lucide-react'
import type { ToolCall } from '@/lib/types'
import { StatusPill } from './envelopes/primitives'

function formatRetention(minutes: number): string {
  if (minutes < 0) return 'kept until page refresh'
  if (minutes >= 60) return `clears after ${minutes / 60} hour${minutes > 60 ? 's' : ''} of inactivity`
  return `clears after ${minutes} minutes of inactivity`
}

export interface ToolCallBannerProps {
  toolCalls: ToolCall[]
  /** Drawer/host open state — toggles header hover & cursor styling. */
  isOpen: boolean
  /** Retention minutes from settings; -1 = kept until refresh. Far-right text. */
  retention: number
  /** Click toggles host open/closed. Wired by host. */
  onClick: () => void
}

/**
 * Faithful extraction of the ToolCallDrawer closed-state header chrome.
 *
 * This is intentionally a 1:1 lift of the existing rendering — same icon,
 * same labels, same StatusPills, same retention hint. Reused by ToolCallDrawer
 * during the transition window, and reserved for future tight-slot reuse
 * elsewhere (the user flagged this as preserve-for-reuse intent in the spec).
 *
 * Drag-resize behavior stays at the host. This component renders only.
 */
export function ToolCallBanner({ toolCalls, isOpen, retention, onClick }: ToolCallBannerProps) {
  const hasTools = toolCalls.length > 0
  const hasRunning = toolCalls.some((tc) => tc.status === 'running')
  const runningCount = toolCalls.filter((tc) => tc.status === 'running').length
  const doneCount = toolCalls.filter((tc) => tc.status === 'done').length
  const currentTool = toolCalls.find((tc) => tc.status === 'running') ?? toolCalls[toolCalls.length - 1] ?? null

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onClick}
      onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') onClick() }}
      className={`flex select-none items-center gap-3 px-3.5 py-2.5 transition-colors ${
        isOpen ? 'border-b border-divider cursor-default' : 'cursor-pointer hover:bg-surface'
      }`}
    >
      <span className={`flex h-[18px] w-[18px] shrink-0 items-center justify-center rounded-[5px] ${
        hasRunning ? 'bg-primary-muted text-primary' : 'bg-surface text-fg-muted'
      }`}>
        {hasRunning ? (
          <Loader2 className="h-3 w-3 animate-spin" />
        ) : hasTools ? (
          <CheckCircle2 className="h-3 w-3" />
        ) : (
          <Cpu className="h-3 w-3" />
        )}
      </span>

      <div className="flex min-w-0 flex-1 items-center gap-2">
        <span className={`font-mono text-[10px] font-semibold uppercase tracking-wide ${hasRunning ? 'text-primary' : 'text-fg-muted'}`}>
          {hasRunning ? 'Running' : 'Tools'}
        </span>
        <StatusPill tone={hasRunning ? 'primary' : 'neutral'}>{toolCalls.length}</StatusPill>

        {currentTool && (
          <>
            <span className="font-mono text-[10px] text-fg-faint">·</span>
            <span className={`truncate font-mono text-[11px] ${hasRunning ? 'text-fg' : 'text-fg-secondary'}`}>
              {currentTool.tool}
            </span>
            {hasRunning && (currentTool.detail || currentTool.summary) && (
              <span className="truncate font-mono text-[10px] text-fg-muted">
                {currentTool.detail || currentTool.summary}
              </span>
            )}
          </>
        )}
      </div>

      <div className="hidden items-center gap-1 sm:flex">
        {runningCount > 0 && <StatusPill tone="primary">{runningCount} live</StatusPill>}
        {!hasRunning && doneCount > 0 && <StatusPill tone="neutral">{doneCount} done</StatusPill>}
      </div>

      <span className="hidden font-mono text-[10px] text-fg-faint lg:block">
        {formatRetention(retention)}
      </span>
    </div>
  )
}
