import { GitBranch } from "lucide-react";
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
import { useWorkflowEvents, useWorkflowRuns } from "@/hooks/useWorkflows";
import { useLayoutStore } from "@/stores/useLayoutStore";
import { WorkflowRunCard } from "./WorkflowRunCard";
import { WorkflowRunDetail } from "./WorkflowRunDetail";

type Filter = "all" | "active";

export function WorkflowTab() {
  const [filter, setFilter] = useState<Filter>("active");
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null);
  const currentPage = useLayoutStore((s) => s.currentPage);

  const queryFilter = filter === "active" ? { status: "running" } : undefined;
  const { data: runs = [], isLoading } = useWorkflowRuns(queryFilter);

  // Subscribe to SSE events for live invalidation only when on the workflows page
  useWorkflowEvents(currentPage === "workflows");

  if (selectedRunId) {
    return <WorkflowRunDetail runId={selectedRunId} onBack={() => setSelectedRunId(null)} />;
  }

  return (
    <div className="flex flex-col h-full min-h-0">
      {/* Filter bar */}
      <div className="px-3 py-2 border-b border-border-subtle shrink-0">
        <div className="flex bg-bg-elevated rounded p-0.5 gap-0.5 w-fit">
          <button
            type="button"
            onClick={() => setFilter("active")}
            className={`text-[10px] px-2 py-0.5 rounded transition-colors ${
              filter === "active" ? "bg-surface text-fg" : "text-fg-faint hover:text-fg-muted"
            }`}
          >
            Active
          </button>
          <button
            type="button"
            onClick={() => setFilter("all")}
            className={`text-[10px] px-2 py-0.5 rounded transition-colors ${
              filter === "all" ? "bg-surface text-fg" : "text-fg-faint hover:text-fg-muted"
            }`}
          >
            All
          </button>
        </div>
      </div>

      {/* Content */}
      {isLoading ? (
        <div className="p-3 space-y-2">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-20 w-full rounded-lg" />
          ))}
        </div>
      ) : runs.length === 0 ? (
        <Empty className="py-12">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <GitBranch />
            </EmptyMedia>
            <EmptyTitle className="text-sm">
              {filter === "active" ? "No active runs" : "No workflow runs"}
            </EmptyTitle>
            <EmptyDescription className="text-xs">
              {filter === "active"
                ? "Switch to All to see completed runs"
                : "Workflow runs will appear here when pipelines execute"}
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <ScrollArea className="flex-1 min-h-0">
          <div className="p-3 space-y-2">
            {runs.map((run) => (
              <WorkflowRunCard
                key={run.run.run_id}
                run={run}
                onClick={() => setSelectedRunId(run.run.run_id)}
              />
            ))}
          </div>
        </ScrollArea>
      )}
    </div>
  );
}
