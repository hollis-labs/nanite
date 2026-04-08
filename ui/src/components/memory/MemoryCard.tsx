import type { Memory, MemoryStatus } from "@/lib/types";

function timeAgo(dateStr: string): string {
  const diff = Date.now() - new Date(dateStr).getTime();
  const seconds = Math.floor(diff / 1000);
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

function statusBadgeClass(status: MemoryStatus): string {
  switch (status) {
    case "canonical":
      return "bg-success/20 text-success";
    case "reviewed":
      return "bg-purple-500/20 text-purple-300";
    case "draft":
      return "bg-info/20 text-info";
    case "deprecated":
      return "bg-fg-muted/10 text-fg-muted";
  }
}

function cardOpacity(status: MemoryStatus): string {
  switch (status) {
    case "deprecated":
      return "opacity-50";
    case "draft":
      return "opacity-75";
    default:
      return "";
  }
}

interface MemoryCardProps {
  memory: Memory;
  onClick: () => void;
}

export function MemoryCard({ memory, onClick }: MemoryCardProps) {
  const confidencePct = Math.round(memory.confidence * 100);

  return (
    <button
      type="button"
      onClick={onClick}
      className={`w-full text-left px-3 py-2.5 rounded-lg border border-border-subtle bg-surface/40 hover:bg-surface/70 transition-colors ${cardOpacity(memory.status)}`}
    >
      {/* Row 1: summary + status badge */}
      <div className="flex items-start gap-2 min-w-0">
        <span className="text-xs font-medium text-fg truncate flex-1 leading-relaxed">
          {memory.summary}
        </span>
        <span
          className={`text-[10px] px-1.5 py-0.5 rounded-full font-medium shrink-0 ${statusBadgeClass(memory.status)}`}
        >
          {memory.status}
        </span>
      </div>

      {/* Row 2: body preview */}
      {memory.body && (
        <p className="mt-1 text-[11px] text-fg-muted line-clamp-2 leading-relaxed">{memory.body}</p>
      )}

      {/* Row 3: metadata */}
      <div className="mt-1.5 flex flex-wrap items-center gap-1.5 text-[10px]">
        <span className="px-1.5 py-0.5 rounded bg-surface/60 text-fg-faint border border-border-subtle">
          {memory.scope}
        </span>
        <span className="px-1.5 py-0.5 rounded bg-surface/60 text-fg-faint border border-border-subtle">
          {memory.origin}
        </span>
        <span className="text-amber-400/80 font-medium">{confidencePct}%</span>
        <span className="text-fg-faint ml-auto">{timeAgo(memory.updated_at)}</span>
      </div>
    </button>
  );
}
