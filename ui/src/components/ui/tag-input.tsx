import { X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

// Reuse tag color palette from TagPills
const TAG_COLORS = [
  { bg: "bg-info/15", border: "border-info/30", text: "text-info" },
  { bg: "bg-purple-500/10", border: "border-purple-500/20", text: "text-purple-400" },
  { bg: "bg-warning/10", border: "border-warning/20", text: "text-warning" },
  { bg: "bg-cyan-500/10", border: "border-cyan-500/20", text: "text-cyan-400" },
  { bg: "bg-pink-500/10", border: "border-pink-500/20", text: "text-pink-400" },
  { bg: "bg-success/10", border: "border-success/20", text: "text-success" },
  { bg: "bg-warning/10", border: "border-orange-500/20", text: "text-warning" },
  { bg: "bg-indigo-500/10", border: "border-indigo-500/20", text: "text-indigo-400" },
];

function hashTag(tag: string): number {
  let hash = 0;
  for (let i = 0; i < tag.length; i++) {
    hash = ((hash << 5) - hash + tag.charCodeAt(i)) | 0;
  }
  return Math.abs(hash) % TAG_COLORS.length;
}

interface TagInputProps {
  /** Current tags */
  value: string[];
  /** Called when tags change */
  onChange: (tags: string[]) => void;
  /** Known tags for autocomplete suggestions */
  suggestions?: string[];
  /** Placeholder when empty */
  placeholder?: string;
  disabled?: boolean;
}

export function TagInput({
  value,
  onChange,
  suggestions = [],
  placeholder = "Add tag...",
  disabled,
}: TagInputProps) {
  const [input, setInput] = useState("");
  const [showSuggestions, setShowSuggestions] = useState(false);
  const [selectedIndex, setSelectedIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  const filtered = useMemo(() => {
    if (!input.trim()) return suggestions.filter((s) => !value.includes(s)).slice(0, 8);
    const q = input.toLowerCase();
    return suggestions.filter((s) => s.toLowerCase().includes(q) && !value.includes(s)).slice(0, 8);
  }, [input, suggestions, value]);

  const addTag = useCallback(
    (tag: string) => {
      const trimmed = tag.trim().toLowerCase();
      if (!trimmed || value.includes(trimmed)) return;
      onChange([...value, trimmed]);
      setInput("");
      setShowSuggestions(false);
      setSelectedIndex(0);
    },
    [value, onChange],
  );

  const removeTag = useCallback(
    (tag: string) => {
      onChange(value.filter((t) => t !== tag));
    },
    [value, onChange],
  );

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === "Enter" || e.key === "Tab" || e.key === ",") {
        e.preventDefault();
        if (showSuggestions && filtered.length > 0 && selectedIndex < filtered.length) {
          addTag(filtered[selectedIndex]);
        } else if (input.trim()) {
          addTag(input);
        }
      } else if (e.key === "Backspace" && !input && value.length > 0) {
        removeTag(value[value.length - 1]);
      } else if (e.key === "ArrowDown") {
        e.preventDefault();
        setSelectedIndex((i) => Math.min(i + 1, filtered.length - 1));
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        setSelectedIndex((i) => Math.max(i - 1, 0));
      } else if (e.key === "Escape") {
        setShowSuggestions(false);
      }
    },
    [input, value, showSuggestions, filtered, selectedIndex, addTag, removeTag],
  );

  // Click outside to close
  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setShowSuggestions(false);
      }
    }
    document.addEventListener("mousedown", handleClick);
    return () => document.removeEventListener("mousedown", handleClick);
  }, []);

  // Reset index when filtered changes
  useEffect(() => {
    setSelectedIndex(0);
  }, [filtered.length]);

  return (
    <div ref={containerRef} className="relative">
      <div
        className={`flex flex-wrap items-center gap-1 px-2 py-1.5 min-h-[36px] rounded-lg border transition-colors ${
          showSuggestions ? "border-primary/50 ring-1 ring-primary/20" : "border-border-subtle"
        } bg-bg-elevated`}
        onClick={() => inputRef.current?.focus()}
      >
        {value.map((tag) => {
          const color = TAG_COLORS[hashTag(tag)]!;
          return (
            <span
              key={tag}
              className={`inline-flex items-center gap-0.5 text-[11px] px-1.5 py-0.5 rounded-md border leading-none ${color.bg} ${color.border} ${color.text}`}
            >
              {tag}
              {!disabled && (
                <button
                  type="button"
                  onClick={(e) => {
                    e.stopPropagation();
                    removeTag(tag);
                  }}
                  className="ml-0.5 p-0 rounded hover:opacity-70 transition-opacity"
                >
                  <X className="w-2.5 h-2.5" />
                </button>
              )}
            </span>
          );
        })}
        <input
          ref={inputRef}
          type="text"
          value={input}
          onChange={(e) => {
            setInput(e.target.value);
            setShowSuggestions(true);
          }}
          onFocus={() => setShowSuggestions(true)}
          onKeyDown={handleKeyDown}
          disabled={disabled}
          placeholder={value.length === 0 ? placeholder : ""}
          className="flex-1 min-w-[60px] bg-transparent text-xs text-fg placeholder:text-fg-faint outline-none"
        />
      </div>

      {/* Autocomplete dropdown */}
      {showSuggestions && filtered.length > 0 && (
        <div className="absolute z-50 mt-1 w-full bg-bg-elevated border border-border-subtle rounded-lg shadow-lg overflow-hidden">
          {filtered.map((suggestion, i) => {
            const color = TAG_COLORS[hashTag(suggestion)]!;
            return (
              <button
                key={suggestion}
                type="button"
                className={`w-full flex items-center gap-2 px-3 py-1.5 text-left text-xs transition-colors ${
                  i === selectedIndex
                    ? "bg-surface-hover text-fg"
                    : "text-fg-secondary hover:bg-surface/50"
                }`}
                onMouseEnter={() => setSelectedIndex(i)}
                onClick={() => addTag(suggestion)}
              >
                <span className={`w-2 h-2 rounded-full ${color.bg} ${color.border} border`} />
                {suggestion}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
