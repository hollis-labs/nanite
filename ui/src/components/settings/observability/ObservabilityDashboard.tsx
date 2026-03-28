import '@/lib/chartSetup'
import { useRecentExecutions, useUtilityCallSummary, useUtilityCallLog } from '@/hooks/useObservability'
import { KPIRow } from './KPIRow'
import { DurationChart } from './DurationChart'
import { ProviderDistributionChart } from './ProviderDistributionChart'
import { UtilityTable } from './UtilityTable'
import { UtilityLogTable } from './UtilityLogTable'
import { RecentExecutionsTable } from './RecentExecutionsTable'
import { ProcessHealthPanel } from './ProcessHealthPanel'

function SectionHeader({ title }: { title: string }) {
  return (
    <div className="border-b border-zinc-800 pb-2 mb-1">
      <h3 className="text-xs font-semibold text-zinc-400 uppercase tracking-wider">{title}</h3>
    </div>
  )
}

export function ObservabilityDashboard() {
  const { data: executions = [] } = useRecentExecutions(50)
  const { data: utilitySummary = [] } = useUtilityCallSummary()
  const { data: utilityLog = [] } = useUtilityCallLog(50)

  return (
    <div className="space-y-6 max-w-6xl">
      <SectionHeader title="Overview" />
      <KPIRow data={executions} />

      <SectionHeader title="Execution Performance" />
      <div className="grid grid-cols-2 gap-4">
        <div className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-4">
          <h4 className="text-[10px] uppercase tracking-wider text-zinc-500 mb-3">Duration Timeline</h4>
          <DurationChart data={executions} />
        </div>
        <div className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-4">
          <h4 className="text-[10px] uppercase tracking-wider text-zinc-500 mb-3">Provider Distribution</h4>
          <ProviderDistributionChart data={executions} />
        </div>
      </div>

      <SectionHeader title="Process Health" />
      <div className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-4">
        <ProcessHealthPanel />
      </div>

      <SectionHeader title="Utility Call Comparison" />
      <div className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-4">
        <UtilityTable data={utilitySummary} />
      </div>

      <SectionHeader title="Utility Call Log" />
      <div className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-4">
        <UtilityLogTable data={utilityLog} />
      </div>

      <SectionHeader title="Recent Executions" />
      <div className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-4">
        <RecentExecutionsTable data={executions} />
      </div>
    </div>
  )
}
