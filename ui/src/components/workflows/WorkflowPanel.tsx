import { X } from 'lucide-react'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { WorkflowList } from './WorkflowList'

export function WorkflowPanel() {
  const workflowPanelOpen = useLayoutStore((s) => s.workflowPanelOpen)
  const setWorkflowPanel = useLayoutStore((s) => s.setWorkflowPanel)

  if (!workflowPanelOpen) return null

  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center">
      <div className="absolute inset-0 bg-black/50" onClick={() => setWorkflowPanel(false)} />
      <div className="relative w-full max-w-md max-h-[70vh] bg-zinc-900 border border-zinc-700 rounded-xl shadow-2xl flex flex-col overflow-hidden">
        <div className="flex items-center justify-between px-5 py-4 border-b border-zinc-800">
          <h2 className="text-sm font-semibold text-zinc-100">Workflows</h2>
          <button
            onClick={() => setWorkflowPanel(false)}
            className="p-1 rounded text-zinc-500 hover:text-zinc-300 hover:bg-zinc-800 transition-colors"
          >
            <X className="w-4 h-4" />
          </button>
        </div>
        <div className="flex-1 overflow-y-auto">
          <WorkflowList />
        </div>
      </div>
    </div>
  )
}
