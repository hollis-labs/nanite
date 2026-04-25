import '@/lib/chartSetup'
import { useRecentExecutions, useUtilityCallSummary, useUtilityCallLog } from '@/hooks/useObservability'
import { KPIRow } from './KPIRow'
import { DurationChart } from './DurationChart'
import { ProviderDistributionChart } from './ProviderDistributionChart'
import { UtilityTable } from './UtilityTable'
import { UtilityLogTable } from './UtilityLogTable'
import { RecentExecutionsTable } from './RecentExecutionsTable'
import { ProcessHealthPanel } from './ProcessHealthPanel'
import { WorkerStatusPanel } from './WorkerStatusPanel'

function Card({
  title,
  children,
  className = '',
}: {
  title: string
  children: React.ReactNode
  className?: string
}) {
  return (
    <div className={`rounded-xl border border-border-subtle bg-bg-elevated overflow-hidden ${className}`}>
      <div className="px-4 py-2.5 border-b border-border/50">
        <h4 className="text-[11px] uppercase tracking-wider text-fg-muted font-medium">{title}</h4>
      </div>
      <div className="p-4">{children}</div>
    </div>
  )
}

export function ObservabilityDashboard() {
  const { data: executions = [] } = useRecentExecutions(50)
  const { data: utilitySummary = [] } = useUtilityCallSummary()
  const { data: utilityLog = [] } = useUtilityCallLog(50)

  return (
    <div className="space-y-4 max-w-6xl">
      <KPIRow data={executions} />

      <div className="grid grid-cols-2 gap-3">
        <Card title="Duration Timeline">
          <DurationChart data={executions} />
        </Card>
        <Card title="Provider Distribution">
          <ProviderDistributionChart data={executions} />
        </Card>
      </div>

      <Card title="Process Health">
        <ProcessHealthPanel />
      </Card>

      <Card title="Worker Status">
        <WorkerStatusPanel />
      </Card>

      <Card title="Utility Call Comparison">
        <UtilityTable data={utilitySummary} />
      </Card>

      <Card title="Utility Call Log">
        <UtilityLogTable data={utilityLog} />
      </Card>

      <Card title="Recent Executions">
        <RecentExecutionsTable data={executions} />
      </Card>
    </div>
  )
}
