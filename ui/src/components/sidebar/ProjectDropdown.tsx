import { useState, useRef, useEffect } from 'react'
import { ChevronDown, FolderOpen, FolderPlus, MessageSquare } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { useAppStore } from '@/stores/useAppStore'
import { api } from '@/lib/api'
import { CreateProjectModal } from '@/components/modals/CreateProjectModal'

interface ProjectDropdownProps {
  workspaceId: string
}

export function ProjectDropdown({ workspaceId }: ProjectDropdownProps) {
  const activeProjectId = useAppStore((s) => s.activeProjectId)
  const setActiveProject = useAppStore((s) => s.setActiveProject)

  const [open, setOpen] = useState(false)
  const [showCreateModal, setShowCreateModal] = useState(false)
  const dropdownRef = useRef<HTMLDivElement>(null)

  const { data: projects = [] } = useQuery({
    queryKey: ['projects', workspaceId],
    queryFn: () => api.listProjects(workspaceId),
    enabled: !!workspaceId,
  })

  const selectedProject = projects.find((p) => p.id === activeProjectId)

  // Close on outside click
  useEffect(() => {
    if (!open) return
    function handleClick(e: MouseEvent) {
      if (dropdownRef.current && !dropdownRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [open])

  return (
    <>
      <div className="relative px-3 py-2 border-b border-border" ref={dropdownRef}>
        <button
          onClick={() => setOpen((o) => !o)}
          className="flex items-center gap-2 w-full px-2 py-1.5 rounded-lg text-left hover:bg-surface/50 transition-colors"
        >
          {selectedProject ? (
            <FolderOpen className="w-3.5 h-3.5 text-accent shrink-0" />
          ) : (
            <MessageSquare className="w-3.5 h-3.5 text-fg-muted shrink-0" />
          )}
          <div className="flex-1 min-w-0">
            <div className="text-xs font-medium text-fg truncate">
              {selectedProject?.name || 'All Chats'}
            </div>
            {selectedProject?.description && (
              <div className="text-[10px] text-fg-faint truncate">{selectedProject.description}</div>
            )}
          </div>
          <ChevronDown className={`w-3 h-3 text-fg-faint transition-transform ${open ? 'rotate-180' : ''}`} />
        </button>

        {open && (
          <div className="absolute left-3 right-3 top-full mt-1 bg-white dark:bg-bg-elevated border border-border-subtle rounded-lg shadow-xl z-50 py-1 max-h-64 overflow-y-auto">
            {/* All Chats */}
            <button
              onClick={() => { setActiveProject(null); setOpen(false) }}
              className={`w-full flex items-center gap-2 px-3 py-2 text-xs text-left transition-colors ${
                !activeProjectId ? 'bg-surface/60 text-fg' : 'text-fg-secondary hover:bg-surface/40'
              }`}
            >
              <MessageSquare className="w-3.5 h-3.5 shrink-0" />
              <span>All Chats</span>
            </button>

            {/* Divider if there are projects */}
            {projects.length > 0 && <div className="my-1 border-t border-border" />}

            {/* Project list */}
            {projects.map((project) => (
              <button
                key={project.id}
                onClick={() => { setActiveProject(project.id); setOpen(false) }}
                className={`w-full flex items-center gap-2 px-3 py-2 text-xs text-left transition-colors ${
                  activeProjectId === project.id ? 'bg-surface/60 text-fg' : 'text-fg-secondary hover:bg-surface/40'
                }`}
              >
                <FolderOpen className="w-3.5 h-3.5 shrink-0" />
                <div className="flex-1 min-w-0">
                  <div className="truncate">{project.name}</div>
                  {project.description && (
                    <div className="text-[10px] text-fg-faint truncate">{project.description}</div>
                  )}
                </div>
              </button>
            ))}

            {/* Divider + New Project */}
            <div className="my-1 border-t border-border" />
            <button
              onClick={() => { setOpen(false); setShowCreateModal(true) }}
              className="w-full flex items-center gap-2 px-3 py-2 text-xs text-fg-muted hover:text-fg-secondary hover:bg-surface/40 transition-colors"
            >
              <FolderPlus className="w-3.5 h-3.5 shrink-0" />
              <span>New Project...</span>
            </button>
          </div>
        )}
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
