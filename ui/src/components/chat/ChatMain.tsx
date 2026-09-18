import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Key, MessageSquare, Puzzle, Settings, User } from "lucide-react";
import { Activity, type ReactNode, useState } from "react";
import { ChatPrimaryDrawer } from "@/components/drawers/ChatPrimaryDrawer";
import { ChatWorkingDrawer } from "@/components/drawers/ChatWorkingDrawer";
import { TaskThreadPanel } from "@/components/messaging/TaskThreadPanel";
import { useChat } from "@/hooks/useChat";
import { useSettings } from "@/hooks/useSettings";
import { useTaskContext } from "@/hooks/useTaskContext";
import { api } from "@/lib/api";
import { useAppStore } from "@/stores/useAppStore";
import { useLayoutStore } from "@/stores/useLayoutStore";
import { ChatComposer } from "./ChatComposer";
import { ChatHeader } from "./ChatHeader";
import { ChatTranscript } from "./ChatTranscript";
import { SetupWizard } from "./SetupWizard";

interface ChatMainProps {
  active?: boolean;
  onEditorReady?: (focus: () => void) => void;
}

export function ChatMain({ active = true, onEditorReady }: ChatMainProps) {
  const activeSessionId = useAppStore((s) => s.activeSessionId);
  const taskThreadOpen = useLayoutStore((s) => s.taskThreadOpen);
  const toggleTaskThread = useLayoutStore((s) => s.toggleTaskThread);
  const {
    messages,
    messagesReady,
    oldestOffset,
    isStreaming,
    streamingContent,
    statusMessage,
    circuitOpen,
    sessionTakeover,
    interruptedTurn,
    sendMessage,
    loadMessages,
    stopStreaming,
    retryStream,
    dismissCircuit,
    dismissInterruptedTurn,
    loadOlderMessages,
    hasOlderMessages,
    loadingOlder,
  } = useChat(activeSessionId);
  const { isTaskSession, taskId } = useTaskContext();

  if (!activeSessionId) {
    return (
      <Activity mode={active ? "visible" : "hidden"}>
        <WelcomeScreen />
      </Activity>
    );
  }

  return (
    <Activity mode={active ? "visible" : "hidden"}>
      <div className="flex-1 flex min-w-0 overflow-hidden">
        <main className="flex-1 flex flex-col min-w-0 bg-bg relative overflow-hidden">
          <ChatHeader />
          <ChatPrimaryDrawer />
          <ChatTranscript
            key={activeSessionId}
            sessionId={activeSessionId}
            messagesReady={messagesReady}
            oldestOffset={oldestOffset}
            messages={messages}
            isStreaming={isStreaming}
            streamingContent={streamingContent}
            onSendMessage={sendMessage}
            onRetry={() => void retryStream()}
            onLoadOlder={loadOlderMessages}
            hasOlderMessages={hasOlderMessages}
            loadingOlder={loadingOlder}
          />
          {statusMessage && !sessionTakeover && !circuitOpen && (
            <div className="max-w-3xl w-full mx-auto px-4 py-1.5 text-xs text-warning animate-pulse">
              {statusMessage}
            </div>
          )}
          <ChatWorkingDrawer
            sessionTakeover={sessionTakeover}
            circuitOpen={circuitOpen}
            interruptedTurn={interruptedTurn}
            onRetry={() => void retryStream()}
            onDismissCircuit={dismissCircuit}
            onDismissInterruptedTurn={dismissInterruptedTurn}
          />
          <div className="max-w-3xl w-full mx-auto px-4 pb-1 shrink-0 relative">
            <ChatComposer
              onSend={sendMessage}
              isStreaming={isStreaming}
              onStop={stopStreaming}
              onEditorReady={onEditorReady}
              reloadMessages={loadMessages}
            />
          </div>
        </main>
        {isTaskSession && taskId && (
          <TaskThreadPanel taskId={taskId} open={taskThreadOpen} onToggle={toggleTaskThread} />
        )}
      </div>
    </Activity>
  );
}

/**
 * How many agents the welcome screen offers before it stops and points at the
 * launcher. Past about six the grid stops being a menu and becomes a list to
 * read, which is the launcher's job — this surface exists to get someone into a
 * conversation in one click.
 */
const MAX_WELCOME_AGENTS = 6;

const SETUP_CARDS = [
  {
    icon: MessageSquare,
    title: "Start a conversation",
    description: "Press Cmd+N or click + in the sidebar to begin a new chat session.",
    action: "new-chat" as const,
  },
  {
    icon: Key,
    title: "Connect a provider",
    description: "Set up an API key for Anthropic, OpenAI, or another provider to enable chat.",
    action: "settings-providers" as const,
  },
  {
    icon: Puzzle,
    title: "Explore plugins",
    description: "Browse available plugins to extend Nanite with new tools and capabilities.",
    action: "settings-plugins" as const,
  },
  {
    icon: Settings,
    title: "Configure preferences",
    description: "Set your default model, keyboard shortcuts, and display options.",
    action: "settings-preferences" as const,
  },
];

/** Parse an agent's `tags` column, which is a JSON array stored as text. */
function agentTags(raw: string | undefined): string[] {
  try {
    const parsed: unknown = JSON.parse(raw || "[]");
    return Array.isArray(parsed) ? parsed.filter((t): t is string => typeof t === "string") : [];
  } catch {
    return [];
  }
}

/**
 * The agents a person can start a conversation with, one card each.
 *
 * Renders `fallback` when there is nothing worth offering — no agents, or an
 * allowlist that matches none. A first run with no agents configured must not
 * land on an empty screen.
 */
function AgentLaunchCards({ onStarted, fallback }: { onStarted: () => void; fallback: ReactNode }) {
  const configVersion = useAppStore((s) => s.configVersion);
  const queryClient = useQueryClient();
  const { data: userSettings } = useSettings();
  const [error, setError] = useState<string | null>(null);

  const { data: agents = [], isLoading } = useQuery({
    queryKey: ["agents", configVersion],
    queryFn: () => api.listAgents(),
  });

  // Same rule the chat picker and header use. /api/agents already applies the
  // operator's NANITE_AGENT_SLUGS allowlist server-side, so this only has to
  // drop the disabled ones — a fifth list with its own idea of what to show is
  // exactly the drift bf83a0f1 went and fixed.
  const launchable = agents.filter((a) => a.status !== "disabled");

  const startChat = useMutation({
    // Provider and model are sent explicitly, as the Start launcher did.
    // POST /api/sessions writes them through with no defaulting — only
    // agent_id has a fallback chain — so omitting them leaves the session
    // with provider='' and leans on resolveProvider reaching
    // user_settings.default_provider at turn time. That tier does carry real
    // traffic, but a launch surface should not depend on it.
    mutationFn: (agentId: string) =>
      api.createSession({
        agent_id: agentId,
        provider: userSettings?.default_provider || undefined,
        model: userSettings?.default_model || undefined,
      }),
    onSuccess: (session) => {
      void queryClient.invalidateQueries({ queryKey: ["sessions"] });
      useAppStore.getState().setActiveSession(session.id);
      onStarted();
    },
    onError: (err: unknown) =>
      setError(err instanceof Error ? err.message : "Could not start that chat."),
  });

  if (isLoading) {
    return (
      <div className="grid w-full max-w-2xl grid-cols-2 gap-3">
        {["a", "b", "c", "d"].map((slot) => (
          <div
            key={slot}
            className="h-[104px] animate-pulse rounded-[10px] border border-border-subtle bg-bg-elevated"
          />
        ))}
      </div>
    );
  }

  // No agents to offer — a first run, or an allowlist that matches nothing.
  // Setup guidance is more use here than an empty grid.
  if (launchable.length === 0) return <>{fallback}</>;

  const shown = launchable.slice(0, MAX_WELCOME_AGENTS);

  return (
    <div className="w-full max-w-2xl">
      <div className="grid grid-cols-2 gap-3">
        {shown.map((agent) => {
          const tags = agentTags(agent.tags).slice(0, 3);
          return (
            <button
              key={agent.id}
              type="button"
              onClick={() => startChat.mutate(agent.id)}
              disabled={startChat.isPending}
              className="group flex flex-col rounded-[10px] border border-border-subtle bg-bg-elevated p-4 text-left transition-all hover:border-border hover:bg-surface disabled:cursor-not-allowed disabled:opacity-60"
            >
              <div className="mb-2 flex items-center gap-2.5">
                <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-surface text-sm">
                  {agent.avatar || <User className="h-4 w-4 text-fg-secondary" />}
                </span>
                <h3 className="min-w-0 flex-1 truncate text-sm font-medium text-fg">
                  {agent.name}
                </h3>
              </div>
              {agent.description && (
                <p className="line-clamp-3 text-xs leading-relaxed text-fg-muted">
                  {agent.description}
                </p>
              )}
              {tags.length > 0 && (
                <div className="mt-3 flex flex-wrap gap-1">
                  {tags.map((tag) => (
                    <span
                      key={tag}
                      className="rounded-[4px] bg-surface px-1.5 py-0.5 font-mono text-[10px] text-fg-muted"
                    >
                      {tag}
                    </span>
                  ))}
                </div>
              )}
              <span className="mt-3 font-mono text-[10px] font-semibold uppercase tracking-wide text-primary opacity-0 transition-opacity group-hover:opacity-100 group-focus-visible:opacity-100">
                {startChat.isPending ? "Starting…" : "Start chat →"}
              </span>
            </button>
          );
        })}
      </div>

      {error && (
        <p className="mt-3 text-center text-xs text-danger" role="alert">
          {error}
        </p>
      )}

      {launchable.length > shown.length && (
        <p className="mt-3 text-center text-xs text-fg-faint">
          {launchable.length - shown.length} more — press Cmd+N to pick one.
        </p>
      )}
    </div>
  );
}

/**
 * The no-session surface. Exported for tests — ChatMain itself pulls in the
 * whole chat runtime, which this does not need.
 */
const SETUP_WIZARD_DISMISSED_KEY = "nanite:setupWizardDismissed";

export function WelcomeScreen() {
  const { data: settings } = useSettings();
  const [wizardDismissed, setWizardDismissed] = useState(() => {
    try {
      return localStorage.getItem(SETUP_WIZARD_DISMISSED_KEY) === "1";
    } catch {
      return false;
    }
  });

  // A fresh seed leaves default_provider empty (internal/store/seed.go) — that's
  // the reliable "nothing usable is configured yet" signal, independent of
  // whether builtin agent profiles happen to exist (they always do).
  const needsSetup = settings?.default_provider === "" && !wizardDismissed;

  const handleDismissWizard = () => {
    try {
      localStorage.setItem(SETUP_WIZARD_DISMISSED_KEY, "1");
    } catch {
      // best-effort — private windows / blocked storage just re-show the wizard next time
    }
    setWizardDismissed(true);
  };

  const handleAction = (action: (typeof SETUP_CARDS)[number]["action"]) => {
    if (action === "new-chat") {
      return;
    }
    // Set hash first — useHashRoute will derive currentPage from the hash
    if (action === "settings-providers") {
      window.location.hash = "#settings/providers";
    } else if (action === "settings-plugins") {
      window.location.hash = "#settings/plugins";
    } else if (action === "settings-preferences") {
      window.location.hash = "#settings";
    }
  };

  const setupCards = (
    <div className="grid w-full max-w-lg grid-cols-2 gap-3">
      {SETUP_CARDS.map((card) => (
        <button
          key={card.action}
          type="button"
          onClick={() => handleAction(card.action)}
          className="group text-left p-4 rounded-[10px] border border-border-subtle bg-bg-elevated hover:border-border hover:bg-surface transition-all"
        >
          <card.icon className="w-5 h-5 text-primary mb-3 group-hover:text-primary-hover transition-colors" />
          <h3 className="text-sm font-medium text-fg mb-1">{card.title}</h3>
          <p className="text-xs text-fg-muted leading-relaxed">{card.description}</p>
        </button>
      ))}
    </div>
  );

  return (
    <main className="flex-1 flex flex-col items-center justify-center min-w-0 bg-bg px-8">
      <div className="max-w-lg w-full text-center mb-10">
        <h1 className="text-2xl font-semibold text-fg mb-2">Welcome to Nanite</h1>
        <p className="text-sm text-fg-muted">
          {needsSetup
            ? "Let's get you connected to an agent."
            : "Pick an agent to start a conversation with."}
        </p>
      </div>

      {needsSetup ? (
        <SetupWizard onDismiss={handleDismissWizard} />
      ) : (
        <AgentLaunchCards
          onStarted={() => {
            useLayoutStore.getState().setCurrentPage("chat");
            window.location.hash = "#chat";
          }}
          fallback={setupCards}
        />
      )}

      <p className="text-xs text-fg-faint mt-8">
        Cmd+N new chat &middot; Cmd+B sidebar &middot; Cmd+/ widgets &middot; Cmd+L focus editor
      </p>
    </main>
  );
}
