import { useQuery } from "@tanstack/react-query";
import { FolderKanban, Plus } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import { NewProjectDialog } from "@/components/sidebar/NewProjectDialog";
import { api } from "@/lib/api";
import { useAppStore } from "@/stores/useAppStore";

export function WorkspaceProjectManager() {
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId);
  const activeProjectId = useAppStore((s) => s.activeProjectId);
  const setActiveProject = useAppStore((s) => s.setActiveProject);

  const [showCreateProject, setShowCreateProject] = useState(false);

  const { data: projects = [], isLoading: projectsLoading } = useQuery({
    queryKey: ["projects", activeWorkspaceId],
    queryFn: () => api.listProjects(activeWorkspaceId!),
    enabled: !!activeWorkspaceId,
  });

  return (
    <div className="space-y-8">
      <section className="space-y-4">
        <div className="flex items-center justify-between">
          <h2 className="text-xl font-semibold text-fg">Projects</h2>
          <Button
            onClick={() => setShowCreateProject(true)}
            disabled={!activeWorkspaceId}
            className="gap-2"
          >
            <Plus className="size-4" />
            New Project
          </Button>
        </div>

        {projectsLoading ? (
          <div className="grid grid-cols-2 gap-3">
            {Array.from({ length: 2 }).map((_, i) => (
              <div
                key={i}
                className="overflow-hidden rounded-[10px] border border-border-subtle bg-bg-elevated"
              >
                <div className="flex items-center gap-2.5 px-3.5 py-3">
                  <Skeleton className="size-9 rounded-lg" />
                  <div className="flex flex-1 flex-col gap-1.5">
                    <Skeleton className="h-3.5 w-1/2" />
                    <Skeleton className="h-2.5 w-1/3" />
                  </div>
                </div>
              </div>
            ))}
          </div>
        ) : projects.length === 0 ? (
          <Empty className="py-8">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <FolderKanban />
              </EmptyMedia>
              <EmptyTitle className="text-sm">No projects yet</EmptyTitle>
              <EmptyDescription className="text-xs">
                Create a project to organize chats and assign agents.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <div className="grid grid-cols-2 gap-3">
            {projects.map((project) => {
              const isActive = project.id === activeProjectId;
              return (
                <button
                  type="button"
                  key={project.id}
                  className={`overflow-hidden rounded-xl border text-left shadow-sm transition-all hover:shadow-md ${
                    isActive
                      ? "border-primary/30 bg-primary/5"
                      : "border-border-subtle bg-bg-elevated"
                  }`}
                  onClick={() => setActiveProject(isActive ? null : project.id)}
                >
                  <div className="flex items-center gap-2.5 px-3.5 py-3">
                    <span
                      className={`inline-flex size-9 shrink-0 items-center justify-center rounded-lg text-sm ${
                        isActive ? "bg-primary/15 text-primary" : "bg-surface-hover text-fg-secondary"
                      }`}
                    >
                      <FolderKanban className="size-4" />
                    </span>
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <span className="truncate text-sm font-semibold text-fg">
                          {project.name}
                        </span>
                        {isActive && (
                          <span className="size-1.5 shrink-0 rounded-full bg-status-ok" />
                        )}
                      </div>
                      {project.description && (
                        <span className="block truncate text-[11px] text-fg-muted">
                          {project.description}
                        </span>
                      )}
                    </div>
                  </div>
                  {project.repo_path && (
                    <div className="border-t border-divider bg-surface px-3.5 py-2">
                      <span className="block truncate font-mono text-[11px] text-fg-muted">
                        {project.repo_path}
                      </span>
                    </div>
                  )}
                </button>
              );
            })}
          </div>
        )}
      </section>

      <NewProjectDialog open={showCreateProject} onOpenChange={setShowCreateProject} />
    </div>
  );
}
