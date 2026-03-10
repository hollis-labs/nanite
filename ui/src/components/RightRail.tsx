import { ScrollArea } from '@/components/ui/ScrollArea'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { SessionInfoWidget } from './widgets/SessionInfoWidget'
import { BookmarksWidget } from './widgets/BookmarksWidget'
import { ContextBudgetWidget } from './widgets/ContextBudgetWidget'
import { AgentStatusWidget } from './widgets/AgentStatusWidget'
import { TokenUsageWidget } from './widgets/TokenUsageWidget'

export function RightRail() {
  const open = useLayoutStore((s) => s.rightRailOpen)

  return (
    <aside
      className={`h-full bg-zinc-950 border-l border-zinc-800 flex flex-col transition-all duration-200 ease-in-out overflow-hidden ${
        open ? 'w-96' : 'w-0'
      }`}
    >
      <div className="min-w-96 h-full flex flex-col overflow-hidden">
        {/* Header */}
        <div className="px-4 py-3 border-b border-zinc-800 shrink-0">
          <h2 className="text-sm font-semibold text-zinc-100">Widgets</h2>
        </div>

        {/* Widget cards */}
        <ScrollArea className="flex-1 min-h-0">
          <div className="p-3 space-y-3">
            <SessionInfoWidget />
            <BookmarksWidget />
            <ContextBudgetWidget />
            <TokenUsageWidget />
            <AgentStatusWidget />
          </div>
        </ScrollArea>
      </div>
    </aside>
  )
}
