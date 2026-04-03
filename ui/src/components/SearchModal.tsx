import { useQuery } from "@tanstack/react-query";
import { FileText, Hash } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command";
import { api } from "@/lib/api";
import type { SearchResult } from "@/lib/types";
import { useAppStore } from "@/stores/useAppStore";
import { useLayoutStore } from "@/stores/useLayoutStore";

interface SearchModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

function formatDate(dateStr: string): string {
  const d = new Date(dateStr);
  const now = new Date();
  const diff = now.getTime() - d.getTime();
  if (diff < 60_000) return "now";
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`;
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`;
  if (diff < 604_800_000) return `${Math.floor(diff / 86_400_000)}d ago`;
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

export function SearchModal({ open, onOpenChange }: SearchModalProps) {
  const [scope, setScope] = useState<"all" | "workspace" | "project">("workspace");
  const [query, setQuery] = useState("");
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId);
  const activeProjectId = useAppStore((s) => s.activeProjectId);
  const setActiveSession = useAppStore((s) => s.setActiveSession);
  const setCurrentPage = useLayoutStore((s) => s.setCurrentPage);

  // Reset query when modal opens
  useEffect(() => {
    if (open) setQuery("");
  }, [open]);

  const { data: sessions = [] } = useQuery({
    queryKey: ["sessions", activeWorkspaceId],
    queryFn: () => api.listSessions(activeWorkspaceId ?? undefined),
    enabled: open,
  });

  const { data: projects = [] } = useQuery({
    queryKey: ["projects", activeWorkspaceId],
    queryFn: () => api.listProjects(activeWorkspaceId!),
    enabled: open && !!activeWorkspaceId,
  });

  // Backend message search (debounced via queryKey changes)
  const searchProjectId = scope === "project" ? (activeProjectId ?? undefined) : undefined;
  const { data: searchResults = [], isFetching: isSearching } = useQuery({
    queryKey: ["search", query, activeWorkspaceId, searchProjectId],
    queryFn: () => api.searchMessages(query, activeWorkspaceId!, searchProjectId),
    enabled: open && !!activeWorkspaceId && query.length >= 2,
    staleTime: 30_000,
  });

  // Session-level filtering (for short queries or browsing)
  const filteredSessions = useMemo(() => {
    let list = sessions;
    if (scope === "project" && activeProjectId) {
      list = list.filter((s) => s.project_id === activeProjectId);
    }
    if (query.length > 0 && query.length < 2) {
      // Filter sessions by title/short_code for single-char queries
      const q = query.toLowerCase();
      list = list.filter(
        (s) => s.title?.toLowerCase().includes(q) || s.short_code?.toLowerCase().includes(q),
      );
    }
    return [...list]
      .sort((a, b) => new Date(b.last_activity).getTime() - new Date(a.last_activity).getTime())
      .slice(0, 20);
  }, [sessions, scope, activeProjectId, query]);

  const activeProjectName = useMemo(() => {
    if (!activeProjectId) return null;
    return projects.find((p) => p.id === activeProjectId)?.name ?? null;
  }, [activeProjectId, projects]);

  const selectSession = useCallback(
    (sessionId: string) => {
      setActiveSession(sessionId);
      setCurrentPage("chat");
      window.location.hash = "#chat";
      onOpenChange(false);
    },
    [setActiveSession, setCurrentPage, onOpenChange],
  );

  const selectMessage = useCallback(
    (result: SearchResult) => {
      setActiveSession(result.session_id);
      setCurrentPage("chat");
      window.location.hash = "#chat";
      onOpenChange(false);
      // TODO: scroll to specific message via jumpToMessage when wired
    },
    [setActiveSession, setCurrentPage, onOpenChange],
  );

  const showMessageResults = query.length >= 2;

  return (
    <CommandDialog
      open={open}
      onOpenChange={onOpenChange}
      title="Search Chats"
      description="Find chat sessions and messages"
      showCloseButton={false}
    >
      <CommandInput
        placeholder="Search chats and messages..."
        value={query}
        onValueChange={setQuery}
      />

      {/* Scope filters */}
      <div className="flex items-center gap-1 px-3 py-1.5 border-b border-border-subtle">
        <ScopeButton active={scope === "all"} onClick={() => setScope("all")}>
          All
        </ScopeButton>
        <ScopeButton active={scope === "workspace"} onClick={() => setScope("workspace")}>
          Workspace
        </ScopeButton>
        {activeProjectId && (
          <ScopeButton active={scope === "project"} onClick={() => setScope("project")}>
            {activeProjectName || "Project"}
          </ScopeButton>
        )}
        {isSearching && (
          <span className="ml-auto text-[10px] text-fg-faint animate-pulse">Searching...</span>
        )}
      </div>

      <CommandList className="max-h-[400px]">
        <CommandEmpty className="text-fg-muted">
          {query.length >= 2
            ? "No results found."
            : "Type at least 2 characters to search messages."}
        </CommandEmpty>

        {/* Message-level search results */}
        {showMessageResults && searchResults.length > 0 && (
          <CommandGroup
            heading={`${searchResults.length} message${searchResults.length === 1 ? "" : "s"}`}
          >
            {searchResults.map((result) => (
              <CommandItem
                key={`${result.session_id}-${result.message_id}`}
                value={`msg-${result.message_id}-${result.snippet}`}
                onSelect={() => selectMessage(result)}
              >
                <FileText className="text-fg-faint shrink-0" />
                <div className="flex flex-col min-w-0 flex-1">
                  <span className="text-xs text-fg-muted flex items-center gap-1.5">
                    <Hash className="size-2.5" />
                    {result.session_short_code}
                    <span className="mx-0.5">·</span>
                    {result.session_title || "Untitled"}
                    <span className="mx-0.5">·</span>
                    <span className="capitalize">{result.role}</span>
                  </span>
                  <span className="truncate text-sm mt-0.5">{result.snippet}</span>
                </div>
                <span className="text-xs text-fg-faint shrink-0">
                  {formatDate(result.created_at)}
                </span>
              </CommandItem>
            ))}
          </CommandGroup>
        )}

        {/* Separator between message results and sessions */}
        {showMessageResults && searchResults.length > 0 && filteredSessions.length > 0 && (
          <CommandSeparator />
        )}

        {/* Session-level results (always shown) */}
        {filteredSessions.length > 0 && (
          <CommandGroup
            heading={
              showMessageResults
                ? "Sessions"
                : `${filteredSessions.length} chat${filteredSessions.length === 1 ? "" : "s"}`
            }
          >
            {filteredSessions.map((session) => (
              <CommandItem key={session.id} onSelect={() => selectSession(session.id)} className="items-start gap-1.5 py-1">
                <div className="w-2.5 h-2.5 rounded-[2px] bg-fg-faint/40 border border-fg-faint/60 shrink-0 mt-[3.5px]" />
                <div className="flex flex-col min-w-0 flex-1">
                  <span className="truncate text-[13px] font-medium leading-snug">{session.title || "Untitled"}</span>
                  <span className="text-[11px] text-fg-faint font-mono leading-none mt-px">
                    #{session.short_code}
                    <span className="mx-1 font-sans">·</span>
                    <span className="font-sans">{session.message_count} msgs</span>
                  </span>
                </div>
                <span className="text-[11px] text-fg-faint shrink-0 mt-[1.5px]">
                  {formatDate(session.last_activity)}
                </span>
              </CommandItem>
            ))}
          </CommandGroup>
        )}
      </CommandList>
    </CommandDialog>
  );
}

function ScopeButton({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`px-2 py-0.5 rounded text-xs font-medium transition-colors ${
        active ? "bg-surface text-fg" : "text-fg-muted hover:text-fg-secondary"
      }`}
    >
      {children}
    </button>
  );
}
