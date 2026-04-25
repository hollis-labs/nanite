import { Check, Pencil, X } from "lucide-react";
import { useCallback, useRef, useState } from "react";
import { Button } from "@/components/ui/button";

interface SystemPromptEditorProps {
  value: string;
  onChange: (value: string) => void;
}

export function SystemPromptEditor({ value, onChange }: SystemPromptEditorProps) {
  const [isEditing, setIsEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  const startEditing = useCallback(() => {
    setDraft(value);
    setIsEditing(true);
    setTimeout(() => {
      const el = textareaRef.current;
      if (el) {
        el.focus();
        el.setSelectionRange(el.value.length, el.value.length);
      }
    }, 0);
  }, [value]);

  const handleSave = useCallback(() => {
    onChange(draft);
    setIsEditing(false);
  }, [draft, onChange]);

  const handleCancel = useCallback(() => {
    setIsEditing(false);
  }, []);

  if (isEditing) {
    return (
      <div className="space-y-2">
        <div className="flex items-center justify-between">
          <span className="text-xs font-medium text-fg-secondary">System Prompt</span>
          <div className="flex items-center gap-1">
            <span className="text-[10px] text-fg-faint tabular-nums mr-2">
              {draft.length.toLocaleString()} chars
            </span>
            <Button
              variant="ghost"
              size="icon"
              className="w-6 h-6 text-success hover:text-success"
              onClick={handleSave}
              title="Save"
            >
              <Check className="w-3.5 h-3.5" />
            </Button>
            <Button
              variant="ghost"
              size="icon"
              className="w-6 h-6 text-fg-muted hover:text-fg"
              onClick={handleCancel}
              title="Cancel"
            >
              <X className="w-3.5 h-3.5" />
            </Button>
          </div>
        </div>
        <textarea
          ref={textareaRef}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Escape") handleCancel();
            if (e.key === "s" && (e.metaKey || e.ctrlKey)) {
              e.preventDefault();
              handleSave();
            }
          }}
          rows={16}
          className="w-full px-3 py-2.5 bg-bg-elevated border border-border-subtle rounded-lg text-xs text-fg font-mono leading-relaxed focus:outline-none focus:border-primary resize-y min-h-[200px]"
        />
        <p className="text-[10px] text-fg-faint">
          Cmd+S to save &middot; Escape to cancel
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium text-fg-secondary">System Prompt</span>
        <Button
          variant="ghost"
          size="sm"
          className="h-6 gap-1 text-[11px] text-fg-muted hover:text-fg"
          onClick={startEditing}
        >
          <Pencil className="w-3 h-3" />
          Edit
        </Button>
      </div>
      {value ? (
        <div
          onClick={startEditing}
          className="cursor-text rounded-lg bg-bg-elevated border border-border-subtle px-3 py-2.5 max-h-48 overflow-y-auto hover:border-border transition-colors"
        >
          <pre className="text-xs text-fg-secondary font-mono whitespace-pre-wrap leading-relaxed">
            {value}
          </pre>
        </div>
      ) : (
        <div
          onClick={startEditing}
          className="cursor-pointer rounded-lg border border-dashed border-border-subtle px-3 py-4 text-center hover:border-border transition-colors"
        >
          <p className="text-xs text-fg-muted">No system prompt configured — click to add</p>
        </div>
      )}
    </div>
  );
}
