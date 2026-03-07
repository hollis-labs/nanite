import { useState, useCallback } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { X, Search, Loader2, User, Plus } from 'lucide-react'
import { api } from '@/lib/api'

interface AgentPickerProps {
  sessionId: string
  existingAgentIds: string[]
  onClose: () => void
}

export function AgentPicker({ sessionId, existingAgentIds, onClose }: AgentPickerProps) {
  const [search, setSearch] = useState('')
  const queryClient = useQueryClient()

  const { data: agents = [], isLoading } = useQuery({
    queryKey: ['agents'],
    queryFn: api.listAgents,
  })

  const addMutation = useMutation({
    mutationFn: (agentId: string) => api.addSessionAgent(sessionId, agentId, 'participant'),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['session-agents', sessionId] })
      onClose()
    },
  })

  const handleAdd = useCallback((agentId: string) => {
    addMutation.mutate(agentId)
  }, [addMutation])

  const filteredAgents = agents.filter((a) => {
    if (existingAgentIds.includes(a.id)) return false
    if (!search) return true
    const q = search.toLowerCase()
    return a.name.toLowerCase().includes(q) || a.slug.toLowerCase().includes(q)
  })

  return (
    <div className="fixed inset-0 z-[60] flex items-center justify-center">
      <div className="absolute inset-0 bg-black/60" onClick={onClose} />
      <div className="relative w-full max-w-sm bg-zinc-900 border border-zinc-700 rounded-xl shadow-2xl flex flex-col overflow-hidden">
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-zinc-800">
          <h2 className="text-sm font-semibold text-zinc-100">Add Agent</h2>
          <button
            onClick={onClose}
            className="p-1 rounded text-zinc-500 hover:text-zinc-300 hover:bg-zinc-800 transition-colors"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        {/* Search */}
        <div className="px-4 py-2 border-b border-zinc-800">
          <div className="flex items-center gap-2 px-3 py-1.5 bg-zinc-800 rounded-lg">
            <Search className="w-3.5 h-3.5 text-zinc-500 shrink-0" />
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search agents..."
              className="flex-1 text-sm bg-transparent text-zinc-200 placeholder-zinc-600 outline-none"
              autoFocus
            />
          </div>
        </div>

        {/* List */}
        <div className="flex-1 overflow-y-auto p-3 space-y-1 max-h-64">
          {isLoading ? (
            <div className="flex items-center justify-center py-6">
              <Loader2 className="w-4 h-4 animate-spin text-zinc-500" />
            </div>
          ) : filteredAgents.length === 0 ? (
            <p className="text-sm text-zinc-500 text-center py-4">
              {search ? 'No matching agents' : 'No agents available'}
            </p>
          ) : (
            filteredAgents.map((agent) => (
              <button
                key={agent.id}
                onClick={() => handleAdd(agent.id)}
                disabled={addMutation.isPending}
                className="w-full flex items-center gap-3 p-2 rounded-lg hover:bg-zinc-800/50 transition-colors text-left group"
              >
                <div className="w-8 h-8 rounded-full bg-zinc-800 flex items-center justify-center shrink-0">
                  {agent.avatar ? (
                    <span className="text-sm">{agent.avatar}</span>
                  ) : (
                    <User className="w-4 h-4 text-zinc-400" />
                  )}
                </div>
                <div className="flex-1 min-w-0">
                  <span className="text-sm font-medium text-zinc-200 truncate block">{agent.name}</span>
                  {agent.description && (
                    <span className="text-xs text-zinc-500 truncate block">{agent.description}</span>
                  )}
                </div>
                <Plus className="w-4 h-4 text-zinc-600 group-hover:text-indigo-400 shrink-0 transition-colors" />
              </button>
            ))
          )}
        </div>
      </div>
    </div>
  )
}
