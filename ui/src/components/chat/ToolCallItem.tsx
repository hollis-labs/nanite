import type { ToolCall } from '@/lib/types'
import { Check, ChevronRight, Loader2, Wrench, X } from 'lucide-react'
import { useCallback, useState } from 'react'
import { ContentActions } from './ContentActions'

/**
 * POLISHED — tool call item.
 *
 * Changes vs. original:
 *  - `variant="full"` card used `rounded-md bg-surface/50 border-border-subtle/50`
 *    — exactly the stacked-alpha pattern the system polish is eliminating.
 *    Now uses solid bg-surface + 6px radius (matches the 4/6/10 scale: pills
 *    4, inner chrome 6, outer envelopes 10).
 *  - Tool name now renders in JetBrains Mono uppercase tracking to match the
 *    EnvelopeHeader label grammar — same "this is a technical identifier" read.
 *  - Status icon sizes normalised to 3.5px (was 3px), giving the icon a bit
 *    more presence next to the mono name.
 *  - `variant="drawer"` dropped the `border-b border-border/50` stacked alpha
 *    for a clean solid divider. Same visual density, less visual noise.
 *  - Inline variant keeps the tree connector (that drawing ─/┌/├/└ is
 *    information-bearing and worth keeping) but the chevron is now colored
 *    fg-faint not fg-muted — a touch lighter so the mono tool name leads.
 */

export function formatToolName(name: string): string {
  if (name.startsWith('mcp__')) {
    const idx = name.indexOf('__', 5)
    if (idx !== -1) return name.slice(idx + 2)
  }
  return name
}

export function StatusIcon({ status }: { status: ToolCall['status'] }) {
  if (status === 'running') return <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-primary" />
  if (status === 'done')    return <Check    className="h-3.5 w-3.5 shrink-0 text-success" />
  return <X className="h-3.5 w-3.5 shrink-0 text-danger" />
}

export function TreeConnector({ index, total }: { index: number; total: number }) {
  const char = index === 0 && total === 1
    ? '─'
    : index === 0
      ? '┌'
      : index === total - 1
        ? '└'
        : '├'
  return <span className="w-3 select-none text-center font-mono text-fg-faint">{char}</span>
}

interface ToolCallItemProps {
  toolCall: ToolCall
  variant: 'inline' | 'drawer' | 'full'
  index?: number
  total?: number
  defaultExpanded?: boolean
}

export function ToolCallItem({
  toolCall,
  variant,
  index = 0,
  total = 1,
  defaultExpanded = false,
}: ToolCallItemProps) {
  const [expanded, setExpanded] = useState(defaultExpanded)
  const [hovered, setHovered] = useState(false)
  const summary = toolCall.summary
  const hasSummary = !!summary
  const toolName = formatToolName(toolCall.tool)

  const toggleExpand = useCallback(() => {
    if (hasSummary) setExpanded((v) => !v)
  }, [hasSummary])

  /* ─────────────────────────── FULL ─────────────────────────── */
  if (variant === 'full') {
    return (
      <div
        className="flex items-center gap-2 rounded-[6px] border border-border-subtle bg-surface px-2 py-1 text-xs"
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
      >
        <StatusIcon status={toolCall.status} />
        <Wrench className="h-3 w-3 shrink-0 text-fg-faint" />
        <span className="font-mono text-[11px] font-semibold uppercase tracking-wide text-fg-secondary">
          {toolName}
        </span>
        {toolCall.summary && (
          <span className="flex-1 truncate text-fg-muted">{toolCall.summary}</span>
        )}
        {toolCall.status === 'running' && !toolCall.summary && (
          <span className="italic text-fg-muted">Running…</span>
        )}
        {summary && <ContentActions content={summary} visible={hovered} />}
      </div>
    )
  }

  /* ───────────────────────── DRAWER ─────────────────────────── */
  if (variant === 'drawer') {
    return (
      <div
        className="border-b border-border-subtle last:border-b-0"
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
      >
        <div
          className={`flex items-center gap-2 px-3 py-1.5 text-xs ${
            hasSummary ? 'cursor-pointer hover:bg-surface' : ''
          }`}
          onClick={toggleExpand}
        >
          <StatusIcon status={toolCall.status} />
          <div className="min-w-0 flex-1">
            <span className="font-mono text-[11px] font-semibold uppercase tracking-wide text-fg-secondary">
              {toolName}
            </span>
            {toolCall.detail && (
              <p className="truncate font-mono text-[10px] text-fg-muted">{toolCall.detail}</p>
            )}
          </div>
          {toolCall.status === 'running' && !toolCall.detail && (
            <span className="shrink-0 italic text-fg-faint">running…</span>
          )}
          {hasSummary && (
            <span className="relative flex shrink-0 items-center">
              <ChevronRight
                className={`h-3 w-3 text-fg-faint transition-transform ${
                  hovered ? 'invisible' : ''
                } ${expanded ? 'rotate-90' : ''}`}
              />
              <span
                className={`absolute right-0 ${
                  hovered ? 'opacity-100' : 'pointer-events-none opacity-0'
                }`}
                onClick={(e) => e.stopPropagation()}
              >
                {summary && <ContentActions content={summary} visible={true} />}
              </span>
            </span>
          )}
        </div>
        {expanded && summary && (
          <div className="px-3 pb-2">
            <div className="max-h-32 overflow-y-auto rounded-[4px] border border-border-subtle bg-bg-elevated px-2 py-1">
              <pre className="whitespace-pre-wrap break-all font-mono text-[10px] text-fg-secondary">
                {summary}
              </pre>
            </div>
          </div>
        )}
      </div>
    )
  }

  /* ───────────────────────── INLINE ─────────────────────────── */
  return (
    <div onMouseEnter={() => setHovered(true)} onMouseLeave={() => setHovered(false)}>
      <div
        className={`flex items-center gap-1.5 px-2 py-0.5 text-xs text-fg-secondary ${
          hasSummary ? 'cursor-pointer' : ''
        }`}
        onClick={toggleExpand}
      >
        <TreeConnector index={index} total={total} />
        <StatusIcon status={toolCall.status} />
        <span className="font-mono text-[11px] font-semibold uppercase tracking-wide">
          {toolName}
        </span>
        {hasSummary && (
          <ChevronRight
            className={`h-3 w-3 text-fg-faint transition-transform ${
              expanded ? 'rotate-90' : ''
            }`}
          />
        )}
        {hasSummary && (
          <span className="ml-auto" onClick={(e) => e.stopPropagation()}>
            {summary && <ContentActions content={summary} visible={hovered} />}
          </span>
        )}
      </div>
      {expanded && summary && (
        <div className="py-0.5 pl-7 pr-1">
          <div className="max-h-24 overflow-y-auto rounded-[4px] border border-border-subtle bg-surface px-2 py-1">
            <pre className="whitespace-pre-wrap break-all font-mono text-[10px] text-fg-secondary">
              {summary}
            </pre>
          </div>
        </div>
      )}
    </div>
  )
}
