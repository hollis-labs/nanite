import {
  AlertCircle,
  ChevronLeft,
  Code2,
  Edit,
  Eye,
  FileText,
  Loader2,
  Plus,
  Trash2,
  X,
} from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { DynamicIcon, IconPicker } from "@/components/ui/icon-picker";
import type { PromptTemplate, TemplateVariable } from "@/lib/types";
import { SystemPromptEditor } from "./editors/SystemPromptEditor";

// ─── Helpers ────────────────────────────────────────────────────────

function parseVariables(s: string): TemplateVariable[] {
  try {
    return JSON.parse(s);
  } catch {
    return [];
  }
}

function getScopeBadgeColor(scope: string) {
  switch (scope) {
    case "system":
      return "bg-blue-500";
    case "mode":
      return "bg-green-500";
    case "skill":
      return "bg-yellow-500";
    case "context":
      return "bg-accent";
    default:
      return "bg-gray-500";
  }
}

// ─── Props ──────────────────────────────────────────────────────────

export interface PromptDetailViewProps {
  template: PromptTemplate;
  onUpdate: (id: string, data: Partial<PromptTemplate>) => void;
  onDelete: (id: string) => void;
  isDeleting?: boolean;
  onBack: () => void;
  previewVariables: Record<string, any>;
  onPreviewVariablesChange: (vars: Record<string, any>) => void;
  renderedPreview: string;
}

// ─── Component ──────────────────────────────────────────────────────

export function PromptDetailView({
  template,
  onUpdate,
  onDelete,
  isDeleting,
  onBack,
  previewVariables,
  onPreviewVariablesChange,
  renderedPreview,
}: PromptDetailViewProps) {
  const [editField, setEditField] = useState<string | null>(null);
  const [editValue, setEditValue] = useState("");
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);

  const variables = useMemo(() => parseVariables(template.variables), [template.variables]);

  const startEditing = useCallback((field: string, value: string) => {
    setEditField(field);
    setEditValue(value);
  }, []);

  const saveField = useCallback(
    (field: string) => {
      onUpdate(template.id, { [field]: field === "priority" ? parseInt(editValue) || 0 : editValue });
      setEditField(null);
      setEditValue("");
    },
    [editValue, onUpdate, template.id],
  );

  const cancelEditing = useCallback(() => {
    setEditField(null);
    setEditValue("");
  }, []);

  const inputClass =
    "w-full px-2 py-1 bg-bg-elevated border border-border-subtle rounded-md text-xs text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent";

  // ─── Inline editable row ──────────────────────────────────────────

  const editableRow = (
    field: string,
    label: string,
    value: string,
    opts?: {
      type?: "input" | "select" | "number";
      selectOptions?: { id: string; label: string }[];
      readOnly?: boolean;
    },
  ) => {
    if (opts?.readOnly) {
      return (
        <div className="flex items-center gap-3 py-1.5">
          <span className="text-xs text-fg-muted w-28 shrink-0">{label}</span>
          <span className="text-xs text-fg-secondary flex-1 truncate">{value || "\u2014"}</span>
        </div>
      );
    }

    const type = opts?.type || "input";
    if (editField === field) {
      const handleSave = () => saveField(field);
      const handleKeyDown = (e: React.KeyboardEvent) => {
        if (e.key === "Enter") handleSave();
        if (e.key === "Escape") cancelEditing();
      };
      return (
        <div className="flex items-center gap-3 py-1.5">
          <span className="text-xs text-fg-muted w-28 shrink-0">{label}</span>
          {type === "select" ? (
            <select
              autoFocus
              value={editValue}
              onChange={(e) => {
                setEditValue(e.target.value);
                onUpdate(template.id, { [field]: e.target.value });
                setEditField(null);
              }}
              onBlur={handleSave}
              onKeyDown={handleKeyDown}
              className={`${inputClass} flex-1`}
            >
              {(opts?.selectOptions || []).map((o) => (
                <option key={o.id} value={o.id}>
                  {o.label}
                </option>
              ))}
            </select>
          ) : (
            <input
              autoFocus
              type={type === "number" ? "number" : "text"}
              value={editValue}
              onChange={(e) => setEditValue(e.target.value)}
              onBlur={handleSave}
              onKeyDown={handleKeyDown}
              className={`${inputClass} flex-1`}
            />
          )}
        </div>
      );
    }
    return (
      <div
        className="flex items-center gap-3 py-1.5 group cursor-pointer rounded px-1 -mx-1 hover:bg-surface/40 transition-colors"
        onClick={() => startEditing(field, value)}
      >
        <span className="text-xs text-fg-muted w-28 shrink-0">{label}</span>
        <span className="text-xs text-fg-secondary flex-1 truncate">{value || "\u2014"}</span>
      </div>
    );
  };

  const isBuiltin = template.is_builtin;
  const scopeOptions = [
    { id: "system", label: "System" },
    { id: "mode", label: "Mode" },
    { id: "skill", label: "Skill" },
    { id: "context", label: "Context" },
  ];

  return (
    <div className="space-y-5">
      {/* ── Header ───────────────────────────────────────────────── */}
      <div className="flex items-center gap-3">
        <Button variant="ghost" size="icon" onClick={onBack}>
          <ChevronLeft className="w-4 h-4" />
        </Button>
        <div className="flex items-center gap-3 flex-1 min-w-0">
          <div className="size-10 rounded-sm bg-surface flex items-center justify-center shrink-0">
            <DynamicIcon
              name={template.icon}
              className="w-5 h-5 text-fg-secondary"
              fallback={FileText}
            />
          </div>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <h2 className="text-xl font-semibold text-fg truncate">{template.name}</h2>
              <div className={`w-2.5 h-2.5 rounded-full ${getScopeBadgeColor(template.scope)}`} />
              {isBuiltin && (
                <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-blue-500/10 text-blue-400 leading-none">
                  builtin
                </span>
              )}
            </div>
            <p className="text-xs text-fg-muted font-mono truncate">{template.slug}</p>
          </div>
        </div>
        {!isBuiltin && (
          <div className="flex gap-1.5">
            <Button
              onClick={() => setShowDeleteConfirm(true)}
              variant="ghost"
              size="icon"
              className="text-fg-muted hover:text-accent"
            >
              <Trash2 className="w-4 h-4" />
            </Button>
          </div>
        )}
      </div>

      {/* ── Two-column layout ────────────────────────────────────── */}
      <div className="grid gap-5 lg:grid-cols-2">
        {/* Left column */}
        <div className="space-y-4">
          {/* Details card */}
          <Card>
            <CardHeader>
              <Code2 className="w-4 h-4" />
              <span>Details</span>
            </CardHeader>
            <div className="px-4 pb-4 space-y-0.5">
              {isBuiltin
                ? editableRow("name", "Name", template.name, { readOnly: true })
                : editableRow("name", "Name", template.name)}
              {isBuiltin
                ? editableRow("slug", "Slug", template.slug, { readOnly: true })
                : editableRow("slug", "Slug", template.slug)}
              {isBuiltin
                ? editableRow("scope", "Scope", template.scope, { readOnly: true })
                : editableRow("scope", "Scope", template.scope, {
                    type: "select",
                    selectOptions: scopeOptions,
                  })}
              {isBuiltin
                ? editableRow("priority", "Priority", String(template.priority), { readOnly: true })
                : editableRow("priority", "Priority", String(template.priority), { type: "number" })}
              <div className="flex items-center gap-3 py-1.5">
                <span className="text-xs text-fg-muted w-28 shrink-0">Type</span>
                <span className="text-xs text-fg-secondary">{isBuiltin ? "Built-in" : "Custom"}</span>
              </div>
              {!isBuiltin && (
                <div className="flex items-center gap-3 py-1.5">
                  <span className="text-xs text-fg-muted w-28 shrink-0">Icon</span>
                  <IconPicker
                    value={template.icon || ""}
                    onChange={(iconName) => onUpdate(template.id, { icon: iconName })}
                  />
                </div>
              )}
            </div>
          </Card>

          {/* Variables card */}
          <Card>
            <CardHeader>
              <Edit className="w-4 h-4" />
              <span>Variables ({variables.length})</span>
            </CardHeader>
            <div className="px-4 pb-4">
              <VariablesList
                value={template.variables}
                onChange={(json) => onUpdate(template.id, { variables: json })}
                readOnly={isBuiltin}
              />
            </div>
          </Card>
        </div>

        {/* Right column */}
        <div className="space-y-4">
          {/* Prompt Body card */}
          <Card>
            <div className="p-4">
              <SystemPromptEditor
                value={template.template}
                onChange={(v) => onUpdate(template.id, { template: v })}
              />
            </div>
          </Card>

          {/* Live Preview card */}
          <Card>
            <CardHeader>
              <Eye className="w-4 h-4" />
              <span>Live Preview</span>
            </CardHeader>
            <div className="px-4 pb-4 space-y-3">
              {/* Variable inputs */}
              {variables.length > 0 && (
                <div className="space-y-2.5">
                  <span className="text-[11px] font-medium text-fg-muted">Sample Values</span>
                  {variables.map((variable) => (
                    <div key={variable.name}>
                      <label className="block text-[11px] text-fg-secondary mb-1">
                        {variable.name}
                        {variable.required && <span className="text-accent ml-0.5">*</span>}
                      </label>
                      {variable.type === "textarea" ? (
                        <textarea
                          rows={2}
                          value={previewVariables[variable.name] ?? variable.default ?? ""}
                          onChange={(e) =>
                            onPreviewVariablesChange({
                              ...previewVariables,
                              [variable.name]: e.target.value,
                            })
                          }
                          className="w-full px-2 py-1 bg-bg-elevated border border-border-subtle rounded-lg text-fg text-xs focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
                          placeholder={variable.description || `Enter ${variable.name}`}
                        />
                      ) : variable.type === "boolean" ? (
                        <select
                          value={
                            previewVariables[variable.name] !== undefined
                              ? String(previewVariables[variable.name])
                              : String(variable.default ?? "false")
                          }
                          onChange={(e) =>
                            onPreviewVariablesChange({
                              ...previewVariables,
                              [variable.name]: e.target.value === "true",
                            })
                          }
                          className="w-full px-2 py-1 bg-bg-elevated border border-border-subtle rounded-lg text-fg text-xs focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
                        >
                          <option value="true">true</option>
                          <option value="false">false</option>
                        </select>
                      ) : variable.type === "select" ? (
                        <select
                          value={previewVariables[variable.name] ?? variable.default ?? ""}
                          onChange={(e) =>
                            onPreviewVariablesChange({
                              ...previewVariables,
                              [variable.name]: e.target.value,
                            })
                          }
                          className="w-full px-2 py-1 bg-bg-elevated border border-border-subtle rounded-lg text-fg text-xs focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
                        >
                          {(variable.options || []).map((opt) => (
                            <option key={opt} value={opt}>
                              {opt}
                            </option>
                          ))}
                        </select>
                      ) : (
                        <input
                          type={variable.type === "number" ? "number" : "text"}
                          value={previewVariables[variable.name] ?? variable.default ?? ""}
                          onChange={(e) =>
                            onPreviewVariablesChange({
                              ...previewVariables,
                              [variable.name]: e.target.value,
                            })
                          }
                          className="w-full px-2 py-1 bg-bg-elevated border border-border-subtle rounded-lg text-fg text-xs focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
                          placeholder={variable.description || `Enter ${variable.name}`}
                        />
                      )}
                    </div>
                  ))}
                </div>
              )}

              {/* Rendered output */}
              <div>
                <span className="text-[11px] font-medium text-fg-muted block mb-1.5">
                  Rendered Prompt
                </span>
                <pre className="text-xs text-fg-secondary bg-bg-elevated/60 border border-border-subtle rounded-lg p-3 overflow-x-auto max-h-80 overflow-y-auto font-mono whitespace-pre-wrap leading-relaxed">
                  {renderedPreview}
                </pre>
              </div>
            </div>
          </Card>
        </div>
      </div>

      {/* ── Delete Confirmation ──────────────────────────────────── */}
      {!isBuiltin && (
        <Dialog open={showDeleteConfirm} onOpenChange={setShowDeleteConfirm}>
          <DialogContent className="sm:max-w-md">
            <DialogHeader className="px-5 pt-5">
              <div className="flex items-start gap-3">
                <AlertCircle className="w-6 h-6 text-red-400 shrink-0 mt-0.5" />
                <div>
                  <DialogTitle>Delete Prompt</DialogTitle>
                  <DialogDescription className="sr-only">Confirm prompt deletion</DialogDescription>
                </div>
              </div>
            </DialogHeader>
            <div className="px-5 py-4">
              <p className="text-fg-secondary">
                Are you sure you want to delete &quot;{template.name}&quot;? This action cannot be
                undone and will remove the prompt from all agents.
              </p>
            </div>
            <DialogFooter className="px-5 pb-5">
              <Button variant="ghost" onClick={() => setShowDeleteConfirm(false)}>
                Cancel
              </Button>
              <Button
                variant="destructive"
                onClick={() => onDelete(template.id)}
                disabled={isDeleting}
                className="gap-2"
              >
                {isDeleting ? (
                  <Loader2 className="w-4 h-4 animate-spin" />
                ) : (
                  <Trash2 className="w-4 h-4" />
                )}
                Delete
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}
    </div>
  );
}

// ─── VariablesList ──────────────────────────────────────────────────

const VARIABLE_TYPES = [
  { id: "text", label: "text" },
  { id: "textarea", label: "textarea" },
  { id: "number", label: "number" },
  { id: "boolean", label: "boolean" },
  { id: "select", label: "select" },
] as const;

function typeBadgeColor(type: string) {
  switch (type) {
    case "text":
      return "bg-blue-500/10 text-blue-400";
    case "textarea":
      return "bg-green-500/10 text-green-400";
    case "number":
      return "bg-yellow-500/10 text-yellow-400";
    case "boolean":
      return "bg-purple-500/10 text-purple-400";
    case "select":
      return "bg-orange-500/10 text-orange-400";
    default:
      return "bg-zinc-500/10 text-zinc-400";
  }
}

interface VariablesListProps {
  value: string;
  onChange: (json: string) => void;
  readOnly?: boolean;
}

function VariablesList({ value, onChange, readOnly }: VariablesListProps) {
  const variables = useMemo(() => parseVariables(value), [value]);
  const [showAddForm, setShowAddForm] = useState(false);
  const [newVar, setNewVar] = useState<TemplateVariable>({
    name: "",
    type: "text",
    required: false,
    default: "",
    description: "",
  });

  const removeVariable = useCallback(
    (index: number) => {
      const updated = variables.filter((_, i) => i !== index);
      onChange(JSON.stringify(updated, null, 2));
    },
    [variables, onChange],
  );

  const addVariable = useCallback(() => {
    if (!newVar.name.trim()) return;
    const updated = [...variables, { ...newVar, name: newVar.name.trim() }];
    onChange(JSON.stringify(updated, null, 2));
    setNewVar({ name: "", type: "text", required: false, default: "", description: "" });
    setShowAddForm(false);
  }, [variables, newVar, onChange]);

  if (variables.length === 0 && !showAddForm) {
    return (
      <div className="space-y-2">
        <p className="text-xs text-fg-muted py-2">No variables defined</p>
        {!readOnly && (
          <Button
            onClick={() => setShowAddForm(true)}
            size="sm"
            variant="ghost"
            className="h-6 gap-1 text-[11px] text-fg-muted hover:text-fg"
          >
            <Plus className="w-3 h-3" />
            Add Variable
          </Button>
        )}
      </div>
    );
  }

  return (
    <div className="space-y-1.5">
      {variables.map((variable, index) => (
        <div
          key={`${variable.name}-${index}`}
          className="group flex items-center gap-2 px-2 py-1.5 rounded-md hover:bg-surface/40 transition-colors"
        >
          <span className="text-xs font-mono text-fg-secondary flex-1 min-w-0 truncate">
            {variable.name}
          </span>
          <span
            className={`text-[10px] px-1.5 py-0.5 rounded-md leading-none ${typeBadgeColor(variable.type)}`}
          >
            {variable.type}
          </span>
          {variable.required && <span className="text-accent text-[10px] font-bold">*</span>}
          {variable.default !== undefined && variable.default !== "" && (
            <span className="text-[10px] text-fg-faint truncate max-w-[80px]">
              ={String(variable.default)}
            </span>
          )}
          {!readOnly && (
            <button
              onClick={() => removeVariable(index)}
              className="p-0.5 rounded opacity-0 group-hover:opacity-100 text-fg-faint hover:text-accent transition-all"
            >
              <X className="w-3 h-3" />
            </button>
          )}
        </div>
      ))}

      {/* Add form */}
      {showAddForm && (
        <div className="mt-2 rounded-lg border border-border-subtle bg-bg-elevated/40 p-3 space-y-2.5">
          <span className="text-[11px] font-medium text-fg-secondary">New Variable</span>
          <div className="grid grid-cols-2 gap-2">
            <div className="space-y-1">
              <label className="text-[10px] text-fg-muted">Name</label>
              <input
                type="text"
                value={newVar.name}
                onChange={(e) => setNewVar((v) => ({ ...v, name: e.target.value }))}
                placeholder="variable_name"
                className="w-full px-2 py-1.5 bg-bg-elevated border border-border-subtle rounded-md text-xs text-fg font-mono focus:outline-none focus:border-accent"
              />
            </div>
            <div className="space-y-1">
              <label className="text-[10px] text-fg-muted">Type</label>
              <select
                value={newVar.type}
                onChange={(e) =>
                  setNewVar((v) => ({ ...v, type: e.target.value as TemplateVariable["type"] }))
                }
                className="w-full px-2 py-1.5 bg-bg-elevated border border-border-subtle rounded-md text-xs text-fg focus:outline-none focus:border-accent"
              >
                {VARIABLE_TYPES.map((t) => (
                  <option key={t.id} value={t.id}>
                    {t.label}
                  </option>
                ))}
              </select>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-2">
            <div className="space-y-1">
              <label className="text-[10px] text-fg-muted">Default</label>
              <input
                type="text"
                value={String(newVar.default ?? "")}
                onChange={(e) => setNewVar((v) => ({ ...v, default: e.target.value }))}
                placeholder="default value"
                className="w-full px-2 py-1.5 bg-bg-elevated border border-border-subtle rounded-md text-xs text-fg focus:outline-none focus:border-accent"
              />
            </div>
            <div className="space-y-1">
              <label className="text-[10px] text-fg-muted">Required</label>
              <button
                type="button"
                onClick={() => setNewVar((v) => ({ ...v, required: !v.required }))}
                className={`w-full px-2 py-1.5 border rounded-md text-xs text-left transition-colors ${
                  newVar.required
                    ? "bg-accent/10 border-accent/30 text-accent"
                    : "bg-bg-elevated border-border-subtle text-fg-muted"
                }`}
              >
                {newVar.required ? "Yes" : "No"}
              </button>
            </div>
          </div>
          <div className="space-y-1">
            <label className="text-[10px] text-fg-muted">Description</label>
            <input
              type="text"
              value={newVar.description ?? ""}
              onChange={(e) => setNewVar((v) => ({ ...v, description: e.target.value }))}
              placeholder="What this variable is for..."
              className="w-full px-2 py-1.5 bg-bg-elevated border border-border-subtle rounded-md text-xs text-fg focus:outline-none focus:border-accent"
            />
          </div>
          <div className="flex items-center gap-2 pt-1">
            <Button
              size="sm"
              className="h-6 text-[11px]"
              onClick={addVariable}
              disabled={!newVar.name.trim()}
            >
              Add
            </Button>
            <Button
              variant="ghost"
              size="sm"
              className="h-6 text-[11px]"
              onClick={() => setShowAddForm(false)}
            >
              Cancel
            </Button>
          </div>
        </div>
      )}

      {!readOnly && !showAddForm && (
        <Button
          onClick={() => setShowAddForm(true)}
          size="sm"
          variant="ghost"
          className="h-6 gap-1 text-[11px] text-fg-muted hover:text-fg mt-1"
        >
          <Plus className="w-3 h-3" />
          Add Variable
        </Button>
      )}
    </div>
  );
}

// ─── Shared Sub-components ──────────────────────────────────────────

function Card({ children }: { children: React.ReactNode }) {
  return (
    <div className="rounded-xl border border-border-subtle bg-white dark:bg-bg-elevated/60 shadow-sm overflow-hidden">
      {children}
    </div>
  );
}

function CardHeader({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex items-center gap-2 px-4 py-3 text-sm font-medium text-fg">
      {children}
    </div>
  );
}
