import { useState } from 'react'
import { useApprovePlan, useRejectPlan, useUpdatePlan } from '@/hooks/usePlans'
import type { PlanStatus } from '@/lib/types'

const STATUS_STYLE: Record<PlanStatus, string> = {
  proposed: 'text-warning bg-warning/10',
  approved: 'text-success bg-success/10',
  in_progress: 'text-info bg-info/10',
  complete: 'text-fg-muted bg-surface',
  abandoned: 'text-fg-faint bg-surface/50',
}

interface PlanReviewCardData {
  plan_id: string
  title: string
  description?: string
  status: PlanStatus
  steps: Array<{ id: string; title: string }>
}

interface PlanReviewCardProps {
  data: PlanReviewCardData
}

export function PlanReviewCard({ data }: PlanReviewCardProps) {
  const approvePlan = useApprovePlan()
  const { reject } = useRejectPlan()
  const updatePlan = useUpdatePlan()
  const [acted, setActed] = useState(false)
  const [currentStatus, setCurrentStatus] = useState<PlanStatus>(data.status)
  const [editing, setEditing] = useState(false)
  const [editSteps, setEditSteps] = useState(data.steps)
  const [newStepTitle, setNewStepTitle] = useState('')

  const handleApprove = () => {
    if (editing) {
      updatePlan.mutate(
        {
          id: data.plan_id,
          updates: {
            steps: editSteps.map((s) => ({
              ...s,
              status: 'pending' as const,
              depends_on: [],
            })),
          },
        },
        {
          onSuccess: () => {
            approvePlan.mutate(
              { id: data.plan_id, createTodos: true },
              {
                onSuccess: () => {
                  setCurrentStatus('approved')
                  setActed(true)
                  setEditing(false)
                },
              },
            )
          },
        },
      )
    } else {
      approvePlan.mutate(
        { id: data.plan_id, createTodos: true },
        {
          onSuccess: () => {
            setCurrentStatus('approved')
            setActed(true)
          },
        },
      )
    }
  }

  const handleReject = () => {
    reject(data.plan_id)
    setCurrentStatus('abandoned')
    setActed(true)
  }

  const handleAddStep = () => {
    const trimmed = newStepTitle.trim()
    if (!trimmed) return
    setEditSteps((prev) => [
      ...prev,
      { id: `new-${Date.now()}`, title: trimmed },
    ])
    setNewStepTitle('')
  }

  const handleRemoveStep = (stepId: string) => {
    setEditSteps((prev) => prev.filter((s) => s.id !== stepId))
  }

  return (
    <div className="rounded-sm border border-border-subtle bg-bg-elevated/60 overflow-hidden my-2">
      <div className="px-3 py-2">
        <div className="flex items-center justify-between mb-1">
          <span className="text-sm font-semibold text-fg">{data.title}</span>
          <span className={`text-[9px] px-1.5 py-0.5 rounded ${STATUS_STYLE[currentStatus]}`}>
            {currentStatus}
          </span>
        </div>
        {data.description && (
          <p className="text-[11px] text-fg-muted mb-2">{data.description}</p>
        )}

        <div className="border-l-2 border-border-subtle pl-2 ml-1 mb-2 space-y-0.5">
          {(editing ? editSteps : data.steps).map((step, i) => (
            <div key={step.id} className="flex items-center gap-1 text-[11px] text-fg-secondary py-0.5">
              <span>{i + 1}. {step.title}</span>
              {editing && (
                <button
                  type="button"
                  onClick={() => handleRemoveStep(step.id)}
                  className="text-danger/60 hover:text-danger ml-auto text-[10px]"
                >
                  remove
                </button>
              )}
            </div>
          ))}
          {editing && (
            <div className="flex gap-1 mt-1">
              <input
                type="text"
                value={newStepTitle}
                onChange={(e) => setNewStepTitle(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); handleAddStep() } }}
                placeholder="Add step..."
                className="flex-1 bg-bg border border-border rounded px-1.5 py-0.5 text-[10px] text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-primary"
              />
              <button
                type="button"
                onClick={handleAddStep}
                className="px-1.5 py-0.5 bg-surface text-fg-muted text-[10px] rounded hover:bg-surface-hover"
              >
                Add
              </button>
            </div>
          )}
        </div>

        {!acted && currentStatus === 'proposed' && (
          <div className="flex gap-2">
            <button
              type="button"
              onClick={handleApprove}
              disabled={approvePlan.isPending || updatePlan.isPending}
              className="px-3 py-1 bg-primary text-white text-[11px] rounded hover:bg-primary/80 transition-colors disabled:opacity-50"
            >
              Approve
            </button>
            <button
              type="button"
              onClick={() => setEditing((v) => !v)}
              className="px-3 py-1 bg-surface text-fg-secondary text-[11px] rounded hover:bg-surface-hover transition-colors"
            >
              {editing ? 'Done Editing' : 'Edit Steps'}
            </button>
            <button
              type="button"
              onClick={handleReject}
              className="px-3 py-1 bg-surface text-fg-muted text-[11px] rounded hover:bg-surface-hover transition-colors"
            >
              Reject
            </button>
          </div>
        )}
      </div>
    </div>
  )
}
