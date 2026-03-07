import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Play, Loader2, Workflow as WorkflowIcon } from 'lucide-react'
import { api } from '@/lib/api'
import type { Workflow } from '@/lib/types'
import { WorkflowRunModal } from './WorkflowRunModal'

export function WorkflowList() {
  const [selectedWorkflow, setSelectedWorkflow] = useState<Workflow | null>(null)

  const { data: workflows = [], isLoading } = useQuery({
    queryKey: ['workflows'],
    queryFn: api.listWorkflows,
  })

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-12">
        <Loader2 className="w-5 h-5 animate-spin text-zinc-500" />
      </div>
    )
  }

  if (workflows.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-12 text-zinc-500">
        <WorkflowIcon className="w-8 h-8 mb-2" />
        <p className="text-sm">No workflows available</p>
      </div>
    )
  }

  return (
    <>
      <div className="grid gap-2 p-4">
        {workflows.map((wf) => (
          <button
            key={wf.name}
            onClick={() => setSelectedWorkflow(wf)}
            className="flex items-start gap-3 p-3 rounded-lg border border-zinc-800 bg-zinc-900/50 hover:bg-zinc-800/70 hover:border-zinc-700 transition-colors text-left group"
          >
            <div className="w-8 h-8 rounded-md bg-indigo-500/15 flex items-center justify-center shrink-0 mt-0.5">
              <WorkflowIcon className="w-4 h-4 text-indigo-400" />
            </div>
            <div className="flex-1 min-w-0">
              <div className="flex items-center gap-2">
                <span className="text-sm font-medium text-zinc-200">{wf.name}</span>
                {(wf.inputs ?? []).length > 0 && (
                  <span className="text-xs text-zinc-500">
                    {wf.inputs.length} input{wf.inputs.length !== 1 ? 's' : ''}
                  </span>
                )}
              </div>
              {wf.description && (
                <p className="text-xs text-zinc-500 mt-0.5 line-clamp-2">{wf.description}</p>
              )}
            </div>
            <Play className="w-4 h-4 text-zinc-600 group-hover:text-indigo-400 shrink-0 mt-1 transition-colors" />
          </button>
        ))}
      </div>

      {selectedWorkflow && (
        <WorkflowRunModal
          workflow={selectedWorkflow}
          onClose={() => setSelectedWorkflow(null)}
        />
      )}
    </>
  )
}
