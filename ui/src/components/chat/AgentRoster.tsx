import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { UserPlus, X, Crown, User } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { SourceBadge } from '@/components/agents/SourceBadge'
import { useAppStore } from '@/stores/useAppStore'
import { api } from '@/lib/api'
import type { SessionAgent } from '@/lib/types'
import { AgentPicker } from './AgentPicker'

interface AgentRosterProps {
  sessionId: string
  onClose: () => void
}

const STATUS_COLORS: Record<SessionAgent['status'], string> = {
  active: 'bg-green-400',
  idle: 'bg-amber-400',
  offline: 'bg-zinc-600',
}

const ROLE_BORDER_COLORS: Record<SessionAgent['role'], string> = {
  primary: 'ring-accent',
  participant: 'ring-zinc-600',
}

export function AgentRoster({ sessionId, onClose }: AgentRosterProps) {
  const [showPicker, setShowPicker] = useState(false)
  const queryClient = useQueryClient()
  const configVersion = useAppStore((s) => s.configVersion)

  const { data: agents = [] } = useQuery({
    queryKey: ['session-agents', sessionId],
    queryFn: () => api.listSessionAgents(sessionId),
  })

  const { data: allProfiles = [] } = useQuery({
    queryKey: ['agents', configVersion],
    queryFn: api.listAgentProfiles,
  })

  const removeMutation = useMutation({
    mutationFn: (agentId: string) => api.removeSessionAgent(sessionId, agentId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['session-agents', sessionId] })
    },
  })

  return (
    <>
      <div className="fixed inset-0 z-50 flex items-center justify-center">
        <div className="absolute inset-0 bg-black/60" onClick={onClose} />
        <div className="relative w-full max-w-sm bg-bg-elevated border border-border-subtle rounded-xl shadow-2xl flex flex-col overflow-hidden">
          {/* Header */}
          <div className="flex items-center justify-between px-5 py-4 border-b border-border">
            <h2 className="text-sm font-semibold text-fg">
              Agents in Session
              {agents.length > 0 && (
                <span className="ml-2 text-xs font-normal text-fg-muted">{agents.length}</span>
              )}
            </h2>
            <button
              onClick={onClose}
              className="p-1 rounded text-fg-muted hover:text-fg-secondary hover:bg-surface transition-colors"
            >
              <X className="w-4 h-4" />
            </button>
          </div>

          {/* Agent list */}
          <div className="flex-1 overflow-y-auto p-3 space-y-1 max-h-64">
            {agents.length === 0 ? (
              <p className="text-sm text-fg-muted text-center py-4">No agents in this session</p>
            ) : (
              agents.map((agent) => (
                <div
                  key={agent.id}
                  className="flex items-center gap-3 p-2 rounded-lg hover:bg-surface/50 transition-colors group"
                >
                  {/* Avatar */}
                  <div className={`relative w-8 h-8 rounded-full ring-2 ${ROLE_BORDER_COLORS[agent.role]} flex items-center justify-center bg-surface shrink-0`}>
                    {agent.avatar ? (
                      <span className="text-sm">{agent.avatar}</span>
                    ) : (
                      <User className="w-4 h-4 text-fg-secondary" />
                    )}
                    {/* Status dot */}
                    <span className={`absolute -bottom-0.5 -right-0.5 w-2.5 h-2.5 rounded-full border-2 border-bg-elevated ${STATUS_COLORS[agent.status]}`} />
                  </div>

                  {/* Info */}
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-1.5">
                      <span className="text-sm font-medium text-fg truncate">{agent.name}</span>
                      {agent.role === 'primary' && (
                        <Crown className="w-3 h-3 text-amber-400 shrink-0" />
                      )}
                      {(() => {
                        const profile = allProfiles.find((p) => p.id === agent.agent_id)
                        return profile?.source ? <SourceBadge source={profile.source} /> : null
                      })()}
                    </div>
                    <div className="flex items-center gap-1.5">
                      <span className="text-xs text-fg-muted capitalize">{agent.role}</span>
                      {(() => {
                        const profile = allProfiles.find((p) => p.id === agent.agent_id)
                        return profile?.version ? (
                          <span className="text-[10px] text-fg-faint">v{profile.version}</span>
                        ) : null
                      })()}
                    </div>
                  </div>

                  {/* Remove button (only for participants) */}
                  {agent.role === 'participant' && (
                    <button
                      onClick={() => removeMutation.mutate(agent.agent_id)}
                      className="p-1 rounded text-fg-faint hover:text-red-400 hover:bg-surface transition-colors opacity-0 group-hover:opacity-100"
                      aria-label={`Remove ${agent.name}`}
                    >
                      <X className="w-3.5 h-3.5" />
                    </button>
                  )}
                </div>
              ))
            )}
          </div>

          {/* Footer */}
          <div className="px-5 py-3 border-t border-border">
            <Button
              variant="secondary"
              size="sm"
              className="w-full gap-1.5"
              onClick={() => setShowPicker(true)}
            >
              <UserPlus className="w-3.5 h-3.5" />
              Add Agent
            </Button>
          </div>
        </div>
      </div>

      {showPicker && (
        <AgentPicker
          sessionId={sessionId}
          existingAgentIds={agents.map((a) => a.agent_id)}
          onClose={() => setShowPicker(false)}
        />
      )}
    </>
  )
}
