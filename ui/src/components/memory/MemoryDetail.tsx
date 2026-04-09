import { ArrowLeft, Trash2 } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  useCreateMemory,
  useDeleteMemory,
  useMemories,
  useUpdateMemory,
  useUpdateMemoryStatus,
} from "@/hooks/useMemories";
import type { Memory, MemoryOrigin, MemoryScope, MemoryStatus } from "@/lib/types";

interface MemoryDetailProps {
  memoryKey: string | null;
  onBack: () => void;
}

const ORIGIN_OPTIONS: MemoryOrigin[] = ["user", "feedback", "project", "reference", "observation"];
const SCOPE_OPTIONS: MemoryScope[] = ["session", "project", "user"];
const STATUS_OPTIONS: MemoryStatus[] = ["draft", "reviewed", "canonical", "deprecated"];

function statusPillClass(status: MemoryStatus, active: boolean): string {
  if (!active)
    return "bg-surface text-fg-secondary hover:text-fg hover:bg-surface-hover cursor-pointer transition-colors";
  switch (status) {
    case "draft":
      return "bg-info-muted text-info cursor-pointer transition-colors";
    case "reviewed":
      return "bg-primary text-white cursor-pointer transition-colors";
    case "canonical":
      return "bg-success-muted text-success cursor-pointer transition-colors";
    case "deprecated":
      return "bg-surface text-fg-muted cursor-pointer transition-colors";
  }
}

const inputClass =
  "w-full px-3 py-2 rounded-md border border-border-subtle bg-bg-elevated text-sm text-fg outline-none focus:border-primary/50 placeholder:text-fg-faint";
const selectClass =
  "w-full appearance-none px-3 py-2 rounded-md border border-border-subtle bg-bg-elevated text-sm text-fg outline-none focus:border-primary/50 cursor-pointer";
const labelClass = "text-xs uppercase tracking-wider text-fg-muted font-medium";

export function MemoryDetail({ memoryKey, onBack }: MemoryDetailProps) {
  const isCreate = memoryKey === null;
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [tagInput, setTagInput] = useState("");

  // Form state
  const [summary, setSummary] = useState("");
  const [body, setBody] = useState("");
  const [origin, setOrigin] = useState<MemoryOrigin>("user");
  const [confidence, setConfidence] = useState(0.8);
  const [scope, setScope] = useState<MemoryScope>("user");
  const [status, setStatus] = useState<MemoryStatus>("draft");
  const [tags, setTags] = useState<string[]>([]);

  // Fetch memories list to find the one being edited
  const { data } = useMemories({ limit: 1000 });
  const memory: Memory | undefined = data?.memories.find((m) => m.memory_key === memoryKey);

  // Hydrate form when memory loads
  useEffect(() => {
    if (!memory) return;
    setSummary(memory.summary);
    setBody(memory.body ?? "");
    setOrigin(memory.origin);
    setConfidence(memory.confidence);
    setScope(memory.scope);
    setStatus(memory.status);
    setTags(memory.tags ?? []);
  }, [memory]);

  const createMutation = useCreateMemory();
  const updateMutation = useUpdateMemory();
  const deleteMutation = useDeleteMemory();
  const statusMutation = useUpdateMemoryStatus();

  const handleSave = useCallback(() => {
    const data = { summary, body: body || undefined, origin, confidence, tags };
    if (isCreate) {
      createMutation.mutate({ ...data, scope }, { onSuccess: onBack });
    } else if (memoryKey) {
      updateMutation.mutate({ key: memoryKey, data }, { onSuccess: onBack });
    }
  }, [
    isCreate,
    memoryKey,
    summary,
    body,
    origin,
    confidence,
    scope,
    tags,
    createMutation,
    updateMutation,
    onBack,
  ]);

  const handleStatusClick = useCallback(
    (s: MemoryStatus) => {
      if (!memoryKey) return;
      setStatus(s);
      statusMutation.mutate({ key: memoryKey, status: s });
    },
    [memoryKey, statusMutation],
  );

  const handleDeleteConfirm = useCallback(() => {
    if (!memoryKey) return;
    deleteMutation.mutate(memoryKey, { onSuccess: onBack });
  }, [memoryKey, deleteMutation, onBack]);

  const handleTagKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLInputElement>) => {
      if (e.key === "Enter") {
        e.preventDefault();
        const val = tagInput.trim();
        if (val && !tags.includes(val)) {
          setTags((prev) => [...prev, val]);
        }
        setTagInput("");
      }
    },
    [tagInput, tags],
  );

  const removeTag = useCallback((tag: string) => {
    setTags((prev) => prev.filter((t) => t !== tag));
  }, []);

  const isSaving = createMutation.isPending || updateMutation.isPending;

  return (
    <div className="flex flex-col h-full min-h-0">
      {/* Header */}
      <div className="px-3 pt-3 pb-2 pr-10 shrink-0 flex items-center gap-2 border-b border-border-subtle">
        <button
          type="button"
          onClick={onBack}
          className="p-1 rounded text-fg-muted hover:text-fg hover:bg-surface transition-colors"
        >
          <ArrowLeft className="size-4" />
        </button>
        <span className="text-sm font-medium text-fg flex-1">
          {isCreate ? "New Memory" : "Edit Memory"}
        </span>
        {!isCreate && (
          <button
            type="button"
            onClick={() => setConfirmDelete(true)}
            className="flex items-center gap-1 text-xs font-medium px-2.5 py-1 rounded-md bg-danger-muted text-danger hover:bg-danger-muted/80 transition-colors"
          >
            <Trash2 className="size-3" />
            Delete
          </button>
        )}
        <button
          type="button"
          onClick={handleSave}
          disabled={isSaving || !summary.trim()}
          className="flex items-center gap-1 text-xs font-medium px-3 py-1 rounded-md bg-primary text-white hover:bg-primary-hover transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {isSaving ? "Saving…" : "Save"}
        </button>
      </div>

      {/* Form */}
      <div className="flex-1 overflow-y-auto p-4 space-y-4">
        {/* Summary */}
        <div className="space-y-1">
          <label className={labelClass}>Summary *</label>
          <input
            type="text"
            value={summary}
            onChange={(e) => setSummary(e.target.value)}
            placeholder="Short summary of this memory"
            className={inputClass}
          />
        </div>

        {/* Body */}
        <div className="space-y-1">
          <label className={labelClass}>Body</label>
          <textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder="Optional extended content…"
            rows={4}
            className={`${inputClass} resize-none`}
          />
        </div>

        {/* Origin + Confidence row */}
        <div className="grid grid-cols-2 gap-3">
          <div className="space-y-1">
            <label className={labelClass}>Origin</label>
            <select
              value={origin}
              onChange={(e) => setOrigin(e.target.value as MemoryOrigin)}
              className={selectClass}
            >
              {ORIGIN_OPTIONS.map((o) => (
                <option key={o} value={o}>
                  {o.charAt(0).toUpperCase() + o.slice(1)}
                </option>
              ))}
            </select>
          </div>

          <div className="space-y-1">
            <label className={labelClass}>Confidence</label>
            <input
              type="number"
              value={confidence}
              onChange={(e) => setConfidence(parseFloat(e.target.value))}
              min={0}
              max={1}
              step={0.05}
              className={inputClass}
            />
          </div>
        </div>

        {/* Scope */}
        <div className="space-y-1">
          <label className={labelClass}>Scope</label>
          <select
            value={scope}
            onChange={(e) => setScope(e.target.value as MemoryScope)}
            disabled={!isCreate}
            className={`${selectClass} disabled:opacity-50 disabled:cursor-not-allowed`}
          >
            {SCOPE_OPTIONS.map((s) => (
              <option key={s} value={s}>
                {s.charAt(0).toUpperCase() + s.slice(1)}
              </option>
            ))}
          </select>
        </div>

        {/* Status pills — edit mode only */}
        {!isCreate && (
          <div className="space-y-1.5">
            <label className={labelClass}>Status</label>
            <div className="flex flex-wrap gap-1.5">
              {STATUS_OPTIONS.map((s) => (
                <button
                  key={s}
                  type="button"
                  onClick={() => handleStatusClick(s)}
                  className={`text-xs px-2.5 py-1 rounded-md font-medium ${statusPillClass(s, status === s)}`}
                >
                  {s.charAt(0).toUpperCase() + s.slice(1)}
                </button>
              ))}
            </div>
          </div>
        )}

        {/* Tags */}
        <div className="space-y-1.5">
          <label className={labelClass}>Tags</label>
          <div className="flex flex-wrap gap-1.5 mb-1.5">
            {tags.map((tag) => (
              <span
                key={tag}
                className="flex items-center gap-1 text-xs px-2 py-0.5 rounded-md bg-surface text-fg-secondary"
              >
                {tag}
                <button
                  type="button"
                  onClick={() => removeTag(tag)}
                  className="text-fg-faint hover:text-fg-muted transition-colors leading-none"
                >
                  ×
                </button>
              </span>
            ))}
          </div>
          <input
            type="text"
            value={tagInput}
            onChange={(e) => setTagInput(e.target.value)}
            onKeyDown={handleTagKeyDown}
            placeholder="Type a tag and press Enter"
            className={`${inputClass} border-dashed`}
          />
        </div>

        {/* Read-only metadata footer — edit mode only */}
        {!isCreate && memory && (
          <div className="pt-2 border-t border-border-subtle space-y-1">
            <p className={`${labelClass} mb-1`}>Metadata</p>
            {memory.session_id && (
              <div className="flex items-center gap-2 text-xs text-fg-faint">
                <span className="text-fg-muted">Session</span>
                <span className="font-mono">{memory.session_id.slice(0, 8)}…</span>
              </div>
            )}
            {memory.revision_id && (
              <div className="flex items-center gap-2 text-xs text-fg-faint">
                <span className="text-fg-muted">Revision</span>
                <span className="font-mono">{memory.revision_id.slice(0, 8)}…</span>
              </div>
            )}
            {memory.trigger && (
              <div className="flex items-center gap-2 text-xs text-fg-faint">
                <span className="text-fg-muted">Trigger</span>
                <span>{memory.trigger}</span>
              </div>
            )}
          </div>
        )}
      </div>

      {/* Delete confirmation dialog */}
      <Dialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete Memory</DialogTitle>
            <DialogDescription>
              Are you sure you want to delete this memory? This action cannot be undone.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirmDelete(false)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={handleDeleteConfirm}
              disabled={deleteMutation.isPending}
            >
              {deleteMutation.isPending ? "Deleting…" : "Delete"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
