import type { RunStatus, StepStatus, WorkflowRun } from "@/lib/types";

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

interface WorkflowRunCardProps {
  run: WorkflowRun;
  onClick: () => void;
}

function statusDotClass(status: RunStatus): string {
  switch (status) {
    case "running":
      return "bg-success animate-pulse";
    case "completed":
      return "bg-success";
    case "failed":
      return "bg-danger";
    case "cancelled":
      return "bg-fg-muted/40";
    case "pending":
      return "bg-warning animate-pulse";
  }
}

function statusBadgeClass(status: RunStatus): string {
  switch (status) {
    case "running":
      return "bg-success/20 text-success";
    case "completed":
      return "bg-success/20 text-success";
    case "failed":
      return "bg-danger/20 text-danger";
    case "cancelled":
      return "bg-fg-muted/10 text-fg-muted";
    case "pending":
      return "bg-warning/20 text-warning";
  }
}

function progressBarClass(status: RunStatus): string {
  return status === "failed" ? "bg-danger" : "bg-success";
}

function stepPillPrefix(status: StepStatus): string {
  switch (status) {
    case "completed":
      return "✓";
    case "running":
      return "▸";
    case "failed":
      return "✗";
    case "skipped":
    case "cancelled":
      return "⊘";
    default:
      return "·";
  }
}

function stepPillClass(status: StepStatus): string {
  switch (status) {
    case "completed":
      return "bg-success/20 text-success";
    case "running":
      return "bg-info/20 text-info";
    case "failed":
      return "bg-danger/20 text-danger";
    case "skipped":
    case "cancelled":
      return "bg-fg-muted/10 text-fg-muted";
    default:
      return "bg-surface/40 text-fg-faint";
  }
}

function timeLabel(run: WorkflowRun): string {
  const { status, started_at, completed_at } = run.run;
  if (!started_at) return "";
  if (status === "running" || status === "pending") {
    return `Started ${timeAgo(started_at)}`;
  }
  if (status === "completed") {
    const ref = completed_at || started_at;
    return `Finished ${timeAgo(ref)}`;
  }
  if (status === "failed") {
    const ref = completed_at || started_at;
    return `Failed ${timeAgo(ref)}`;
  }
  return `Started ${timeAgo(started_at)}`;
}

export function WorkflowRunCard({ run, onClick }: WorkflowRunCardProps) {
  const { pipeline, run: runState } = run;
  const status = runState.status;
  const isTerminal = status === "completed" || status === "failed" || status === "cancelled";

  const stepStates = Object.entries(runState.step_states ?? {});
  const totalSteps = Math.max(pipeline.step_count, stepStates.length);
  const completedSteps = stepStates.filter(([, s]) => s.status === "completed").length;
  const progress = totalSteps > 0 ? (completedSteps / totalSteps) * 100 : 0;

  const time = timeLabel(run);
  const stepCountLabel = `${completedSteps}/${totalSteps} steps`;

  return (
    <button
      type="button"
      onClick={onClick}
      className={`w-full text-left px-3 py-2.5 rounded-lg border border-border-subtle bg-surface/40 hover:bg-surface/70 transition-colors ${
        isTerminal ? "opacity-70" : ""
      }`}
    >
      {/* Row 1: status dot + name + badge */}
      <div className="flex items-center gap-2 min-w-0">
        <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${statusDotClass(status)}`} />
        <span className="text-xs font-medium text-fg truncate flex-1">{pipeline.name}</span>
        <span
          className={`text-[10px] px-1.5 py-0.5 rounded-full font-medium shrink-0 ${statusBadgeClass(status)}`}
        >
          {status}
        </span>
      </div>

      {/* Row 2: time + step count */}
      {(time || stepCountLabel) && (
        <div className="mt-1 flex items-center gap-1.5 text-[11px] text-fg-faint pl-3.5">
          {time && <span>{time}</span>}
          {time && stepCountLabel && <span>·</span>}
          <span>{stepCountLabel}</span>
        </div>
      )}

      {/* Row 3: progress bar */}
      <div className="mt-2 h-0.5 bg-border rounded-full overflow-hidden">
        <div
          className={`h-full rounded-full transition-all ${progressBarClass(status)}`}
          style={{ width: `${progress}%` }}
        />
      </div>

      {/* Row 4: step pills */}
      {stepStates.length > 0 && (
        <div className="mt-2 flex flex-wrap gap-1 pl-0.5">
          {stepStates.map(([stepId, stepState]) => (
            <span
              key={stepId}
              className={`text-[10px] px-1.5 py-0.5 rounded font-mono ${stepPillClass(stepState.status)}`}
            >
              {stepPillPrefix(stepState.status)} {stepId}
            </span>
          ))}
        </div>
      )}
    </button>
  );
}
