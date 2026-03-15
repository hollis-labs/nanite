import { Bot } from 'lucide-react'
import { Widget } from './Widget'
import { useChatStore } from '@/stores/useChatStore'
import { AVAILABLE_MODELS, type AgentMode } from '@/lib/types'

const MODE_BADGE_STYLES: Record<AgentMode, { bg: string; text: string }> = {
  default: { bg: 'bg-blue-500/15', text: 'text-blue-400' },
  architect: { bg: 'bg-purple-500/15', text: 'text-purple-400' },
  planner: { bg: 'bg-green-500/15', text: 'text-green-400' },
  writer: { bg: 'bg-amber-500/15', text: 'text-amber-400' },
}

export function AgentStatusWidget() {
  const activeMode = useChatStore((s) => s.activeMode)
  const activeModel = useChatStore((s) => s.activeModel)
  const setActiveMode = useChatStore((s) => s.setActiveMode)

  const modelLabel = AVAILABLE_MODELS.find((m) => m.id === activeModel)?.label || activeModel
  const modeStyle = MODE_BADGE_STYLES[activeMode]

  return (
    <Widget id="agent-status" title="Agent Status" icon={Bot}>
      <div className="space-y-2.5">
        <div className="flex justify-between text-xs">
          <span className="text-zinc-500">Agent</span>
          <span className="text-zinc-300">Conduit</span>
        </div>
        <div className="flex justify-between text-xs items-center">
          <span className="text-zinc-500">Mode</span>
          <span
            className={`px-1.5 py-0.5 rounded text-xs ${modeStyle.bg} ${modeStyle.text} capitalize`}
          >
            {activeMode}
          </span>
        </div>
        <div className="flex justify-between text-xs">
          <span className="text-zinc-500">Model</span>
          <span className="text-zinc-300">{modelLabel}</span>
        </div>
        <button
          onClick={() => {
            // Cycle through modes for quick switching
            const modes: AgentMode[] = ['default', 'architect', 'planner', 'writer']
            const idx = modes.indexOf(activeMode)
            const next = modes[(idx + 1) % modes.length]
            setActiveMode(next)
          }}
          className="w-full text-xs text-center py-1.5 rounded-md bg-zinc-800 text-zinc-400 hover:text-zinc-200 hover:bg-zinc-700 transition-colors"
        >
          Switch Mode
        </button>
      </div>
    </Widget>
  )
}
