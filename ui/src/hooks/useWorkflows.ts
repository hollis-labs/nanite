import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { api } from "@/lib/api";

const workflowEventTypes = [
  "pipeline.started",
  "pipeline.completed",
  "pipeline.failed",
  "pipeline.canceled",
  "step.started",
  "step.completed",
  "step.failed",
  "step.skipped",
  "step.canceled",
] as const;

export function useWorkflowRuns(filter?: { status?: string; pipeline_id?: string }) {
  return useQuery({
    queryKey: ["workflow-runs", filter],
    queryFn: () => api.listWorkflowRuns(filter),
    refetchInterval: 10_000,
  });
}

export function useWorkflowRun(runId: string | null) {
  return useQuery({
    queryKey: ["workflow-run", runId],
    queryFn: () => {
      if (!runId) {
        throw new Error("workflow run id is required");
      }
      return api.getWorkflowRun(runId);
    },
    enabled: !!runId,
    refetchInterval: 5_000,
  });
}

export function useCancelWorkflowRun() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (runId: string) => api.cancelWorkflowRun(runId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["workflow-runs"] });
      queryClient.invalidateQueries({ queryKey: ["workflow-run"] });
    },
  });
}

export function useWorkflowEvents(enabled = true) {
  const queryClient = useQueryClient();
  const esRef = useRef<EventSource | null>(null);

  useEffect(() => {
    if (!enabled || typeof EventSource === "undefined") return;
    const es = new EventSource("/api/workflows/events");
    esRef.current = es;

    const invalidateWorkflowQueries = () => {
      queryClient.invalidateQueries({ queryKey: ["workflow-runs"] });
      queryClient.invalidateQueries({ queryKey: ["workflow-run"] });
    };

    // The durable endpoint uses named SSE events. EventSource.onmessage only
    // receives the implicit "message" event, so subscribe to the stable
    // Agent Workflows vocabulary explicitly while retaining onmessage for
    // compatibility with older servers.
    es.onmessage = invalidateWorkflowQueries;
    for (const eventType of workflowEventTypes) {
      es.addEventListener(eventType, invalidateWorkflowQueries);
    }

    es.onerror = () => {
      // EventSource auto-reconnects
    };

    return () => {
      for (const eventType of workflowEventTypes) {
        es.removeEventListener(eventType, invalidateWorkflowQueries);
      }
      es.close();
      esRef.current = null;
    };
  }, [queryClient, enabled]);
}
