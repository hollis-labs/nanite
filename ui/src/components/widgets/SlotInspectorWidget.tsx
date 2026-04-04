import { Columns3 } from 'lucide-react'
import { Widget } from './Widget'
import { useAppStore } from '@/stores/useAppStore'
import { SlotInspectorContent } from '@/components/chat/debug/SlotInspectorPanel'

export function SlotInspectorWidget() {
  const activeSessionId = useAppStore((s) => s.activeSessionId)

  return (
    <Widget id="slot-inspector" title="Context Slots" icon={Columns3}>
      {activeSessionId ? (
        <SlotInspectorContent sessionId={activeSessionId} />
      ) : (
        <p className="text-[11px] text-fg-faint">Select a session to view context slots.</p>
      )}
    </Widget>
  )
}
