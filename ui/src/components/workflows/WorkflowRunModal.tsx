import { useState, useCallback } from 'react'
import { useMutation } from '@tanstack/react-query'
import { X, Loader2, Play } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { api } from '@/lib/api'
import type { Workflow, WorkflowResult } from '@/lib/types'
import { WorkflowResultCard } from './WorkflowResultCard'

interface WorkflowRunModalProps {
  workflow: Workflow
  onClose: () => void
}

export function WorkflowRunModal({ workflow, onClose }: WorkflowRunModalProps) {
  const [formValues, setFormValues] = useState<Record<string, unknown>>(() => {
    const defaults: Record<string, unknown> = {}
    for (const input of workflow.inputs) {
      if (input.default !== undefined) {
        defaults[input.name] = input.default
      } else if (input.type === 'boolean') {
        defaults[input.name] = false
      } else if (input.type === 'number') {
        defaults[input.name] = 0
      } else {
        defaults[input.name] = ''
      }
    }
    return defaults
  })

  const [result, setResult] = useState<WorkflowResult | null>(null)

  const runMutation = useMutation({
    mutationFn: (inputs: Record<string, unknown>) => api.runWorkflow(workflow.name, inputs),
    onSuccess: (data) => setResult(data),
  })

  const handleChange = useCallback((name: string, value: unknown) => {
    setFormValues((prev) => ({ ...prev, [name]: value }))
  }, [])

  const handleSubmit = useCallback((e: React.FormEvent) => {
    e.preventDefault()
    runMutation.mutate(formValues)
  }, [formValues, runMutation])

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      {/* Backdrop */}
      <div className="absolute inset-0 bg-black/60" onClick={onClose} />

      {/* Dialog */}
      <div className="relative w-full max-w-lg max-h-[80vh] bg-zinc-900 border border-zinc-700 rounded-xl shadow-2xl flex flex-col overflow-hidden">
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-zinc-800">
          <div>
            <h2 className="text-sm font-semibold text-zinc-100">{workflow.name}</h2>
            {workflow.description && (
              <p className="text-xs text-zinc-500 mt-0.5">{workflow.description}</p>
            )}
          </div>
          <button
            onClick={onClose}
            className="p-1 rounded text-zinc-500 hover:text-zinc-300 hover:bg-zinc-800 transition-colors"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        {/* Body */}
        <div className="flex-1 overflow-y-auto p-5">
          {result ? (
            <WorkflowResultCard result={result} />
          ) : (
            <form id="workflow-form" onSubmit={handleSubmit} className="space-y-4">
              {workflow.inputs.length === 0 ? (
                <p className="text-sm text-zinc-500">This workflow has no inputs. Click Run to execute.</p>
              ) : (
                workflow.inputs.map((input) => (
                  <div key={input.name} className="space-y-1.5">
                    <label className="block text-xs font-medium text-zinc-400">
                      {input.label || input.name}
                      {input.required && <span className="text-red-400 ml-0.5">*</span>}
                    </label>

                    {input.type === 'text' && (
                      <input
                        type="text"
                        value={String(formValues[input.name] ?? '')}
                        onChange={(e) => handleChange(input.name, e.target.value)}
                        required={input.required}
                        className="w-full px-3 py-2 text-sm bg-zinc-800 border border-zinc-700 rounded-lg text-zinc-200 placeholder-zinc-600 outline-none focus:border-indigo-500 transition-colors"
                      />
                    )}

                    {input.type === 'textarea' && (
                      <textarea
                        value={String(formValues[input.name] ?? '')}
                        onChange={(e) => handleChange(input.name, e.target.value)}
                        required={input.required}
                        rows={3}
                        className="w-full px-3 py-2 text-sm bg-zinc-800 border border-zinc-700 rounded-lg text-zinc-200 placeholder-zinc-600 outline-none focus:border-indigo-500 transition-colors resize-y"
                      />
                    )}

                    {input.type === 'number' && (
                      <input
                        type="number"
                        value={String(formValues[input.name] ?? 0)}
                        onChange={(e) => handleChange(input.name, Number(e.target.value))}
                        required={input.required}
                        className="w-full px-3 py-2 text-sm bg-zinc-800 border border-zinc-700 rounded-lg text-zinc-200 outline-none focus:border-indigo-500 transition-colors"
                      />
                    )}

                    {input.type === 'select' && (
                      <select
                        value={String(formValues[input.name] ?? '')}
                        onChange={(e) => handleChange(input.name, e.target.value)}
                        required={input.required}
                        className="w-full px-3 py-2 text-sm bg-zinc-800 border border-zinc-700 rounded-lg text-zinc-200 outline-none focus:border-indigo-500 transition-colors"
                      >
                        <option value="">Select...</option>
                        {input.options?.map((opt) => (
                          <option key={opt} value={opt}>{opt}</option>
                        ))}
                      </select>
                    )}

                    {input.type === 'boolean' && (
                      <label className="flex items-center gap-2 cursor-pointer">
                        <input
                          type="checkbox"
                          checked={Boolean(formValues[input.name])}
                          onChange={(e) => handleChange(input.name, e.target.checked)}
                          className="w-4 h-4 rounded border-zinc-600 bg-zinc-800 text-indigo-500 focus:ring-indigo-500 focus:ring-offset-0"
                        />
                        <span className="text-sm text-zinc-400">Enabled</span>
                      </label>
                    )}
                  </div>
                ))
              )}
            </form>
          )}

          {runMutation.isError && (
            <div className="mt-3 p-3 rounded-lg bg-red-500/10 border border-red-500/20 text-sm text-red-400">
              {runMutation.error instanceof Error ? runMutation.error.message : 'Workflow execution failed'}
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-end gap-2 px-5 py-3 border-t border-zinc-800">
          {result ? (
            <Button variant="secondary" size="sm" onClick={onClose}>
              Close
            </Button>
          ) : (
            <>
              <Button variant="ghost" size="sm" onClick={onClose}>
                Cancel
              </Button>
              <Button
                type="submit"
                form="workflow-form"
                size="sm"
                disabled={runMutation.isPending}
                className="gap-1.5"
              >
                {runMutation.isPending ? (
                  <Loader2 className="w-3.5 h-3.5 animate-spin" />
                ) : (
                  <Play className="w-3.5 h-3.5" />
                )}
                Run
              </Button>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
