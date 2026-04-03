import { useAppStore } from '@/stores/useAppStore'
import { BrokerDecisionsPanel } from './BrokerDecisionsPanel'
import { SlotInspectorPanel } from './SlotInspectorPanel'
import { TurnSnapshotPanel } from './TurnSnapshotPanel'

/**
 * Container for all debug panels. Renders in the right rail "Debug" tab
 * or anywhere debug visibility is needed. Only shown in developer_mode.
 */
export function DebugPanelsContainer() {
  const activeSessionId = useAppStore((s) => s.activeSessionId)

  if (!activeSessionId) {
    return (
      <p className="text-[11px] text-fg-faint px-2 py-4">
        Select a session to view debug information.
      </p>
    )
  }

  return (
    <div className="space-y-2">
      <BrokerDecisionsPanel sessionId={activeSessionId} />
      <SlotInspectorPanel sessionId={activeSessionId} />
      <TurnSnapshotPanel sessionId={activeSessionId} />
    </div>
  )
}
