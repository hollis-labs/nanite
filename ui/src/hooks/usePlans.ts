import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { Plan, PlanFilter, PlanStep, PlanStepStatus } from '@/lib/types'
import { useWorkStore } from '@/stores/useWorkStore'

export function usePlans(filter: PlanFilter) {
  return useQuery({
    queryKey: ['plans', filter],
    queryFn: () => api.listPlans(filter),
    enabled: !!filter.scope_id || filter.scope === 'workspace',
  })
}

export function useCreatePlan() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (data: {
      title: string
      scope: string
      scope_id?: string
      description?: string
      steps?: PlanStep[]
    }) => api.createPlan(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['plans'] })
    },
  })
}

export function useUpdatePlan() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({
      id,
      updates,
    }: {
      id: string
      updates: Partial<Pick<Plan, 'title' | 'description' | 'status' | 'steps' | 'metadata'>>
    }) => api.updatePlan(id, updates),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['plans'] })
    },
  })
}

export function useUpdatePlanStep() {
  const queryClient = useQueryClient()
  const recordChange = useWorkStore((s) => s.recordChange)

  return useMutation({
    mutationFn: ({
      planId,
      stepId,
      updates,
    }: {
      planId: string
      stepId: string
      updates: Partial<Pick<PlanStep, 'title' | 'status' | 'notes'>>
    }) => api.updatePlanStep(planId, stepId, updates),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: ['plans'] })
      if (variables.updates.status === 'done') {
        recordChange({ type: 'plan_step_checked', id: variables.stepId, planId: variables.planId })
      }
    },
  })
}

export function useApprovePlan() {
  const queryClient = useQueryClient()
  const recordChange = useWorkStore((s) => s.recordChange)

  return useMutation({
    mutationFn: ({ id, createTodos = true }: { id: string; createTodos?: boolean }) =>
      api.approvePlan(id, createTodos),
    onSuccess: (plan) => {
      queryClient.invalidateQueries({ queryKey: ['plans'] })
      queryClient.invalidateQueries({ queryKey: ['todos'] })
      recordChange({ type: 'plan_approved', id: plan.id })
    },
  })
}

export function useRejectPlan() {
  const updatePlan = useUpdatePlan()
  const recordChange = useWorkStore((s) => s.recordChange)

  return {
    reject: (id: string) => {
      updatePlan.mutate({ id, updates: { status: 'abandoned' } })
      recordChange({ type: 'plan_rejected', id })
    },
  }
}

export function useTogglePlanStep() {
  const updateStep = useUpdatePlanStep()
  const recordChange = useWorkStore((s) => s.recordChange)

  return {
    check: (planId: string, stepId: string) => {
      updateStep.mutate({ planId, stepId, updates: { status: 'done' as PlanStepStatus } })
    },
    uncheck: (planId: string, stepId: string, reason?: string) => {
      updateStep.mutate({ planId, stepId, updates: { status: 'pending' as PlanStepStatus } })
      recordChange({ type: 'plan_step_unchecked', id: stepId, planId, reason })
    },
  }
}
