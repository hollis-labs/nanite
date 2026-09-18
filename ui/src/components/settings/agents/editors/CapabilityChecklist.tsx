import { useState } from "react";

interface CapabilityChecklistProps<T> {
  title: string;
  description?: string;
  items: T[];
  isLoading?: boolean;
  getKey: (item: T) => string;
  getLabel: (item: T) => string;
  getDetail?: (item: T) => string | undefined;
  selected: Set<string>;
  onToggle: (key: string) => void;
  disabled?: boolean;
  emptyText?: string;
}

/** A searchable, multi-select checklist over a catalog (tools, skills, MCP
 *  servers, ...) — the field-based alternative to typing raw names into a
 *  JSON-array textarea. `selected`/`onToggle` are keyed by `getKey`, not by
 *  array index, so the caller owns the actual selection state. */
export function CapabilityChecklist<T>({
  title,
  description,
  items,
  isLoading = false,
  getKey,
  getLabel,
  getDetail,
  selected,
  onToggle,
  disabled = false,
  emptyText = "Nothing available.",
}: CapabilityChecklistProps<T>) {
  const [search, setSearch] = useState("");
  const filtered = items.filter((item) =>
    search.trim() ? getLabel(item).toLowerCase().includes(search.trim().toLowerCase()) : true,
  );

  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium text-fg-secondary">{title}</span>
        <span className="text-[10px] text-fg-faint">{selected.size} selected</span>
      </div>
      {description ? <p className="text-[10px] text-fg-faint">{description}</p> : null}
      <input
        type="text"
        value={search}
        onChange={(e) => setSearch(e.target.value)}
        placeholder="Filter..."
        disabled={disabled}
        className="w-full px-2.5 py-1.5 bg-bg-elevated border border-border-subtle rounded-md text-xs text-fg focus:outline-none focus:border-primary transition-colors"
      />
      <div className="max-h-36 overflow-y-auto rounded-lg border border-border-subtle bg-bg-elevated/50">
        {isLoading ? (
          <p className="px-3 py-2 text-xs text-fg-muted">Loading...</p>
        ) : items.length === 0 ? (
          <p className="px-3 py-2 text-xs text-fg-muted">{emptyText}</p>
        ) : filtered.length === 0 ? (
          <p className="px-3 py-2 text-xs text-fg-muted">No matches for &quot;{search}&quot;.</p>
        ) : (
          filtered.map((item) => {
            const key = getKey(item);
            const checked = selected.has(key);
            return (
              <label
                key={key}
                className="flex items-center gap-2 px-3 py-1.5 cursor-pointer hover:bg-surface/40 transition-colors border-b border-border-subtle/60 last:border-b-0"
              >
                <input
                  type="checkbox"
                  checked={checked}
                  onChange={() => onToggle(key)}
                  disabled={disabled}
                  className="shrink-0"
                />
                <div className="min-w-0 flex-1">
                  <div className="text-xs font-mono text-fg truncate">{getLabel(item)}</div>
                  {getDetail?.(item) ? (
                    <div className="text-[10px] text-fg-muted truncate">{getDetail(item)}</div>
                  ) : null}
                </div>
              </label>
            );
          })
        )}
      </div>
    </div>
  );
}
