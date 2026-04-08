import { Users, XCircle } from "lucide-react";
import { useState } from "react";
import { useCancelWorker, useWorkers } from "@/hooks/useObservability";
import type { Worker, WorkerStatus, WorkerType } from "@/lib/types";

function formatRelativeTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  const s = Math.floor(diff / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

function TypeBadge({ type }: { type: WorkerType }) {
  if (type === "full") {
    return (
      <span className="text-[10px] px-1.5 py-0.5 rounded bg-info/20 text-info font-medium">
        full
      </span>
    );
  }
  return (
    <span className="text-[10px] px-1.5 py-0.5 rounded bg-fg-muted/20 text-fg-muted font-medium">
      light
    </span>
  );
}

function StatusBadge({ status }: { status: WorkerStatus }) {
  switch (status) {
    case "spawning":
      return (
        <span className="text-[10px] px-1.5 py-0.5 rounded bg-warning/20 text-warning font-medium animate-pulse">
          spawning
        </span>
      );
    case "running":
      return (
        <span className="text-[10px] px-1.5 py-0.5 rounded bg-info/20 text-info font-medium">
          running
        </span>
      );
    case "completed":
      return (
        <span className="text-[10px] px-1.5 py-0.5 rounded bg-success/20 text-success font-medium">
          completed
        </span>
      );
    case "failed":
      return (
        <span className="text-[10px] px-1.5 py-0.5 rounded bg-danger/20 text-danger font-medium">
          failed
        </span>
      );
    case "cancelled":
      return (
        <span className="text-[10px] px-1.5 py-0.5 rounded bg-fg-muted/20 text-fg-muted font-medium">
          cancelled
        </span>
      );
  }
}

function WorkerRow({
  worker,
  onCancel,
  isCancelling,
}: {
  worker: Worker;
  onCancel: (id: string) => void;
  isCancelling: boolean;
}) {
  const isActive = worker.status === "spawning" || worker.status === "running";

  return (
    <tr className="border-b border-border/30 hover:bg-surface/20 transition-colors">
      <td className="py-1.5 pr-3 font-mono text-fg-secondary text-xs max-w-[120px] truncate">
        {worker.agent_id}
      </td>
      <td className="py-1.5 pr-3">
        <TypeBadge type={worker.type} />
      </td>
      <td className="py-1.5 pr-3">
        <StatusBadge status={worker.status} />
      </td>
      <td className="py-1.5 pr-3 font-mono text-fg-secondary text-xs">
        {worker.parent_session_id.slice(0, 8)}
      </td>
      <td className="py-1.5 pr-3 text-fg-secondary text-xs tabular-nums">
        {formatRelativeTime(worker.created_at)}
      </td>
      <td className="py-1.5 pr-3 font-mono text-fg-faint text-[10px] max-w-[160px] truncate">
        {worker.worktree_path ?? "—"}
      </td>
      <td className="py-1.5 text-center">
        {isActive && (
          <div className="flex justify-center">
            <button
              type="button"
              onClick={() => onCancel(worker.id)}
              disabled={isCancelling}
              className="flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded bg-danger/10 text-danger hover:bg-danger/20 transition-colors disabled:opacity-50"
            >
              <XCircle className="w-3 h-3" />
              Cancel
            </button>
          </div>
        )}
      </td>
    </tr>
  );
}

export function WorkerStatusPanel() {
  const [cancellingIds, setCancellingIds] = useState<Set<string>>(new Set());

  const { data: workers = [], isLoading } = useWorkers();
  const cancel = useCancelWorker();

  function handleCancel(id: string) {
    setCancellingIds((prev) => new Set(prev).add(id));
    cancel.mutate(id, {
      onSettled: () => {
        setCancellingIds((prev) => {
          const next = new Set(prev);
          next.delete(id);
          return next;
        });
      },
    });
  }

  const activeCount = workers.filter(
    (w) => w.status === "spawning" || w.status === "running",
  ).length;

  if (isLoading) {
    return <p className="text-xs text-fg-faint italic py-2">Loading workers...</p>;
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2 text-xs text-fg-secondary">
        <Users className="w-3.5 h-3.5" />
        <span>
          {workers.length} worker{workers.length !== 1 && "s"}
          {activeCount > 0 && <span className="text-info ml-1">({activeCount} active)</span>}
        </span>
      </div>

      {workers.length === 0 ? (
        <p className="text-xs text-fg-faint italic py-2">No workers found</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead>
              <tr className="text-[10px] uppercase tracking-wider text-fg-muted border-b border-border">
                <th className="text-left py-2 pr-3 font-medium">Agent</th>
                <th className="text-left py-2 pr-3 font-medium">Type</th>
                <th className="text-left py-2 pr-3 font-medium">Status</th>
                <th className="text-left py-2 pr-3 font-medium">Parent Session</th>
                <th className="text-left py-2 pr-3 font-medium">Created</th>
                <th className="text-left py-2 pr-3 font-medium">Worktree</th>
                <th className="text-center py-2 font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {workers.map((worker) => (
                <WorkerRow
                  key={worker.id}
                  worker={worker}
                  onCancel={handleCancel}
                  isCancelling={cancellingIds.has(worker.id)}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
