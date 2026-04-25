import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertCircle,
  ArrowUpCircle,
  Globe,
  Loader2,
  Package,
  Power,
  PowerOff,
  RefreshCw,
  Settings2,
  Trash2,
} from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import { api } from "@/lib/api";
import type { PluginInfo } from "@/lib/types";
import { useAppStore } from "@/stores/useAppStore";
import { CatalogBrowser } from "./CatalogBrowser";
import { CatalogSourceManager } from "./CatalogSourceManager";
import { PluginConfigPanel } from "./PluginConfigPanel";
import { PluginDetailView } from "./PluginDetailView";

// --- Trust tier badge (J.2) ---

// TrustTierBadge surfaces the install-time signature outcome per plugin.
// "signed" is the happy path in production (catalog + per-plugin sig verified);
// "unsigned" appears in any devmode build (the backend stamps unsigned for all
// installed plugins under the devmode build tag, regardless of the user's
// allow_unsigned_plugins setting — the setting only controls what the
// installer *accepts*, not how the UI labels already-installed plugins);
// "untrusted" is a defensive-only state and should be unreachable in shipped
// builds because the installer refuses bad signatures.
function TrustTierBadge({ tier }: { tier: "signed" | "unsigned" | "untrusted" }) {
  const style =
    tier === "signed"
      ? "bg-emerald-600/10 border-emerald-600/40 text-emerald-700 dark:text-emerald-400"
      : tier === "unsigned"
        ? "bg-amber-600/10 border-amber-600/40 text-amber-700 dark:text-amber-400"
        : "bg-red-600/10 border-red-600/40 text-red-700 dark:text-red-400";
  const label =
    tier === "signed"
      ? "signed \u2713"
      : tier === "unsigned"
        ? "unsigned \u26A0"
        : "untrusted \u{1F6AB}";
  const title =
    tier === "signed"
      ? "Signature verified against a trusted key"
      : tier === "unsigned"
        ? "Plugin was installed without a verified signature (devmode build)"
        : "Signature verification failed — do not trust";
  return (
    <>
      <span className="text-fg-faint text-[10px]">&middot;</span>
      <span
        className={`inline-flex items-center text-[10px] px-1.5 py-0.5 rounded-md border leading-none ${style}`}
        title={title}
      >
        {label}
      </span>
    </>
  );
}

// --- Toast notification ---

interface Toast {
  id: number;
  message: string;
}

let toastId = 0;

type Tab = "installed" | "catalog";
type SubView = "list" | "config" | "detail" | "sources";

// --- Main Component ---

export function PluginManager() {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [confirmUninstall, setConfirmUninstall] = useState<string | null>(null);
  const [pendingAction, setPendingAction] = useState<string | null>(null);
  const [configuringPlugin, setConfiguringPlugin] = useState<PluginInfo | null>(null);
  const [detailPlugin, setDetailPlugin] = useState<PluginInfo | null>(null);
  const [activeTab, setActiveTab] = useState<Tab>("installed");
  const [subView, setSubView] = useState<SubView>("list");
  const [catalogFocusEntry, setCatalogFocusEntry] = useState<string | null>(null);
  const queryClient = useQueryClient();
  const bumpConfigVersion = useAppStore((s) => s.bumpConfigVersion);
  const configVersion = useAppStore((s) => s.configVersion);

  const {
    data: plugins = [],
    isLoading,
    isError,
    error,
    refetch,
  } = useQuery({
    queryKey: ["plugins", configVersion],
    queryFn: api.listPlugins,
    retry: 2,
    staleTime: 0,
  });

  // Catalog query is reused by the installed tab so we can cross-reference
  // update_available per installed plugin and show a badge.
  const { data: catalogEntries = [] } = useQuery({
    queryKey: ["catalog-browse"],
    queryFn: api.browseCatalog,
    staleTime: 60_000,
    retry: 1,
  });

  // Map: plugin name → latest catalog version when update_available.
  const updateMap = useMemo(() => {
    const m = new Map<string, string>();
    for (const e of catalogEntries) {
      if (e.update_available) m.set(e.name, e.version);
    }
    return m;
  }, [catalogEntries]);

  const addToast = useCallback((message: string) => {
    const id = ++toastId;
    setToasts((prev) => [...prev, { id, message }]);
    setTimeout(() => {
      setToasts((prev) => prev.filter((t) => t.id !== id));
    }, 6000);
  }, []);

  const handlePostAction = useCallback(
    (actionLabel: string) => {
      addToast(`Plugin ${actionLabel}.`);
      bumpConfigVersion();
      void queryClient.invalidateQueries({ queryKey: ["plugins"] });
      void queryClient.invalidateQueries({ queryKey: ["agents"] });
      void queryClient.invalidateQueries({ queryKey: ["catalog-browse"] });
      void refetch();
      setPendingAction(null);
    },
    [addToast, bumpConfigVersion, queryClient, refetch],
  );

  const installMutation = useMutation({
    mutationFn: api.installPlugin,
    onMutate: (name) => setPendingAction(name),
    onSuccess: () => void handlePostAction("installed"),
    onError: (err: Error) => {
      addToast(`Failed to install: ${err.message}`);
      setPendingAction(null);
    },
  });

  const uninstallMutation = useMutation({
    mutationFn: api.uninstallPlugin,
    onMutate: (name) => setPendingAction(name),
    onSuccess: () => void handlePostAction("uninstalled"),
    onError: (err: Error) => {
      addToast(`Failed to uninstall: ${err.message}`);
      setPendingAction(null);
    },
  });

  const disableMutation = useMutation({
    mutationFn: api.disablePlugin,
    onMutate: (name) => setPendingAction(name),
    onSuccess: () => void handlePostAction("disabled"),
    onError: (err: Error) => {
      addToast(`Failed to disable: ${err.message}`);
      setPendingAction(null);
    },
  });

  const enableMutation = useMutation({
    mutationFn: api.enablePlugin,
    onMutate: (name) => setPendingAction(name),
    onSuccess: () => void handlePostAction("enabled"),
    onError: (err: Error) => {
      addToast(`Failed to enable: ${err.message}`);
      setPendingAction(null);
    },
  });

  const isActionPending = (name: string) => pendingAction === name;

  const handleUninstallConfirm = useCallback(() => {
    if (confirmUninstall) {
      uninstallMutation.mutate(confirmUninstall);
      setConfirmUninstall(null);
    }
  }, [confirmUninstall, uninstallMutation]);

  // Sort: active first, then disabled, then available
  const sortedPlugins = [...plugins].sort((a, b) => {
    const order: Record<string, number> = { active: 0, disabled: 1, available: 2, "no-binary": 3 };
    const diff = (order[a.status] ?? 4) - (order[b.status] ?? 4);
    if (diff !== 0) return diff;
    if (a.type === "core" && b.type !== "core") return -1;
    if (a.type !== "core" && b.type === "core") return 1;
    return a.name.localeCompare(b.name);
  });

  // --- Sub-views (detail, config, sources) ---

  if (subView === "sources") {
    return <CatalogSourceManager onBack={() => setSubView("list")} />;
  }

  if (detailPlugin) {
    return <PluginDetailView plugin={detailPlugin} onBack={() => setDetailPlugin(null)} />;
  }

  if (configuringPlugin) {
    return (
      <PluginConfigPanel
        pluginId={configuringPlugin.name}
        pluginName={configuringPlugin.name}
        onBack={() => setConfiguringPlugin(null)}
      />
    );
  }

  return (
    <div className="space-y-4">
      {/* Tab bar */}
      <div className="flex items-center gap-2">
        <div className="inline-flex items-center bg-surface rounded-lg p-0.5">
          <button
            onClick={() => setActiveTab("installed")}
            className={`px-3 py-1.5 text-xs font-medium rounded-md transition-all ${
              activeTab === "installed"
                ? "bg-bg-elevated text-fg shadow-sm"
                : "text-fg hover:text-fg"
            }`}
          >
            Installed
            {plugins.length > 0 && (
              <span className="ml-1.5 text-[10px] text-fg-secondary">
                {plugins.filter((p) => p.installed).length}
              </span>
            )}
          </button>
          <button
            onClick={() => setActiveTab("catalog")}
            className={`px-3 py-1.5 text-xs font-medium rounded-md transition-all flex items-center gap-1.5 ${
              activeTab === "catalog"
                ? "bg-bg-elevated text-fg shadow-sm"
                : "text-fg hover:text-fg"
            }`}
          >
            <Globe className="w-3 h-3" />
            Catalog
          </button>
        </div>
      </div>

      {/* Catalog tab */}
      {activeTab === "catalog" && (
        <CatalogBrowser
          onManageSources={() => setSubView("sources")}
          focusEntryName={catalogFocusEntry}
          onFocusHandled={() => setCatalogFocusEntry(null)}
        />
      )}

      {/* Installed tab */}
      {activeTab === "installed" && (
        <>
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
            <div className="rounded-xl border border-danger/30 bg-danger/5 p-4 flex items-start gap-3">
              <AlertCircle className="w-4 h-4 text-danger shrink-0 mt-0.5" />
              <div>
                <p className="text-sm font-medium text-fg">Failed to load plugins</p>
                <p className="text-xs text-fg-muted mt-1">
                  {(error as Error)?.message || "The plugin API may not be available yet."}
                </p>
              </div>
            </div>
          )}

          {/* Loading */}
          {isLoading && !isError && (
            <div className="grid grid-cols-2 gap-3">
              {Array.from({ length: 4 }).map((_, i) => (
                <div
                  key={i}
                  className="rounded-xl border border-border-subtle bg-bg-elevated shadow-sm overflow-hidden"
                >
                  <div className="px-3.5 py-3 flex items-center gap-2.5">
                    <Skeleton className="size-9 rounded-lg" />
                    <div className="flex flex-col gap-1.5 flex-1">
                      <Skeleton className="h-3.5 w-1/2" />
                      <Skeleton className="h-2.5 w-1/3" />
                    </div>
                  </div>
                  <div className="border-t border-border-subtle px-3.5 py-2">
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
                <EmptyMedia variant="icon">
                  <Package />
                </EmptyMedia>
                <EmptyTitle className="text-sm">No plugins found</EmptyTitle>
                <EmptyDescription className="text-xs">
                  Plugins will appear here once available.
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          )}

          {/* Plugin grid */}
          {!isLoading && !isError && sortedPlugins.length > 0 && (
            <div className="grid gap-3 grid-cols-2">
              {sortedPlugins.map((plugin) => {
                const isActive = plugin.status === "active";
                const isDisabled = plugin.status === "disabled";
                const isAvailable = plugin.status === "available";
                const accentColor = isActive
                  ? 'var(--color-status-ok)'
                  : isDisabled
                    ? 'var(--color-fg-faint)'
                    : 'var(--color-border)';
                return (
                  <ContextMenu key={plugin.name}>
                    <ContextMenuTrigger asChild>
                      <div
                        className={`rounded-xl border overflow-hidden transition-all cursor-pointer group border-l-2 ${
                          isActive
                            ? "border-border-subtle bg-bg-elevated hover:border-border"
                            : isDisabled
                              ? "border-border-subtle bg-bg-elevated opacity-60"
                              : "border-border bg-bg/30 opacity-45"
                        }`}
                        style={{ borderLeftColor: accentColor }}
                        onClick={() => {
                          if (isActive || isDisabled) setDetailPlugin(plugin);
                        }}
                      >
                        {/* Header */}
                        <div className="flex items-center gap-2.5 px-3.5 py-3">
                          <span
                            className={`inline-flex items-center justify-center w-9 h-9 rounded-lg shrink-0 transition-colors ${
                              isActive
                                ? "bg-surface text-fg-secondary group-hover:text-fg"
                                : "bg-surface text-fg-muted"
                            }`}
                          >
                            <Package className="w-4 h-4" />
                          </span>
                          <div className="flex-1 min-w-0">
                            <div className="flex items-center gap-2">
                              <span
                                className={`text-sm font-semibold truncate ${isActive ? "text-fg" : "text-fg-muted"}`}
                              >
                                {plugin.display_name || plugin.name}
                              </span>
                              {isActive && (
                                <span className="w-1.5 h-1.5 rounded-full bg-status-ok shrink-0" />
                              )}
                            </div>
                            <div className="flex items-center gap-1.5 mt-0.5">
                              <span className="text-[11px] text-fg-muted">
                                v{plugin.version || "0.0.0"}
                              </span>
                              {updateMap.has(plugin.name) && (
                                <button
                                  type="button"
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    setCatalogFocusEntry(plugin.name);
                                    setActiveTab("catalog");
                                  }}
                                  className="inline-flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded-md bg-amber-600/10 border border-amber-600/40 text-amber-600 leading-none hover:bg-amber-600/20 transition-colors"
                                  title={`Update available: v${updateMap.get(plugin.name)}`}
                                >
                                  <ArrowUpCircle className="w-2.5 h-2.5" />
                                  Update available
                                </button>
                              )}
                              {plugin.author && (
                                <>
                                  <span className="text-fg-faint text-[10px]">&middot;</span>
                                  <span className="text-[11px] text-fg-muted truncate">
                                    {plugin.author}
                                  </span>
                                </>
                              )}
                              {plugin.installed && plugin.trust_tier && (
                                <TrustTierBadge tier={plugin.trust_tier} />
                              )}
                            </div>
                          </div>
                          {/* Actions */}
                          {/* eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-static-element-interactions */}
                          <div
                            className="flex items-center gap-1 shrink-0"
                            onClick={(e) => e.stopPropagation()}
                          >
                            {plugin.type === "core" ? (
                              <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-bg-elevated border border-border-subtle text-fg-muted leading-none">
                                core
                              </span>
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
                                  {isActionPending(plugin.name) ? (
                                    <Loader2 className="w-3 h-3 animate-spin" />
                                  ) : (
                                    "Disable"
                                  )}
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
                                  {isActionPending(plugin.name) ? (
                                    <Loader2 className="w-3 h-3 animate-spin" />
                                  ) : (
                                    "Enable"
                                  )}
                                </button>
                              </>
                            ) : isAvailable ? (
                              <button
                                onClick={() => installMutation.mutate(plugin.name)}
                                disabled={isActionPending(plugin.name)}
                                className="px-2 py-1 text-[11px] font-medium text-brand-fg bg-brand hover:bg-brand-hover rounded-md transition-colors disabled:opacity-40"
                              >
                                {isActionPending(plugin.name) ? (
                                  <Loader2 className="w-3 h-3 animate-spin" />
                                ) : (
                                  "Install"
                                )}
                              </button>
                            ) : null}
                          </div>
                        </div>

                        {/* Detail footer */}
                        <div className="border-t border-border-subtle px-3.5 py-2 bg-bg/40">
                          <p className="text-[11px] text-fg-muted line-clamp-2">
                            {plugin.short_desc || plugin.description || "No description"}
                          </p>
                        </div>
                      </div>
                    </ContextMenuTrigger>
                    <ContextMenuContent>
                      {(isActive || isDisabled) && (
                        <ContextMenuItem
                          className="gap-2 text-xs"
                          onClick={() => setDetailPlugin(plugin)}
                        >
                          <Settings2 className="w-3.5 h-3.5" />
                          Details
                        </ContextMenuItem>
                      )}
                      {isActive && (
                        <ContextMenuItem
                          className="gap-2 text-xs"
                          onClick={() => disableMutation.mutate(plugin.name)}
                          disabled={isActionPending(plugin.name)}
                        >
                          <PowerOff className="w-3.5 h-3.5" />
                          Disable
                        </ContextMenuItem>
                      )}
                      {isDisabled && (
                        <ContextMenuItem
                          className="gap-2 text-xs"
                          onClick={() => enableMutation.mutate(plugin.name)}
                          disabled={isActionPending(plugin.name)}
                        >
                          <Power className="w-3.5 h-3.5" />
                          Enable
                        </ContextMenuItem>
                      )}
                      {plugin.type !== "core" && (isActive || isDisabled) && (
                        <ContextMenuItem
                          className="gap-2 text-xs text-danger focus:text-danger"
                          onClick={() => setConfirmUninstall(plugin.name)}
                          disabled={isActionPending(plugin.name)}
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                          Uninstall
                        </ContextMenuItem>
                      )}
                    </ContextMenuContent>
                  </ContextMenu>
                );
              })}
            </div>
          )}
        </>
      )}

      {/* Uninstall confirmation dialog */}
      <AlertDialog
        open={confirmUninstall !== null}
        onOpenChange={(open) => {
          if (!open) setConfirmUninstall(null);
        }}
      >
        <AlertDialogContent size="sm">
          <AlertDialogHeader>
            <AlertDialogTitle>Uninstall {confirmUninstall ?? "plugin"}?</AlertDialogTitle>
            <AlertDialogDescription>
              This removes the plugin from this workspace. You can reinstall from the catalog.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={() => setConfirmUninstall(null)}>Cancel</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={handleUninstallConfirm}>
              Uninstall
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

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
  );
}
