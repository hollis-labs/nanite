import { useQuery } from "@tanstack/react-query";
import { Check, ChevronsUpDown, FolderOpen, MessageSquare, Plus } from "lucide-react";
import { useCallback, useState } from "react";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { api } from "@/lib/api";
import type { Project } from "@/lib/types";
import { useAppStore } from "@/stores/useAppStore";
import { NewProjectDialog } from "./NewProjectDialog";

interface ScopeSelectorProps {
  workspaceId: string;
}

export function ScopeSelector({ workspaceId }: ScopeSelectorProps) {
  const [open, setOpen] = useState(false);
  const [showNewProject, setShowNewProject] = useState(false);

  const activeProjectId = useAppStore((s) => s.activeProjectId);
  const setActiveProject = useAppStore((s) => s.setActiveProject);

  const { data: projects = [] } = useQuery({
    queryKey: ["projects", workspaceId],
    queryFn: () => api.listProjects(workspaceId),
    enabled: !!workspaceId,
  });

  const selectedProject = projects.find((p: Project) => p.id === activeProjectId);

  const handleSelectProject = useCallback(
    (id: string | null) => {
      setActiveProject(id);
      setOpen(false);
    },
    [setActiveProject],
  );

  const handleOpenNewProject = useCallback(() => {
    setOpen(false);
    setShowNewProject(true);
  }, []);

  return (
    <>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <button
            type="button"
            className="flex h-full w-full items-center gap-2 rounded-md px-2 py-1.5 text-left outline-none transition-colors hover:bg-surface/50"
          >
            <span className="flex size-5 shrink-0 items-center justify-center rounded bg-primary/15">
              {selectedProject ? (
                <FolderOpen className="size-3 text-primary" />
              ) : (
                <MessageSquare className="size-3 text-primary" />
              )}
            </span>
            <span className="min-w-0 flex-1 truncate text-[13px] font-semibold text-fg">
              {selectedProject?.name || "All Chats"}
            </span>
            <ChevronsUpDown className="size-3 shrink-0 text-fg-faint" />
          </button>
        </PopoverTrigger>

        <PopoverContent
          align="start"
          sideOffset={9}
          className="w-[var(--radix-popover-trigger-width)] min-w-[220px] border-border-subtle p-0"
        >
          <Command>
            <CommandInput placeholder="Search projects..." />
            <CommandList>
              <CommandEmpty>No projects found.</CommandEmpty>
              <CommandGroup heading="Projects">
                <CommandItem
                  value="project:All Chats"
                  onSelect={() => handleSelectProject(null)}
                  className="gap-2"
                >
                  <MessageSquare className="size-3.5 shrink-0 text-primary" />
                  <span className="flex-1">All Chats</span>
                  {!activeProjectId && <Check className="size-3 shrink-0 text-success" />}
                </CommandItem>
                {projects.map((proj: Project) => (
                  <CommandItem
                    key={proj.id}
                    value={`project:${proj.id}:${proj.name}`}
                    onSelect={() => handleSelectProject(proj.id)}
                    className="gap-2"
                  >
                    <FolderOpen className="size-3.5 shrink-0 text-primary" />
                    <span className="flex-1 truncate">{proj.name}</span>
                    {activeProjectId === proj.id && (
                      <Check className="size-3 shrink-0 text-success" />
                    )}
                  </CommandItem>
                ))}
                <CommandItem
                  value="project:__new__"
                  onSelect={handleOpenNewProject}
                  className="gap-2 text-fg-secondary"
                >
                  <Plus className="size-3.5 shrink-0" />
                  <span className="flex-1">New project…</span>
                </CommandItem>
              </CommandGroup>
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>

      <NewProjectDialog open={showNewProject} onOpenChange={setShowNewProject} />
    </>
  );
}
