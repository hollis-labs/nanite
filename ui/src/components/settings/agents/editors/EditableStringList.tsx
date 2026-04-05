import { Plus, X, type LucideIcon } from "lucide-react";
import { useCallback, useRef, useState } from "react";
import { Button } from "@/components/ui/button";

interface EditableStringListProps {
  /** JSON string of string[] */
  value: string;
  /** Called with new JSON string on change */
  onChange: (json: string) => void;
  /** Icon shown next to each item */
  icon: LucideIcon;
  /** Section label */
  label: string;
  /** Input placeholder */
  placeholder?: string;
  /** Shown when list is empty */
  emptyText?: string;
  /** If true, renders paths with directory styling */
  pathStyle?: boolean;
}

export function EditableStringList({
  value,
  onChange,
  icon: Icon,
  label,
  placeholder = "Add item...",
  emptyText = "None configured",
  pathStyle = false,
}: EditableStringListProps) {
  const [editingIndex, setEditingIndex] = useState<number | null>(null);
  const [editValue, setEditValue] = useState("");
  const [isAdding, setIsAdding] = useState(false);
  const [addValue, setAddValue] = useState("");
  const addInputRef = useRef<HTMLInputElement>(null);

  const items: string[] = (() => {
    try {
      const parsed = JSON.parse(value || "[]");
      return Array.isArray(parsed) ? parsed : [];
    } catch {
      return [];
    }
  })();

  const emit = useCallback(
    (newItems: string[]) => onChange(JSON.stringify(newItems)),
    [onChange],
  );

  const handleAdd = useCallback(() => {
    const trimmed = addValue.trim();
    if (!trimmed || items.includes(trimmed)) return;
    emit([...items, trimmed]);
    setAddValue("");
    // Keep add mode open for rapid entry
    setTimeout(() => addInputRef.current?.focus(), 0);
  }, [addValue, items, emit]);

  const handleRemove = useCallback(
    (index: number) => {
      emit(items.filter((_, i) => i !== index));
      if (editingIndex === index) {
        setEditingIndex(null);
      }
    },
    [items, emit, editingIndex],
  );

  const handleEditSave = useCallback(
    (index: number) => {
      const trimmed = editValue.trim();
      if (!trimmed) {
        handleRemove(index);
      } else {
        const updated = [...items];
        updated[index] = trimmed;
        emit(updated);
      }
      setEditingIndex(null);
    },
    [editValue, items, emit, handleRemove],
  );

  const startEditing = useCallback((index: number, currentValue: string) => {
    setEditingIndex(index);
    setEditValue(currentValue);
  }, []);

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium text-fg-secondary">{label}</span>
        {!isAdding && (
          <Button
            variant="ghost"
            size="sm"
            className="h-6 gap-1 text-[11px] text-fg-muted hover:text-fg"
            onClick={() => {
              setIsAdding(true);
              setTimeout(() => addInputRef.current?.focus(), 0);
            }}
          >
            <Plus className="w-3 h-3" />
            Add
          </Button>
        )}
      </div>

      {items.length === 0 && !isAdding && (
        <p className="text-xs text-fg-muted py-2">{emptyText}</p>
      )}

      {/* Item list */}
      <div className="space-y-0.5">
        {items.map((item, index) => (
          <div key={`${item}-${index}`} className="group flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-surface/40 transition-colors">
            <Icon className="w-3.5 h-3.5 text-fg-muted shrink-0" />
            {editingIndex === index ? (
              <input
                autoFocus
                type="text"
                value={editValue}
                onChange={(e) => setEditValue(e.target.value)}
                onBlur={() => handleEditSave(index)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") handleEditSave(index);
                  if (e.key === "Escape") setEditingIndex(null);
                }}
                className="flex-1 min-w-0 bg-transparent text-xs text-fg font-mono outline-none border-b border-border-subtle focus:border-primary py-0.5"
              />
            ) : (
              <span
                className="flex-1 min-w-0 text-xs font-mono text-fg-secondary truncate cursor-text"
                onClick={() => startEditing(index, item)}
              >
                {pathStyle ? <PathDisplay path={item} /> : item}
              </span>
            )}
            <button
              onClick={() => handleRemove(index)}
              className="p-0.5 rounded opacity-0 group-hover:opacity-100 text-fg-faint hover:text-primary transition-all"
            >
              <X className="w-3 h-3" />
            </button>
          </div>
        ))}
      </div>

      {/* Add new item */}
      {isAdding && (
        <div className="flex items-center gap-2 px-2 py-1.5">
          <Icon className="w-3.5 h-3.5 text-fg-muted shrink-0" />
          <input
            ref={addInputRef}
            type="text"
            value={addValue}
            onChange={(e) => setAddValue(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") handleAdd();
              if (e.key === "Escape") {
                setIsAdding(false);
                setAddValue("");
              }
            }}
            placeholder={placeholder}
            className="flex-1 min-w-0 bg-transparent text-xs text-fg font-mono outline-none border-b border-border-subtle focus:border-primary py-0.5 placeholder:text-fg-faint"
          />
          <div className="flex items-center gap-1">
            <Button
              variant="ghost"
              size="icon"
              className="w-5 h-5 text-fg-muted hover:text-fg"
              onClick={handleAdd}
              disabled={!addValue.trim()}
            >
              <Plus className="w-3 h-3" />
            </Button>
            <Button
              variant="ghost"
              size="icon"
              className="w-5 h-5 text-fg-muted hover:text-fg"
              onClick={() => {
                setIsAdding(false);
                setAddValue("");
              }}
            >
              <X className="w-3 h-3" />
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

/** Renders a path with dimmed separators */
function PathDisplay({ path }: { path: string }) {
  const segments = path.split("/").filter(Boolean);
  const hasTrailingSlash = path.endsWith("/");
  return (
    <span className="inline-flex items-center gap-0">
      {segments.map((seg, i) => (
        <span key={i} className="inline-flex items-center">
          {i > 0 && <span className="text-fg-faint mx-0.5">/</span>}
          <span>{seg}</span>
        </span>
      ))}
      {hasTrailingSlash && <span className="text-fg-faint">/</span>}
    </span>
  );
}
