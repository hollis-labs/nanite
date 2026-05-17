import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bot, ChevronDown, Copy, GitFork, MoreHorizontal, RotateCcw, Sparkles, Users } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { SourceBadge } from "@/components/agents/SourceBadge";
import { Tooltip } from "@/components/ui/tooltip";
import { usePluginSlots } from "@/hooks/usePluginSlots";
import { api } from "@/lib/api";
import { resolveIcon } from "@/lib/icons";
import type { UISlotEntry } from "@/lib/types";
import { useAppStore } from "@/stores/useAppStore";
import { useActiveModel, useIsStreaming } from "@/stores/useChatStore";
import { useLayoutStore } from "@/stores/useLayoutStore";
import { AgentRoster } from "./AgentRoster";

function formatTokens(n: number): string {
  if (n >= 1000) return `${Math.round(n / 1000)}K`;
  return String(n);
}

export function ChatHeader() {
  const activeSessionId = useAppStore((s) => s.activeSessionId);
  const headerChipsVisible = useLayoutStore((s) => s.headerChipsVisible);
  const activeModel = useActiveModel();
  const isStreaming = useIsStreaming();
  const queryClient = useQueryClient();

  const [agentDropOpen, setAgentDropOpen] = useState(false);
  const [modeDropOpen, setModeDropOpen] = useState(false);
  const [moreOpen, setMoreOpen] = useState(false);
  const [rosterOpen, setRosterOpen] = useState(false);
  const agentDropRef = useRef<HTMLDivElement>(null);
  const modeDropRef = useRef<HTMLDivElement>(null);
  const moreRef = useRef<HTMLDivElement>(null);

  const { data: session } = useQuery({
    queryKey: ["session", activeSessionId],
    queryFn: () => api.getSession(activeSessionId!),
    enabled: !!activeSessionId,
  });
  const { data: sessionAgents = [] } = useQuery({
    queryKey: ["session-agents", activeSessionId],
    queryFn: () => api.listSessionAgents(activeSessionId!),
    enabled: !!activeSessionId,
  });
  const { data: tools = [] } = useQuery({
    queryKey: ["tools"],
    queryFn: api.fetchTools,
    staleTime: 60_000,
  });
  const { data: breakdown } = useQuery({
    queryKey: ["context-breakdown", activeSessionId],
    queryFn: () => api.getContextBreakdown(activeSessionId!),
    enabled: !!activeSessionId,
    refetchInterval: isStreaming ? 10_000 : 60_000,
    staleTime: 30_000,
  });

  const configVersion = useAppStore((s) => s.configVersion);
  const { data: allAgents = [] } = useQuery({
    queryKey: ["agents", configVersion],
    queryFn: api.listAgents,
  });

  // B1 (CW-20260428-0009): session-level Mode chip + dropdown.
  // Backed by sessions.current_mode_id → modes.id; null = "chat" default.
  const { data: allModes = [] } = useQuery({
    queryKey: ["modes"],
    queryFn: api.listModes,
    staleTime: 60_000,
  });
  const { data: sessionMode } = useQuery({
    queryKey: ["session-mode", activeSessionId],
    queryFn: () => api.getSessionMode(activeSessionId!),
    enabled: !!activeSessionId,
  });

  const primaryAgent = sessionAgents.find((a) => a.role === "primary");
  const primaryAgentProfile = allAgents.find((a) => a.id === primaryAgent?.agent_id);
  const activeAgentName = primaryAgentProfile?.name || "Nanite";
  const agentCount = sessionAgents.length;
  const toolCount = tools.length;
  const modelName = session?.model || activeModel || primaryAgentProfile?.default_model || null;
  const shortModel = modelName
    ? modelName
        .split("/")
        .pop()
        ?.replace(/-\d{8}$/, "")
    : null;

  // Context window
  const ctxTotal = breakdown?.total ?? 0;
  const ctxCeiling = breakdown?.ceiling ?? 0;
  const contextChip =
    ctxCeiling > 0 ? `${formatTokens(ctxTotal)} / ${formatTokens(ctxCeiling)}` : null;

  // Close dropdowns on outside click
  useEffect(() => {
    if (!agentDropOpen && !moreOpen && !modeDropOpen) return;
    function onDown(e: MouseEvent) {
      if (agentDropRef.current && !agentDropRef.current.contains(e.target as Node))
        setAgentDropOpen(false);
      if (modeDropRef.current && !modeDropRef.current.contains(e.target as Node))
        setModeDropOpen(false);
      if (moreRef.current && !moreRef.current.contains(e.target as Node)) setMoreOpen(false);
    }
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [agentDropOpen, moreOpen, modeDropOpen]);

  const setSessionModeMutation = useMutation({
    mutationFn: async (slug: string) => {
      if (!activeSessionId) return null;
      return api.setSessionMode(activeSessionId, { slug });
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["session-mode", activeSessionId] });
      void queryClient.invalidateQueries({ queryKey: ["session", activeSessionId] });
    },
  });

  const switchAgentMutation = useMutation({
    mutationFn: async (agentId: string) => {
      if (!activeSessionId) return;
      if (primaryAgent) {
        try {
          await api.removeSessionAgent(activeSessionId, primaryAgent.agent_id);
        } catch {
          /* ignore */
        }
      }
      return api.addSessionAgent(activeSessionId, agentId, "primary");
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["session-agents", activeSessionId] });
    },
  });

  const setActiveSession = useAppStore((s) => s.setActiveSession);
  const forkMutation = useMutation({
    mutationFn: (includeMessages: boolean) => {
      if (!activeSessionId) throw new Error("No active session");
      return api.forkSession(activeSessionId, { include_messages: includeMessages });
    },
    onSuccess: (newSession) => {
      void queryClient.invalidateQueries({ queryKey: ["sessions"] });
      setActiveSession(newSession.id);
      setMoreOpen(false);
    },
  });

  // CW-20260516-0057: reboot just this session's runtime agent so the next
  // turn cold-boots a fresh agent process + boot dir from the current
  // binary. The DB transcript is untouched. The menu stays open on a
  // rejected reboot ("busy"/"error") so the inline outcome is visible.
  const rebootMutation = useMutation({
    mutationFn: () => {
      if (!activeSessionId) throw new Error("No active session");
      return api.rebootSessionAgent(activeSessionId);
    },
    onSuccess: (result) => {
      if (result === "rebooted" || result === "no_active_agent") {
        setMoreOpen(false);
      }
    },
  });

  const pluginActions = usePluginSlots("chat-header-action");
  const handlePluginAction = useCallback(
    (entry: UISlotEntry) => {
      switch (entry.action) {
        case "command":
          if (activeSessionId && entry.props?.command) {
            void api.executeCommand(String(entry.props.command), activeSessionId, "");
          }
          break;
        case "navigate":
          if (entry.props?.hash) window.location.hash = String(entry.props.hash);
          break;
        case "handler":
          window.dispatchEvent(
            new CustomEvent("plugin-action", { detail: { id: entry.id, entry } }),
          );
          break;
        case "modal":
          window.dispatchEvent(
            new CustomEvent("plugin-modal", {
              detail: { id: entry.id, component: entry.component, props: entry.props },
            }),
          );
          break;
      }
      setMoreOpen(false);
    },
    [activeSessionId],
  );

  const metaParts: string[] = [];
  if (shortModel) metaParts.push(shortModel);
  if (toolCount > 0) metaParts.push(`${toolCount} tools`);

  return (
    <header className="flex h-[52px] shrink-0 border-b border-divider">
      <div className="max-w-3xl w-full mx-auto flex items-center justify-between px-[18px]">
        {/* ── Left ── */}
        <div className="flex items-center gap-3">
          {/* Agent info block */}
          <div className="flex items-center gap-2.5">
            {/* Brand chip */}
            <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-[7px] bg-brand font-mono text-[13px] font-bold leading-none text-brand-fg">
              {activeAgentName.charAt(0).toUpperCase()}
            </div>

            {/* Two-row block */}
            <div className="flex min-w-0 flex-col gap-[2px]">
              {/* Row 1: name picker + mode pill */}
              <div className="relative flex items-center gap-2" ref={agentDropRef}>
                <button
                  type="button"
                  onClick={() => setAgentDropOpen((o) => !o)}
                  className="flex items-center gap-1 rounded-[4px] px-0.5 transition-colors hover:bg-surface"
                >
                  <span className="text-[14px] font-semibold text-fg">{activeAgentName}</span>
                  <ChevronDown size={11} className="shrink-0 text-fg-muted" />
                </button>
                {/* Mode chip + dropdown (B1, CW-20260428-0009).
                  Default to "chat" when no session-mode pointer is set. */}
                <div className="relative" ref={modeDropRef}>
                  <button
                    type="button"
                    onClick={() => setModeDropOpen((o) => !o)}
                    disabled={!activeSessionId}
                    className="flex items-center gap-1 rounded-[6px] border border-border-subtle bg-bg-elevated px-1.5 py-0.5 text-[10px] font-mono uppercase tracking-wide text-fg-secondary transition-colors hover:bg-surface hover:text-fg disabled:opacity-50"
                  >
                    <Sparkles size={9} className="shrink-0 text-fg-muted" />
                    <span>{sessionMode?.slug ?? "chat"}</span>
                    <ChevronDown size={9} className="shrink-0 text-fg-muted" />
                  </button>
                  {modeDropOpen && (
                    <div className="absolute left-0 top-full z-50 mt-1 w-52 rounded-[10px] border border-border-subtle bg-bg-elevated py-1 shadow-2xl">
                      <div className="px-3 py-1.5 font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-muted">
                        Switch mode
                      </div>
                      {allModes.map((mode) => {
                        const isActive = sessionMode?.id === mode.id;
                        return (
                          <button
                            key={mode.id}
                            type="button"
                            onClick={() => {
                              setSessionModeMutation.mutate(mode.slug);
                              setModeDropOpen(false);
                            }}
                            disabled={setSessionModeMutation.isPending}
                            className={`flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs transition-colors ${
                              isActive
                                ? "bg-surface text-fg"
                                : "text-fg-secondary hover:bg-surface hover:text-fg"
                            }`}
                          >
                            <Sparkles className="h-3 w-3 shrink-0 text-fg-muted" />
                            <span className="truncate">{mode.name}</span>
                            <span className="ml-auto font-mono text-[10px] text-fg-muted">
                              {mode.slug}
                            </span>
                          </button>
                        );
                      })}
                    </div>
                  )}
                </div>
                {primaryAgentProfile?.source && <SourceBadge source={primaryAgentProfile.source} />}

                {/* Agent picker dropdown */}
                {agentDropOpen && (
                  <div className="absolute left-0 top-full z-50 mt-1 w-56 rounded-[10px] border border-border-subtle bg-bg-elevated py-1 shadow-2xl">
                    <div className="px-3 py-1.5 font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-muted">
                      Switch agent
                    </div>
                    {allAgents
                      .filter((a) => a.status !== "disabled")
                      .map((agent) => (
                        <button
                          key={agent.id}
                          type="button"
                          onClick={() => {
                            switchAgentMutation.mutate(agent.id);
                            setAgentDropOpen(false);
                          }}
                          disabled={switchAgentMutation.isPending}
                          className={`flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs transition-colors ${
                            primaryAgent?.agent_id === agent.id
                              ? "bg-surface text-fg"
                              : "text-fg-secondary hover:bg-surface hover:text-fg"
                          }`}
                        >
                          <Bot className="h-3 w-3 shrink-0 text-fg-muted" />
                          <span className="truncate">{agent.name}</span>
                          {agent.source && (
                            <SourceBadge source={agent.source} className="ml-auto" />
                          )}
                        </button>
                      ))}
                  </div>
                )}
              </div>

              {/* Row 2: mono meta — #code · model · N tools */}
              {headerChipsVisible && metaParts.length > 0 && (
                <div className="flex items-center gap-[10px] font-mono text-[11px] text-fg-muted">
                  {metaParts.map((part, i) => (
                    <span key={i} className="flex items-center gap-[10px]">
                      {i > 0 && <span className="opacity-40">·</span>}
                      {part}
                    </span>
                  ))}
                </div>
              )}
            </div>
          </div>
        </div>

        {/* ── Right ── */}
        <div className="flex items-center gap-1.5">
          {/* Context window chip */}
          {contextChip && (
            <div className="flex items-center gap-1.5 rounded-[6px] border border-border-subtle bg-bg-elevated px-2.5 py-1">
              <div className="h-1.5 w-1.5 rounded-full bg-success" />
              <span className="font-mono text-[11px] text-fg-secondary">{contextChip}</span>
            </div>
          )}

          {/* ··· More menu */}
          <div className="relative" ref={moreRef}>
            <Tooltip content="More options" side="bottom">
              <button
                type="button"
                onClick={() => setMoreOpen((o) => !o)}
                className="flex h-7 w-7 items-center justify-center rounded-[6px] text-fg-muted transition-colors hover:bg-surface hover:text-fg"
              >
                <MoreHorizontal size={15} />
              </button>
            </Tooltip>

            {moreOpen && (
              <div className="absolute right-0 top-full z-50 mt-1 w-52 rounded-[10px] border border-border-subtle bg-bg-elevated py-1 shadow-2xl">
                {agentCount > 1 && (
                  <>
                    <button
                      type="button"
                      onClick={() => {
                        setRosterOpen(true);
                        setMoreOpen(false);
                      }}
                      className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-fg-secondary transition-colors hover:bg-surface hover:text-fg"
                    >
                      <Users className="h-3 w-3 text-fg-muted" />
                      View agents ({agentCount})
                    </button>
                    <div className="my-1 h-px bg-divider" />
                  </>
                )}
                <div className="px-3 py-1 font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-muted">
                  Session
                </div>
                <button
                  type="button"
                  onClick={() => forkMutation.mutate(false)}
                  disabled={forkMutation.isPending || !activeSessionId}
                  className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-fg-secondary transition-colors hover:bg-surface hover:text-fg"
                >
                  <Copy className="h-3 w-3 text-fg-muted" />
                  Clone (empty)
                </button>
                <button
                  type="button"
                  onClick={() => forkMutation.mutate(true)}
                  disabled={forkMutation.isPending || !activeSessionId}
                  className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-fg-secondary transition-colors hover:bg-surface hover:text-fg"
                >
                  <GitFork className="h-3 w-3 text-fg-muted" />
                  Fork (with history)
                </button>
                <button
                  type="button"
                  onClick={() => rebootMutation.mutate()}
                  disabled={rebootMutation.isPending || !activeSessionId}
                  title="Stop this session's agent so the next turn boots a fresh one from the current binary"
                  className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-fg-secondary transition-colors hover:bg-surface hover:text-fg"
                >
                  <RotateCcw className="h-3 w-3 text-fg-muted" />
                  {rebootMutation.isPending ? "Rebooting agent…" : "Reboot agent"}
                </button>
                {(rebootMutation.data === "busy" || rebootMutation.data === "error") && (
                  <div className="px-3 pb-1 text-[10px] text-fg-muted">
                    {rebootMutation.data === "busy"
                      ? "A turn is in progress — try again when it finishes."
                      : "Reboot failed — check the service."}
                  </div>
                )}
                {pluginActions.length > 0 && (
                  <>
                    <div className="my-1 h-px bg-divider" />
                    <div className="px-3 py-1 font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-muted">
                      Actions
                    </div>
                    {pluginActions.map((entry) => {
                      const PluginIcon = resolveIcon(entry.icon);
                      return (
                        <button
                          key={entry.id}
                          type="button"
                          onClick={() => handlePluginAction(entry)}
                          className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-fg-secondary transition-colors hover:bg-surface hover:text-fg"
                        >
                          <PluginIcon className="h-3 w-3 text-fg-muted" />
                          {entry.label}
                        </button>
                      );
                    })}
                  </>
                )}
              </div>
            )}
          </div>
        </div>
      </div>

      {rosterOpen && activeSessionId && (
        <AgentRoster sessionId={activeSessionId} onClose={() => setRosterOpen(false)} />
      )}
    </header>
  );
}
