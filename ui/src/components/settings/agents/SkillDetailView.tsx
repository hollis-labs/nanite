import {
  ChevronLeft,
  Code2,
  Eye,
  Loader2,
  Plus,
  Server,
  Trash2,
  Wrench,
  X,
} from "lucide-react";
import { useCallback, useState } from "react";
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
import type { Skill, ToolBinding } from "@/lib/types";

// ─── Constants ─────────────────────────────────────────────────────

const SKILL_CATEGORIES = [
  "general",
  "development",
  "communication",
  "analysis",
  "automation",
  "integration",
  "productivity",
  "other",
] as const;

// ─── Helpers ───────────────────────────────────────────────────────

function parseToolBindings(s: string): ToolBinding[] {
  try {
    return JSON.parse(s);
  } catch {
    return [];
  }
}

function parseInputSchema(s: string): Record<string, unknown> | null {
  try {
    const v = JSON.parse(s);
    if (v && typeof v === "object" && Object.keys(v).length > 0) return v;
    return null;
  } catch {
    return null;
  }
}

function formatDate(iso: string): string {
  try {
    return new Date(iso).toLocaleDateString(undefined, {
      year: "numeric",
      month: "short",
      day: "numeric",
    });
  } catch {
    return iso;
  }
}

// ─── Props ─────────────────────────────────────────────────────────

export interface SkillDetailViewProps {
  skill: Skill;
  onUpdate: (field: string, value: string) => void;
  onDelete: (id: string) => void;
  isDeleting?: boolean;
  onBack: () => void;
}

// ─── ToolBindingsList ──────────────────────────────────────────────

function ToolBindingsList({
  value,
  onChange,
  readOnly,
}: {
  value: string;
  onChange: (json: string) => void;
  readOnly?: boolean;
}) {
  const bindings = parseToolBindings(value);
  const [adding, setAdding] = useState(false);
  const [newServer, setNewServer] = useState("");
  const [newTool, setNewTool] = useState("");

  const handleRemove = (index: number) => {
    const next = bindings.filter((_, i) => i !== index);
    onChange(JSON.stringify(next));
  };

  const handleAdd = () => {
    if (!newServer.trim() || !newTool.trim()) return;
    const next = [...bindings, { server: newServer.trim(), tool: newTool.trim() }];
    onChange(JSON.stringify(next));
    setNewServer("");
    setNewTool("");
    setAdding(false);
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter") handleAdd();
    if (e.key === "Escape") {
      setAdding(false);
      setNewServer("");
      setNewTool("");
    }
  };

  return (
    <div className="space-y-1.5">
      {bindings.length === 0 && !adding && (
        <p className="text-xs text-fg-muted py-3 text-center">No tool bindings configured</p>
      )}

      {bindings.map((binding, index) => (
        <div
          key={index}
          className="group flex items-center gap-2 px-2 py-1.5 rounded-md hover:bg-surface/40 transition-colors"
        >
          <Server className="w-3.5 h-3.5 text-fg-muted shrink-0" />
          <span className="text-xs font-medium text-fg">{binding.server}</span>
          <span className="text-xs text-fg-faint">/</span>
          <span className="text-xs text-fg-secondary flex-1 truncate">{binding.tool}</span>
          {!readOnly && (
            <button
              onClick={() => handleRemove(index)}
              className="p-0.5 rounded opacity-0 group-hover:opacity-100 text-fg-faint hover:text-accent transition-all"
            >
              <X className="w-3 h-3" />
            </button>
          )}
        </div>
      ))}

      {adding && (
        <div className="flex items-center gap-2 px-2 py-1.5">
          <Server className="w-3.5 h-3.5 text-fg-muted shrink-0" />
          <input
            autoFocus
            type="text"
            value={newServer}
            onChange={(e) => setNewServer(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="server"
            className="w-28 px-2 py-1 bg-bg-elevated border border-border-subtle rounded-md text-xs text-fg focus:outline-none focus:ring-1 focus:ring-accent"
          />
          <span className="text-xs text-fg-faint">/</span>
          <input
            type="text"
            value={newTool}
            onChange={(e) => setNewTool(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="tool"
            className="flex-1 px-2 py-1 bg-bg-elevated border border-border-subtle rounded-md text-xs text-fg focus:outline-none focus:ring-1 focus:ring-accent"
          />
          <Button
            size="sm"
            variant="ghost"
            className="h-6 w-6 p-0"
            onClick={handleAdd}
            disabled={!newServer.trim() || !newTool.trim()}
          >
            <Plus className="w-3 h-3" />
          </Button>
          <Button
            size="sm"
            variant="ghost"
            className="h-6 w-6 p-0"
            onClick={() => {
              setAdding(false);
              setNewServer("");
              setNewTool("");
            }}
          >
            <X className="w-3 h-3" />
          </Button>
        </div>
      )}

      {!readOnly && !adding && (
        <button
          onClick={() => setAdding(true)}
          className="flex items-center gap-1.5 px-2 py-1.5 text-[11px] text-fg-muted hover:text-fg transition-colors rounded-md hover:bg-surface/40 w-full"
        >
          <Plus className="w-3 h-3" />
          Add binding
        </button>
      )}
    </div>
  );
}

// ─── Card Sub-components ───────────────────────────────────────────

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

function MetaRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center gap-3 py-1.5">
      <span className="text-xs text-fg-muted w-28 shrink-0">{label}</span>
      <span className="text-xs text-fg-secondary">{value}</span>
    </div>
  );
}

// ─── Main Component ────────────────────────────────────────────────

export function SkillDetailView({
  skill,
  onUpdate,
  onDelete,
  isDeleting,
  onBack,
}: SkillDetailViewProps) {
  const [editField, setEditField] = useState<string | null>(null);
  const [editValue, setEditValue] = useState("");
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);

  const startEditing = useCallback((field: string, value: string) => {
    setEditField(field);
    setEditValue(value);
  }, []);

  const saveField = useCallback(
    (field: string) => {
      onUpdate(field, editValue);
      setEditField(null);
      setEditValue("");
    },
    [editValue, onUpdate],
  );

  const cancelEditing = useCallback(() => {
    setEditField(null);
    setEditValue("");
  }, []);

  const inputClass =
    "w-full px-2 py-1 bg-bg-elevated border border-border-subtle rounded-md text-xs text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent";

  // ─── Inline editable field ──────────────────────────────────────

  const editableRow = (
    field: string,
    label: string,
    value: string,
    opts?: {
      type?: "input" | "select";
      selectOptions?: { id: string; label: string }[];
    },
  ) => {
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
                onUpdate(field, e.target.value);
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
              type="text"
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
        className={`flex items-center gap-3 py-1.5 rounded px-1 -mx-1 transition-colors ${
          skill.is_builtin ? "" : "group cursor-pointer hover:bg-surface/40"
        }`}
        onClick={skill.is_builtin ? undefined : () => startEditing(field, value)}
      >
        <span className="text-xs text-fg-muted w-28 shrink-0">{label}</span>
        <span className="text-xs text-fg-secondary flex-1 truncate">{value || "\u2014"}</span>
      </div>
    );
  };

  const categoryOptions = SKILL_CATEGORIES.map((c) => ({
    id: c,
    label: c.charAt(0).toUpperCase() + c.slice(1),
  }));

  const schema = parseInputSchema(skill.input_schema);

  // ─── Render ─────────────────────────────────────────────────────

  return (
    <div className="space-y-5">
      {/* Header */}
      <div className="flex items-center gap-3">
        <Button variant="ghost" size="icon" onClick={onBack}>
          <ChevronLeft className="w-4 h-4" />
        </Button>
        <div className="flex items-center gap-3 flex-1 min-w-0">
          <div className="size-12 rounded-sm bg-surface flex items-center justify-center shrink-0">
            <DynamicIcon
              name={skill.icon}
              className="w-6 h-6 text-fg-secondary"
              fallback={Wrench}
            />
          </div>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <h2 className="text-xl font-semibold text-fg truncate">{skill.name}</h2>
              {skill.is_builtin && (
                <span
                  className="w-2 h-2 rounded-full bg-success shrink-0"
                  title="Built-in skill"
                />
              )}
            </div>
            <p className="text-xs text-fg-muted font-mono truncate">{skill.slug}</p>
          </div>
        </div>
        {!skill.is_builtin && (
          <Button
            onClick={() => setShowDeleteConfirm(true)}
            variant="ghost"
            size="icon"
            className="text-red-400 hover:text-red-300"
          >
            <Trash2 className="w-4 h-4" />
          </Button>
        )}
      </div>

      {/* Two-column cards */}
      <div className="grid gap-4 lg:grid-cols-2">
        {/* Left: Details */}
        <Card>
          <CardHeader>
            <Eye className="w-4 h-4" />
            <span>Details</span>
          </CardHeader>
          <div className="px-4 pb-4 space-y-0.5">
            {editableRow("name", "Name", skill.name)}
            {editableRow("slug", "Slug", skill.slug)}
            {editableRow("category", "Category", skill.category, {
              type: "select",
              selectOptions: categoryOptions,
            })}
            {editableRow("description", "Description", skill.description)}
            {!skill.is_builtin && (
              <div className="flex items-center gap-3 py-1.5">
                <span className="text-xs text-fg-muted w-28 shrink-0">Icon</span>
                <IconPicker
                  value={skill.icon || ""}
                  onChange={(iconName) => onUpdate("icon", iconName)}
                />
              </div>
            )}
            <MetaRow label="Type" value={skill.is_builtin ? "Built-in" : "Custom"} />
            {skill.created_at && (
              <MetaRow label="Created" value={formatDate(skill.created_at)} />
            )}
          </div>
        </Card>

        {/* Right: Tool Bindings */}
        <Card>
          <CardHeader>
            <Wrench className="w-4 h-4" />
            <span>Tool Bindings ({parseToolBindings(skill.tool_bindings).length})</span>
          </CardHeader>
          <div className="px-4 pb-4">
            <ToolBindingsList
              value={skill.tool_bindings}
              onChange={(json) => onUpdate("tool_bindings", json)}
              readOnly={skill.is_builtin}
            />
          </div>
        </Card>
      </div>

      {/* Input Schema (full width, conditional) */}
      {schema && (
        <Card>
          <CardHeader>
            <Code2 className="w-4 h-4" />
            <span>Input Schema</span>
          </CardHeader>
          <div className="px-4 pb-4">
            <pre className="text-xs text-fg-secondary overflow-x-auto font-mono bg-bg-elevated/40 rounded-lg p-3 border border-border-subtle">
              {JSON.stringify(schema, null, 2)}
            </pre>
          </div>
        </Card>
      )}

      {/* Delete confirmation dialog */}
      {!skill.is_builtin && (
        <Dialog open={showDeleteConfirm} onOpenChange={setShowDeleteConfirm}>
          <DialogContent className="sm:max-w-md">
            <DialogHeader className="px-5 pt-5">
              <DialogTitle>Delete Skill</DialogTitle>
              <DialogDescription className="sr-only">Confirm skill deletion</DialogDescription>
            </DialogHeader>
            <div className="px-5 py-4">
              <p className="text-fg-secondary">
                Are you sure you want to delete &quot;{skill.name}&quot;? This action cannot be
                undone.
              </p>
            </div>
            <DialogFooter className="px-5 pb-5">
              <Button variant="ghost" onClick={() => setShowDeleteConfirm(false)}>
                Cancel
              </Button>
              <Button
                variant="destructive"
                onClick={() => onDelete(skill.id)}
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
