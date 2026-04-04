import { Activity } from 'lucide-react'
import { Widget } from './Widget'
import { useAppStore } from '@/stores/useAppStore'
import { TurnSnapshotContent } from '@/components/chat/debug/TurnSnapshotPanel'

export function TurnSnapshotWidget() {
  const activeSessionId = useAppStore((s) => s.activeSessionId)

  return (
    <Widget id="turn-snapshots" title="Turn Snapshots" icon={Activity}>
      {activeSessionId ? (
        <TurnSnapshotContent sessionId={activeSessionId} />
      ) : (
        <p className="text-[11px] text-fg-faint">Select a session to view turn snapshots.</p>
      )}
    </Widget>
  )
}
