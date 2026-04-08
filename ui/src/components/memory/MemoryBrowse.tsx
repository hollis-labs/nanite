import { Brain, Plus, Search } from "lucide-react";
import { useState } from "react";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Skeleton } from "@/components/ui/skeleton";
import { useMemories } from "@/hooks/useMemories";
import { MemoryCard } from "./MemoryCard";

type ScopeFilter = "" | "session" | "project" | "user";
type StatusFilter = "" | "canonical" | "draft" | "reviewed" | "deprecated";

interface MemoryBrowseProps {
  onSelect: (key: string) => void;
  onCreate: () => void;
}

const SCOPE_OPTIONS: { label: string; value: ScopeFilter }[] = [
  { label: "All", value: "" },
  { label: "User", value: "user" },
  { label: "Project", value: "project" },
  { label: "Session", value: "session" },
];

const STATUS_OPTIONS: { label: string; value: StatusFilter }[] = [
  { label: "All", value: "" },
  { label: "Canonical", value: "canonical" },
  { label: "Reviewed", value: "reviewed" },
  { label: "Draft", value: "draft" },
  { label: "Deprecated", value: "deprecated" },
];

function pillClass(active: boolean): string {
  return active
    ? "bg-indigo-500/20 text-indigo-300 border border-indigo-500/40"
    : "bg-surface/40 text-fg-muted hover:text-fg border border-transparent";
}

export function MemoryBrowse({ onSelect, onCreate }: MemoryBrowseProps) {
  const [search, setSearch] = useState("");
  const [scope, setScope] = useState<ScopeFilter>("");
  const [status, setStatus] = useState<StatusFilter>("");

  const filters = {
    q: search || undefined,
    scope: scope || undefined,
    status: status || undefined,
  };

  const { data, isLoading } = useMemories(filters);
  const memories = data?.memories ?? [];
  const total = data?.total ?? 0;

  return (
    <div className="flex flex-col h-full min-h-0">
      {/* Header */}
      <div className="px-3 pt-3 pb-2 shrink-0 flex items-center gap-2 border-b border-border-subtle">
        <span className="text-sm font-medium text-fg flex-1">Memories</span>
        {total > 0 && (
          <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-surface/60 text-fg-faint border border-border-subtle">
            {total}
          </span>
        )}
        <button
          type="button"
          onClick={onCreate}
          className="flex items-center gap-1 text-[11px] px-2 py-1 rounded bg-indigo-500/20 text-indigo-300 hover:bg-indigo-500/30 border border-indigo-500/40 transition-colors"
        >
          <Plus className="size-3" />
          New
        </button>
      </div>

      {/* Search + filters */}
      <div className="px-3 py-2 space-y-2 shrink-0 border-b border-border-subtle">
        {/* Search input */}
        <div className="flex items-center gap-1.5 bg-bg-elevated rounded px-2 py-1.5 border border-border-subtle">
          <Search className="size-3 text-fg-faint shrink-0" />
          <input
            type="text"
            placeholder="Search memories…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="flex-1 text-xs bg-transparent text-fg placeholder:text-fg-faint outline-none min-w-0 font-sans"
          />
        </div>

        {/* Scope pills */}
        <div className="flex flex-wrap gap-1">
          {SCOPE_OPTIONS.map((opt) => (
            <button
              key={opt.value}
              type="button"
              onClick={() => setScope(opt.value)}
              className={`text-[10px] px-2 py-0.5 rounded-full transition-colors ${pillClass(scope === opt.value)}`}
            >
              {opt.label}
            </button>
          ))}
        </div>

        {/* Status pills */}
        <div className="flex flex-wrap gap-1">
          {STATUS_OPTIONS.map((opt) => (
            <button
              key={opt.value}
              type="button"
              onClick={() => setStatus(opt.value)}
              className={`text-[10px] px-2 py-0.5 rounded-full transition-colors ${pillClass(status === opt.value)}`}
            >
              {opt.label}
            </button>
          ))}
        </div>
      </div>

      {/* Content */}
      {isLoading ? (
        <div className="p-3 space-y-2">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-16 w-full rounded-lg" />
          ))}
        </div>
      ) : memories.length === 0 ? (
        <Empty className="py-12">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <Brain />
            </EmptyMedia>
            <EmptyTitle className="text-sm">No memories found</EmptyTitle>
            <EmptyDescription className="text-xs">
              {search || scope || status
                ? "Try adjusting your filters"
                : "Memories will appear here as they are captured"}
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <ScrollArea className="flex-1 min-h-0">
          <div className="p-3 space-y-2">
            {memories.map((memory) => (
              <MemoryCard
                key={memory.memory_key}
                memory={memory}
                onClick={() => onSelect(memory.memory_key)}
              />
            ))}
          </div>
        </ScrollArea>
      )}
    </div>
  );
}
