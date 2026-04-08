import { useState, useCallback } from 'react'
import { Check, ChevronsUpDown, FolderOpen, MessageSquare } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import {
  Command,
  CommandInput,
  CommandList,
  CommandEmpty,
  CommandGroup,
  CommandItem,
  CommandSeparator,
} from '@/components/ui/command'
import { useAppStore } from '@/stores/useAppStore'
import { api } from '@/lib/api'
import type { Workspace, Project } from '@/lib/types'

type FilterType = 'workspace' | 'project'

interface ScopeSelectorProps {
  workspaceId: string
}

export function ScopeSelector({ workspaceId }: ScopeSelectorProps) {
  const [open, setOpen] = useState(false)
  const [filters, setFilters] = useState<Set<FilterType>>(new Set(['workspace', 'project']))

  const activeProjectId = useAppStore((s) => s.activeProjectId)
  const setActiveWorkspace = useAppStore((s) => s.setActiveWorkspace)
  const setActiveProject = useAppStore((s) => s.setActiveProject)

  const { data: workspaces = [] } = useQuery({
    queryKey: ['workspaces'],
    queryFn: api.listWorkspaces,
  })

  const { data: projects = [] } = useQuery({
    queryKey: ['projects', workspaceId],
    queryFn: () => api.listProjects(workspaceId),
    enabled: !!workspaceId,
  })

  const activeWorkspace = workspaces.find((w: Workspace) => w.id === workspaceId)
  const selectedProject = projects.find((p: Project) => p.id === activeProjectId)

  const toggleFilter = useCallback((type: FilterType) => {
    setFilters((prev) => {
      const next = new Set(prev)
      if (next.has(type)) {
        // Guard: if this is the only active filter, swap to the other
        if (next.size === 1) {
          next.delete(type)
          next.add(type === 'workspace' ? 'project' : 'workspace')
        } else {
          next.delete(type)
        }
      } else {
        next.add(type)
      }
      return next
    })
  }, [])

  const handleSelectWorkspace = useCallback((id: string) => {
    setActiveWorkspace(id)
    setOpen(false)
  }, [setActiveWorkspace])

  const handleSelectProject = useCallback((id: string | null) => {
    setActiveProject(id)
    setOpen(false)
  }, [setActiveProject])

  const showWs = filters.has('workspace')
  const showProj = filters.has('project')

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button type="button" className="flex items-center gap-2 w-full px-2 py-1.5 rounded-md text-left hover:bg-surface/50 transition-colors outline-none">
          <span className="w-5 h-5 rounded bg-primary/15 flex items-center justify-center shrink-0">
            {selectedProject ? (
              <FolderOpen className="size-3 text-primary" />
            ) : (
              <MessageSquare className="size-3 text-primary" />
            )}
          </span>
          <span className="flex-1 min-w-0">
            <span className="block text-sm font-semibold text-fg truncate">
              {selectedProject?.name || 'All Chats'}
            </span>
            {activeWorkspace && (
              <span className="block text-[10px] text-fg-faint truncate">
                {activeWorkspace.name}
              </span>
            )}
          </span>
          <ChevronsUpDown className="size-3 text-fg-faint shrink-0" />
        </button>
      </PopoverTrigger>

      <PopoverContent
        align="start"
        sideOffset={9}
        className="w-[var(--radix-popover-trigger-width)] min-w-[220px] p-0 border-border-subtle"
      >
        <Command>
          <CommandInput placeholder="Search..." />

          {/* Filter bar — 50/50 split */}
          <div className="flex border-b border-border">
            <button
              type="button"
              onClick={() => toggleFilter('workspace')}
              className={`flex-1 py-1.5 text-center text-[11px] font-medium cursor-pointer transition-colors ${
                showWs
                  ? 'bg-primary/15 text-primary'
                  : 'bg-surface text-fg-muted hover:text-fg-secondary'
              }`}
            >
              Workspace
            </button>
            <div className="w-px bg-border" />
            <button
              type="button"
              onClick={() => toggleFilter('project')}
              className={`flex-1 py-1.5 text-center text-[11px] font-medium cursor-pointer transition-colors ${
                showProj
                  ? 'bg-primary/15 text-primary'
                  : 'bg-surface text-fg-muted hover:text-fg-secondary'
              }`}
            >
              Project
            </button>
          </div>

          <CommandList>
            <CommandEmpty>No results found.</CommandEmpty>

            {showWs && (
              <CommandGroup heading="Workspaces">
                {workspaces.map((ws: Workspace) => (
                  <CommandItem
                    key={ws.id}
                    value={`workspace:${ws.id}:${ws.name}`}
                    onSelect={() => handleSelectWorkspace(ws.id)}
                    className="gap-2"
                  >
                    <span className="w-[18px] h-[18px] rounded bg-surface-hover flex items-center justify-center text-[9px] font-bold text-fg-secondary shrink-0">
                      {ws.icon || ws.name.charAt(0).toUpperCase()}
                    </span>
                    <span className="flex-1 truncate">{ws.name}</span>
                    {ws.id === workspaceId && (
                      <Check className="size-3 text-success shrink-0" />
                    )}
                  </CommandItem>
                ))}
              </CommandGroup>
            )}

            {showWs && showProj && <CommandSeparator />}

            {showProj && (
              <CommandGroup heading="Projects">
                <CommandItem
                  value="project:All Chats"
                  onSelect={() => handleSelectProject(null)}
                  className="gap-2"
                >
                  <MessageSquare className="size-3.5 text-primary shrink-0" />
                  <span className="flex-1">All Chats</span>
                  {!activeProjectId && (
                    <Check className="size-3 text-success shrink-0" />
                  )}
                </CommandItem>
                {projects.map((proj: Project) => (
                  <CommandItem
                    key={proj.id}
                    value={`project:${proj.id}:${proj.name}`}
                    onSelect={() => handleSelectProject(proj.id)}
                    className="gap-2"
                  >
                    <FolderOpen className="size-3.5 text-primary shrink-0" />
                    <span className="flex-1 truncate">{proj.name}</span>
                    {activeProjectId === proj.id && (
                      <Check className="size-3 text-success shrink-0" />
                    )}
                  </CommandItem>
                ))}
              </CommandGroup>
            )}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
