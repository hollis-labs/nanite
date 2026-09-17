import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Bot,
  ChevronDown,
  Copy,
  GitFork,
  Info,
  MoreHorizontal,
  RotateCcw,
  Users,
} from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { SourceBadge } from "@/components/agents/SourceBadge";
import { StartSurfaceDialog } from "@/components/sidebar/StartSurfaceDialog";
import { Tooltip } from "@/components/ui/tooltip";
import { usePluginSlots } from "@/hooks/usePluginSlots";
import { api } from "@/lib/api";
import { resolveIcon } from "@/lib/icons";
import type {
  ForkSessionRequest,
  SessionDetailsResponse,
  StartSurfacePrefill,
  UISlotEntry,
} from "@/lib/types";
import { useAppStore } from "@/stores/useAppStore";
import { useActiveModel, useIsStreaming } from "@/stores/useChatStore";
import { useLayoutStore } from "@/stores/useLayoutStore";
import { AgentRoster } from "./AgentRoster";
import { SessionDetailsPanel } from "./SessionDetailsPanel";

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
  const [moreOpen, setMoreOpen] = useState(false);
  const [rosterOpen, setRosterOpen] = useState(false);
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [startOpen, setStartOpen] = useState(false);
  const [startPrefill, setStartPrefill] = useState<StartSurfacePrefill | null>(null);
  // Transient confirmation surface for actions whose trigger UI closes on
  // success (e.g. Recover session). Lives outside the menu/panel so it stays
  // visible regardless of where the action was invoked from. Auto-dismisses.
  const [transientNote, setTransientNote] = useState<
    { kind: "info" | "warn" | "error"; text: string } | null
  >(null);
  const transientNoteTimerRef = useRef<number | null>(null);
  const showTransientNote = useCallback(
    (note: { kind: "info" | "warn" | "error"; text: string }, ttlMs = 5000) => {
      if (transientNoteTimerRef.current !== null) {
        window.clearTimeout(transientNoteTimerRef.current);
      }
      setTransientNote(note);
      transientNoteTimerRef.current = window.setTimeout(() => {
        setTransientNote(null);
        transientNoteTimerRef.current = null;
      }, ttlMs);
    },
    [],
  );
  useEffect(
    () => () => {
      if (transientNoteTimerRef.current !== null) {
        window.clearTimeout(transientNoteTimerRef.current);
      }
    },
    [],
  );
  const agentDropRef = useRef<HTMLDivElement>(null);
  const moreRef = useRef<HTMLDivElement>(null);

  const { data: session } = useQuery({
    queryKey: ["session", activeSessionId],
    queryFn: () => {
      if (!activeSessionId) throw new Error("active session is required");
      return api.getSession(activeSessionId);
    },
    enabled: !!activeSessionId,
  });
  const { data: sessionAgents = [] } = useQuery({
    queryKey: ["session-agents", activeSessionId],
    queryFn: () => {
      if (!activeSessionId) throw new Error("active session is required");
      return api.listSessionAgents(activeSessionId);
    },
    enabled: !!activeSessionId,
  });
  const { data: tools = [] } = useQuery({
    queryKey: ["tools"],
    queryFn: api.fetchTools,
    staleTime: 60_000,
  });
  const { data: breakdown } = useQuery({
    queryKey: ["context-breakdown", activeSessionId],
    queryFn: () => {
      if (!activeSessionId) throw new Error("active session is required");
      return api.getContextBreakdown(activeSessionId);
    },
    enabled: !!activeSessionId,
    refetchInterval: isStreaming ? 10_000 : 60_000,
    staleTime: 30_000,
  });

  const configVersion = useAppStore((s) => s.configVersion);
  const { data: allAgents = [] } = useQuery({
    queryKey: ["agents", configVersion],
    queryFn: () => api.listAgents(),
  });

  const primaryAgent = sessionAgents.find((a) => a.is_primary);
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
    if (!agentDropOpen && !moreOpen) return;
    function onDown(e: MouseEvent) {
      if (agentDropRef.current && !agentDropRef.current.contains(e.target as Node))
        setAgentDropOpen(false);
      if (moreRef.current && !moreRef.current.contains(e.target as Node)) setMoreOpen(false);
    }
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [agentDropOpen, moreOpen]);

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
    mutationFn: (request: ForkSessionRequest) => {
      if (!activeSessionId) throw new Error("No active session");
      return api.forkSession(activeSessionId, request);
    },
    onSuccess: (newSession) => {
      void queryClient.invalidateQueries({ queryKey: ["sessions"] });
      void queryClient.invalidateQueries({ queryKey: ["session", newSession.id] });
      setActiveSession(newSession.id);
      setMoreOpen(false);
      setDetailsOpen(false);
    },
  });

  const forkSession = useCallback(
    (details: SessionDetailsResponse, includeMessages: boolean) => {
      forkMutation.mutate({
        include_messages: includeMessages,
        provider: details.session.provider || undefined,
        model: details.session.model || undefined,
      });
    },
    [forkMutation],
  );

  const openStartFromDetails = useCallback((details: SessionDetailsResponse) => {
    const durableID = details.current_durable_agent?.id || "";
    setStartPrefill({
      path: durableID ? "durable" : "chat",
      provider: details.session.provider || undefined,
      model: details.session.model || undefined,
      agent_id: details.primary_agent?.id || undefined,
      durable_agent_id: durableID || undefined,
    });
    setDetailsOpen(false);
    setMoreOpen(false);
    setStartOpen(true);
  }, []);

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

  // CW-20260525-0001 Slice 2: recover this session — evict the runtime so the
  // next turn cold-boots WITH recovery (recovery pack + provider resume),
  // resuming prior context. Distinct from Reboot (which boots fresh).
  const recoverMutation = useMutation({
    mutationFn: () => {
      if (!activeSessionId) throw new Error("No active session");
      return api.recoverSession(activeSessionId);
    },
    onSuccess: (result) => {
      if (result === "recovered") {
        setMoreOpen(false);
        setDetailsOpen(false);
        showTransientNote({
          kind: "info",
          text: "Recovery armed — your next message will resume prior context.",
        });
      } else if (result === "busy") {
        showTransientNote({
          kind: "warn",
          text: "A turn is in progress — try Recover again when it finishes.",
        });
      } else {
        showTransientNote({
          kind: "error",
          text: "Recover failed — check the service.",
        });
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
    <>
    {transientNote && (
      <div
        role="status"
        aria-live="polite"
        className={`fixed top-[60px] left-1/2 -translate-x-1/2 z-50 max-w-[480px] rounded-md border px-3 py-1.5 text-[11px] shadow-md transition-opacity ${
          transientNote.kind === "info"
            ? "border-divider bg-surface text-fg"
            : transientNote.kind === "warn"
              ? "border-warning bg-surface text-warning"
              : "border-danger bg-surface text-danger"
        }`}
      >
        {transientNote.text}
      </div>
    )}
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
              {/* Row 1: name picker */}
              <div className="relative flex items-center gap-2" ref={agentDropRef}>
                <button
                  type="button"
                  onClick={() => setAgentDropOpen((o) => !o)}
                  className="flex items-center gap-1 rounded-[4px] px-0.5 transition-colors hover:bg-surface"
                >
                  <span className="text-[14px] font-semibold text-fg">{activeAgentName}</span>
                  <ChevronDown size={11} className="shrink-0 text-fg-muted" />
                </button>
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
                    <span key={part} className="flex items-center gap-[10px]">
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
                aria-label="More options"
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
                  onClick={() => {
                    setDetailsOpen(true);
                    setMoreOpen(false);
                  }}
                  disabled={!activeSessionId}
                  className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-fg-secondary transition-colors hover:bg-surface hover:text-fg disabled:opacity-50"
                >
                  <Info className="h-3 w-3 text-fg-muted" />
                  Session details
                </button>
                <button
                  type="button"
                  onClick={() =>
                    forkMutation.mutate({
                      include_messages: false,
                      provider: session?.provider || undefined,
                      model: session?.model || undefined,
                    })
                  }
                  disabled={forkMutation.isPending || !activeSessionId}
                  className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-fg-secondary transition-colors hover:bg-surface hover:text-fg"
                >
                  <RotateCcw className="h-3 w-3 text-fg-muted" />
                  Restart as new session
                </button>
                <button
                  type="button"
                  onClick={() =>
                    forkMutation.mutate({
                      include_messages: true,
                      provider: session?.provider || undefined,
                      model: session?.model || undefined,
                    })
                  }
                  disabled={forkMutation.isPending || !activeSessionId}
                  className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-fg-secondary transition-colors hover:bg-surface hover:text-fg"
                >
                  <GitFork className="h-3 w-3 text-fg-muted" />
                  Fork (with history)
                </button>
                <button
                  type="button"
                  onClick={() => {
                    setStartPrefill({
                      path: "chat",
                      provider: session?.provider || undefined,
                      model: session?.model || undefined,
                    });
                    setStartOpen(true);
                    setMoreOpen(false);
                  }}
                  disabled={!activeSessionId}
                  className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-fg-secondary transition-colors hover:bg-surface hover:text-fg"
                >
                  <Copy className="h-3 w-3 text-fg-muted" />
                  Open Start from here
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
                <button
                  type="button"
                  onClick={() => recoverMutation.mutate()}
                  disabled={recoverMutation.isPending || !activeSessionId}
                  title="Resume this session after a restart — the next message reloads prior context (recovery pack + provider resume)"
                  className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-fg-secondary transition-colors hover:bg-surface hover:text-fg"
                >
                  <RotateCcw className="h-3 w-3 text-fg-muted" />
                  {recoverMutation.isPending ? "Recovering…" : "Recover session"}
                </button>
                {/* Success/busy/error feedback for Recover is surfaced via the
                    transient note at the top of the header (rendered outside
                    this dropdown), so it stays visible after the menu auto-
                    closes on success. */}
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
      <SessionDetailsPanel
        sessionId={activeSessionId}
        open={detailsOpen}
        onOpenChange={setDetailsOpen}
        onFork={forkSession}
        onRestart={(details) => forkSession(details, false)}
        onRecover={() => recoverMutation.mutate()}
        onOpenStartFromHere={openStartFromDetails}
      />
      <StartSurfaceDialog
        open={startOpen}
        onOpenChange={setStartOpen}
        projectId={session?.project_id || null}
        defaultProvider={session?.provider}
        defaultModel={session?.model}
        prefill={startPrefill}
        onSessionStarted={setActiveSession}
      />
    </header>
    </>
  );
}
