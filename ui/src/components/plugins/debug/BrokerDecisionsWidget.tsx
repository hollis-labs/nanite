import { GitBranch } from 'lucide-react'
import { Widget } from '@/components/widgets/Widget'
import { useAppStore } from '@/stores/useAppStore'
import { BrokerDecisionsContent } from './BrokerDecisionsPanel'

export function BrokerDecisionsWidget() {
  const activeSessionId = useAppStore((s) => s.activeSessionId)

  return (
    <Widget id="broker-decisions" title="Broker Decisions" icon={GitBranch}>
      {activeSessionId ? (
        <BrokerDecisionsContent sessionId={activeSessionId} />
      ) : (
        <p className="text-[11px] text-fg-faint">Select a session to view broker decisions.</p>
      )}
    </Widget>
  )
}
