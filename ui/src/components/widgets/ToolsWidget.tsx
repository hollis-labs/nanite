import { Wrench, RefreshCw, Loader2, Server } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { Widget } from './Widget'
import { api } from '@/lib/api'
import { useToolRefresh } from '@/hooks/useToolRefresh'
import type { ToolDefinition, ServerInfo } from '@/lib/types'

export function ToolsWidget() {
  const { refreshTools, isRefreshing } = useToolRefresh()

  const { data: tools = [] as ToolDefinition[], isLoading: toolsLoading } = useQuery({
    queryKey: ['tools'],
    queryFn: api.fetchTools,
  })

  const { data: servers = [] as ServerInfo[], isLoading: serversLoading } = useQuery({
    queryKey: ['tool-servers'],
    queryFn: api.fetchToolServers,
  })

  const isLoading = toolsLoading || serversLoading || isRefreshing
  const connectedServers = servers.filter((s) => s.connected).length

  return (
    <Widget id="tools" title="Tools" icon={Wrench}>
      <div className="space-y-2.5">
        {isLoading ? (
          <div className="flex items-center justify-center py-3">
            <Loader2 className="w-4 h-4 animate-spin text-fg-muted" />
            <span className="ml-2 text-xs text-fg-muted">
              {isRefreshing ? 'Refreshing tools...' : 'Loading...'}
            </span>
          </div>
        ) : (
          <>
            <div className="flex justify-between text-xs">
              <span className="text-fg-muted flex items-center gap-1">
                <Wrench className="w-3 h-3" />
                Tools Available
              </span>
              <span className="text-fg-secondary">{tools.length}</span>
            </div>
            <div className="flex justify-between text-xs">
              <span className="text-fg-muted flex items-center gap-1">
                <Server className="w-3 h-3" />
                MCP Servers
              </span>
              <span className="text-fg-secondary">
                {connectedServers}/{servers.length} connected
              </span>
            </div>
          </>
        )}
        <button
          onClick={refreshTools}
          disabled={isRefreshing}
          className="w-full text-xs text-center py-1.5 rounded-md bg-surface text-fg-secondary hover:text-fg hover:bg-surface-hover transition-colors disabled:opacity-50 disabled:cursor-not-allowed flex items-center justify-center gap-1.5"
        >
          {isRefreshing ? (
            <Loader2 className="w-3 h-3 animate-spin" />
          ) : (
            <RefreshCw className="w-3 h-3" />
          )}
          Refresh Tools
        </button>
      </div>
    </Widget>
  )
}
