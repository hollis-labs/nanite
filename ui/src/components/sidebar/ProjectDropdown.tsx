import { useState } from 'react'
import { ChevronsUpDown, FolderOpen, FolderPlus, MessageSquare, Check } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useAppStore } from '@/stores/useAppStore'
import { api } from '@/lib/api'
import { CreateProjectModal } from '@/components/modals/CreateProjectModal'

interface ProjectDropdownProps {
  workspaceId: string
}

export function ProjectDropdown({ workspaceId }: ProjectDropdownProps) {
  const activeProjectId = useAppStore((s) => s.activeProjectId)
  const setActiveProject = useAppStore((s) => s.setActiveProject)
  const [showCreateModal, setShowCreateModal] = useState(false)

  const { data: projects = [] } = useQuery({
    queryKey: ['projects', workspaceId],
    queryFn: () => api.listProjects(workspaceId),
    enabled: !!workspaceId,
  })

  const selectedProject = projects.find((p) => p.id === activeProjectId)

  return (
    <>
      <div className="px-3 py-2 border-b border-border">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button className="flex items-center gap-2 w-full px-2 py-1.5 rounded-lg text-left hover:bg-surface/50 transition-colors outline-none">
              {selectedProject ? (
                <FolderOpen className="size-3.5 text-accent shrink-0" />
              ) : (
                <MessageSquare className="size-3.5 text-fg-muted shrink-0" />
              )}
              <div className="flex-1 min-w-0">
                <div className="text-xs font-medium text-fg truncate">
                  {selectedProject?.name || 'All Chats'}
                </div>
                {selectedProject?.description && (
                  <div className="text-[10px] text-fg-faint truncate">{selectedProject.description}</div>
                )}
              </div>
              <ChevronsUpDown className="size-3 text-fg-faint shrink-0" />
            </button>
          </DropdownMenuTrigger>

          <DropdownMenuContent
            align="start"
            className="w-[var(--radix-dropdown-menu-trigger-width)] min-w-[200px]"
          >
            <DropdownMenuGroup>
              <DropdownMenuItem
                onSelect={() => setActiveProject(null)}
                className="gap-2 text-xs"
              >
                <MessageSquare className="size-3.5 shrink-0" />
                <span className="flex-1">All Chats</span>
                {!activeProjectId && <Check className="size-3 text-accent" />}
              </DropdownMenuItem>
            </DropdownMenuGroup>

            {projects.length > 0 && (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuGroup>
                  {projects.map((project) => (
                    <DropdownMenuItem
                      key={project.id}
                      onSelect={() => setActiveProject(project.id)}
                      className="gap-2 text-xs"
                    >
                      <FolderOpen className="size-3.5 shrink-0" />
                      <div className="flex-1 min-w-0">
                        <div className="truncate">{project.name}</div>
                        {project.description && (
                          <div className="text-[10px] text-fg-faint truncate">{project.description}</div>
                        )}
                      </div>
                      {activeProjectId === project.id && <Check className="size-3 text-accent shrink-0" />}
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuGroup>
              </>
            )}

            <DropdownMenuSeparator />
            <DropdownMenuItem
              onSelect={() => setShowCreateModal(true)}
              className="gap-2 text-xs text-fg-muted"
            >
              <FolderPlus className="size-3.5 shrink-0" />
              <span>New Project...</span>
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      {showCreateModal && (
        <CreateProjectModal
          workspaceId={workspaceId}
          onClose={() => setShowCreateModal(false)}
          onCreated={(project) => {
            setActiveProject(project.id)
            setShowCreateModal(false)
          }}
        />
      )}
    </>
  )
}
