import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Check,
  Loader2,
  Pencil,
  Plus,
  ShieldAlert,
  TestTube2,
  Trash2,
  X,
  XCircle,
} from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type {
  AgentReflexActionKind,
  AgentReflexRow,
  AgentReflexStatus,
  AgentReflexTriggerKind,
  CreateAgentReflexRequest,
  PatchAgentReflexRequest,
  PendingReflexRow,
  ValidateReflexResponse,
} from "@/lib/types";

const TRIGGER_KIND_OPTIONS: AgentReflexTriggerKind[] = ["predicate", "event", "interval"];
const ACTION_KIND_OPTIONS: AgentReflexActionKind[] = [
  "inject_reminder",
  "halt_session",
  "force_tool_choice",
  "send_message",
  "add_schedule",
];
const STATUS_OPTIONS: AgentReflexStatus[] = ["active", "paused", "expired"];

type EditorState = {
  id?: string;
  name: string;
  trigger_kind: AgentReflexTriggerKind;
  trigger_spec: string;
  action_kind: AgentReflexActionKind;
  action_spec: string;
  priority: string;
  status: AgentReflexStatus;
  session_id: string;
  synthetic_state: string;
};

const EMPTY_EDITOR: EditorState = {
  name: "",
  trigger_kind: "predicate",
  trigger_spec: '{"kind":"tool_calls_window","window":2,"op":"=","value":0}',
  action_kind: "inject_reminder",
  action_spec: '{"body":"Ground yourself before responding.","urgency":"warn"}',
  priority: "50",
  status: "active",
  session_id: "",
  synthetic_state: "",
};

export function AgentReflexesPanel({
  agentId,
  isReadOnly,
}: {
  agentId: string;
  isReadOnly: boolean;
}) {
  const queryClient = useQueryClient();
  const [editor, setEditor] = useState<EditorState | null>(null);
  const [validation, setValidation] = useState<ValidateReflexResponse | null>(null);
  const [localError, setLocalError] = useState<string | null>(null);

  const reflexesQuery = useQuery({
    queryKey: ["agent-reflexes", agentId],
    queryFn: () => api.listAgentReflexes(agentId),
  });

  const pendingQuery = useQuery({
    queryKey: ["pending-reflexes", agentId],
    queryFn: async () => {
      const rows = await api.listPendingReflexes("pending");
      return rows.filter((row) => row.target_agent_id === "" || row.target_agent_id === agentId);
    },
  });

  const createMutation = useMutation({
    mutationFn: (payload: CreateAgentReflexRequest) => api.createAgentReflex(agentId, payload),
    onSuccess: () => {
      setEditor(null);
      setValidation(null);
      setLocalError(null);
      void queryClient.invalidateQueries({ queryKey: ["agent-reflexes", agentId] });
    },
    onError: (error) => setLocalError(errorMessage(error)),
  });

  const updateMutation = useMutation({
    mutationFn: ({ reflexId, payload }: { reflexId: string; payload: PatchAgentReflexRequest }) =>
      api.patchAgentReflex(agentId, reflexId, payload),
    onSuccess: () => {
      setEditor(null);
      setValidation(null);
      setLocalError(null);
      void queryClient.invalidateQueries({ queryKey: ["agent-reflexes", agentId] });
    },
    onError: (error) => setLocalError(errorMessage(error)),
  });

  const deleteMutation = useMutation({
    mutationFn: (reflexId: string) => api.deleteAgentReflex(agentId, reflexId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["agent-reflexes", agentId] });
    },
  });

  const validateMutation = useMutation({
    mutationFn: api.validateReflex,
    onSuccess: (result) => {
      setValidation(result);
      setLocalError(null);
    },
    onError: (error) => setLocalError(errorMessage(error)),
  });

  const approveMutation = useMutation({
    mutationFn: (pendingId: string) =>
      api.approvePendingReflex(pendingId, { reviewed_by: "operator-ui" }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["agent-reflexes", agentId] });
      void queryClient.invalidateQueries({ queryKey: ["pending-reflexes", agentId] });
    },
  });

  const rejectMutation = useMutation({
    mutationFn: (pendingId: string) =>
      api.rejectPendingReflex(pendingId, { reviewed_by: "operator-ui" }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["pending-reflexes", agentId] });
    },
  });

  const inherited = useMemo(
    () => (reflexesQuery.data ?? []).filter((row) => isInherited(row)),
    [reflexesQuery.data],
  );
  const scoped = useMemo(
    () => (reflexesQuery.data ?? []).filter((row) => !isInherited(row)),
    [reflexesQuery.data],
  );

  const submitEditor = () => {
    if (!editor) return;
    const priority = Number.parseInt(editor.priority, 10);
    const payload = {
      name: editor.name.trim(),
      trigger_kind: editor.trigger_kind,
      trigger_spec: editor.trigger_spec.trim(),
      action_kind: editor.action_kind,
      action_spec: editor.action_spec.trim(),
      priority: Number.isNaN(priority) ? 0 : priority,
      status: editor.status,
    };
    setLocalError(null);
    if (editor.id) {
      updateMutation.mutate({ reflexId: editor.id, payload });
      return;
    }
    createMutation.mutate(payload);
  };

  const runValidation = () => {
    if (!editor) return;
    let state: Record<string, unknown> | undefined;
    if (editor.synthetic_state.trim()) {
      try {
        state = JSON.parse(editor.synthetic_state) as Record<string, unknown>;
      } catch {
        setValidation({
          valid: false,
          errors: ["Synthetic state must be valid JSON."],
          fired: false,
          state_source: "request",
          state_summary: {
            messages: 0,
            user_messages: 0,
            events: 0,
            mail_unread_count: 0,
            tick_n: 0,
            prefix_tokens: 0,
          },
        });
        return;
      }
    }
    validateMutation.mutate({
      trigger_kind: editor.trigger_kind,
      trigger_spec: editor.trigger_spec.trim(),
      action_kind: editor.action_kind,
      action_spec: editor.action_spec.trim(),
      agent_id: agentId,
      session_id: editor.session_id.trim() || undefined,
      state,
    });
  };

  const openCreate = () => {
    setEditor({ ...EMPTY_EDITOR });
    setValidation(null);
    setLocalError(null);
  };

  const openEdit = (row: AgentReflexRow) => {
    setEditor({
      id: row.id,
      name: row.name,
      trigger_kind: row.trigger_kind,
      trigger_spec: row.trigger_spec,
      action_kind: row.action_kind,
      action_spec: row.action_spec,
      priority: String(row.priority),
      status: row.status,
      session_id: "",
      synthetic_state: "",
    });
    setValidation(null);
    setLocalError(null);
  };

  return (
    <div className="space-y-4">
      <SectionCard
        title={`Active Reflexes (${(reflexesQuery.data ?? []).length})`}
        action={
          <Button
            size="sm"
            variant="ghost"
            className="h-7 gap-1 text-xs"
            onClick={openCreate}
            disabled={isReadOnly}
          >
            <Plus className="w-3.5 h-3.5" />
            New Reflex
          </Button>
        }
      >
        {isReadOnly ? (
          <div className="rounded-md border border-border-subtle bg-surface/50 px-3 py-2 text-xs text-fg-muted">
            This profile is file-backed. Reflex management is disabled in this surface.
          </div>
        ) : null}
        {reflexesQuery.isLoading ? <PanelMessage>Loading reflexes...</PanelMessage> : null}
        {reflexesQuery.error ? (
          <PanelError message={`Failed to load reflexes: ${errorMessage(reflexesQuery.error)}`} />
        ) : null}
        {!reflexesQuery.isLoading && !reflexesQuery.error && (reflexesQuery.data?.length ?? 0) === 0 ? (
          <PanelMessage>No reflexes configured for this agent yet.</PanelMessage>
        ) : null}
        {inherited.map((row) => (
          <ReflexRowCard key={row.id} row={row} inherited />
        ))}
        {scoped.map((row) => (
          <ReflexRowCard
            key={row.id}
            row={row}
            inherited={false}
            actions={
              <div className="flex items-center gap-1">
                <Button
                  type="button"
                  size="icon"
                  variant="ghost"
                  className="h-7 w-7"
                  onClick={() => openEdit(row)}
                  disabled={isReadOnly}
                  title="Edit reflex"
                  aria-label={`Edit reflex ${row.name}`}
                >
                  <Pencil className="w-3.5 h-3.5" />
                </Button>
                <Button
                  type="button"
                  size="icon"
                  variant="ghost"
                  className="h-7 w-7 text-danger hover:text-danger"
                  onClick={() => deleteMutation.mutate(row.id)}
                  disabled={isReadOnly || deleteMutation.isPending}
                  title="Delete reflex"
                  aria-label={`Delete reflex ${row.name}`}
                >
                  <Trash2 className="w-3.5 h-3.5" />
                </Button>
              </div>
            }
          />
        ))}
      </SectionCard>

      {editor ? (
        <SectionCard
          title={editor.id ? "Edit Reflex" : "New Reflex"}
          action={
            <Button
              size="icon"
              variant="ghost"
              className="h-7 w-7"
              onClick={() => {
                setEditor(null);
                setValidation(null);
                setLocalError(null);
              }}
              aria-label="Close reflex editor"
            >
              <X className="w-3.5 h-3.5" />
            </Button>
          }
        >
          <div className="grid gap-3 md:grid-cols-2">
            <Field label="Name">
              <Input
                value={editor.name}
                onChange={(event) => setEditor({ ...editor, name: event.target.value })}
                placeholder="clean-status-without-tools"
              />
            </Field>
            <Field label="Priority">
              <Input
                value={editor.priority}
                onChange={(event) => setEditor({ ...editor, priority: event.target.value })}
                inputMode="numeric"
              />
            </Field>
            <Field label="Trigger Kind">
              <select
                className="h-9 rounded-md border border-input bg-transparent px-3 text-sm"
                value={editor.trigger_kind}
                onChange={(event) =>
                  setEditor({ ...editor, trigger_kind: event.target.value as AgentReflexTriggerKind })
                }
              >
                {TRIGGER_KIND_OPTIONS.map((option) => (
                  <option key={option} value={option}>
                    {option}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="Status">
              <select
                className="h-9 rounded-md border border-input bg-transparent px-3 text-sm"
                value={editor.status}
                onChange={(event) =>
                  setEditor({ ...editor, status: event.target.value as AgentReflexStatus })
                }
              >
                {STATUS_OPTIONS.map((option) => (
                  <option key={option} value={option}>
                    {option}
                  </option>
                ))}
              </select>
            </Field>
          </div>

          <Field label="Trigger Spec">
            <Textarea
              value={editor.trigger_spec}
              onChange={(event) => setEditor({ ...editor, trigger_spec: event.target.value })}
              rows={5}
            />
          </Field>

          <Field label="Action Kind">
            <select
              className="h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm"
              value={editor.action_kind}
              onChange={(event) =>
                setEditor({ ...editor, action_kind: event.target.value as AgentReflexActionKind })
              }
            >
              {ACTION_KIND_OPTIONS.map((option) => (
                <option key={option} value={option}>
                  {option}
                </option>
              ))}
            </select>
          </Field>

          <Field label="Action Spec">
            <Textarea
              value={editor.action_spec}
              onChange={(event) => setEditor({ ...editor, action_spec: event.target.value })}
              rows={5}
            />
          </Field>

          <div className="grid gap-3 md:grid-cols-2">
            <Field label="Validation Session ID">
              <Input
                value={editor.session_id}
                onChange={(event) => setEditor({ ...editor, session_id: event.target.value })}
                placeholder="Optional live session for store-backed dry-run"
              />
            </Field>
            <Field label="Synthetic State JSON">
              <Textarea
                value={editor.synthetic_state}
                onChange={(event) => setEditor({ ...editor, synthetic_state: event.target.value })}
                rows={3}
                placeholder='Optional synthetic state, for example {"messages":[{"tool_calls":0}]}'
              />
            </Field>
          </div>

          {localError ? <PanelError message={localError} /> : null}
          {validation ? <ValidationCard result={validation} /> : null}

          <div className="flex flex-wrap items-center gap-2">
            <Button
              type="button"
              variant="secondary"
              size="sm"
              className="gap-1"
              onClick={runValidation}
              disabled={validateMutation.isPending}
            >
              {validateMutation.isPending ? (
                <Loader2 className="w-3.5 h-3.5 animate-spin" />
              ) : (
                <TestTube2 className="w-3.5 h-3.5" />
              )}
              Validate
            </Button>
            <Button
              type="button"
              size="sm"
              className="gap-1"
              onClick={submitEditor}
              disabled={createMutation.isPending || updateMutation.isPending}
            >
              {createMutation.isPending || updateMutation.isPending ? (
                <Loader2 className="w-3.5 h-3.5 animate-spin" />
              ) : (
                <Check className="w-3.5 h-3.5" />
              )}
              {editor.id ? "Save Changes" : "Create Reflex"}
            </Button>
          </div>
        </SectionCard>
      ) : null}

      <SectionCard title={`Pending Review (${pendingQuery.data?.length ?? 0})`}>
        {pendingQuery.isLoading ? <PanelMessage>Loading pending reflexes...</PanelMessage> : null}
        {pendingQuery.error ? (
          <PanelError
            message={`Failed to load pending reflexes: ${errorMessage(pendingQuery.error)}`}
          />
        ) : null}
        {!pendingQuery.isLoading && !pendingQuery.error && (pendingQuery.data?.length ?? 0) === 0 ? (
          <PanelMessage>No pending reflex proposals for this agent.</PanelMessage>
        ) : null}
        {(pendingQuery.data ?? []).map((row) => (
          <PendingReflexCard
            key={row.id}
            row={row}
            onApprove={() => approveMutation.mutate(row.id)}
            onReject={() => rejectMutation.mutate(row.id)}
            busy={isReadOnly || approveMutation.isPending || rejectMutation.isPending}
          />
        ))}
      </SectionCard>
    </div>
  );
}

function ReflexRowCard({
  row,
  inherited,
  actions,
}: {
  row: AgentReflexRow;
  inherited: boolean;
  actions?: React.ReactNode;
}) {
  return (
    <div className="rounded-lg border border-border-subtle bg-surface/40 px-3 py-3">
      <div className="flex items-start gap-3">
        <div className="flex-1 min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm font-medium text-fg">{row.name}</span>
            <ScopePill inherited={inherited} />
            <StatusPill status={row.status} />
            <span className="text-[11px] text-fg-muted">P{row.priority}</span>
          </div>
          <div className="mt-1 text-xs text-fg-muted">
            {inherited ? `Class base: ${row.class_tag || "shared"}` : `Agent scoped: ${row.agent_id}`}
          </div>
        </div>
        {actions}
      </div>
      <div className="mt-3 grid gap-3 md:grid-cols-2">
        <SpecBlock label={`Trigger · ${row.trigger_kind}`} value={row.trigger_spec} />
        <SpecBlock label={`Action · ${row.action_kind}`} value={row.action_spec} />
      </div>
      <div className="mt-3 flex flex-wrap items-center gap-3 text-[11px] text-fg-muted">
        <span>Fired {row.fired_count} times</span>
        <span>Last fired {row.last_fired_at || "never"}</span>
        <span>Created by {row.created_by || "unknown"}</span>
      </div>
    </div>
  );
}

function PendingReflexCard({
  row,
  onApprove,
  onReject,
  busy,
}: {
  row: PendingReflexRow;
  onApprove: () => void;
  onReject: () => void;
  busy: boolean;
}) {
  return (
    <div className="rounded-lg border border-border-subtle bg-surface/40 px-3 py-3">
      <div className="flex items-start gap-3">
        <div className="flex-1 min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm font-medium text-fg">{row.name}</span>
            <span className="rounded-full bg-amber-500/10 px-2 py-0.5 text-[11px] font-medium text-amber-700">
              Pending
            </span>
          </div>
          <div className="mt-1 text-xs text-fg-muted">
            Proposed by {row.proposed_by} for {row.target_agent_id || "shared scope"}
          </div>
          <p className="mt-2 text-xs text-fg-secondary">{row.rationale}</p>
        </div>
        <div className="flex items-center gap-1">
          <Button
            type="button"
            size="sm"
            variant="secondary"
            className="gap-1"
            onClick={onApprove}
            disabled={busy}
          >
            <Check className="w-3.5 h-3.5" />
            Approve
          </Button>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            className="gap-1 text-danger hover:text-danger"
            onClick={onReject}
            disabled={busy}
          >
            <XCircle className="w-3.5 h-3.5" />
            Reject
          </Button>
        </div>
      </div>
      <div className="mt-3 grid gap-3 md:grid-cols-2">
        <SpecBlock label={`Trigger · ${row.trigger_kind}`} value={row.trigger_spec} />
        <SpecBlock label={`Action · ${row.action_kind}`} value={row.action_spec} />
      </div>
    </div>
  );
}

function ValidationCard({ result }: { result: ValidateReflexResponse }) {
  return (
    <div className="rounded-lg border border-border-subtle bg-surface/40 px-3 py-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-medium text-fg">Validation Result</span>
        <span
          className={`rounded-full px-2 py-0.5 text-[11px] font-medium ${
            result.valid ? "bg-emerald-500/10 text-emerald-700" : "bg-rose-500/10 text-rose-700"
          }`}
        >
          {result.valid ? "Valid" : "Invalid"}
        </span>
        <span
          className={`rounded-full px-2 py-0.5 text-[11px] font-medium ${
            result.fired ? "bg-sky-500/10 text-sky-700" : "bg-surface text-fg-muted"
          }`}
        >
          {result.fired ? "Would fire" : "Would not fire"}
        </span>
        <span className="text-[11px] text-fg-muted">State source: {result.state_source}</span>
      </div>
      {result.errors.length > 0 ? (
        <ul className="mt-2 space-y-1 text-xs text-danger">
          {result.errors.map((error) => (
            <li key={error}>{error}</li>
          ))}
        </ul>
      ) : null}
      <div className="mt-3 flex flex-wrap gap-3 text-[11px] text-fg-muted">
        <span>Messages: {result.state_summary.messages}</span>
        <span>User messages: {result.state_summary.user_messages}</span>
        <span>Events: {result.state_summary.events}</span>
        <span>Unread mail: {result.state_summary.mail_unread_count}</span>
      </div>
    </div>
  );
}

function SectionCard({
  title,
  action,
  children,
}: {
  title: string;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="rounded-xl border border-border-subtle bg-bg-elevated overflow-hidden">
      <div className="flex items-center gap-2 border-b border-border-subtle px-4 py-3">
        <ShieldAlert className="w-4 h-4 text-fg-muted" />
        <span className="text-sm font-medium text-fg">{title}</span>
        <div className="flex-1" />
        {action}
      </div>
      <div className="space-y-3 p-4">{children}</div>
    </div>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="block space-y-1.5">
      <span className="text-xs font-medium text-fg-secondary">{label}</span>
      {children}
    </label>
  );
}

function SpecBlock({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <div className="mb-1 text-[11px] font-medium text-fg-secondary">{label}</div>
      <pre className="overflow-x-auto rounded-md bg-bg px-2 py-2 text-[11px] text-fg-secondary">
        {value}
      </pre>
    </div>
  );
}

function PanelMessage({ children }: { children: ReactNode }) {
  return <div className="text-xs text-fg-muted">{children}</div>;
}

function PanelError({ message }: { message: string }) {
  return <div className="text-xs text-danger">{message}</div>;
}

function ScopePill({ inherited }: { inherited: boolean }) {
  return (
    <span
      className={`rounded-full px-2 py-0.5 text-[11px] font-medium ${
        inherited ? "bg-slate-500/10 text-slate-700" : "bg-emerald-500/10 text-emerald-700"
      }`}
    >
      {inherited ? "Inherited" : "Scoped"}
    </span>
  );
}

function StatusPill({ status }: { status: string }) {
  return (
    <span className="rounded-full bg-surface px-2 py-0.5 text-[11px] font-medium text-fg-secondary">
      {status}
    </span>
  );
}

function isInherited(row: AgentReflexRow) {
  return row.agent_id === "";
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : "Unknown error";
}
