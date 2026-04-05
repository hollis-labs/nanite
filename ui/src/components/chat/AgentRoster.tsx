import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { UserPlus, Crown, User, X } from 'lucide-react'
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter
} from '@/components/ui/dialog'
import { Separator } from '@/components/ui/separator'
import { Button } from '@/components/ui/button'
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription } from '@/components/ui/empty'
import { SourceBadge } from '@/components/agents/SourceBadge'
import { TagPills } from '@/components/agents/TagPills'
import { useAppStore } from '@/stores/useAppStore'
import { api } from '@/lib/api'
import type { SessionAgent } from '@/lib/types'
import { AgentPicker } from './AgentPicker'

interface AgentRosterProps {
  sessionId: string
  onClose: () => void
}

const STATUS_COLORS: Record<SessionAgent['status'], string> = {
  active: 'bg-success',
  idle: 'bg-warning',
  offline: 'bg-fg-faint',
}

const STATUS_LABELS: Record<SessionAgent['status'], string> = {
  active: 'Active',
  idle: 'Idle',
  offline: 'Offline',
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
    queryFn: api.listAgents,
  })

  const removeMutation = useMutation({
    mutationFn: (agentId: string) => api.removeSessionAgent(sessionId, agentId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['session-agents', sessionId] })
    },
  })

  return (
    <>
      <Dialog open={true} onOpenChange={() => onClose()}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader className="px-5 pt-5">
            <DialogTitle className="text-sm">
              Agents in Session
              {agents.length > 0 && (
                <span className="ml-2 text-xs font-normal text-fg-muted">{agents.length}</span>
              )}
            </DialogTitle>
            <DialogDescription className="sr-only">View and manage agents in this chat session</DialogDescription>
          </DialogHeader>

          {/* Agent list */}
          <div className="overflow-y-auto max-h-80 px-5 py-2">
            {agents.length === 0 ? (
              <Empty className="py-8">
                <EmptyHeader>
                  <EmptyMedia variant="icon"><User /></EmptyMedia>
                  <EmptyTitle className="text-sm">No agents</EmptyTitle>
                  <EmptyDescription className="text-xs">Add an agent to start collaborating</EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : (
              agents.map((agent, idx) => {
                const profile = allProfiles.find((p) => p.id === agent.agent_id)
                return (
                  <div key={agent.id}>
                    {idx > 0 && <Separator className="my-2" />}
                    <div className="flex items-start gap-3 py-1.5 group">
                      {/* Square avatar with rounded-sm */}
                      <div className="relative size-9 rounded-sm bg-surface flex items-center justify-center shrink-0">
                        {agent.avatar ? (
                          <span className="text-base">{agent.avatar}</span>
                        ) : (
                          <User className="size-4 text-fg-secondary" />
                        )}
                        {/* Status dot */}
                        <span className={`absolute -bottom-0.5 -right-0.5 size-2.5 rounded-full border-2 border-bg-elevated ${STATUS_COLORS[agent.status]}`} />
                      </div>

                      {/* Info — mini-card layout matching settings cards */}
                      <div className="flex-1 min-w-0">
                        <div className="flex items-center gap-1.5">
                          <span className="text-sm font-medium text-fg truncate">{agent.name}</span>
                          {agent.role === 'primary' && (
                            <Crown className="size-3 text-warning shrink-0" />
                          )}
                        </div>
                        <div className="flex items-center gap-1.5 mt-0.5">
                          <span className={`inline-flex items-center gap-1 text-[10px] text-fg-muted`}>
                            <span className={`size-1.5 rounded-full ${STATUS_COLORS[agent.status]}`} />
                            {STATUS_LABELS[agent.status]}
                          </span>
                          <span className="text-fg-faint text-[10px]">·</span>
                          <span className="text-[10px] text-fg-muted capitalize">{agent.role}</span>
                          {profile?.source && (
                            <>
                              <span className="text-fg-faint text-[10px]">·</span>
                              <SourceBadge source={profile.source} />
                            </>
                          )}
                          {profile?.version && (
                            <span className="text-[10px] text-fg-faint">v{profile.version}</span>
                          )}
                        </div>
                        {profile?.description && (
                          <p className="text-xs text-fg-muted mt-1 line-clamp-1">{profile.description}</p>
                        )}
                        {profile?.tags && (
                          <div className="mt-1">
                            <TagPills tags={profile.tags} max={3} />
                          </div>
                        )}
                      </div>

                      {/* Remove button (only for participants) */}
                      {agent.role === 'participant' && (
                        <button
                          onClick={() => removeMutation.mutate(agent.agent_id)}
                          className="p-1 rounded text-fg-faint hover:text-primary hover:bg-surface transition-colors opacity-0 group-hover:opacity-100 shrink-0"
                          aria-label={`Remove ${agent.name}`}
                        >
                          <X className="size-3.5" />
                        </button>
                      )}
                    </div>
                  </div>
                )
              })
            )}
          </div>

          <DialogFooter className="px-5 py-3 border-t border-border">
            <Button
              variant="secondary"
              size="sm"
              className="w-full gap-1.5"
              onClick={() => setShowPicker(true)}
            >
              <UserPlus className="size-3.5" />
              Add Agent
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

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
