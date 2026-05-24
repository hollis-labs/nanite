import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2, RefreshCcw, Save, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import type { AgentBootPlanDocument, AgentBootPlanDryRunResponse, AgentProfile } from "@/lib/types";
import {
  AgentBootPlanEditor,
  AgentBootPlanPreview,
  createEmptyBootPlan,
  hasBootPlanContent,
} from "./AgentBootPlanEditor";

export function AgentBootPlanPanel({
  agent,
  isReadOnly,
}: {
  agent: AgentProfile;
  isReadOnly: boolean;
}) {
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<AgentBootPlanDocument>(createEmptyBootPlan(agent.id));
  const [preview, setPreview] = useState<AgentBootPlanDryRunResponse | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [loadedSignature, setLoadedSignature] = useState<string | null>(null);
  const signature = useMemo(() => JSON.stringify(draft), [draft]);

  const planQuery = useQuery({
    queryKey: ["agent-boot-plan", agent.id],
    queryFn: () => api.getAgentBootPlan(agent.id),
  });

  useEffect(() => {
    if (!planQuery.data) return;
    const next = { ...planQuery.data, agent_id: agent.id };
    setDraft(next);
    setPreview(null);
    setLoadedSignature(JSON.stringify(next));
    setSaveError(null);
  }, [agent.id, planQuery.data]);

  const refresh = () => queryClient.invalidateQueries({ queryKey: ["agent-boot-plan", agent.id] });

  const dryRunMutation = useMutation({
    mutationFn: () => api.dryRunAgentBootPlan(agent.id, draft),
    onSuccess: (response) => {
      setPreview(response);
      setSaveError(null);
    },
    onError: (error) => {
      setSaveError(error instanceof Error ? error.message : "Dry-run failed.");
    },
  });

  const saveMutation = useMutation({
    mutationFn: () => {
      const payload =
        preview && JSON.stringify(preview.normalized_plan) === signature && preview.valid
          ? preview.normalized_plan
          : draft;
      return api.updateAgentBootPlan(agent.id, { ...payload, agent_id: agent.id });
    },
    onSuccess: async (response) => {
      setDraft(response);
      setLoadedSignature(JSON.stringify(response));
      setPreview(null);
      setSaveError(null);
      await refresh();
    },
    onError: (error) => {
      setSaveError(error instanceof Error ? error.message : "Save failed.");
    },
  });

  const deleteMutation = useMutation({
    mutationFn: () => api.deleteAgentBootPlan(agent.id),
    onSuccess: async () => {
      const empty = createEmptyBootPlan(agent.id);
      setDraft(empty);
      setLoadedSignature(JSON.stringify(empty));
      setPreview(null);
      setSaveError(null);
      await refresh();
    },
    onError: (error) => {
      setSaveError(error instanceof Error ? error.message : "Delete failed.");
    },
  });

  const isDirty = loadedSignature !== null && loadedSignature !== signature;

  if (planQuery.isLoading) {
    return (
      <div className="flex items-center gap-2 rounded-[10px] border border-border-subtle bg-surface/30 px-3 py-3 text-sm text-fg-muted">
        <Loader2 className="h-4 w-4 animate-spin" />
        Loading boot plan…
      </div>
    );
  }

  if (planQuery.error) {
    return (
      <div className="space-y-3">
        <div className="rounded-[10px] border border-red-500/30 bg-red-500/10 px-3 py-3 text-sm text-red-100">
          Failed to load boot plan.
        </div>
        <Button type="button" variant="outline" onClick={() => refresh()}>
          <RefreshCcw className="mr-1.5 h-3.5 w-3.5" />
          Retry
        </Button>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {isReadOnly ? (
        <div className="rounded-[10px] border border-border-subtle bg-surface/30 px-3 py-3 text-xs text-fg-muted">
          This agent is file-backed and read-only. You can inspect the boot plan but mutations stay disabled.
        </div>
      ) : null}

      <div className="flex flex-wrap items-center gap-2">
        <Button
          type="button"
          variant="outline"
          onClick={() => dryRunMutation.mutate()}
          disabled={dryRunMutation.isPending}
        >
          {dryRunMutation.isPending ? (
            <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
          ) : (
            <RefreshCcw className="mr-1.5 h-3.5 w-3.5" />
          )}
          Dry Run
        </Button>
        <Button
          type="button"
          onClick={() => saveMutation.mutate()}
          disabled={isReadOnly || saveMutation.isPending}
        >
          {saveMutation.isPending ? (
            <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
          ) : (
            <Save className="mr-1.5 h-3.5 w-3.5" />
          )}
          Save Boot Plan
        </Button>
        <Button
          type="button"
          variant="ghost"
          onClick={() => deleteMutation.mutate()}
          disabled={isReadOnly || !hasBootPlanContent(draft) || deleteMutation.isPending}
        >
          {deleteMutation.isPending ? (
            <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
          ) : (
            <Trash2 className="mr-1.5 h-3.5 w-3.5" />
          )}
          Clear Plan
        </Button>
        <div className="ml-auto text-xs text-fg-muted">
          {isDirty ? "Draft has unsaved changes." : "Draft matches saved plan."}
        </div>
      </div>

      {saveError ? (
        <div className="rounded-[10px] border border-red-500/30 bg-red-500/10 px-3 py-3 text-xs text-red-100">
          {saveError}
        </div>
      ) : null}

      <AgentBootPlanEditor plan={draft} onChange={setDraft} readOnly={isReadOnly} />

      {preview ? (
        <div className="rounded-[12px] border border-border-subtle bg-bg-elevated p-4">
          <div className="mb-3">
            <h4 className="text-[14px] font-semibold text-fg">Dry Run Preview</h4>
            <p className="mt-1 text-[12px] text-fg-muted">
              Dry-run shows the normalized plan, redactions, and which callbacks remain preview-only.
            </p>
          </div>
          <AgentBootPlanPreview preview={preview} />
        </div>
      ) : null}
    </div>
  );
}
