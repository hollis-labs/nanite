import { AlertTriangle, Key, MessageSquare, Puzzle, RefreshCw, Settings, X } from "lucide-react";
import { TaskThreadPanel } from "@/components/messaging/TaskThreadPanel";
import { useChat } from "@/hooks/useChat";
import { useTaskContext } from "@/hooks/useTaskContext";
import { useAppStore } from "@/stores/useAppStore";
import { useLayoutStore } from "@/stores/useLayoutStore";
import { ChatComposer } from "./ChatComposer";
import { ChatHeader } from "./ChatHeader";
import { ChatTranscript } from "./ChatTranscript";
import { ToolCallDrawer } from "./ToolCallDrawer";

interface ChatMainProps {
  onEditorReady?: (focus: () => void) => void;
}

export function ChatMain({ onEditorReady }: ChatMainProps) {
  const activeSessionId = useAppStore((s) => s.activeSessionId);
  const taskThreadOpen = useLayoutStore((s) => s.taskThreadOpen);
  const toggleTaskThread = useLayoutStore((s) => s.toggleTaskThread);
  const {
    messages,
    isStreaming,
    streamingContent,
    statusMessage,
    circuitOpen,
    sessionTakeover,
    sendMessage,
    loadMessages,
    stopStreaming,
    retryStream,
    dismissCircuit,
    loadOlderMessages,
    hasOlderMessages,
    loadingOlder,
  } = useChat(activeSessionId);
  const { isTaskSession, taskId } = useTaskContext();

  if (!activeSessionId) {
    return <WelcomeScreen />;
  }

  return (
    <div className="flex-1 flex min-w-0">
      <main className="flex-1 flex flex-col min-w-0 bg-bg">
        <ChatHeader />
        <ToolCallDrawer />
        <ChatTranscript
          messages={messages}
          isStreaming={isStreaming}
          streamingContent={streamingContent}
          onSendMessage={sendMessage}
          onLoadOlder={loadOlderMessages}
          hasOlderMessages={hasOlderMessages}
          loadingOlder={loadingOlder}
        />
        {sessionTakeover && (
          <div className="mx-4 mb-2 rounded-lg border border-info/30 bg-info/15 p-4">
            <div className="flex items-start gap-3">
              <AlertTriangle className="w-5 h-5 text-info mt-0.5 flex-shrink-0" />
              <div className="flex-1 min-w-0">
                <p className="text-sm font-medium text-info">
                  This session is now active in another tab
                </p>
                <p className="text-xs text-info/70 mt-1">
                  The streaming connection was moved to a newer tab. Reload this page to reconnect
                  here.
                </p>
                <div className="flex gap-2 mt-3">
                  <button
                    onClick={() => window.location.reload()}
                    className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium bg-info/15 text-info hover:bg-info/30 transition-colors"
                  >
                    <RefreshCw className="w-3.5 h-3.5" />
                    Reconnect
                  </button>
                </div>
              </div>
            </div>
          </div>
        )}
        {circuitOpen && (
          <div className="mx-4 mb-2 rounded-lg border border-warning/30 bg-warning/10 p-4">
            <div className="flex items-start gap-3">
              <AlertTriangle className="w-5 h-5 text-warning mt-0.5 flex-shrink-0" />
              <div className="flex-1 min-w-0">
                <p className="text-sm font-medium text-warning">
                  Provider rate limited after multiple retries
                </p>
                <p className="text-xs text-warning/70 mt-1">
                  The API provider has been returning rate limit errors. You can retry or dismiss to
                  keep the partial response.
                </p>
                <div className="flex gap-2 mt-3">
                  <button
                    onClick={() => void retryStream()}
                    className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium bg-warning/20 text-warning hover:bg-warning/30 transition-colors"
                  >
                    <RefreshCw className="w-3.5 h-3.5" />
                    Retry
                  </button>
                  <button
                    onClick={dismissCircuit}
                    className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium bg-surface/50 text-fg-secondary hover:bg-surface-hover transition-colors"
                  >
                    <X className="w-3.5 h-3.5" />
                    Dismiss
                  </button>
                </div>
              </div>
            </div>
          </div>
        )}
        {statusMessage && (
          <div className="px-4 py-1.5 text-xs text-warning bg-warning/10 border-t border-warning/30 animate-pulse">
            {statusMessage}
          </div>
        )}
        <ChatComposer
          onSend={sendMessage}
          isStreaming={isStreaming}
          onStop={stopStreaming}
          onEditorReady={onEditorReady}
          reloadMessages={loadMessages}
        />
      </main>
      {isTaskSession && taskId && (
        <TaskThreadPanel taskId={taskId} open={taskThreadOpen} onToggle={toggleTaskThread} />
      )}
    </div>
  );
}

const WELCOME_CARDS = [
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

function WelcomeScreen() {
  const handleAction = (action: (typeof WELCOME_CARDS)[number]["action"]) => {
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

  return (
    <main className="flex-1 flex flex-col items-center justify-center min-w-0 bg-bg px-8">
      <div className="max-w-lg w-full text-center mb-10">
        <h1 className="text-2xl font-semibold text-fg mb-2">Welcome to Nanite</h1>
        <p className="text-sm text-fg-muted">
          Multi-agent chat harness for Fragments Engine. Get started by picking an action below.
        </p>
      </div>

      <div className="grid grid-cols-2 gap-3 max-w-lg w-full">
        {WELCOME_CARDS.map((card) => (
          <button
            key={card.action}
            onClick={() => handleAction(card.action)}
            className="group text-left p-4 rounded-lg border border-border bg-bg-elevated/50 hover:border-border-subtle hover:bg-bg-elevated transition-all"
          >
            <card.icon className="w-5 h-5 text-primary mb-3 group-hover:text-primary-hover transition-colors" />
            <h3 className="text-sm font-medium text-fg mb-1">{card.title}</h3>
            <p className="text-xs text-fg-muted leading-relaxed">{card.description}</p>
          </button>
        ))}
      </div>

      <p className="text-xs text-fg-faint mt-8">
        Cmd+N new chat &middot; Cmd+B sidebar &middot; Cmd+/ widgets &middot; Cmd+L focus editor
      </p>
    </main>
  );
}
