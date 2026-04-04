import { useState } from 'react'
import { Terminal, ChevronDown, ChevronRight } from 'lucide-react'

interface ShellExecMeta {
  command: string
  exit_code: number
  duration_ms: number
  truncated: boolean
}

interface ShellMessageProps {
  content: string
  meta: ShellExecMeta
}

const COLLAPSE_LINE_THRESHOLD = 20

export function ShellMessage({ content, meta }: ShellMessageProps) {
  // Extract the output (skip the "$ command\n" prefix in content)
  const outputStart = content.indexOf('\n')
  const output = outputStart >= 0 ? content.slice(outputStart + 1) : ''
  const lines = output.split('\n')
  const isLong = lines.length > COLLAPSE_LINE_THRESHOLD
  const [expanded, setExpanded] = useState(!isLong)

  const displayOutput = expanded ? output : lines.slice(0, COLLAPSE_LINE_THRESHOLD).join('\n')
  const hiddenCount = lines.length - COLLAPSE_LINE_THRESHOLD

  const exitOk = meta.exit_code === 0

  return (
    <div className="rounded-lg border border-border-subtle overflow-hidden bg-[#1a1b26]">
      {/* Header: command + badges */}
      <div className="flex items-center gap-2 px-3 py-1.5 bg-[#1e1f2e] border-b border-border-subtle">
        <Terminal className="w-3.5 h-3.5 text-fg-faint shrink-0" />
        <code className="text-xs text-fg-secondary font-mono flex-1 truncate">
          $ {meta.command}
        </code>
        <span
          className={`text-[10px] font-mono px-1.5 py-0.5 rounded ${
            exitOk
              ? 'bg-success/15 text-success'
              : 'bg-danger/15 text-danger'
          }`}
        >
          {meta.exit_code}
        </span>
        <span className="text-[10px] text-fg-faint font-mono">
          {meta.duration_ms < 1000
            ? `${meta.duration_ms}ms`
            : `${(meta.duration_ms / 1000).toFixed(1)}s`}
        </span>
      </div>

      {/* Output */}
      {output && (
        <div className="px-3 py-2">
          <pre className="text-xs font-mono text-[#a9b1d6] whitespace-pre-wrap break-all leading-relaxed">
            {displayOutput}
          </pre>
          {isLong && !expanded && (
            <button
              onClick={() => setExpanded(true)}
              className="flex items-center gap-1 mt-1.5 text-xs text-primary hover:text-primary/80 transition-colors"
            >
              <ChevronDown className="w-3 h-3" />
              Show {hiddenCount} more lines
            </button>
          )}
          {isLong && expanded && (
            <button
              onClick={() => setExpanded(false)}
              className="flex items-center gap-1 mt-1.5 text-xs text-primary hover:text-primary/80 transition-colors"
            >
              <ChevronRight className="w-3 h-3" />
              Collapse
            </button>
          )}
        </div>
      )}

      {/* Truncation notice */}
      {meta.truncated && (
        <div className="px-3 py-1 text-[10px] text-warning border-t border-border-subtle bg-warning/10">
          Output truncated — full output saved on server
        </div>
      )}
    </div>
  )
}
