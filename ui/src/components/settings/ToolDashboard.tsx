import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  RefreshCw,
  Server,
  Wrench,
  Search,
  Activity,
  AlertCircle,
  Loader2,
  Plus,
  Pencil,
  Trash2,
  Upload,
  Download,
  CheckCircle2,
  SlidersHorizontal,
  ToggleLeft,
  ToggleRight,
} from 'lucide-react'
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription } from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { api } from '@/lib/api'
import type { ToolDefinition, DiscoveryDiff, ToolSelection, ServerInfo, MCPServerConfig, ToolLoadItem } from '@/lib/types'

// --- Server Form Types ---

interface ServerFormData {
  name: string
  transport_type: 'stdio' | 'sse'
  command: string
  url: string
  args: string    // comma-separated for display
  env: string     // newline-separated KEY=VALUE for display
  enabled: boolean
}

const emptyForm: ServerFormData = {
  name: '',
  transport_type: 'stdio',
  command: '',
  url: '',
  args: '',
  env: '',
  enabled: true,
}

function formToPayload(form: ServerFormData) {
  const args = form.args.trim()
    ? form.args.split(',').map(a => a.trim()).filter(Boolean)
    : []
  const env = form.env.trim()
    ? form.env.split('\n').map(e => e.trim()).filter(Boolean)
    : []
  return {
    name: form.name.trim(),
    transport_type: form.transport_type,
    command: form.command.trim(),
    url: form.url.trim(),
    args: JSON.stringify(args),
    env: JSON.stringify(env),
    enabled: form.enabled,
  }
}

function configToForm(cfg: MCPServerConfig): ServerFormData {
  let args = ''
  try {
    const parsed = JSON.parse(cfg.args || '[]')
    if (Array.isArray(parsed)) args = parsed.join(', ')
  } catch { /* ignore */ }

  let env = ''
  try {
    const parsed = JSON.parse(cfg.env || '[]')
    if (Array.isArray(parsed)) env = parsed.join('\n')
  } catch { /* ignore */ }

  return {
    name: cfg.name,
    transport_type: cfg.transport_type,
    command: cfg.command,
    url: cfg.url,
    args,
    env,
    enabled: cfg.enabled,
  }
}

// --- Main Component ---

interface ToolDashboardProps {}

export function ToolDashboard({}: ToolDashboardProps) {
  const [selectedTool, setSelectedTool] = useState<ToolDefinition | null>(null)
  const [filterServer, setFilterServer] = useState<string>('')
  const [intentQuery, setIntentQuery] = useState('')
  const [refreshResult, setRefreshResult] = useState<DiscoveryDiff | null>(null)

  // Server form state
  const [showServerForm, setShowServerForm] = useState(false)
  const [editingServer, setEditingServer] = useState<string | null>(null) // name of server being edited
  const [serverForm, setServerForm] = useState<ServerFormData>(emptyForm)
  const [serverFormError, setServerFormError] = useState<string | null>(null)

  // Delete confirmation state
  const [deletingServer, setDeletingServer] = useState<string | null>(null)

  const queryClient = useQueryClient()

  // Data queries
  const { data: servers = [] as ServerInfo[], isLoading: serversLoading } = useQuery({
    queryKey: ['tool-servers'],
    queryFn: api.fetchToolServers,
  })

  const { data: mcpConfigs = [] as MCPServerConfig[] } = useQuery({
    queryKey: ['mcp-servers'],
    queryFn: api.listMCPServers,
  })

  const { data: tools = [] as ToolDefinition[], isLoading: toolsLoading } = useQuery({
    queryKey: ['tools'],
    queryFn: api.fetchTools,
  })

  const { data: allToolsWithLoad = [] as ToolLoadItem[], isLoading: loadPrefsLoading } = useQuery({
    queryKey: ['tools-with-load-type'],
    queryFn: api.fetchAllToolsWithLoadType,
  })

  const loadPrefMutation = useMutation({
    mutationFn: (updates: Record<string, string>) => api.updateToolLoadPreferences(updates),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tools-with-load-type'] })
    },
  })

  // Build a lookup of user-managed server names
  const managedServerNames = new Set(mcpConfigs.map(c => c.name))

  // Mutations
  const refreshMutation = useMutation({
    mutationFn: api.refreshTools,
    onSuccess: (diff: DiscoveryDiff) => {
      setRefreshResult(diff)
      queryClient.invalidateQueries({ queryKey: ['tools'] })
      queryClient.invalidateQueries({ queryKey: ['tool-servers'] })
      setTimeout(() => setRefreshResult(null), 5000)
    },
  })

  const addServerMutation = useMutation({
    mutationFn: (data: ReturnType<typeof formToPayload>) => api.addMCPServer(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['mcp-servers'] })
      queryClient.invalidateQueries({ queryKey: ['tool-servers'] })
      queryClient.invalidateQueries({ queryKey: ['tools'] })
      closeServerForm()
    },
    onError: (err: Error) => {
      setServerFormError(err.message)
    },
  })

  const updateServerMutation = useMutation({
    mutationFn: ({ name, data }: { name: string; data: ReturnType<typeof formToPayload> }) =>
      api.updateMCPServer(name, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['mcp-servers'] })
      queryClient.invalidateQueries({ queryKey: ['tool-servers'] })
      queryClient.invalidateQueries({ queryKey: ['tools'] })
      closeServerForm()
    },
    onError: (err: Error) => {
      setServerFormError(err.message)
    },
  })

  const deleteServerMutation = useMutation({
    mutationFn: (name: string) => api.deleteMCPServer(name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['mcp-servers'] })
      queryClient.invalidateQueries({ queryKey: ['tool-servers'] })
      queryClient.invalidateQueries({ queryKey: ['tools'] })
      setDeletingServer(null)
    },
  })

  // Import/Export state
  const [showImportDialog, setShowImportDialog] = useState(false)
  const [importText, setImportText] = useState('')
  const [importError, setImportError] = useState<string | null>(null)
  const [importResult, setImportResult] = useState<{ created: string[]; skipped: string[] } | null>(null)

  const importMutation = useMutation({
    mutationFn: (json: string) => api.importMCPServers(json),
    onSuccess: (result) => {
      setImportResult(result)
      setImportError(null)
      queryClient.invalidateQueries({ queryKey: ['mcp-servers'] })
      queryClient.invalidateQueries({ queryKey: ['tool-servers'] })
      queryClient.invalidateQueries({ queryKey: ['tools'] })
    },
    onError: (err: Error) => {
      setImportError(err.message)
    },
  })

  const handleImportSubmit = () => {
    setImportError(null)
    setImportResult(null)
    if (!importText.trim()) {
      setImportError('Paste or upload a .mcp.json file')
      return
    }
    importMutation.mutate(importText)
  }

  const closeImportDialog = () => {
    setShowImportDialog(false)
    setImportText('')
    setImportError(null)
    setImportResult(null)
  }

  const handleImportFile = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => {
      setImportText(reader.result as string)
      setImportError(null)
      setImportResult(null)
    }
    reader.readAsText(file)
    e.target.value = '' // reset so same file can be re-selected
  }

  const handleExport = async () => {
    try {
      const json = await api.exportMCPServers()
      const blob = new Blob([json], { type: 'application/json' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = '.mcp.json'
      a.click()
      URL.revokeObjectURL(url)
    } catch {
      // silent — unlikely to fail
    }
  }

  const intentTestMutation = useMutation<ToolSelection[], Error, string>({
    mutationFn: api.selectTools,
  })

  // Note: auto-refresh on session switch is handled globally by useToolRefresh in AppShell.

  const handleRefresh = () => {
    refreshMutation.mutate()
  }

  const handleIntentTest = () => {
    if (intentQuery.trim()) {
      intentTestMutation.mutate(intentQuery.trim())
    }
  }

  const openAddForm = () => {
    setServerForm(emptyForm)
    setEditingServer(null)
    setServerFormError(null)
    setShowServerForm(true)
  }

  const openEditForm = (name: string) => {
    const cfg = mcpConfigs.find(c => c.name === name)
    if (cfg) {
      setServerForm(configToForm(cfg))
      setEditingServer(name)
      setServerFormError(null)
      setShowServerForm(true)
    }
  }

  const closeServerForm = () => {
    setShowServerForm(false)
    setEditingServer(null)
    setServerForm(emptyForm)
    setServerFormError(null)
  }

  const handleServerFormSubmit = () => {
    setServerFormError(null)

    if (!serverForm.name.trim()) {
      setServerFormError('Name is required')
      return
    }

    if (serverForm.transport_type === 'stdio' && !serverForm.command.trim()) {
      setServerFormError('Command is required for stdio transport')
      return
    }

    if (serverForm.transport_type === 'sse' && !serverForm.url.trim()) {
      setServerFormError('URL is required for SSE transport')
      return
    }

    const payload = formToPayload(serverForm)

    if (editingServer) {
      updateServerMutation.mutate({ name: editingServer, data: payload })
    } else {
      addServerMutation.mutate(payload)
    }
  }

  const isFormSubmitting = addServerMutation.isPending || updateServerMutation.isPending

  // Filter tools by server
  const filteredTools = filterServer
    ? tools.filter(tool => tool.name.includes(filterServer))
    : tools

  const [activeTab, setActiveTab] = useState<'servers' | 'tools' | 'loading'>('servers')
  const [toolSearch, setToolSearch] = useState('')
  const [loadSearch, setLoadSearch] = useState('')

  const searchedTools = toolSearch
    ? filteredTools.filter(t => t.name.toLowerCase().includes(toolSearch.toLowerCase()) || t.description?.toLowerCase().includes(toolSearch.toLowerCase()))
    : filteredTools

  return (
    <div className="space-y-4">
      {/* Toolbar: Tabs + Controls */}
      <div className="flex items-center gap-3 flex-wrap">
        {/* Tabs */}
        <div className="flex items-center gap-1 bg-surface/50 rounded-lg p-0.5">
          <button
            onClick={() => setActiveTab('servers')}
            className={`flex items-center gap-1.5 px-3 py-1.5 rounded-md text-sm font-medium transition-colors ${
              activeTab === 'servers'
                ? 'bg-bg-elevated text-fg shadow-sm'
                : 'text-fg-muted hover:text-fg-secondary'
            }`}
          >
            <Server className="w-3.5 h-3.5" />
            Servers
            <span className="text-[11px] text-fg-faint tabular-nums">{servers.length}</span>
          </button>
          <button
            onClick={() => setActiveTab('tools')}
            className={`flex items-center gap-1.5 px-3 py-1.5 rounded-md text-sm font-medium transition-colors ${
              activeTab === 'tools'
                ? 'bg-bg-elevated text-fg shadow-sm'
                : 'text-fg-muted hover:text-fg-secondary'
            }`}
          >
            <Wrench className="w-3.5 h-3.5" />
            Tools
            <span className="text-[11px] text-fg-faint tabular-nums">{tools.length}</span>
          </button>
          <button
            onClick={() => setActiveTab('loading')}
            className={`flex items-center gap-1.5 px-3 py-1.5 rounded-md text-sm font-medium transition-colors ${
              activeTab === 'loading'
                ? 'bg-bg-elevated text-fg shadow-sm'
                : 'text-fg-muted hover:text-fg-secondary'
            }`}
          >
            <SlidersHorizontal className="w-3.5 h-3.5" />
            Load Preferences
          </button>
        </div>

        {/* Refresh */}
        <Button
          variant="ghost"
          size="sm"
          className="text-xs text-fg-secondary hover:text-fg"
          onClick={handleRefresh}
          disabled={refreshMutation.isPending}
        >
          {refreshMutation.isPending ? (
            <RefreshCw className="w-3 h-3 animate-spin mr-1" />
          ) : (
            <RefreshCw className="w-3.5 h-3.5 mr-1" />
          )}
          Refresh
        </Button>

        {refreshResult && (
          <span className="text-[11px] text-fg-muted">
            +{(refreshResult.added ?? []).length} -{(refreshResult.removed ?? []).length} ({refreshResult.total} total)
          </span>
        )}

        <div className="flex-1" />

        {/* Tab-specific controls */}
        {activeTab === 'servers' && (
          <div className="flex items-center gap-1.5">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setShowImportDialog(true)}
              className="gap-1.5 text-xs text-fg-secondary hover:text-fg"
            >
              <Upload className="w-3.5 h-3.5" />
              Import
            </Button>
            <Button
              variant="ghost"
              size="sm"
              onClick={handleExport}
              className="gap-1.5 text-xs text-fg-secondary hover:text-fg"
            >
              <Download className="w-3.5 h-3.5" />
              Export
            </Button>
            <Button
              size="sm"
              onClick={openAddForm}
              className="gap-1.5"
            >
              <Plus className="w-3.5 h-3.5" />
              Add Server
            </Button>
          </div>
        )}

        {activeTab === 'loading' && (
          <div className="flex items-center gap-2">
            <div className="relative">
              <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-fg-faint pointer-events-none" />
              <input
                type="text"
                value={loadSearch}
                onChange={(e) => setLoadSearch(e.target.value)}
                placeholder="Filter tools..."
                className="w-40 bg-surface/50 border border-border rounded-md pl-8 pr-3 py-1.5 text-xs text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-primary"
              />
            </div>
          </div>
        )}

        {activeTab === 'tools' && (
          <div className="flex items-center gap-2">
            <div className="relative">
              <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-fg-faint pointer-events-none" />
              <input
                type="text"
                value={toolSearch}
                onChange={(e) => setToolSearch(e.target.value)}
                placeholder="Search tools..."
                className="w-40 bg-surface/50 border border-border rounded-md pl-8 pr-3 py-1.5 text-xs text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-primary"
              />
            </div>
            <select
              value={filterServer}
              onChange={(e) => setFilterServer(e.target.value)}
              className="appearance-none px-3 pr-8 py-1.5 bg-surface/50 border border-border rounded-lg text-fg text-xs focus:outline-none focus:ring-1 focus:ring-primary cursor-pointer"
            >
              <option value="">All servers</option>
              {servers.map(server => (
                <option key={server.name} value={server.name}>{server.name}</option>
              ))}
            </select>
          </div>
        )}
      </div>

      {/* Servers tab */}
      {activeTab === 'servers' && (
        <div className="grid gap-3 grid-cols-2">
          {serversLoading && (
            <>
              {Array.from({ length: 4 }).map((_, i) => (
                <div key={i} className="rounded-xl border border-border-subtle bg-bg-elevated/60 shadow-sm overflow-hidden">
                  <div className="px-3.5 py-3 flex items-center gap-2.5">
                    <Skeleton className="size-9 rounded-lg" />
                    <div className="flex flex-col gap-1.5 flex-1">
                      <Skeleton className="h-3.5 w-1/2" />
                      <Skeleton className="h-2.5 w-1/3" />
                    </div>
                  </div>
                  <div className="border-t border-border/50 px-3.5 py-2">
                    <Skeleton className="h-2.5 w-3/4" />
                  </div>
                </div>
              ))}
            </>
          )}

          {!serversLoading && servers.length === 0 && (
            <div className="col-span-2">
              <Empty className="py-12">
                <EmptyHeader>
                  <EmptyMedia variant="icon"><Server /></EmptyMedia>
                  <EmptyTitle className="text-sm">No MCP servers connected</EmptyTitle>
                  <EmptyDescription className="text-xs">Add a server to discover tools</EmptyDescription>
                </EmptyHeader>
              </Empty>
            </div>
          )}

          {servers.map(server => {
            const isManaged = managedServerNames.has(server.name)
            const cfg = isManaged ? mcpConfigs.find(c => c.name === server.name) : null
            return (
              <div
                key={server.name}
                className={`rounded-xl border shadow-sm overflow-hidden transition-all cursor-pointer hover:shadow-md ${
                  server.connected
                    ? 'border-border-subtle bg-white dark:bg-bg-elevated/60'
                    : 'border-border bg-white dark:bg-bg/30 opacity-45'
                }`}
                onClick={() => {
                  setFilterServer(server.name)
                  setActiveTab('tools')
                }}
              >
                {/* Header */}
                <div className="flex items-center gap-2.5 px-3.5 py-3">
                  <span className={`inline-flex items-center justify-center w-9 h-9 rounded-lg shrink-0 ${
                    server.connected ? 'bg-surface-hover text-fg-secondary' : 'bg-surface text-fg-muted'
                  }`}>
                    <Server className="w-4 h-4" />
                  </span>
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-semibold text-fg truncate">{server.name}</span>
                      {server.connected && <span className="w-1.5 h-1.5 rounded-full bg-success shrink-0" />}
                    </div>
                    <div className="flex items-center gap-1.5 mt-0.5">
                      <span className="text-[11px] text-fg-muted">{server.tool_count} tools</span>
                      {!isManaged && (
                        <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-bg-elevated border border-border-subtle text-fg-muted leading-none">built-in</span>
                      )}
                    </div>
                  </div>
                  {isManaged && (
                    <div className="flex items-center gap-0.5 shrink-0">
                      <button
                        onClick={(e) => { e.stopPropagation(); openEditForm(server.name) }}
                        className="p-1.5 rounded text-fg-faint hover:text-fg-secondary hover:bg-surface transition-colors"
                      >
                        <Pencil className="w-3.5 h-3.5" />
                      </button>
                      <button
                        onClick={(e) => { e.stopPropagation(); setDeletingServer(server.name) }}
                        className="p-1.5 rounded text-fg-faint hover:text-danger hover:bg-surface transition-colors"
                      >
                        <Trash2 className="w-3.5 h-3.5" />
                      </button>
                    </div>
                  )}
                </div>

                {/* Detail footer */}
                <div className="border-t border-border/50 px-3.5 py-2 bg-bg-elevated/40 flex items-center gap-3">
                  <span className="text-[11px] text-fg-muted capitalize">
                    {cfg?.transport_type?.toUpperCase() ?? 'Built-in'}
                  </span>
                  {cfg?.command && (
                    <>
                      <div className="w-px h-3.5 bg-border shrink-0" />
                      <code className="text-[11px] text-fg-secondary font-mono truncate">{cfg.command}</code>
                    </>
                  )}
                  {cfg?.url && (
                    <>
                      <div className="w-px h-3.5 bg-border shrink-0" />
                      <code className="text-[11px] text-fg-secondary font-mono truncate">{cfg.url}</code>
                    </>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      )}

      {/* Tools tab */}
      {activeTab === 'tools' && (
        <div className="grid gap-3 grid-cols-2">
          {toolsLoading && (
            <>
              {Array.from({ length: 4 }).map((_, i) => (
                <div key={i} className="rounded-xl border border-border-subtle bg-bg-elevated/60 shadow-sm overflow-hidden">
                  <div className="px-3.5 py-3 flex items-center gap-2.5">
                    <Skeleton className="size-9 rounded-lg" />
                    <div className="flex flex-col gap-1.5 flex-1">
                      <Skeleton className="h-3.5 w-1/2" />
                      <Skeleton className="h-2.5 w-1/3" />
                    </div>
                  </div>
                  <div className="border-t border-border/50 px-3.5 py-2">
                    <Skeleton className="h-2.5 w-3/4" />
                  </div>
                </div>
              ))}
            </>
          )}

          {!toolsLoading && searchedTools.length === 0 && (
            <div className="col-span-2">
              <Empty className="py-12">
                <EmptyHeader>
                  <EmptyMedia variant="icon"><Wrench /></EmptyMedia>
                  <EmptyTitle className="text-sm">
                    {toolSearch || filterServer ? 'No tools match your filter' : 'No tools available'}
                  </EmptyTitle>
                  <EmptyDescription className="text-xs">
                    {toolSearch || filterServer
                      ? 'Try adjusting your search or filter criteria.'
                      : 'Connect a server to discover tools.'}
                  </EmptyDescription>
                </EmptyHeader>
                {(toolSearch || filterServer) && (
                  <button
                    onClick={() => { setToolSearch(''); setFilterServer('') }}
                    className="text-xs text-primary hover:text-primary-hover mt-2 transition-colors"
                  >
                    Clear filters
                  </button>
                )}
              </Empty>
            </div>
          )}

          {searchedTools.map(tool => {
            const serverName = tool.name.includes('mcp_') ? tool.name.split('__')[1] : undefined
            return (
              <div
                key={tool.name}
                className="rounded-xl border border-border-subtle bg-white dark:bg-bg-elevated/60 shadow-sm overflow-hidden transition-all cursor-pointer hover:shadow-md"
                onClick={() => setSelectedTool(tool)}
              >
                {/* Header */}
                <div className="flex items-center gap-2.5 px-3.5 py-3">
                  <span className="inline-flex items-center justify-center w-9 h-9 rounded-lg bg-surface-hover text-fg-secondary shrink-0">
                    <Wrench className="w-4 h-4" />
                  </span>
                  <div className="flex-1 min-w-0">
                    <span className="text-sm font-semibold text-fg truncate block">{tool.name.split('__').pop()}</span>
                    {serverName && (
                      <span className="text-[11px] text-fg-muted truncate block">{serverName}</span>
                    )}
                  </div>
                </div>

                {/* Detail footer */}
                <div className="border-t border-border/50 px-3.5 py-2 bg-bg-elevated/40">
                  <p className="text-[11px] text-fg-muted line-clamp-2">
                    {tool.description || 'No description'}
                  </p>
                </div>
              </div>
            )
          })}
        </div>
      )}

      {/* Load Preferences tab */}
      {activeTab === 'loading' && (
        <div className="space-y-3">
          <p className="text-xs text-fg-muted">
            Control which tools load automatically vs. on-demand. <strong className="text-fg-secondary">Auto</strong> tools are available in every request. <strong className="text-fg-secondary">Opt-in</strong> tools are only loaded when explicitly needed.
          </p>

          {loadPrefsLoading && (
            <div className="space-y-2">
              {Array.from({ length: 6 }).map((_, i) => (
                <Skeleton key={i} className="h-12 w-full rounded-lg" />
              ))}
            </div>
          )}

          {!loadPrefsLoading && allToolsWithLoad.length === 0 && (
            <Empty className="py-12">
              <EmptyHeader>
                <EmptyMedia variant="icon"><SlidersHorizontal /></EmptyMedia>
                <EmptyTitle className="text-sm">No tools discovered</EmptyTitle>
                <EmptyDescription className="text-xs">Connect a server and refresh to see tools here.</EmptyDescription>
              </EmptyHeader>
            </Empty>
          )}

          {!loadPrefsLoading && allToolsWithLoad.length > 0 && (() => {
            const searched = loadSearch
              ? allToolsWithLoad.filter(t =>
                  t.name.toLowerCase().includes(loadSearch.toLowerCase()) ||
                  t.description?.toLowerCase().includes(loadSearch.toLowerCase())
                )
              : allToolsWithLoad

            // Group by server
            const grouped = new Map<string, ToolLoadItem[]>()
            for (const tool of searched) {
              const server = tool.name.includes('__') ? tool.name.split('__')[1] : '_builtin'
              const list = grouped.get(server) || []
              list.push(tool)
              grouped.set(server, list)
            }

            return (
              <div className="space-y-4">
                {Array.from(grouped.entries()).map(([server, serverTools]) => (
                  <div key={server}>
                    <div className="flex items-center gap-2 mb-2">
                      <Server className="w-3 h-3 text-fg-faint" />
                      <span className="text-xs font-medium text-fg-secondary">{server === '_builtin' ? 'Built-in' : server}</span>
                      <span className="text-[10px] text-fg-faint tabular-nums">{serverTools.length}</span>
                    </div>
                    <div className="rounded-lg border border-border-subtle overflow-hidden divide-y divide-border/50">
                      {serverTools.map(tool => {
                        const shortName = tool.name.split('__').pop() || tool.name
                        const isAuto = tool.load_type === 'auto'
                        const hasUserOverride = tool.load_type_source === 'user'
                        return (
                          <div
                            key={tool.name}
                            className="flex items-center gap-3 px-3.5 py-2.5 bg-bg-elevated/60 hover:bg-bg-elevated transition-colors"
                          >
                            <div className="flex-1 min-w-0">
                              <div className="flex items-center gap-2">
                                <span className="text-sm font-medium text-fg truncate">{shortName}</span>
                                {hasUserOverride && (
                                  <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-indigo-500/20 border border-indigo-500/30 text-indigo-300 leading-none">override</span>
                                )}
                              </div>
                              {tool.description && (
                                <p className="text-[11px] text-fg-muted truncate mt-0.5">{tool.description}</p>
                              )}
                            </div>
                            <button
                              onClick={() => {
                                const newType = isAuto ? 'opt-in' : 'auto'
                                loadPrefMutation.mutate({ [tool.name]: newType })
                              }}
                              disabled={loadPrefMutation.isPending}
                              className="flex items-center gap-1.5 shrink-0 group"
                              title={isAuto ? 'Click to set opt-in' : 'Click to set auto'}
                            >
                              {isAuto ? (
                                <ToggleRight className="w-5 h-5 text-success group-hover:text-success transition-colors" />
                              ) : (
                                <ToggleLeft className="w-5 h-5 text-fg-faint group-hover:text-fg-muted transition-colors" />
                              )}
                              <span className={`text-[11px] font-medium w-10 ${isAuto ? 'text-success' : 'text-fg-faint'}`}>
                                {isAuto ? 'Auto' : 'Opt-in'}
                              </span>
                            </button>
                            {hasUserOverride && (
                              <button
                                onClick={() => loadPrefMutation.mutate({ [tool.name]: '' })}
                                disabled={loadPrefMutation.isPending}
                                className="text-[10px] text-fg-faint hover:text-fg-muted transition-colors"
                                title="Remove override (use default)"
                              >
                                reset
                              </button>
                            )}
                          </div>
                        )
                      })}
                    </div>
                  </div>
                ))}
              </div>
            )
          })()}
        </div>
      )}

      {/* Intent Test — compact inline */}
      <div className="flex items-center gap-2">
        <Activity className="w-3.5 h-3.5 text-fg-faint shrink-0" />
        <div className="relative flex-1">
          <input
            type="text"
            value={intentQuery}
            onChange={(e) => setIntentQuery(e.target.value)}
            placeholder="Test tool selection by intent..."
            className="w-full bg-surface/50 border border-border rounded-lg pl-3 pr-16 py-1.5 text-xs text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-primary"
            onKeyDown={(e) => { if (e.key === 'Enter') handleIntentTest() }}
          />
          <button
            onClick={handleIntentTest}
            disabled={!intentQuery.trim() || intentTestMutation.isPending}
            className="absolute right-1.5 top-1/2 -translate-y-1/2 px-2 py-0.5 text-[11px] font-medium text-fg-secondary hover:text-fg bg-bg-elevated border border-border-subtle rounded-md disabled:opacity-40 transition-colors"
          >
            {intentTestMutation.isPending ? 'Testing...' : 'Test'}
          </button>
        </div>
      </div>

      {intentTestMutation.data && intentTestMutation.data.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {intentTestMutation.data.map((tool, index) => (
            <span key={index} className="text-[10px] px-1.5 py-0.5 rounded-md bg-bg-elevated border border-border-subtle text-fg-muted leading-none font-mono">
              {tool.name.split('__').pop()}
            </span>
          ))}
        </div>
      )}

      {intentTestMutation.error && (
        <p className="text-xs text-danger flex items-center gap-1.5">
          <AlertCircle className="w-3 h-3" />
          {intentTestMutation.error.message}
        </p>
      )}

      {/* Tool Detail Modal */}
      <Dialog open={!!selectedTool} onOpenChange={() => setSelectedTool(null)}>
        <DialogContent className="sm:max-w-2xl max-h-[80vh] flex flex-col">
          <DialogHeader className="px-5 pt-5">
            <div className="flex items-center gap-2">
              <Wrench className="w-5 h-5 text-fg-secondary" />
              <DialogTitle>Tool Details</DialogTitle>
            </div>
            <DialogDescription className="sr-only">Detailed information about the selected tool</DialogDescription>
          </DialogHeader>

          {selectedTool && (
            <div className="flex-1 overflow-auto px-5 py-4 space-y-4">
              <div>
                <h4 className="text-sm font-medium text-fg-secondary mb-2">Name</h4>
                <code className="px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg font-mono text-sm text-fg block">
                  {selectedTool.name}
                </code>
              </div>

              <div>
                <h4 className="text-sm font-medium text-fg-secondary mb-2">Description</h4>
                <p className="text-sm text-fg-secondary leading-relaxed">
                  {selectedTool.description || 'No description available'}
                </p>
              </div>

              <div>
                <h4 className="text-sm font-medium text-fg-secondary mb-2">Server</h4>
                <p className="text-sm text-fg-secondary">
                  {selectedTool.name.includes('mcp_') ? selectedTool.name.split('__')[1] : 'Unknown server'}
                </p>
              </div>

              <div>
                <h4 className="text-sm font-medium text-fg-secondary mb-2">Input Schema</h4>
                <div className="bg-white dark:bg-bg-elevated/60 rounded-xl border border-border-subtle shadow-sm p-4 overflow-auto">
                  <pre className="text-sm text-fg-secondary font-mono whitespace-pre-wrap">
                    {selectedTool.input_schema ?
                      JSON.stringify(selectedTool.input_schema, null, 2) :
                      'No schema available'
                    }
                  </pre>
                </div>
              </div>

              <div>
                <h4 className="text-sm font-medium text-fg-secondary mb-2">Referenced by Skills</h4>
                <div className="text-sm text-fg-muted">
                  Skills integration coming soon...
                </div>
              </div>
            </div>
          )}

          <DialogFooter className="px-5 pb-5 border-t border-border-subtle pt-4">
            <Button
              onClick={() => setSelectedTool(null)}
              variant="outline"
              className="w-full"
            >
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Add/Edit Server Modal */}
      <Dialog open={showServerForm} onOpenChange={() => closeServerForm()}>
        <DialogContent className="sm:max-w-md max-h-[85vh] flex flex-col">
          <DialogHeader className="px-5 pt-5">
            <div className="flex items-center gap-2">
              <Server className="w-5 h-5 text-fg-secondary" />
              <DialogTitle>
                {editingServer ? 'Edit MCP Server' : 'Add MCP Server'}
              </DialogTitle>
            </div>
            <DialogDescription className="sr-only">Configure an MCP server connection</DialogDescription>
          </DialogHeader>

          <div className="flex-1 overflow-auto px-5 py-4 space-y-4">
            {/* Name */}
            <div>
              <label className="block text-sm font-medium text-fg-secondary mb-1">Name</label>
              <input
                type="text"
                value={serverForm.name}
                onChange={(e) => setServerForm(f => ({ ...f, name: e.target.value }))}
                disabled={!!editingServer}
                placeholder="my-server"
                className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg placeholder:text-fg-muted focus:outline-none focus:ring-1 focus:ring-primary disabled:opacity-50"
              />
              {editingServer && (
                <p className="text-xs text-fg-muted mt-1">Name cannot be changed after creation.</p>
              )}
            </div>

            {/* Transport Type */}
            <div>
              <label className="block text-sm font-medium text-fg-secondary mb-1">Transport Type</label>
              <select
                value={serverForm.transport_type}
                onChange={(e) => setServerForm(f => ({ ...f, transport_type: e.target.value as 'stdio' | 'sse' }))}
                className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg focus:outline-none focus:ring-1 focus:ring-primary"
              >
                <option value="stdio">stdio (subprocess)</option>
                <option value="sse">SSE / HTTP</option>
              </select>
            </div>

            {/* Command (stdio only) */}
            {serverForm.transport_type === 'stdio' && (
              <div>
                <label className="block text-sm font-medium text-fg-secondary mb-1">Command</label>
                <input
                  type="text"
                  value={serverForm.command}
                  onChange={(e) => setServerForm(f => ({ ...f, command: e.target.value }))}
                  placeholder="/path/to/binary"
                  className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg placeholder:text-fg-muted focus:outline-none focus:ring-1 focus:ring-primary"
                />
              </div>
            )}

            {/* URL (sse only) */}
            {serverForm.transport_type === 'sse' && (
              <div>
                <label className="block text-sm font-medium text-fg-secondary mb-1">URL</label>
                <input
                  type="text"
                  value={serverForm.url}
                  onChange={(e) => setServerForm(f => ({ ...f, url: e.target.value }))}
                  placeholder="http://localhost:8080"
                  className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg placeholder:text-fg-muted focus:outline-none focus:ring-1 focus:ring-primary"
                />
              </div>
            )}

            {/* Args (stdio only) */}
            {serverForm.transport_type === 'stdio' && (
              <div>
                <label className="block text-sm font-medium text-fg-secondary mb-1">Arguments</label>
                <input
                  type="text"
                  value={serverForm.args}
                  onChange={(e) => setServerForm(f => ({ ...f, args: e.target.value }))}
                  placeholder="mcp, --flag, value"
                  className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg placeholder:text-fg-muted focus:outline-none focus:ring-1 focus:ring-primary"
                />
                <p className="text-xs text-fg-muted mt-1">Comma-separated list of arguments.</p>
              </div>
            )}

            {/* Env */}
            <div>
              <label className="block text-sm font-medium text-fg-secondary mb-1">Environment Variables</label>
              <textarea
                value={serverForm.env}
                onChange={(e) => setServerForm(f => ({ ...f, env: e.target.value }))}
                placeholder={"KEY=value\nANOTHER_KEY=value"}
                rows={3}
                className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg placeholder:text-fg-muted focus:outline-none focus:ring-1 focus:ring-primary font-mono text-sm"
              />
              <p className="text-xs text-fg-muted mt-1">One KEY=VALUE per line.</p>
            </div>

            {/* Error */}
            {serverFormError && (
              <div className="flex items-center gap-2 px-3 py-2 rounded-md bg-danger/20 border border-danger/50 text-sm text-danger">
                <AlertCircle className="w-4 h-4 flex-shrink-0" />
                <span>{serverFormError}</span>
              </div>
            )}
          </div>

          <DialogFooter className="px-5 pb-5 border-t border-border-subtle pt-4 flex gap-3">
            <Button onClick={closeServerForm} variant="outline" className="flex-1">
              Cancel
            </Button>
            <Button
              onClick={handleServerFormSubmit}
              disabled={isFormSubmitting}
              className="flex-1"
            >
              {isFormSubmitting ? (
                <Loader2 className="w-4 h-4 animate-spin" />
              ) : editingServer ? (
                'Save Changes'
              ) : (
                'Add Server'
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete Confirmation Modal */}
      <Dialog open={!!deletingServer} onOpenChange={() => setDeletingServer(null)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader className="px-5 pt-5">
            <div className="flex items-center gap-2">
              <AlertCircle className="w-5 h-5 text-danger" />
              <DialogTitle>Delete Server</DialogTitle>
            </div>
            <DialogDescription className="sr-only">Confirm server deletion</DialogDescription>
          </DialogHeader>
          <div className="px-5 py-4">
            <p className="text-fg-secondary text-sm">
              Are you sure you want to delete <span className="font-medium text-fg">{deletingServer}</span>?
              This will disconnect the server and remove its configuration.
            </p>
          </div>
          <DialogFooter className="px-5 pb-5">
            <Button
              onClick={() => setDeletingServer(null)}
              variant="outline"
              className="flex-1"
            >
              Cancel
            </Button>
            <Button
              onClick={() => deletingServer && deleteServerMutation.mutate(deletingServer)}
              disabled={deleteServerMutation.isPending}
              variant="destructive"
              className="flex-1"
            >
              {deleteServerMutation.isPending ? (
                <Loader2 className="w-4 h-4 animate-spin" />
              ) : (
                'Delete'
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Import MCP Config Modal */}
      <Dialog open={showImportDialog} onOpenChange={() => closeImportDialog()}>
        <DialogContent className="sm:max-w-lg max-h-[85vh] flex flex-col">
          <DialogHeader className="px-5 pt-5">
            <div className="flex items-center gap-2">
              <Upload className="w-5 h-5 text-fg-secondary" />
              <DialogTitle>Import .mcp.json</DialogTitle>
            </div>
            <DialogDescription className="text-xs text-fg-muted mt-1">
              Paste a Claude Code <code className="font-mono">.mcp.json</code> config or upload a file. Existing servers with the same name will be skipped.
            </DialogDescription>
          </DialogHeader>

          <div className="flex-1 overflow-auto px-5 py-4 space-y-4">
            {/* File upload */}
            <div>
              <label className="inline-flex items-center gap-2 px-3 py-1.5 text-xs font-medium text-fg-secondary bg-bg-elevated border border-border-subtle rounded-lg cursor-pointer hover:bg-surface transition-colors">
                <Upload className="w-3.5 h-3.5" />
                Choose file
                <input
                  type="file"
                  accept=".json,.mcp.json"
                  onChange={handleImportFile}
                  className="hidden"
                />
              </label>
            </div>

            {/* Text area */}
            <textarea
              value={importText}
              onChange={(e) => { setImportText(e.target.value); setImportError(null); setImportResult(null) }}
              placeholder={'{\n  "mcpServers": {\n    "my-server": {\n      "command": "/path/to/binary",\n      "args": ["--flag"],\n      "env": { "KEY": "value" }\n    }\n  }\n}'}
              rows={10}
              className="w-full px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-primary font-mono text-xs leading-relaxed"
            />

            {/* Error */}
            {importError && (
              <div className="flex items-center gap-2 px-3 py-2 rounded-md bg-danger/20 border border-danger/50 text-sm text-danger">
                <AlertCircle className="w-4 h-4 flex-shrink-0" />
                <span>{importError}</span>
              </div>
            )}

            {/* Success */}
            {importResult && (
              <div className="px-3 py-2 rounded-md bg-success/20 border border-success/50 text-sm text-success space-y-1">
                <div className="flex items-center gap-2">
                  <CheckCircle2 className="w-4 h-4 flex-shrink-0" />
                  <span>{importResult.created?.length ?? 0} created, {importResult.skipped?.length ?? 0} skipped</span>
                </div>
                {(importResult.created?.length ?? 0) > 0 && (
                  <div className="text-xs text-success/80 pl-6">
                    {importResult.created.map(n => <div key={n}>+ {n}</div>)}
                  </div>
                )}
                {(importResult.skipped?.length ?? 0) > 0 && (
                  <div className="text-xs text-fg-muted pl-6">
                    {importResult.skipped.map(n => <div key={n}>~ {n} (exists)</div>)}
                  </div>
                )}
              </div>
            )}
          </div>

          <DialogFooter className="px-5 pb-5 border-t border-border-subtle pt-4 flex gap-3">
            <Button onClick={closeImportDialog} variant="outline" className="flex-1">
              {importResult ? 'Done' : 'Cancel'}
            </Button>
            {!importResult && (
              <Button
                onClick={handleImportSubmit}
                disabled={importMutation.isPending || !importText.trim()}
                className="flex-1"
              >
                {importMutation.isPending ? (
                  <Loader2 className="w-4 h-4 animate-spin" />
                ) : (
                  'Import'
                )}
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
