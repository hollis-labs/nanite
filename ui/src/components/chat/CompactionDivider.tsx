import { Layers } from 'lucide-react'
import { Tooltip } from '@/components/ui/tooltip'

/**
 * Visual divider indicating that earlier messages were compacted (summarized).
 * Rendered in the message stream when a message has compacted metadata.
 */
export function CompactionDivider() {
  return (
    <div className="flex items-center gap-3 py-1">
      <div className="flex-1 h-px bg-border/40" />
      <Tooltip content="Earlier messages were summarized to save context space" side="top">
        <div className="flex items-center gap-1.5 px-2 py-0.5 rounded-full bg-surface/40 border border-border/30">
          <Layers className="w-3 h-3 text-fg-faint" />
          <span className="text-[10px] text-fg-muted">Earlier messages summarized</span>
        </div>
      </Tooltip>
      <div className="flex-1 h-px bg-border/40" />
    </div>
  )
}
