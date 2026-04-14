// TODO(g5-followup): derive tier from a real CatalogEntry.Tier field once backend adds it.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertCircle,
  ArrowUpCircle,
  CheckCircle2,
  Download,
  Globe,
  Loader2,
  Package,
  RefreshCw,
  Search,
  Settings2,
  Tag,
} from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import { api } from "@/lib/api";
import type { CatalogBrowseEntry } from "@/lib/types";
import { PluginInstallProgress } from "./PluginInstallProgress";

type Tier = "all" | "core" | "default" | "available";

function deriveTier(entry: CatalogBrowseEntry): Exclude<Tier, "all"> {
  const tags = entry.tags ?? [];
  if (tags.includes("core")) return "core";
  if (tags.includes("default")) return "default";
  return "available";
}

interface Toast {
  id: number;
  message: string;
  variant?: "default" | "success" | "error";
}

let toastId = 0;

interface CatalogBrowserProps {
  onManageSources: () => void;
  /** When set, scrolls the matching catalog row into view after render. */
  focusEntryName?: string | null;
  onFocusHandled?: () => void;
}

export function CatalogBrowser({
  onManageSources,
  focusEntryName,
  onFocusHandled,
}: CatalogBrowserProps) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [search, setSearch] = useState("");
  const [tagFilter, setTagFilter] = useState<string | null>(null);
  const [tierFilter, setTierFilter] = useState<Tier>("all");
  const [installingName, setInstallingName] = useState<string | null>(null);
  const rowRefs = useRef<Map<string, HTMLDivElement | null>>(new Map());
  const queryClient = useQueryClient();

  const {
    data: entries = [],
    isLoading,
    isError,
    error,
    refetch,
  } = useQuery({
    queryKey: ["catalog-browse"],
    queryFn: api.browseCatalog,
    staleTime: 60_000,
  });

  const addToast = useCallback((message: string, variant: Toast["variant"] = "default") => {
    const id = ++toastId;
    setToasts((prev) => [...prev, { id, message, variant }]);
    setTimeout(() => setToasts((prev) => prev.filter((t) => t.id !== id)), 6000);
  }, []);

  const refreshMutation = useMutation({
    mutationFn: api.refreshCatalog,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["catalog-browse"] });
      void refetch();
    },
  });

  const installMutation = useMutation({
    mutationFn: api.catalogInstall,
    onMutate: (name) => setInstallingName(name),
    onSuccess: (data) => {
      addToast(data.message || "Plugin installed.", "success");
      setInstallingName(null);
      void queryClient.invalidateQueries({ queryKey: ["catalog-browse"] });
      void queryClient.invalidateQueries({ queryKey: ["plugins"] });
      void queryClient.invalidateQueries({ queryKey: ["plugins-for-esm"] });
    },
    onError: (err: Error) => {
      addToast(`Install failed: ${err.message}`, "error");
      setInstallingName(null);
    },
  });

  // Collect all unique tags for filtering.
  const allTags = Array.from(new Set(entries.flatMap((e) => e.tags ?? []))).sort();

  // Filter entries.
  const filtered = entries.filter((entry) => {
    if (search) {
      const q = search.toLowerCase();
      const match =
        entry.name.toLowerCase().includes(q) ||
        entry.description?.toLowerCase().includes(q) ||
        entry.author?.toLowerCase().includes(q) ||
        entry.tags?.some((t) => t.toLowerCase().includes(q));
      if (!match) return false;
    }
    if (tagFilter && !entry.tags?.includes(tagFilter)) return false;
    if (tierFilter !== "all" && deriveTier(entry) !== tierFilter) return false;
    return true;
  });

  // Sort: not-installed first, then updates available, then installed.
  const sorted = [...filtered].sort((a, b) => {
    if (a.installed !== b.installed) return a.installed ? 1 : -1;
    if (a.update_available !== b.update_available) return a.update_available ? -1 : 1;
    return a.name.localeCompare(b.name);
  });

  // Scroll into view when the parent tab switch requests a focus target.
  // biome-ignore lint/correctness/useExhaustiveDependencies: re-run after sorted list renders so row refs exist
  useEffect(() => {
    if (!focusEntryName) return;
    const el = rowRefs.current.get(focusEntryName);
    if (el) {
      el.scrollIntoView({ block: "center", behavior: "smooth" });
      onFocusHandled?.();
    }
  }, [focusEntryName, sorted, onFocusHandled]);

  const tiers: { id: Tier; label: string }[] = [
    { id: "all", label: "All" },
    { id: "core", label: "Core" },
    { id: "default", label: "Default" },
    { id: "available", label: "Available" },
  ];

  return (
    <div className="space-y-4">
      {/* Tier filter row */}
      <div className="flex items-center gap-1">
        {tiers.map((t) => {
          const active = tierFilter === t.id;
          return (
            <button
              key={t.id}
              type="button"
              onClick={() => setTierFilter(t.id)}
              className={`text-[11px] px-2 py-1 rounded-md border leading-none transition-colors ${
                active
                  ? "bg-bg-elevated text-fg border-border-subtle shadow-sm"
                  : "bg-transparent text-fg-muted border-transparent hover:bg-surface/50"
              }`}
            >
              {t.label}
            </button>
          );
        })}
      </div>

      {/* Toolbar */}
      <div className="flex items-center gap-3">
        <div className="relative">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-fg-faint" />
          <input
            type="text"
            placeholder="Search catalog..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="w-48 bg-surface/50 border border-border rounded-md pl-8 pr-3 py-1.5 text-xs text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-border-subtle"
          />
        </div>

        {allTags.length > 0 && (
          <div className="flex items-center gap-1">
            <button
              onClick={() => setTagFilter(null)}
              className={`text-[10px] px-1.5 py-0.5 rounded-md border leading-none transition-colors ${
                tagFilter === null
                  ? "bg-bg-elevated text-fg border-border-subtle shadow-sm"
                  : "bg-transparent text-fg-muted border-transparent hover:bg-surface/50"
              }`}
            >
              All
            </button>
            {allTags.slice(0, 6).map((tag) => (
              <button
                key={tag}
                onClick={() => setTagFilter(tagFilter === tag ? null : tag)}
                className={`text-[10px] px-1.5 py-0.5 rounded-md border leading-none transition-colors ${
                  tagFilter === tag
                    ? "bg-bg-elevated text-fg border-border-subtle shadow-sm"
                    : "bg-transparent text-fg-muted border-transparent hover:bg-surface/50"
                }`}
              >
                {tag}
              </button>
            ))}
          </div>
        )}

        <div className="flex-1" />

        <Button
          variant="ghost"
          size="sm"
          className="text-xs text-fg-secondary hover:text-fg"
          onClick={() => onManageSources()}
        >
          <Settings2 className="w-3.5 h-3.5 mr-1" />
          Sources
        </Button>

        <Button
          variant="ghost"
          size="sm"
          className="text-xs text-fg-secondary hover:text-fg"
          onClick={() => refreshMutation.mutate()}
          disabled={refreshMutation.isPending || isLoading}
        >
          <RefreshCw
            className={`w-3.5 h-3.5 mr-1 ${refreshMutation.isPending ? "animate-spin" : ""}`}
          />
          Refresh
        </Button>
      </div>

      {/* Error state */}
      {isError && (
        <div className="rounded-xl border border-danger/30 bg-danger/5 p-4 flex items-start gap-3">
          <AlertCircle className="w-4 h-4 text-danger shrink-0 mt-0.5" />
          <div>
            <p className="text-sm font-medium text-fg">Failed to load catalog</p>
            <p className="text-xs text-fg-muted mt-1">
              {(error as Error)?.message || "Check that catalog sources are configured."}
            </p>
          </div>
        </div>
      )}

      {/* Loading */}
      {isLoading && !isError && (
        <div className="grid grid-cols-2 gap-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <div
              key={i}
              className="rounded-xl border border-border-subtle bg-bg-elevated/60 shadow-sm overflow-hidden"
            >
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
      {!isLoading && !isError && sorted.length === 0 && (
        <Empty className="py-12">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <Globe />
            </EmptyMedia>
            <EmptyTitle className="text-sm">
              {search || tagFilter ? "No matching plugins" : "Catalog is empty"}
            </EmptyTitle>
            <EmptyDescription className="text-xs">
              {search || tagFilter
                ? "Try adjusting your search or filter."
                : "Add a catalog source to browse available plugins."}
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      )}

      {/* Catalog grid */}
      {!isLoading && !isError && sorted.length > 0 && (
        <div className="grid gap-3 grid-cols-2">
          {sorted.map((entry) => (
            <CatalogEntryCard
              key={`${entry.source_id}-${entry.name}`}
              entry={entry}
              installing={installingName === entry.name}
              onInstall={() => installMutation.mutate(entry.name)}
              rowRef={(el) => {
                if (el) rowRefs.current.set(entry.name, el);
                else rowRefs.current.delete(entry.name);
              }}
              highlight={focusEntryName === entry.name}
            />
          ))}
        </div>
      )}

      {/* Install progress — dormant until backend bridges install.Installer
          events onto /api/plugins/events. Falls back to a spinner otherwise. */}
      {installingName && (
        <PluginInstallProgress
          pluginId={installingName}
          pending={installMutation.isPending}
          onClose={() => setInstallingName(null)}
        />
      )}

      {/* Toast container */}
      {toasts.length > 0 && (
        <div className="fixed bottom-4 right-4 z-50 flex flex-col gap-2">
          {toasts.map((toast) => (
            <div
              key={toast.id}
              className={`px-4 py-3 border rounded-lg shadow-xl text-sm max-w-sm animate-in fade-in slide-in-from-bottom-2 ${
                toast.variant === "error"
                  ? "bg-danger/20 border-danger/30 text-danger"
                  : toast.variant === "success"
                    ? "bg-success/5 border-success/30 text-fg"
                    : "bg-surface border-border-subtle text-fg"
              }`}
            >
              {toast.message}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// --- Entry Card ---

function CatalogEntryCard({
  entry,
  installing,
  onInstall,
  rowRef,
  highlight,
}: {
  entry: CatalogBrowseEntry;
  installing: boolean;
  onInstall: () => void;
  rowRef?: (el: HTMLDivElement | null) => void;
  highlight?: boolean;
}) {
  const isInstalled = entry.installed;
  const hasUpdate = entry.update_available;

  return (
    <div
      ref={rowRef}
      className={`rounded-xl border shadow-sm overflow-hidden transition-all ${
        highlight ? "ring-2 ring-amber-500/60 " : ""
      }${
        isInstalled
          ? "border-border-subtle bg-white dark:bg-bg-elevated/60 opacity-70"
          : "border-border-subtle bg-white dark:bg-bg-elevated/60"
      }`}
    >
      {/* Header */}
      <div className="flex items-center gap-2.5 px-3.5 py-3">
        <span className="inline-flex items-center justify-center w-9 h-9 rounded-lg shrink-0 bg-surface-hover text-fg-secondary">
          <Package className="w-4 h-4" />
        </span>
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-sm font-semibold text-fg truncate">{entry.name}</span>
            {isInstalled && !hasUpdate && (
              <CheckCircle2 className="w-3.5 h-3.5 text-success shrink-0" />
            )}
            {hasUpdate && <ArrowUpCircle className="w-3.5 h-3.5 text-warning shrink-0" />}
          </div>
          <div className="flex items-center gap-1.5 mt-0.5">
            <span className="text-[11px] text-fg-muted">v{entry.version}</span>
            {entry.author && (
              <>
                <span className="text-fg-faint text-[10px]">&middot;</span>
                <span className="text-[11px] text-fg-muted truncate">{entry.author}</span>
              </>
            )}
            <span className="text-fg-faint text-[10px]">&middot;</span>
            <span className="text-[10px] text-fg-faint truncate">{entry.source_name}</span>
          </div>
        </div>

        {/* Actions */}
        <div className="flex items-center gap-1 shrink-0">
          {isInstalled && !hasUpdate && (
            <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-bg-elevated border border-border-subtle text-fg-muted leading-none">
              installed
            </span>
          )}
          {hasUpdate && (
            <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-warning/10 border border-warning/30 text-warning leading-none">
              v{entry.installed_version} &rarr; v{entry.version}
            </span>
          )}
          {!isInstalled && (
            <button
              onClick={onInstall}
              disabled={installing}
              className="flex items-center gap-1 px-2 py-1 text-[11px] font-medium text-white bg-primary hover:bg-primary-hover rounded-md transition-colors disabled:opacity-40"
            >
              {installing ? (
                <Loader2 className="w-3 h-3 animate-spin" />
              ) : (
                <Download className="w-3 h-3" />
              )}
              Install
            </button>
          )}
        </div>
      </div>

      {/* Detail footer */}
      <div className="border-t border-border/50 px-3.5 py-2 bg-bg-elevated/40">
        <p className="text-[11px] text-fg-muted line-clamp-2">
          {entry.description || "No description"}
        </p>
        {entry.tags && entry.tags.length > 0 && (
          <div className="flex items-center gap-1 mt-1.5">
            <Tag className="w-2.5 h-2.5 text-fg-faint shrink-0" />
            {entry.tags.map((tag) => (
              <span
                key={tag}
                className="text-[9px] px-1 py-0.5 rounded bg-surface/60 text-fg-faint leading-none"
              >
                {tag}
              </span>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
