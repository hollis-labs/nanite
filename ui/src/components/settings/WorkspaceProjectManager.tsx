import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Building2, ChevronRight, FolderKanban, Loader2, Plus, Trash2 } from "lucide-react";
import { useCallback, useState } from "react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import { DynamicIcon, IconPicker } from "@/components/ui/icon-picker";
import { Skeleton } from "@/components/ui/skeleton";
import { api } from "@/lib/api";
import type { Workspace } from "@/lib/types";
import { useAppStore } from "@/stores/useAppStore";

export function WorkspaceProjectManager() {
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId);
  const setActiveWorkspace = useAppStore((s) => s.setActiveWorkspace);
  const activeProjectId = useAppStore((s) => s.activeProjectId);
  const setActiveProject = useAppStore((s) => s.setActiveProject);
  const queryClient = useQueryClient();

  const [showCreateWorkspace, setShowCreateWorkspace] = useState(false);
  const [showCreateProject, setShowCreateProject] = useState(false);
  const [deleteWorkspaceId, setDeleteWorkspaceId] = useState<string | null>(null);
  const [editingWorkspace, setEditingWorkspace] = useState<Workspace | null>(null);

  // Queries
  const { data: workspaces = [], isLoading: workspacesLoading } = useQuery({
    queryKey: ["workspaces"],
    queryFn: api.listWorkspaces,
  });

  const { data: projects = [], isLoading: projectsLoading } = useQuery({
    queryKey: ["projects", activeWorkspaceId],
    queryFn: () => api.listProjects(activeWorkspaceId!),
    enabled: !!activeWorkspaceId,
  });

  // Workspace mutations
  const createWorkspaceMutation = useMutation({
    mutationFn: api.createWorkspace,
    onSuccess: (newWs) => {
      void queryClient.invalidateQueries({ queryKey: ["workspaces"] });
      setActiveWorkspace(newWs.id);
      setShowCreateWorkspace(false);
    },
  });

  const updateWorkspaceMutation = useMutation({
    mutationFn: ({ id, data }: { id: string; data: Partial<Workspace> }) =>
      api.updateWorkspace(id, data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["workspaces"] });
      setEditingWorkspace(null);
    },
  });

  const deleteWorkspaceMutation = useMutation({
    mutationFn: api.deleteWorkspace,
    onSuccess: (_data, id) => {
      void queryClient.invalidateQueries({ queryKey: ["workspaces"] });
      if (activeWorkspaceId === id) {
        const remaining = workspaces.filter((w) => w.id !== id);
        setActiveWorkspace(remaining[0]?.id ?? "");
      }
      setDeleteWorkspaceId(null);
    },
  });

  // Project mutations
  const createProjectMutation = useMutation({
    mutationFn: ({
      workspaceId,
      data,
    }: {
      workspaceId: string;
      data: { name: string; description?: string };
    }) => api.createProject(workspaceId, data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["projects", activeWorkspaceId] });
      setShowCreateProject(false);
    },
  });

  const handleCreateWorkspace = useCallback(
    (formData: FormData) => {
      createWorkspaceMutation.mutate({
        name: formData.get("name") as string,
        description: (formData.get("description") as string) || undefined,
        icon: (formData.get("icon") as string) || undefined,
      });
    },
    [createWorkspaceMutation],
  );

  const handleUpdateWorkspace = useCallback(
    (formData: FormData) => {
      if (!editingWorkspace) return;
      updateWorkspaceMutation.mutate({
        id: editingWorkspace.id,
        data: {
          name: formData.get("name") as string,
          description: formData.get("description") as string,
        },
      });
    },
    [editingWorkspace, updateWorkspaceMutation],
  );

  const handleCreateProject = useCallback(
    (formData: FormData) => {
      if (!activeWorkspaceId) return;
      createProjectMutation.mutate({
        workspaceId: activeWorkspaceId,
        data: {
          name: formData.get("name") as string,
          description: (formData.get("description") as string) || undefined,
        },
      });
    },
    [activeWorkspaceId, createProjectMutation],
  );

  const activeWorkspace = workspaces.find((w) => w.id === activeWorkspaceId);

  return (
    <div className="space-y-8">
      {/* Workspaces Section */}
      <section className="space-y-4">
        <div className="flex items-center justify-between">
          <h2 className="text-xl font-semibold text-fg">Workspaces</h2>
          <Button onClick={() => setShowCreateWorkspace(true)} className="gap-2">
            <Plus className="w-4 h-4" />
            New Workspace
          </Button>
        </div>

        {workspacesLoading ? (
          <div className="grid grid-cols-2 gap-3">
            {Array.from({ length: 2 }).map((_, i) => (
              <div
                key={i}
                className="rounded-[10px] border border-border-subtle bg-bg-elevated overflow-hidden"
              >
                <div className="px-3.5 py-3 flex items-center gap-2.5">
                  <Skeleton className="size-9 rounded-lg" />
                  <div className="flex flex-col gap-1.5 flex-1">
                    <Skeleton className="h-3.5 w-1/2" />
                    <Skeleton className="h-2.5 w-1/3" />
                  </div>
                </div>
              </div>
            ))}
          </div>
        ) : workspaces.length === 0 ? (
          <Empty className="py-12">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <Building2 />
              </EmptyMedia>
              <EmptyTitle className="text-sm">No workspaces</EmptyTitle>
              <EmptyDescription className="text-xs">
                Create a workspace to organize your projects and chats.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <div className="grid grid-cols-2 gap-3">
            {workspaces.map((ws) => {
              const isActive = ws.id === activeWorkspaceId;
              return (
                <ContextMenu key={ws.id}>
                  <ContextMenuTrigger asChild>
                    <div
                      className={`rounded-xl border shadow-sm overflow-hidden transition-all cursor-pointer hover:shadow-md ${
                        isActive
                          ? "border-primary/30 bg-primary/5"
                          : "border-border-subtle bg-bg-elevated"
                      }`}
                      onClick={() => setActiveWorkspace(ws.id)}
                    >
                      <div className="flex items-center gap-2.5 px-3.5 py-3">
                        <span
                          className={`inline-flex items-center justify-center w-9 h-9 rounded-lg text-sm shrink-0 ${
                            isActive ? "bg-primary/15 text-primary" : "bg-surface-hover text-fg-secondary"
                          }`}
                        >
                          <DynamicIcon name={ws.icon} className="w-4 h-4" fallback={Building2} />
                        </span>
                        <div className="flex-1 min-w-0">
                          <div className="flex items-center gap-2">
                            <span className="text-sm font-semibold text-fg truncate">
                              {ws.name}
                            </span>
                            {isActive && (
                              <span className="w-1.5 h-1.5 rounded-full bg-status-ok shrink-0" />
                            )}
                          </div>
                          {ws.description && (
                            <span className="text-[11px] text-fg-muted truncate block">
                              {ws.description}
                            </span>
                          )}
                        </div>
                        <ChevronRight className="w-4 h-4 text-fg-faint shrink-0" />
                      </div>
                    </div>
                  </ContextMenuTrigger>
                  <ContextMenuContent>
                    <ContextMenuItem
                      className="gap-2 text-xs"
                      onSelect={() => setEditingWorkspace(ws)}
                    >
                      <Building2 className="size-3.5" />
                      Edit
                    </ContextMenuItem>
                    <ContextMenuSeparator />
                    <ContextMenuItem
                      className="gap-2 text-xs text-primary focus:text-primary"
                      onSelect={() => setDeleteWorkspaceId(ws.id)}
                    >
                      <Trash2 className="size-3.5" />
                      Delete
                    </ContextMenuItem>
                  </ContextMenuContent>
                </ContextMenu>
              );
            })}
          </div>
        )}
      </section>

      {/* Projects Section (for active workspace) */}
      {activeWorkspaceId && (
        <section className="space-y-4">
          <div className="flex items-center justify-between">
            <h2 className="text-xl font-semibold text-fg flex items-center gap-2">
              Projects
              {activeWorkspace && (
                <span className="text-sm font-normal text-fg-muted">in {activeWorkspace.name}</span>
              )}
            </h2>
            <Button onClick={() => setShowCreateProject(true)} className="gap-2">
              <Plus className="w-4 h-4" />
              New Project
            </Button>
          </div>

          {projectsLoading ? (
            <div className="grid grid-cols-2 gap-3">
              {Array.from({ length: 2 }).map((_, i) => (
                <div
                  key={i}
                  className="rounded-[10px] border border-border-subtle bg-bg-elevated overflow-hidden"
                >
                  <div className="px-3.5 py-3 flex items-center gap-2.5">
                    <Skeleton className="size-9 rounded-lg" />
                    <div className="flex flex-col gap-1.5 flex-1">
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
                  <div
                    key={project.id}
                    className={`rounded-xl border shadow-sm overflow-hidden transition-all cursor-pointer hover:shadow-md ${
                      isActive
                        ? "border-primary/30 bg-primary/5"
                        : "border-border-subtle bg-bg-elevated"
                    }`}
                    onClick={() => setActiveProject(isActive ? "" : project.id)}
                  >
                    <div className="flex items-center gap-2.5 px-3.5 py-3">
                      <span
                        className={`inline-flex items-center justify-center w-9 h-9 rounded-lg text-sm shrink-0 ${
                          isActive ? "bg-primary/15 text-primary" : "bg-surface-hover text-fg-secondary"
                        }`}
                      >
                        <FolderKanban className="w-4 h-4" />
                      </span>
                      <div className="flex-1 min-w-0">
                        <div className="flex items-center gap-2">
                          <span className="text-sm font-semibold text-fg truncate">
                            {project.name}
                          </span>
                          {isActive && (
                            <span className="w-1.5 h-1.5 rounded-full bg-status-ok shrink-0" />
                          )}
                        </div>
                        {project.description && (
                          <span className="text-[11px] text-fg-muted truncate block">
                            {project.description}
                          </span>
                        )}
                      </div>
                    </div>
                    {project.repo_path && (
                      <div className="border-t border-divider px-3.5 py-2 bg-surface">
                        <span className="text-[11px] text-fg-muted font-mono truncate block">
                          {project.repo_path}
                        </span>
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </section>
      )}

      {/* Create Workspace Dialog */}
      <Dialog open={showCreateWorkspace} onOpenChange={setShowCreateWorkspace}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Create Workspace</DialogTitle>
            <DialogDescription>
              A workspace groups related projects and chat sessions.
            </DialogDescription>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              handleCreateWorkspace(new FormData(e.currentTarget));
            }}
            className="space-y-4"
          >
            <div>
              <label className="block font-mono text-[11px] font-semibold uppercase tracking-[0.04em] text-fg-muted mb-1.5">Name</label>
              <input
                name="name"
                type="text"
                required
                autoFocus
                className="w-full px-3 py-[7px] bg-surface border border-border-subtle rounded-[7px] text-[13px] text-fg focus:outline-none focus:ring-1 focus:ring-primary focus:border-primary"
                placeholder="My Workspace"
              />
            </div>
            <div>
              <label className="block font-mono text-[11px] font-semibold uppercase tracking-[0.04em] text-fg-muted mb-1.5">
                Description
              </label>
              <input
                name="description"
                type="text"
                className="w-full px-3 py-[7px] bg-surface border border-border-subtle rounded-[7px] text-[13px] text-fg focus:outline-none focus:ring-1 focus:ring-primary focus:border-primary"
                placeholder="Optional description"
              />
            </div>
            <DialogFooter>
              <Button type="button" variant="ghost" onClick={() => setShowCreateWorkspace(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={createWorkspaceMutation.isPending} className="gap-2">
                {createWorkspaceMutation.isPending && (
                  <Loader2 className="w-3.5 h-3.5 animate-spin" />
                )}
                Create
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* Edit Workspace Dialog */}
      <Dialog
        open={!!editingWorkspace}
        onOpenChange={(open) => {
          if (!open) setEditingWorkspace(null);
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Edit Workspace</DialogTitle>
            <DialogDescription>Update workspace name and description.</DialogDescription>
          </DialogHeader>
          {editingWorkspace && (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                handleUpdateWorkspace(new FormData(e.currentTarget));
              }}
              className="space-y-4"
            >
              <div>
                <label className="block font-mono text-[11px] font-semibold uppercase tracking-[0.04em] text-fg-muted mb-1.5">Name</label>
                <input
                  name="name"
                  type="text"
                  required
                  autoFocus
                  defaultValue={editingWorkspace.name}
                  className="w-full px-3 py-[7px] bg-surface border border-border-subtle rounded-[7px] text-[13px] text-fg focus:outline-none focus:ring-1 focus:ring-primary focus:border-primary"
                />
              </div>
              <div>
                <label className="block font-mono text-[11px] font-semibold uppercase tracking-[0.04em] text-fg-muted mb-1.5">
                  Description
                </label>
                <input
                  name="description"
                  type="text"
                  defaultValue={editingWorkspace.description}
                  className="w-full px-3 py-[7px] bg-surface border border-border-subtle rounded-[7px] text-[13px] text-fg focus:outline-none focus:ring-1 focus:ring-primary focus:border-primary"
                />
              </div>
              <div className="flex items-center justify-between">
                <label className="font-mono text-[11px] font-semibold uppercase tracking-[0.04em] text-fg-muted">Icon</label>
                <IconPicker
                  value={editingWorkspace.icon || ""}
                  onChange={(iconName) => {
                    updateWorkspaceMutation.mutate({
                      id: editingWorkspace.id,
                      data: { icon: iconName },
                    });
                  }}
                />
              </div>
              <DialogFooter>
                <Button type="button" variant="ghost" onClick={() => setEditingWorkspace(null)}>
                  Cancel
                </Button>
                <Button
                  type="submit"
                  disabled={updateWorkspaceMutation.isPending}
                  className="gap-2"
                >
                  {updateWorkspaceMutation.isPending && (
                    <Loader2 className="w-3.5 h-3.5 animate-spin" />
                  )}
                  Save
                </Button>
              </DialogFooter>
            </form>
          )}
        </DialogContent>
      </Dialog>

      {/* Create Project Dialog */}
      <Dialog open={showCreateProject} onOpenChange={setShowCreateProject}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Create Project</DialogTitle>
            <DialogDescription>
              A project scopes chats and agents within {activeWorkspace?.name || "this workspace"}.
            </DialogDescription>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              handleCreateProject(new FormData(e.currentTarget));
            }}
            className="space-y-4"
          >
            <div>
              <label className="block font-mono text-[11px] font-semibold uppercase tracking-[0.04em] text-fg-muted mb-1.5">Name</label>
              <input
                name="name"
                type="text"
                required
                autoFocus
                className="w-full px-3 py-[7px] bg-surface border border-border-subtle rounded-[7px] text-[13px] text-fg focus:outline-none focus:ring-1 focus:ring-primary focus:border-primary"
                placeholder="My Project"
              />
            </div>
            <div>
              <label className="block font-mono text-[11px] font-semibold uppercase tracking-[0.04em] text-fg-muted mb-1.5">
                Description
              </label>
              <input
                name="description"
                type="text"
                className="w-full px-3 py-[7px] bg-surface border border-border-subtle rounded-[7px] text-[13px] text-fg focus:outline-none focus:ring-1 focus:ring-primary focus:border-primary"
                placeholder="Optional description"
              />
            </div>
            <DialogFooter>
              <Button type="button" variant="ghost" onClick={() => setShowCreateProject(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={createProjectMutation.isPending} className="gap-2">
                {createProjectMutation.isPending && (
                  <Loader2 className="w-3.5 h-3.5 animate-spin" />
                )}
                Create
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* Delete Workspace Confirmation */}
      <AlertDialog
        open={!!deleteWorkspaceId}
        onOpenChange={(open) => {
          if (!open) setDeleteWorkspaceId(null);
        }}
      >
        <AlertDialogContent className="sm:max-w-md">
          <AlertDialogHeader>
            <AlertDialogTitle>Delete workspace?</AlertDialogTitle>
            <AlertDialogDescription>
              This will permanently delete this workspace, all its projects, and all associated chat
              sessions. This action cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => deleteWorkspaceId && deleteWorkspaceMutation.mutate(deleteWorkspaceId)}
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
