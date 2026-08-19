import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { api } from "@/lib/api";
import { useAppStore } from "@/stores/useAppStore";

interface NewProjectDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated?: (projectId: string) => void;
}

export function NewProjectDialog({ open, onOpenChange, onCreated }: NewProjectDialogProps) {
  const setActiveProject = useAppStore((s) => s.setActiveProject);
  const queryClient = useQueryClient();
  const formRef = useRef<HTMLFormElement>(null);
  const [error, setError] = useState<string | null>(null);

  const createMutation = useMutation({
    mutationFn: (data: { name: string; description?: string; repo_path?: string }) =>
      api.createProject(data),
    onSuccess: (project) => {
      void queryClient.invalidateQueries({ queryKey: ["projects"] });
      setActiveProject(project.id);
      onCreated?.(project.id);
      onOpenChange(false);
    },
    onError: (err: Error) => setError(err.message),
  });

  useEffect(() => {
    if (!open) {
      setError(null);
      formRef.current?.reset();
    }
  }, [open]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>New project</DialogTitle>
          <DialogDescription>Group chats and agents under a named project.</DialogDescription>
        </DialogHeader>
        <form
          ref={formRef}
          onSubmit={(e) => {
            e.preventDefault();
            const fd = new FormData(e.currentTarget);
            const name = String(fd.get("name") || "").trim();
            if (!name) return;
            const description = String(fd.get("description") || "").trim();
            const repoPath = String(fd.get("repo_path") || "").trim();
            createMutation.mutate({
              name,
              description: description || undefined,
              repo_path: repoPath || undefined,
            });
          }}
          className="space-y-4"
        >
          <div>
            <label
              htmlFor="new-project-name"
              className="mb-1.5 block font-mono text-[11px] font-semibold uppercase tracking-[0.04em] text-fg-muted"
            >
              Name
            </label>
            <input
              id="new-project-name"
              name="name"
              type="text"
              required
              autoFocus
              className="w-full rounded-[7px] border border-border-subtle bg-surface px-3 py-[7px] text-[13px] text-fg focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary"
              placeholder="My project"
            />
          </div>
          <div>
            <label
              htmlFor="new-project-description"
              className="mb-1.5 block font-mono text-[11px] font-semibold uppercase tracking-[0.04em] text-fg-muted"
            >
              Description
            </label>
            <textarea
              id="new-project-description"
              name="description"
              rows={2}
              className="w-full resize-none rounded-[7px] border border-border-subtle bg-surface px-3 py-[7px] text-[13px] text-fg focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary"
              placeholder="Optional"
            />
          </div>
          <div>
            <label
              htmlFor="new-project-repo-path"
              className="mb-1.5 block font-mono text-[11px] font-semibold uppercase tracking-[0.04em] text-fg-muted"
            >
              Filepath
            </label>
            <input
              id="new-project-repo-path"
              name="repo_path"
              type="text"
              className="w-full rounded-[7px] border border-border-subtle bg-surface px-3 py-[7px] font-mono text-[12px] text-fg focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary"
              placeholder="/absolute/path/to/repo (optional)"
            />
          </div>
          {error && <p className="text-[12px] text-danger">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button
              type="submit"
              disabled={createMutation.isPending}
              className="gap-2"
            >
              {createMutation.isPending && <Loader2 className="size-3.5 animate-spin" />}
              Create
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
