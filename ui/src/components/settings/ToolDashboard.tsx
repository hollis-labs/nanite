import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  RefreshCw,
  Server,
  Wrench,
  ChevronDown,
  ChevronRight,
  Search,
  Eye,
  Activity,
  X,
  CheckCircle,
  AlertCircle,
  Loader2,
} from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { api } from '@/lib/api'
import type { ToolDefinition, DiscoveryDiff, ToolSelection, ServerInfo } from '@/lib/types'

interface ToolDashboardProps {}

export function ToolDashboard({}: ToolDashboardProps) {
  const [expandedServers, setExpandedServers] = useState<Set<string>>(new Set())
  const [selectedTool, setSelectedTool] = useState<ToolDefinition | null>(null)
  const [filterServer, setFilterServer] = useState<string>('')
  const [intentQuery, setIntentQuery] = useState('')
  const [refreshResult, setRefreshResult] = useState<DiscoveryDiff | null>(null)

  const queryClient = useQueryClient()

  // Data queries
  const { data: servers = [] as ServerInfo[], isLoading: serversLoading } = useQuery({
    queryKey: ['tool-servers'],
    queryFn: api.fetchToolServers,
  })

  const { data: tools = [] as ToolDefinition[], isLoading: toolsLoading } = useQuery({
    queryKey: ['tools'],
    queryFn: api.fetchTools,
  })

  // Mutations
  const refreshMutation = useMutation({
    mutationFn: api.refreshTools,
    onSuccess: (diff: DiscoveryDiff) => {
      setRefreshResult(diff)
      queryClient.invalidateQueries({ queryKey: ['tools'] })
      queryClient.invalidateQueries({ queryKey: ['tool-servers'] })
      setTimeout(() => setRefreshResult(null), 5000) // Clear after 5 seconds
    },
  })

  const intentTestMutation = useMutation<ToolSelection[], Error, string>({
    mutationFn: api.selectTools,
  })

  // Event handlers
  const toggleServer = (serverName: string) => {
    const newExpanded = new Set(expandedServers)
    if (newExpanded.has(serverName)) {
      newExpanded.delete(serverName)
    } else {
      newExpanded.add(serverName)
    }
    setExpandedServers(newExpanded)
  }

  const handleRefresh = () => {
    refreshMutation.mutate()
  }

  const handleIntentTest = () => {
    if (intentQuery.trim()) {
      intentTestMutation.mutate(intentQuery.trim())
    }
  }

  // Filter tools by server
  const filteredTools = filterServer
    ? tools.filter(tool => tool.name.includes(filterServer))
    : tools

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <h2 className="text-lg font-semibold text-zinc-100 mb-2">Tools & MCP Servers</h2>
        <p className="text-zinc-400 text-sm">
          Manage connected MCP servers and their discovered tools.
        </p>
      </div>

      {/* Refresh Controls */}
      <div className="flex items-center gap-4">
        <Button
          onClick={handleRefresh}
          disabled={refreshMutation.isPending}
          variant="outline"
          className="flex items-center gap-2"
        >
          {refreshMutation.isPending ? (
            <Loader2 className="w-4 h-4 animate-spin" />
          ) : (
            <RefreshCw className="w-4 h-4" />
          )}
          Refresh Tools
        </Button>

        {refreshResult && (
          <div className="flex items-center gap-2 px-3 py-2 rounded-md bg-blue-900/30 border border-blue-700/50">
            <CheckCircle className="w-4 h-4 text-blue-400" />
            <span className="text-sm text-blue-300">
              Discovery complete: +{(refreshResult.added ?? []).length} -{(refreshResult.removed ?? []).length} (total: {refreshResult.total})
            </span>
            <button onClick={() => setRefreshResult(null)}>
              <X className="w-4 h-4 text-blue-400 hover:text-blue-300" />
            </button>
          </div>
        )}
      </div>

      {/* MCP Servers Section */}
      <div className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-6">
        <div className="flex items-center gap-2 mb-4">
          <Server className="w-5 h-5 text-zinc-400" />
          <h3 className="text-lg font-medium text-zinc-100">MCP Servers</h3>
        </div>

        {serversLoading ? (
          <div className="flex items-center justify-center py-8">
            <Loader2 className="w-6 h-6 animate-spin text-zinc-500" />
          </div>
        ) : servers.length === 0 ? (
          <div className="text-center py-8">
            <AlertCircle className="w-8 h-8 mx-auto mb-2 text-zinc-500" />
            <p className="text-zinc-400">No MCP servers connected</p>
          </div>
        ) : (
          <div className="space-y-2">
            {servers.map(server => (
              <div key={server.name} className="border border-zinc-700 rounded-lg overflow-hidden">
                <button
                  onClick={() => toggleServer(server.name)}
                  className="w-full flex items-center justify-between p-4 text-left hover:bg-zinc-800/50 transition-colors"
                >
                  <div className="flex items-center gap-3">
                    {expandedServers.has(server.name) ? (
                      <ChevronDown className="w-4 h-4 text-zinc-400" />
                    ) : (
                      <ChevronRight className="w-4 h-4 text-zinc-400" />
                    )}
                    <div className="flex items-center gap-2">
                      <div className={`w-2 h-2 rounded-full ${server.connected ? 'bg-green-400' : 'bg-red-400'}`} />
                      <span className="font-medium text-zinc-100">{server.name}</span>
                    </div>
                  </div>
                  <div className="flex items-center gap-4 text-sm text-zinc-400">
                    <span>{server.tool_count} tools</span>
                    <span className="capitalize">{server.connected ? 'Connected' : 'Disconnected'}</span>
                  </div>
                </button>

                {expandedServers.has(server.name) && (
                  <div className="px-4 pb-4 border-t border-zinc-700/50">
                    <div className="grid grid-cols-3 gap-4 pt-4 text-sm">
                      <div>
                        <span className="text-zinc-400">Status:</span>
                        <span className={`ml-2 ${server.connected ? 'text-green-400' : 'text-red-400'}`}>
                          {server.connected ? 'Connected' : 'Disconnected'}
                        </span>
                      </div>
                      <div>
                        <span className="text-zinc-400">Tool Count:</span>
                        <span className="ml-2 text-zinc-100">{server.tool_count}</span>
                      </div>
                      <div>
                        <span className="text-zinc-400">Transport:</span>
                        <span className="ml-2 text-zinc-100">HTTP</span>
                      </div>
                    </div>
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Tools List Section */}
      <div className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-6">
        <div className="flex items-center justify-between mb-4">
          <div className="flex items-center gap-2">
            <Wrench className="w-5 h-5 text-zinc-400" />
            <h3 className="text-lg font-medium text-zinc-100">Discovered Tools</h3>
          </div>
          <div className="flex items-center gap-2">
            <Search className="w-4 h-4 text-zinc-400" />
            <select
              value={filterServer}
              onChange={(e) => setFilterServer(e.target.value)}
              className="px-3 py-2 bg-zinc-800 border border-zinc-700 rounded-md text-zinc-100 text-sm focus:outline-none focus:ring-1 focus:ring-zinc-400"
            >
              <option value="">All servers</option>
              {servers.map(server => (
                <option key={server.name} value={server.name}>{server.name}</option>
              ))}
            </select>
          </div>
        </div>

        {toolsLoading ? (
          <div className="flex items-center justify-center py-8">
            <Loader2 className="w-6 h-6 animate-spin text-zinc-500" />
          </div>
        ) : filteredTools.length === 0 ? (
          <div className="text-center py-8">
            <AlertCircle className="w-8 h-8 mx-auto mb-2 text-zinc-500" />
            <p className="text-zinc-400">
              {filterServer ? `No tools found for "${filterServer}"` : 'No tools available'}
            </p>
          </div>
        ) : (
          <div className="space-y-2">
            {filteredTools.map(tool => (
              <div
                key={tool.name}
                className="border border-zinc-700 rounded-lg p-4 hover:bg-zinc-800/30 transition-colors"
              >
                <div className="flex items-center justify-between">
                  <div className="flex-1">
                    <div className="flex items-center gap-2 mb-1">
                      <code className="px-2 py-1 bg-zinc-800 rounded text-sm text-blue-300 font-mono">
                        {tool.name}
                      </code>
                      {/* Server name would come from backend - for now we'll derive it */}
                      <span className="text-xs text-zinc-500">
                        {tool.name.includes('mcp_') ? tool.name.split('__')[1] : 'unknown'}
                      </span>
                    </div>
                    <p className="text-sm text-zinc-300 leading-relaxed">
                      {tool.description || 'No description available'}
                    </p>
                  </div>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => setSelectedTool(tool)}
                    className="flex items-center gap-2"
                  >
                    <Eye className="w-4 h-4" />
                    Details
                  </Button>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Intent Test Section */}
      <div className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-6">
        <div className="flex items-center gap-2 mb-4">
          <Activity className="w-5 h-5 text-zinc-400" />
          <h3 className="text-lg font-medium text-zinc-100">Intent Testing</h3>
        </div>

        <div className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-zinc-300 mb-2">
              Test Tool Selection
            </label>
            <div className="flex gap-2">
              <input
                type="text"
                value={intentQuery}
                onChange={(e) => setIntentQuery(e.target.value)}
                placeholder="Enter intent query (e.g., 'list directory', 'search files')"
                className="flex-1 px-3 py-2 bg-zinc-800 border border-zinc-700 rounded-md text-zinc-100 placeholder:text-zinc-500 focus:outline-none focus:ring-1 focus:ring-zinc-400"
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    handleIntentTest()
                  }
                }}
              />
              <Button
                onClick={handleIntentTest}
                disabled={!intentQuery.trim() || intentTestMutation.isPending}
                variant="outline"
              >
                {intentTestMutation.isPending ? (
                  <Loader2 className="w-4 h-4 animate-spin" />
                ) : (
                  'Test'
                )}
              </Button>
            </div>
          </div>

          {intentTestMutation.data && (
            <div className="mt-4">
              <h4 className="text-sm font-medium text-zinc-300 mb-2">Selected Tools:</h4>
              {intentTestMutation.data.length === 0 ? (
                <p className="text-sm text-zinc-500">No tools selected for this intent</p>
              ) : (
                <div className="space-y-2">
                  {intentTestMutation.data.map((tool, index) => (
                    <div key={index} className="flex items-center gap-2 text-sm">
                      <CheckCircle className="w-4 h-4 text-green-400" />
                      <code className="px-2 py-1 bg-zinc-800 rounded text-blue-300 font-mono">
                        {tool.name}
                      </code>
                      <span className="text-zinc-400">- {tool.description}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}

          {intentTestMutation.error && (
            <div className="mt-4 flex items-center gap-2 text-sm text-red-400">
              <AlertCircle className="w-4 h-4" />
              <span>Error testing intent: {intentTestMutation.error.message}</span>
            </div>
          )}
        </div>
      </div>

      {/* Tool Detail Modal */}
      {selectedTool && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
          <div className="bg-zinc-900 rounded-lg border border-zinc-700 max-w-2xl w-full max-h-[80vh] overflow-hidden flex flex-col">
            <div className="flex items-center justify-between p-6 border-b border-zinc-700">
              <div className="flex items-center gap-2">
                <Wrench className="w-5 h-5 text-zinc-400" />
                <h3 className="text-lg font-medium text-zinc-100">Tool Details</h3>
              </div>
              <Button
                variant="ghost"
                size="icon"
                onClick={() => setSelectedTool(null)}
              >
                <X className="w-4 h-4" />
              </Button>
            </div>

            <div className="flex-1 overflow-auto p-6 space-y-4">
              <div>
                <h4 className="text-sm font-medium text-zinc-300 mb-2">Name</h4>
                <code className="px-3 py-2 bg-zinc-800 rounded text-blue-300 font-mono text-sm block">
                  {selectedTool.name}
                </code>
              </div>

              <div>
                <h4 className="text-sm font-medium text-zinc-300 mb-2">Description</h4>
                <p className="text-sm text-zinc-300 leading-relaxed">
                  {selectedTool.description || 'No description available'}
                </p>
              </div>

              <div>
                <h4 className="text-sm font-medium text-zinc-300 mb-2">Server</h4>
                <p className="text-sm text-zinc-400">
                  {selectedTool.name.includes('mcp_') ? selectedTool.name.split('__')[1] : 'Unknown server'}
                </p>
              </div>

              <div>
                <h4 className="text-sm font-medium text-zinc-300 mb-2">Input Schema</h4>
                <div className="bg-zinc-800 rounded-lg p-4 overflow-auto">
                  <pre className="text-sm text-zinc-300 font-mono whitespace-pre-wrap">
                    {selectedTool.input_schema ?
                      JSON.stringify(selectedTool.input_schema, null, 2) :
                      'No schema available'
                    }
                  </pre>
                </div>
              </div>

              <div>
                <h4 className="text-sm font-medium text-zinc-300 mb-2">Referenced by Skills</h4>
                <div className="text-sm text-zinc-500">
                  {/* This would require additional API call to get skills that reference this tool */}
                  Skills integration coming soon...
                </div>
              </div>
            </div>

            <div className="p-6 border-t border-zinc-700">
              <Button
                onClick={() => setSelectedTool(null)}
                variant="outline"
                className="w-full"
              >
                Close
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}