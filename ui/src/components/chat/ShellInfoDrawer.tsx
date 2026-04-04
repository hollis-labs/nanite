import { useQuery } from '@tanstack/react-query'
import { Lock, LockOpen, GitBranch } from 'lucide-react'
import { api } from '@/lib/api'
import type { ShellMode } from '@/lib/types'

interface ShellInfoDrawerProps {
  sessionId: string
  shellMode: ShellMode
  onToggleDenylist: () => void
}

export function ShellInfoDrawer({ sessionId, shellMode, onToggleDenylist }: ShellInfoDrawerProps) {
  const { data: info } = useQuery({
    queryKey: ['shell-info', sessionId],
    queryFn: () => api.getShellInfo(sessionId),
    staleTime: 30_000,
  })

  const denylistActive = shellMode !== 'yolo'
  const workDir = info?.work_dir ?? '~'
  // Shorten home dir for display
  const displayDir = workDir.replace(/^\/Users\/[^/]+/, '~')

  return (
    <div className="px-3 py-1.5 border-b border-border-subtle bg-bg-elevated/50 text-xs text-fg-muted flex items-center gap-2 animate-in slide-in-from-top-1 duration-150">
      <button
        onClick={onToggleDenylist}
        className={`p-0.5 rounded transition-colors ${
          denylistActive
            ? 'text-fg-secondary hover:text-fg'
            : 'text-amber-400 hover:text-amber-300'
        }`}
        title={denylistActive ? 'Denylist active — click to disable' : 'Denylist disabled — click to enable'}
      >
        {denylistActive ? (
          <Lock className="w-3.5 h-3.5" />
        ) : (
          <LockOpen className="w-3.5 h-3.5" />
        )}
      </button>
      <span className="font-mono text-fg-secondary">{displayDir}</span>
      {info?.git_branch && (
        <>
          <span className="text-fg-faint">·</span>
          <span className="flex items-center gap-1">
            <GitBranch className="w-3 h-3 text-fg-faint" />
            <span className="font-mono">{info.git_branch}</span>
          </span>
          {info.git_status && (
            <>
              <span className="text-fg-faint">·</span>
              <span className={info.git_status === 'clean' ? 'text-green-400' : 'text-amber-400'}>
                {info.git_status}
              </span>
            </>
          )}
        </>
      )}
    </div>
  )
}
