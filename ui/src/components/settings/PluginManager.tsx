import { useState, useCallback } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Package,
  Loader2,
  AlertCircle,
  RefreshCw,
  Settings2,
  Power,
  PowerOff,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
} from '@/components/ui/context-menu'
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription } from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { api } from '@/lib/api'
import { useAppStore } from '@/stores/useAppStore'
import { PluginConfigPanel } from './PluginConfigPanel'
import type { PluginInfo } from '@/lib/types'

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
    <div className="space-y-4">
      {/* Toolbar */}
      <div className="flex items-center gap-3">
        <Button
          variant="ghost"
          size="sm"
          className="text-xs text-fg-secondary hover:text-fg"
          onClick={() => refetch()}
          disabled={isLoading}
        >
          {isLoading ? (
            <RefreshCw className="w-3 h-3 animate-spin mr-1" />
          ) : (
            <RefreshCw className="w-3.5 h-3.5 mr-1" />
          )}
          Refresh
        </Button>
        <div className="flex-1" />
        <p className="text-[11px] text-fg-faint">Changes require a restart</p>
      </div>

      {/* Error state */}
      {isError && (
        <div className="rounded-xl border border-red-500/30 bg-red-500/5 p-4 flex items-start gap-3">
          <AlertCircle className="w-4 h-4 text-red-400 shrink-0 mt-0.5" />
          <div>
            <p className="text-sm font-medium text-fg">Failed to load plugins</p>
            <p className="text-xs text-fg-muted mt-1">
              {(error as Error)?.message || 'The plugin API may not be available yet.'}
            </p>
          </div>
        </div>
      )}

      {/* Loading */}
      {isLoading && !isError && (
        <div className="grid grid-cols-2 gap-3">
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
        </div>
      )}

      {/* Empty */}
      {!isLoading && !isError && sortedPlugins.length === 0 && (
        <Empty className="py-12">
          <EmptyHeader>
            <EmptyMedia variant="icon"><Package /></EmptyMedia>
            <EmptyTitle className="text-sm">No plugins found</EmptyTitle>
            <EmptyDescription className="text-xs">Plugins will appear here once available.</EmptyDescription>
          </EmptyHeader>
        </Empty>
      )}

      {/* Plugin grid */}
      {!isLoading && !isError && sortedPlugins.length > 0 && (
        <div className="grid gap-3 grid-cols-2">
          {sortedPlugins.map((plugin) => {
            const isActive = plugin.status === 'active'
            const isDisabled = plugin.status === 'disabled'
            const isAvailable = plugin.status === 'available'
            return (
              <ContextMenu key={plugin.name}>
                <ContextMenuTrigger asChild>
              <div
                className={`rounded-xl border shadow-sm overflow-hidden transition-all cursor-pointer hover:shadow-md ${
                  isActive
                    ? 'border-border-subtle bg-white dark:bg-bg-elevated/60'
                    : isDisabled
                      ? 'border-border-subtle bg-white dark:bg-bg-elevated/60 opacity-55'
                      : 'border-border bg-white dark:bg-bg/30 opacity-45'
                }`}
                onClick={() => {
                  if (isActive || isDisabled) setConfiguringPlugin(plugin)
                }}
              >
                {/* Header */}
                <div className="flex items-center gap-2.5 px-3.5 py-3">
                  <span className={`inline-flex items-center justify-center w-9 h-9 rounded-lg shrink-0 ${
                    isActive ? 'bg-zinc-700 text-zinc-300' : 'bg-zinc-300 text-zinc-500'
                  }`}>
                    <Package className="w-4 h-4" />
                  </span>
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span className={`text-sm font-semibold truncate ${isActive ? 'text-fg' : 'text-fg-muted'}`}>
                        {plugin.name}
                      </span>
                      {isActive && <span className="w-1.5 h-1.5 rounded-full bg-success shrink-0" />}
                    </div>
                    <div className="flex items-center gap-1.5 mt-0.5">
                      <span className="text-[11px] text-fg-muted">v{plugin.version || '0.0.0'}</span>
                      {plugin.author && (
                        <>
                          <span className="text-fg-faint text-[10px]">&middot;</span>
                          <span className="text-[11px] text-fg-muted truncate">{plugin.author}</span>
                        </>
                      )}
                    </div>
                  </div>
                  {/* Actions */}
                  {/* eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-static-element-interactions */}
                  <div className="flex items-center gap-1 shrink-0" onClick={(e) => e.stopPropagation()}>
                    {plugin.type === 'core' ? (
                      <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-bg-elevated border border-border-subtle text-fg-muted leading-none">core</span>
                    ) : isActive ? (
                      <>
                        <button
                          onClick={() => setConfiguringPlugin(plugin)}
                          className="p-1.5 rounded text-fg-faint hover:text-fg-secondary hover:bg-surface transition-colors"
                        >
                          <Settings2 className="w-3.5 h-3.5" />
                        </button>
                        <button
                          onClick={() => disableMutation.mutate(plugin.name)}
                          disabled={isActionPending(plugin.name)}
                          className="px-2 py-1 text-[11px] font-medium text-fg-muted hover:text-fg-secondary bg-bg-elevated border border-border-subtle rounded-md transition-colors disabled:opacity-40"
                        >
                          {isActionPending(plugin.name) ? <Loader2 className="w-3 h-3 animate-spin" /> : 'Disable'}
                        </button>
                      </>
                    ) : isDisabled ? (
                      <>
                        <button
                          onClick={() => setConfiguringPlugin(plugin)}
                          className="p-1.5 rounded text-fg-faint hover:text-fg-secondary hover:bg-surface transition-colors"
                        >
                          <Settings2 className="w-3.5 h-3.5" />
                        </button>
                        <button
                          onClick={() => enableMutation.mutate(plugin.name)}
                          disabled={isActionPending(plugin.name)}
                          className="px-2 py-1 text-[11px] font-medium text-fg-muted hover:text-fg-secondary bg-bg-elevated border border-border-subtle rounded-md transition-colors disabled:opacity-40"
                        >
                          {isActionPending(plugin.name) ? <Loader2 className="w-3 h-3 animate-spin" /> : 'Enable'}
                        </button>
                      </>
                    ) : isAvailable ? (
                      <button
                        onClick={() => installMutation.mutate(plugin.name)}
                        disabled={isActionPending(plugin.name)}
                        className="px-2 py-1 text-[11px] font-medium text-white bg-accent hover:bg-accent-hover rounded-md transition-colors disabled:opacity-40"
                      >
                        {isActionPending(plugin.name) ? <Loader2 className="w-3 h-3 animate-spin" /> : 'Install'}
                      </button>
                    ) : null}
                  </div>
                </div>

                {/* Detail footer */}
                <div className="border-t border-border/50 px-3.5 py-2 bg-bg-elevated/40">
                  <p className="text-[11px] text-fg-muted line-clamp-2">
                    {plugin.short_desc || plugin.description || 'No description'}
                  </p>
                </div>
              </div>
                </ContextMenuTrigger>
                <ContextMenuContent>
                  {(isActive || isDisabled) && (
                    <ContextMenuItem className="gap-2 text-xs" onClick={() => setConfiguringPlugin(plugin)}>
                      <Settings2 className="w-3.5 h-3.5" />
                      Settings
                    </ContextMenuItem>
                  )}
                  {isActive && (
                    <ContextMenuItem className="gap-2 text-xs" onClick={() => disableMutation.mutate(plugin.name)} disabled={isActionPending(plugin.name)}>
                      <PowerOff className="w-3.5 h-3.5" />
                      Disable
                    </ContextMenuItem>
                  )}
                  {isDisabled && (
                    <ContextMenuItem className="gap-2 text-xs" onClick={() => enableMutation.mutate(plugin.name)} disabled={isActionPending(plugin.name)}>
                      <Power className="w-3.5 h-3.5" />
                      Enable
                    </ContextMenuItem>
                  )}
                </ContextMenuContent>
              </ContextMenu>
            )
          })}
        </div>
      )}

      {/* Uninstall confirmation modal */}
      {confirmUninstall && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
          <div className="bg-white dark:bg-bg-elevated border border-border-subtle rounded-xl shadow-2xl max-w-sm w-full p-6">
            <div className="flex items-center gap-2 mb-4">
              <AlertCircle className="w-5 h-5 text-red-400" />
              <h3 className="text-lg font-medium text-fg">Uninstall Plugin</h3>
            </div>
            <p className="text-fg-secondary text-sm mb-6">
              Are you sure you want to uninstall{' '}
              <span className="font-medium text-fg">{confirmUninstall}</span>?
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
              className="px-4 py-3 bg-surface border border-border-subtle rounded-lg shadow-xl text-sm text-fg max-w-sm animate-in fade-in slide-in-from-bottom-2"
            >
              {toast.message}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
