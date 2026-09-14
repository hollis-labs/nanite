import { Key, MessageSquare, Puzzle, Settings } from "lucide-react";
import { Activity } from "react";
import { ChatPrimaryDrawer } from "@/components/drawers/ChatPrimaryDrawer";
import { ChatWorkingDrawer } from "@/components/drawers/ChatWorkingDrawer";
import { TaskThreadPanel } from "@/components/messaging/TaskThreadPanel";
import { useChat } from "@/hooks/useChat";
import { useTaskContext } from "@/hooks/useTaskContext";
import { useAppStore } from "@/stores/useAppStore";
import { useLayoutStore } from "@/stores/useLayoutStore";
import { ChatComposer } from "./ChatComposer";
import { ChatHeader } from "./ChatHeader";
import { ChatTranscript } from "./ChatTranscript";

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
          Multi-agent chat harness. Get started by picking an action below.
        </p>
      </div>

      <div className="grid grid-cols-2 gap-3 max-w-lg w-full">
        {WELCOME_CARDS.map((card) => (
          <button
            key={card.action}
            onClick={() => handleAction(card.action)}
            className="group text-left p-4 rounded-[10px] border border-border-subtle bg-bg-elevated hover:border-border hover:bg-surface transition-all"
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
