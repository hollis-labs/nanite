import { Plus, Shield, ShieldOff, X } from "lucide-react";
import { useCallback, useRef, useState } from "react";
import { Button } from "@/components/ui/button";

interface ToolPermissions {
  allow?: string[];
  deny?: string[];
}

interface ToolPermissionsEditorProps {
  /** JSON string of {allow: string[], deny: string[]} */
  value: string;
  /** Called with new JSON string on change */
  onChange: (json: string) => void;
}

export function ToolPermissionsEditor({ value, onChange }: ToolPermissionsEditorProps) {
  const perms: ToolPermissions = (() => {
    try {
      const parsed = JSON.parse(value || "{}");
      return typeof parsed === "object" && parsed !== null ? parsed : {};
    } catch {
      return {};
    }
  })();

  const emit = useCallback(
    (updated: ToolPermissions) => {
      const clean: ToolPermissions = {};
      if (updated.allow && updated.allow.length > 0) clean.allow = updated.allow;
      if (updated.deny && updated.deny.length > 0) clean.deny = updated.deny;
      onChange(JSON.stringify(Object.keys(clean).length > 0 ? clean : {}));
    },
    [onChange],
  );

  const handleAdd = useCallback(
    (section: "allow" | "deny", pattern: string) => {
      const trimmed = pattern.trim();
      if (!trimmed) return;
      const list = [...(perms[section] || [])];
      if (list.includes(trimmed)) return;
      list.push(trimmed);
      emit({ ...perms, [section]: list });
    },
    [perms, emit],
  );

  const handleRemove = useCallback(
    (section: "allow" | "deny", index: number) => {
      const list = [...(perms[section] || [])];
      list.splice(index, 1);
      emit({ ...perms, [section]: list });
    },
    [perms, emit],
  );

  const allowList = perms.allow || [];
  const denyList = perms.deny || [];
  const isEmpty = allowList.length === 0 && denyList.length === 0;

  return (
    <div className="space-y-3">
      <span className="text-xs font-medium text-fg-secondary">Tool Permissions</span>

      {isEmpty && (
        <p className="text-xs text-fg-muted py-1">
          No permissions configured — all tools allowed by default
        </p>
      )}

      <PermissionSection
        label="Allow"
        icon={Shield}
        items={allowList}
        accentClass="text-success"
        onAdd={(p) => handleAdd("allow", p)}
        onRemove={(i) => handleRemove("allow", i)}
      />

      <PermissionSection
        label="Deny"
        icon={ShieldOff}
        items={denyList}
        accentClass="text-primary"
        onAdd={(p) => handleAdd("deny", p)}
        onRemove={(i) => handleRemove("deny", i)}
      />
    </div>
  );
}

function PermissionSection({
  label,
  icon: Icon,
  items,
  accentClass,
  onAdd,
  onRemove,
}: {
  label: string;
  icon: typeof Shield;
  items: string[];
  accentClass: string;
  onAdd: (pattern: string) => void;
  onRemove: (index: number) => void;
}) {
  const [isAdding, setIsAdding] = useState(false);
  const [addValue, setAddValue] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  const handleAdd = () => {
    if (addValue.trim()) {
      onAdd(addValue.trim());
      setAddValue("");
      setTimeout(() => inputRef.current?.focus(), 0);
    }
  };

  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-1.5">
          <Icon className={`w-3 h-3 ${accentClass}`} />
          <span className="text-[11px] font-medium text-fg-muted uppercase tracking-wide">{label}</span>
          {items.length > 0 && (
            <span className="text-[10px] text-fg-faint">({items.length})</span>
          )}
        </div>
        {!isAdding && (
          <Button
            variant="ghost"
            size="sm"
            className="h-5 gap-1 text-[10px] text-fg-faint hover:text-fg px-1.5"
            onClick={() => {
              setIsAdding(true);
              setTimeout(() => inputRef.current?.focus(), 0);
            }}
          >
            <Plus className="w-2.5 h-2.5" />
            Add
          </Button>
        )}
      </div>

      {items.map((item, index) => (
        <div key={`${item}-${index}`} className="group flex items-center gap-2 px-2 py-1 rounded-md hover:bg-surface/40 transition-colors">
          <span className={`w-1.5 h-1.5 rounded-full ${accentClass === "text-success" ? "bg-success" : "bg-primary"} shrink-0`} />
          <span className="flex-1 text-xs font-mono text-fg-secondary truncate">
            {item === "*" ? (
              <span className="text-fg-muted italic">All tools</span>
            ) : (
              item
            )}
          </span>
          <button
            onClick={() => onRemove(index)}
            className="p-0.5 rounded opacity-0 group-hover:opacity-100 text-fg-faint hover:text-primary transition-all"
          >
            <X className="w-3 h-3" />
          </button>
        </div>
      ))}

      {isAdding && (
        <div className="flex items-center gap-2 px-2 py-1">
          <span className={`w-1.5 h-1.5 rounded-full ${accentClass === "text-success" ? "bg-success" : "bg-primary"} shrink-0`} />
          <input
            ref={inputRef}
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
            placeholder="mcp__server__tool_name or *"
            className="flex-1 min-w-0 bg-transparent text-xs text-fg font-mono outline-none border-b border-border-subtle focus:border-primary py-0.5 placeholder:text-fg-faint"
          />
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
      )}
    </div>
  );
}
