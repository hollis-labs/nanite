import {
  Check,
  Pencil,
  Plus,
  Trash2,
  X,
  Zap,
} from "lucide-react";
import { useCallback, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Kbd, KbdGroup } from "@/components/ui/kbd";
import { formatKeys } from "./ShortcutsPanel";
import { api } from "@/lib/api";
import type { CustomAction } from "@/lib/types";
import { AUTO_TRIGGER_OPTIONS } from "@/lib/types";

// ── Action Form (create / edit) ──────────────────────────────────────────

interface ActionFormData {
  name: string;
  description: string;
  keybinding: string;
  command: string;
  slash_command: string;
  auto_triggers: string[];
  enabled: boolean;
}

const emptyForm: ActionFormData = {
  name: "",
  description: "",
  keybinding: "",
  command: "",
  slash_command: "",
  auto_triggers: [],
  enabled: true,
};

function parseAutoTriggers(raw: string): string[] {
  try {
    const arr = JSON.parse(raw);
    return Array.isArray(arr) ? arr : [];
  } catch {
    return [];
  }
}

function ActionForm({
  initial,
  onSave,
  onCancel,
  saving,
}: {
  initial: ActionFormData;
  onSave: (data: ActionFormData) => void;
  onCancel: () => void;
  saving: boolean;
}) {
  const [form, setForm] = useState<ActionFormData>(initial);
  const [capturingKey, setCapturingKey] = useState(false);

  const update = <K extends keyof ActionFormData>(key: K, value: ActionFormData[K]) =>
    setForm((prev) => ({ ...prev, [key]: value }));

  const toggleTrigger = (value: string) => {
    setForm((prev) => ({
      ...prev,
      auto_triggers: prev.auto_triggers.includes(value)
        ? prev.auto_triggers.filter((t) => t !== value)
        : [...prev.auto_triggers, value],
    }));
  };

  const handleKeyCapture = useCallback(
    (e: React.KeyboardEvent) => {
      if (!capturingKey) return;
      e.preventDefault();
      e.stopPropagation();
      if (["Meta", "Control", "Shift", "Alt"].includes(e.key)) return;
      if (e.key === "Escape") {
        setCapturingKey(false);
        return;
      }
      const parts: string[] = [];
      if (e.metaKey || e.ctrlKey) parts.push("mod");
      if (e.shiftKey) parts.push("shift");
      if (e.altKey) parts.push("alt");
      let key = e.key.toLowerCase();
      if (key === " ") key = "space";
      parts.push(key);
      update("keybinding", parts.join("+"));
      setCapturingKey(false);
    },
    [capturingKey],
  );

  const valid = form.name.trim() !== "" && form.command.trim() !== "";

  return (
    <div className="rounded-xl border border-primary/40 bg-bg-elevated overflow-hidden">
      <div className="px-4 py-3 border-b border-border/50 flex items-center justify-between">
        <span className="text-sm font-medium text-fg">
          {initial.name ? "Edit Action" : "New Action"}
        </span>
        <div className="flex items-center gap-1.5">
          <Button
            variant="ghost"
            size="icon"
            className="w-7 h-7 text-fg-faint hover:text-fg-secondary"
            onClick={onCancel}
          >
            <X className="w-3.5 h-3.5" />
          </Button>
        </div>
      </div>

      <div className="p-4 space-y-3">
        {/* Name + Enabled */}
        <div className="flex gap-3">
          <div className="flex-1">
            <label className="text-[11px] text-fg-muted mb-1 block">Name *</label>
            <input
              className="w-full bg-surface/50 border border-border rounded-md px-3 py-1.5 text-sm text-fg placeholder:text-fg-faint focus:outline-none focus:border-primary/50"
              value={form.name}
              onChange={(e) => update("name", e.target.value)}
              placeholder="My Action"
            />
          </div>
          <div className="flex flex-col items-center justify-end">
            <label className="text-[11px] text-fg-muted mb-1 block">Enabled</label>
            <button
              type="button"
              onClick={() => update("enabled", !form.enabled)}
              className={`w-9 h-5 rounded-full transition-colors relative ${
                form.enabled ? "bg-toggle-on" : "bg-surface-hover"
              }`}
            >
              <span
                className={`absolute top-0.5 w-4 h-4 rounded-full bg-white shadow transition-transform ${
                  form.enabled ? "translate-x-4" : "translate-x-0.5"
                }`}
              />
            </button>
          </div>
        </div>

        {/* Description */}
        <div>
          <label className="text-[11px] text-fg-muted mb-1 block">Description</label>
          <input
            className="w-full bg-surface/50 border border-border rounded-md px-3 py-1.5 text-sm text-fg placeholder:text-fg-faint focus:outline-none focus:border-primary/50"
            value={form.description}
            onChange={(e) => update("description", e.target.value)}
            placeholder="What this action does"
          />
        </div>

        {/* Command */}
        <div>
          <label className="text-[11px] text-fg-muted mb-1 block">Command *</label>
          <textarea
            className="w-full bg-surface/50 border border-border rounded-md px-3 py-1.5 text-sm text-fg placeholder:text-fg-faint focus:outline-none focus:border-primary/50 font-mono resize-none"
            rows={2}
            value={form.command}
            onChange={(e) => update("command", e.target.value)}
            placeholder="Text to inject into chat"
          />
        </div>

        {/* Keybinding + Slash Command */}
        <div className="grid grid-cols-2 gap-3">
          <div>
            <label className="text-[11px] text-fg-muted mb-1 block">Keybinding</label>
            <div
              tabIndex={0}
              onKeyDown={handleKeyCapture}
              onClick={() => setCapturingKey(true)}
              onBlur={() => setCapturingKey(false)}
              className={`w-full bg-surface/50 border rounded-md px-3 py-1.5 text-sm cursor-pointer flex items-center gap-2 min-h-[34px] ${
                capturingKey
                  ? "border-primary/50 ring-1 ring-primary/20"
                  : "border-border hover:border-border-subtle"
              }`}
            >
              {capturingKey ? (
                <span className="text-fg-muted italic text-xs">Press keys...</span>
              ) : form.keybinding ? (
                <KbdGroup>
                  {formatKeys(form.keybinding).map((k, i) => (
                    <Kbd key={i} className="min-w-6 h-6 px-1.5 text-[11px] font-mono">
                      {k}
                    </Kbd>
                  ))}
                </KbdGroup>
              ) : (
                <span className="text-fg-faint text-xs">Click to set</span>
              )}
              {form.keybinding && !capturingKey && (
                <button
                  onClick={(e) => {
                    e.stopPropagation();
                    update("keybinding", "");
                  }}
                  className="ml-auto text-fg-faint hover:text-fg-secondary"
                >
                  <X className="w-3 h-3" />
                </button>
              )}
            </div>
          </div>
          <div>
            <label className="text-[11px] text-fg-muted mb-1 block">Slash Command</label>
            <div className="flex items-center gap-0">
              <span className="text-sm text-fg-muted font-mono pl-1">/</span>
              <input
                className="flex-1 bg-surface/50 border border-border rounded-md px-2 py-1.5 text-sm text-fg font-mono placeholder:text-fg-faint focus:outline-none focus:border-primary/50"
                value={form.slash_command}
                onChange={(e) =>
                  update("slash_command", e.target.value.replace(/[^a-z0-9_-]/gi, "").toLowerCase())
                }
                placeholder="my-action"
              />
            </div>
          </div>
        </div>

        {/* Auto Triggers */}
        <div>
          <label className="text-[11px] text-fg-muted mb-1 block">Auto Triggers</label>
          <div className="flex gap-2 flex-wrap">
            {AUTO_TRIGGER_OPTIONS.map((opt) => (
              <button
                key={opt.value}
                type="button"
                onClick={() => toggleTrigger(opt.value)}
                className={`text-[11px] px-2.5 py-1 rounded-md border transition-colors ${
                  form.auto_triggers.includes(opt.value)
                    ? "border-success/30 bg-success/5 text-success"
                    : "border-border-subtle bg-bg-elevated text-fg-muted hover:text-fg-secondary"
                }`}
              >
                {opt.label}
              </button>
            ))}
          </div>
        </div>
      </div>

      {/* Footer */}
      <div className="border-t border-border-subtle px-4 py-2.5 flex items-center justify-end gap-2 bg-bg/40">
        <Button
          variant="ghost"
          size="sm"
          className="text-xs"
          onClick={onCancel}
        >
          Cancel
        </Button>
        <Button
          size="sm"
          className="text-xs bg-primary hover:bg-primary-hover text-white"
          disabled={!valid || saving}
          onClick={() => onSave(form)}
        >
          <Check className="w-3 h-3 mr-1" />
          {saving ? "Saving..." : "Save"}
        </Button>
      </div>
    </div>
  );
}

// ── Action Card ──────────────────────────────────────────────────────────

function ActionCard({
  action,
  onEdit,
  onDelete,
  onToggle,
}: {
  action: CustomAction;
  onEdit: () => void;
  onDelete: () => void;
  onToggle: () => void;
}) {
  const triggers = parseAutoTriggers(action.auto_triggers);

  return (
    <div
      className={`rounded-xl border border-border-subtle bg-bg-elevated overflow-hidden ${
        !action.enabled ? "opacity-45" : ""
      }`}
    >
      {/* Header */}
      <div className="px-3.5 py-3 flex items-center gap-3">
        <div className="w-9 h-9 rounded-lg bg-surface-hover text-fg-secondary flex items-center justify-center shrink-0">
          <Zap className="w-4 h-4" />
        </div>
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium text-fg truncate">{action.name}</span>
            {action.enabled && <span className="w-1.5 h-1.5 rounded-full bg-status-ok shrink-0" />}
          </div>
          {action.description && (
            <div className="text-[11px] text-fg-muted mt-0.5 truncate">{action.description}</div>
          )}
        </div>
        <div className="flex items-center gap-1 shrink-0">
          <Button
            variant="ghost"
            size="icon"
            className="w-7 h-7 text-fg-faint hover:text-fg-secondary"
            onClick={onEdit}
          >
            <Pencil className="w-3.5 h-3.5" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="w-7 h-7 text-fg-faint hover:text-primary"
            onClick={onDelete}
          >
            <Trash2 className="w-3.5 h-3.5" />
          </Button>
          <button
            onClick={onToggle}
            className={`w-8 h-[18px] rounded-full transition-colors relative ml-1 ${
              action.enabled ? "bg-toggle-on" : "bg-surface-hover"
            }`}
          >
            <span
              className={`absolute top-[1px] w-4 h-4 rounded-full bg-white shadow transition-transform ${
                action.enabled ? "translate-x-[14px]" : "translate-x-[1px]"
              }`}
            />
          </button>
        </div>
      </div>

      {/* Detail footer */}
      <div className="border-t border-border-subtle px-3.5 py-2 bg-bg/40 flex items-center gap-3 flex-wrap">
        {action.keybinding && (
          <KbdGroup>
            {formatKeys(action.keybinding).map((k, i) => (
              <Kbd key={i} className="min-w-5 h-5 px-1 text-[10px] font-mono">
                {k}
              </Kbd>
            ))}
          </KbdGroup>
        )}
        {action.slash_command && (
          <span className="text-[10px] font-mono text-primary">/{action.slash_command}</span>
        )}
        {triggers.length > 0 &&
          triggers.map((t) => (
            <span
              key={t}
              className="text-[10px] px-1.5 py-0.5 rounded-md bg-bg-elevated border border-border-subtle text-fg-muted leading-none"
            >
              {AUTO_TRIGGER_OPTIONS.find((o) => o.value === t)?.label ?? t}
            </span>
          ))}
        <span className="text-[10px] text-fg-faint font-mono ml-auto truncate max-w-40">
          {action.command.length > 40 ? `${action.command.slice(0, 40)}...` : action.command}
        </span>
      </div>
    </div>
  );
}

// ── Main Panel ───────────────────────────────────────────────────────────

export function ActionsPanel() {
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState<string | "new" | null>(null);

  const { data, isLoading } = useQuery({
    queryKey: ["actions"],
    queryFn: () => api.listActions(),
  });
  const actions = data?.actions ?? [];

  const createMutation = useMutation({
    mutationFn: (form: ActionFormData) =>
      api.createAction({
        ...form,
        auto_triggers: JSON.stringify(form.auto_triggers),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["actions"] });
      setEditing(null);
    },
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, form }: { id: string; form: ActionFormData }) =>
      api.updateAction(id, {
        ...form,
        auto_triggers: JSON.stringify(form.auto_triggers),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["actions"] });
      setEditing(null);
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.deleteAction(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["actions"] });
    },
  });

  const toggleMutation = useMutation({
    mutationFn: (action: CustomAction) =>
      api.updateAction(action.id, { enabled: !action.enabled }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["actions"] });
    },
  });

  const handleSave = useCallback(
    (form: ActionFormData) => {
      if (editing === "new") {
        createMutation.mutate(form);
      } else if (editing) {
        updateMutation.mutate({ id: editing, form });
      }
    },
    [editing, createMutation, updateMutation],
  );

  const editingAction = editing && editing !== "new" ? actions.find((a) => a.id === editing) : null;

  return (
    <div className="space-y-4">
      {/* Toolbar */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Zap className="w-4 h-4 text-fg-muted" />
          <p className="text-xs text-fg-muted">
            Custom actions with keybindings, slash commands, and auto-triggers
          </p>
        </div>
        <Button
          size="sm"
          className="text-xs bg-primary hover:bg-primary-hover text-white"
          onClick={() => setEditing("new")}
          disabled={editing !== null}
        >
          <Plus className="w-3 h-3 mr-1" />
          New Action
        </Button>
      </div>

      {/* Create form */}
      {editing === "new" && (
        <ActionForm
          initial={emptyForm}
          onSave={handleSave}
          onCancel={() => setEditing(null)}
          saving={createMutation.isPending}
        />
      )}

      {/* Edit form */}
      {editingAction && (
        <ActionForm
          initial={{
            name: editingAction.name,
            description: editingAction.description,
            keybinding: editingAction.keybinding,
            command: editingAction.command,
            slash_command: editingAction.slash_command,
            auto_triggers: parseAutoTriggers(editingAction.auto_triggers),
            enabled: editingAction.enabled,
          }}
          onSave={handleSave}
          onCancel={() => setEditing(null)}
          saving={updateMutation.isPending}
        />
      )}

      {/* Action cards */}
      {isLoading ? (
        <div className="grid grid-cols-2 gap-3">
          {[1, 2].map((i) => (
            <div
              key={i}
              className="rounded-xl border border-border-subtle bg-bg-elevated h-24 animate-pulse"
            />
          ))}
        </div>
      ) : actions.length === 0 && editing === null ? (
        <div className="text-center py-12">
          <Zap className="w-8 h-8 text-fg-faint mx-auto mb-2" />
          <p className="text-sm text-fg-muted">No custom actions yet</p>
          <p className="text-xs text-fg-faint mt-1">
            Create actions to bind keybindings, slash commands, and auto-triggers
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-2 gap-3">
          {actions
            .filter((a) => a.id !== editing)
            .map((action) => (
              <ActionCard
                key={action.id}
                action={action}
                onEdit={() => setEditing(action.id)}
                onDelete={() => deleteMutation.mutate(action.id)}
                onToggle={() => toggleMutation.mutate(action)}
              />
            ))}
        </div>
      )}
    </div>
  );
}
