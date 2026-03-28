import { useState, useCallback } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Package,
  Loader2,
  AlertCircle,
  ExternalLink,
  RefreshCw,
  Settings2,
} from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { api } from '@/lib/api'
import { useAppStore } from '@/stores/useAppStore'
import { PluginConfigPanel } from './PluginConfigPanel'
import type { PluginInfo } from '@/lib/types'

// --- Status / Type badge helpers ---

function statusBadge(status: PluginInfo['status']) {
  switch (status) {
    case 'active':
      return (
        <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-green-900/40 text-green-400 border border-green-700/40">
          Active
        </span>
      )
    case 'disabled':
      return (
        <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-yellow-900/40 text-yellow-400 border border-yellow-700/40">
          Disabled
        </span>
      )
    case 'available':
      return (
        <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-zinc-800 text-zinc-400 border border-zinc-700">
          Available
        </span>
      )
    case 'no-binary':
      return (
        <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-red-900/40 text-red-400 border border-red-700/40">
          No Binary
        </span>
      )
  }
}

function typeBadge(type: PluginInfo['type']) {
  if (type === 'core') {
    return (
      <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-zinc-800/60 text-zinc-500 border border-zinc-700/50">
        Core
      </span>
    )
  }
  return (
    <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-indigo-900/30 text-indigo-400 border border-indigo-700/40">
      User
    </span>
  )
}

// --- Toast notification ---

interface Toast {
  id: number
  message: string
}

let toastId = 0


// --- Main Component ---

export function PluginManager() {
  const [toasts, setToasts] = useState<Toast[]>([])
  const [confirmUninstall, setConfirmUninstall] = useState<string | null>(null)
  const [pendingAction, setPendingAction] = useState<string | null>(null) // plugin name with action in progress
  const [configuringPlugin, setConfiguringPlugin] = useState<PluginInfo | null>(null)
  const queryClient = useQueryClient()
  const bumpConfigVersion = useAppStore((s) => s.bumpConfigVersion)
  const configVersion = useAppStore((s) => s.configVersion)

  // Use configVersion as part of the query key so bumping it triggers refetch
  const {
    data: plugins = [],
    isLoading,
    isError,
    error,
    refetch,
  } = useQuery({
    queryKey: ['plugins', configVersion],
    queryFn: api.listPlugins,
    retry: 2,
    staleTime: 0,
  })

  const addToast = useCallback((message: string) => {
    const id = ++toastId
    setToasts((prev) => [...prev, { id, message }])
    setTimeout(() => {
      setToasts((prev) => prev.filter((t) => t.id !== id))
    }, 6000)
  }, [])

  const handlePostAction = useCallback(
    (actionLabel: string) => {
      addToast(`Plugin ${actionLabel}.`)
      bumpConfigVersion()
      void queryClient.invalidateQueries({ queryKey: ['plugins'] })
      void queryClient.invalidateQueries({ queryKey: ['agents'] })
      void refetch()
      setPendingAction(null)
    },
    [addToast, bumpConfigVersion, queryClient, refetch]
  )

  const installMutation = useMutation({
    mutationFn: api.installPlugin,
    onMutate: (name) => setPendingAction(name),
    onSuccess: () => void handlePostAction('installed'),
    onError: (err: Error) => {
      addToast(`Failed to install: ${err.message}`)
      setPendingAction(null)
    },
  })

  const uninstallMutation = useMutation({
    mutationFn: api.uninstallPlugin,
    onMutate: (name) => setPendingAction(name),
    onSuccess: () => void handlePostAction('uninstalled'),
    onError: (err: Error) => {
      addToast(`Failed to uninstall: ${err.message}`)
      setPendingAction(null)
    },
  })

  const disableMutation = useMutation({
    mutationFn: api.disablePlugin,
    onMutate: (name) => setPendingAction(name),
    onSuccess: () => void handlePostAction('disabled'),
    onError: (err: Error) => {
      addToast(`Failed to disable: ${err.message}`)
      setPendingAction(null)
    },
  })

  const enableMutation = useMutation({
    mutationFn: api.enablePlugin,
    onMutate: (name) => setPendingAction(name),
    onSuccess: () => void handlePostAction('enabled'),
    onError: (err: Error) => {
      addToast(`Failed to enable: ${err.message}`)
      setPendingAction(null)
    },
  })

  const isActionPending = (name: string) => pendingAction === name

  const handleUninstallConfirm = useCallback(() => {
    if (confirmUninstall) {
      uninstallMutation.mutate(confirmUninstall)
      setConfirmUninstall(null)
    }
  }, [confirmUninstall, uninstallMutation])

  // Sort: active first, then disabled, then available
  const sortedPlugins = [...plugins].sort((a, b) => {
    const order: Record<string, number> = { active: 0, disabled: 1, available: 2, 'no-binary': 3 }
    const diff = (order[a.status] ?? 4) - (order[b.status] ?? 4)
    if (diff !== 0) return diff
    // Core before user within same status
    if (a.type === 'core' && b.type !== 'core') return -1
    if (a.type !== 'core' && b.type === 'core') return 1
    return a.name.localeCompare(b.name)
  })

  // Show config panel when a plugin is selected for configuration
  if (configuringPlugin) {
    return (
      <PluginConfigPanel
        pluginId={configuringPlugin.name}
        pluginName={configuringPlugin.name}
        onBack={() => setConfiguringPlugin(null)}
      />
    )
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold text-zinc-100 mb-1">Plugins</h2>
          <p className="text-zinc-400 text-sm">
            Manage installed plugins. Plugin changes require a Conduit restart.
          </p>
        </div>
        <Button
          onClick={() => refetch()}
          variant="outline"
          className="flex items-center gap-2"
          disabled={isLoading}
        >
          {isLoading ? (
            <Loader2 className="w-4 h-4 animate-spin" />
          ) : (
            <RefreshCw className="w-4 h-4" />
          )}
          Refresh
        </Button>
      </div>

      {/* Error state */}
      {isError && (
        <div className="rounded-lg border border-red-700/50 bg-red-900/20 p-4 flex items-start gap-3">
          <AlertCircle className="w-5 h-5 text-red-400 shrink-0 mt-0.5" />
          <div>
            <p className="text-sm font-medium text-red-300">Failed to load plugins</p>
            <p className="text-sm text-red-400 mt-1">
              {(error as Error)?.message || 'The plugin API may not be available yet.'}
            </p>
            <Button
              variant="outline"
              size="sm"
              className="mt-3"
              onClick={() => refetch()}
            >
              Retry
            </Button>
          </div>
        </div>
      )}

      {/* Loading state */}
      {isLoading && !isError && (
        <div className="flex items-center justify-center py-12">
          <Loader2 className="w-6 h-6 animate-spin text-zinc-500" />
        </div>
      )}

      {/* Plugin grid */}
      {!isLoading && !isError && sortedPlugins.length === 0 && (
        <div className="text-center py-12">
          <Package className="w-10 h-10 mx-auto mb-3 text-zinc-600" />
          <p className="text-zinc-400">No plugins found</p>
          <p className="text-zinc-500 text-sm mt-1">The plugin registry is empty.</p>
        </div>
      )}

      {!isLoading && !isError && sortedPlugins.length > 0 && (
        <div className="grid gap-3">
          {sortedPlugins.map((plugin) => (
            <div
              key={plugin.name}
              className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-5 hover:border-zinc-700 transition-colors"
            >
              <div className="flex items-start justify-between gap-4">
                {/* Left: info */}
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2 mb-1.5 flex-wrap">
                    <span className="font-medium text-zinc-100">{plugin.name}</span>
                    <span className="text-xs text-zinc-500">v{plugin.version || '0.0.0'}</span>
                    {statusBadge(plugin.status)}
                    {typeBadge(plugin.type)}
                  </div>
                  <p className="text-sm text-zinc-400 leading-relaxed">
                    {plugin.short_desc || plugin.description || 'No description available'}
                  </p>
                  <div className="flex items-center gap-4 mt-2 text-xs text-zinc-500">
                    {plugin.author && <span>by {plugin.author}</span>}
                    {plugin.url && (
                      <a
                        href={plugin.url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="flex items-center gap-1 text-indigo-400 hover:text-indigo-300"
                      >
                        <ExternalLink className="w-3 h-3" />
                        Website
                      </a>
                    )}
                  </div>
                </div>

                {/* Right: actions */}
                <div className="flex items-center gap-2 shrink-0">
                  {plugin.type === 'core' ? (
                    <span className="text-xs text-zinc-600 italic">Always active</span>
                  ) : plugin.status === 'active' ? (
                    <>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="w-7 h-7 text-zinc-500 hover:text-zinc-300"
                        title="Configure"
                        onClick={() => setConfiguringPlugin(plugin)}
                      >
                        <Settings2 className="w-3.5 h-3.5" />
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={isActionPending(plugin.name)}
                        onClick={() => disableMutation.mutate(plugin.name)}
                      >
                        {isActionPending(plugin.name) ? (
                          <Loader2 className="w-3.5 h-3.5 animate-spin" />
                        ) : (
                          'Disable'
                        )}
                      </Button>
                      <Button
                        variant="destructive"
                        size="sm"
                        disabled={isActionPending(plugin.name)}
                        onClick={() => setConfirmUninstall(plugin.name)}
                      >
                        Uninstall
                      </Button>
                    </>
                  ) : plugin.status === 'disabled' ? (
                    <>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="w-7 h-7 text-zinc-500 hover:text-zinc-300"
                        title="Configure"
                        onClick={() => setConfiguringPlugin(plugin)}
                      >
                        <Settings2 className="w-3.5 h-3.5" />
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={isActionPending(plugin.name)}
                        onClick={() => enableMutation.mutate(plugin.name)}
                      >
                        {isActionPending(plugin.name) ? (
                          <Loader2 className="w-3.5 h-3.5 animate-spin" />
                        ) : (
                          'Enable'
                        )}
                      </Button>
                      <Button
                        variant="destructive"
                        size="sm"
                        disabled={isActionPending(plugin.name)}
                        onClick={() => setConfirmUninstall(plugin.name)}
                      >
                        Uninstall
                      </Button>
                    </>
                  ) : plugin.status === 'available' ? (
                    <Button
                      size="sm"
                      disabled={isActionPending(plugin.name)}
                      onClick={() => installMutation.mutate(plugin.name)}
                      className="bg-indigo-600 hover:bg-indigo-700 text-white"
                    >
                      {isActionPending(plugin.name) ? (
                        <Loader2 className="w-3.5 h-3.5 animate-spin" />
                      ) : (
                        'Install'
                      )}
                    </Button>
                  ) : (
                    <span className="text-xs text-zinc-600 italic">Binary missing</span>
                  )}
                </div>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Uninstall confirmation modal */}
      {confirmUninstall && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
          <div className="bg-zinc-900 rounded-lg border border-zinc-700 max-w-sm w-full p-6">
            <div className="flex items-center gap-2 mb-4">
              <AlertCircle className="w-5 h-5 text-red-400" />
              <h3 className="text-lg font-medium text-zinc-100">Uninstall Plugin</h3>
            </div>
            <p className="text-zinc-300 text-sm mb-6">
              Are you sure you want to uninstall{' '}
              <span className="font-medium text-zinc-100">{confirmUninstall}</span>?
              This will remove the plugin and restart Conduit.
            </p>
            <div className="flex gap-3">
              <Button
                onClick={() => setConfirmUninstall(null)}
                variant="outline"
                className="flex-1"
              >
                Cancel
              </Button>
              <Button
                onClick={handleUninstallConfirm}
                className="flex-1 bg-red-600 hover:bg-red-700 text-white"
              >
                Uninstall
              </Button>
            </div>
          </div>
        </div>
      )}

      {/* Toast container */}
      {toasts.length > 0 && (
        <div className="fixed bottom-4 right-4 z-50 flex flex-col gap-2">
          {toasts.map((toast) => (
            <div
              key={toast.id}
              className="px-4 py-3 bg-zinc-800 border border-zinc-700 rounded-lg shadow-xl text-sm text-zinc-200 max-w-sm animate-in fade-in slide-in-from-bottom-2"
            >
              {toast.message}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
