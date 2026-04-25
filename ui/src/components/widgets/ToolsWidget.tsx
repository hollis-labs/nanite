import { Wrench, RefreshCw, Loader2, Server } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { Widget, WidgetRow } from './Widget'
import { Skeleton } from '@/components/ui/skeleton'
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
      <div className="flex flex-col gap-2">
        {isLoading && !isRefreshing ? (
          <div className="flex flex-col gap-2">
            <div className="flex justify-between items-center">
              <Skeleton className="h-3 w-24" />
              <Skeleton className="h-3 w-8" />
            </div>
            <div className="flex justify-between items-center">
              <Skeleton className="h-3 w-20" />
              <Skeleton className="h-3 w-16" />
            </div>
          </div>
        ) : isRefreshing ? (
          <div className="flex items-center justify-center py-3">
            <Loader2 className="w-4 h-4 animate-spin text-fg-muted" />
            <span className="ml-2 text-[12px] text-fg-muted">Refreshing…</span>
          </div>
        ) : (
          <>
            <WidgetRow label={<span className="flex items-center gap-1.5"><Wrench className="w-3 h-3" />Tools available</span>} mono>
              {String(tools.length)}
            </WidgetRow>
            <WidgetRow label={<span className="flex items-center gap-1.5"><Server className="w-3 h-3" />MCP servers</span>}>
              <span className="font-mono text-[11px]">
                <span className="text-success">{connectedServers}</span>
                <span className="text-fg-muted">/{servers.length}</span>
              </span>
            </WidgetRow>
          </>
        )}
        <button
          onClick={refreshTools}
          disabled={isRefreshing}
          className="mt-0.5 w-full h-[26px] inline-flex items-center justify-center gap-1.5 rounded-[5px] border border-border-subtle bg-surface text-[11px] font-medium text-fg-secondary hover:text-fg hover:bg-surface-hover transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {isRefreshing ? (
            <Loader2 className="w-3 h-3 animate-spin" />
          ) : (
            <RefreshCw className="w-3 h-3" />
          )}
          Refresh
        </button>
      </div>
    </Widget>
  )
}
