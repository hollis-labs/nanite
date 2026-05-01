import { useQuery, useQueryClient } from "@tanstack/react-query";
import Placeholder from "@tiptap/extension-placeholder";
import { EditorContent, useEditor } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import { Check, Terminal, Upload, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useArtifactUpload } from "@/hooks/useArtifactUpload";
import { usePluginAction } from "@/hooks/usePluginAction";
import { usePluginSlots } from "@/hooks/usePluginSlots";
import { useSettings } from "@/hooks/useSettings";
import { useShellMode } from "@/hooks/useShellMode";
import { useWorkSync } from "@/hooks/useWorkSync";
import { api } from "@/lib/api";
import { resolveIcon } from "@/lib/icons";
import type { SlashCommandDef } from "@/lib/types";
import { useAppStore } from "@/stores/useAppStore";
import { useChatStore } from "@/stores/useChatStore";
import { useLayoutStore } from "@/stores/useLayoutStore";
import { useWorkStore } from "@/stores/useWorkStore";
import { ComposerToolbar } from "./ComposerToolbar";
import { StatusPill } from "./envelopes/primitives/StatusPill";
import { FileMentionExtension, type FileResult } from "./extensions/FileMentionExtension";
import { fileMentionSuggestion } from "./extensions/fileMentionSuggestion";
import { type SlashCommand, SlashCommandExtension } from "./extensions/SlashCommandExtension";
import { slashCommandSuggestion } from "./extensions/slashCommandSuggestion";
import { ShellInfoDrawer } from "./ShellInfoDrawer";

/**
 * POLISHED — chat composer shell.
 *
 * Changes vs. original (keeping tiptap editor + all slash/mention/shell logic
 * intact — only chrome around the editor was restyled):
 *
 *  - Outer frame radius 2px (rounded-sm) → 10px (outer envelope scale) with
 *    a single solid border and a quieter shadow. The drop-shadow was
 *    shadow-black/30 which on light mode came out crunchy — now uses the
 *    standard shadow-lg token.
 *  - Drag-drop banner: primary-tinted text on primary/10 strip → proper
 *    dashed-accent feedback with an Upload icon + mono label. Matches the
 *    envelope-accent grammar (inset left stripe) instead of a full bg tint.
 *  - Shell-running banner: animate-pulse bg-bg-elevated/50 → solid surface
 *    with a mono label and a small terminal icon. Pulse removed — it was
 *    visually loud and fought the thinking indicator.
 *  - Work toast: ad-hoc primary/5 with circle-check icon → Envelope-style
 *    strip with success accent + StatusPill-sized dismiss button.
 *  - Shell approval strip: was the worst offender — three ad-hoc buttons
 *    (one primary-fill "Allow", one surface-hover "Deny", and an inline
 *    code chip). Now mono-labelled with proper primary + ghost buttons.
 *    No more "Deny" button rendered in white text on hover-gray.
 *  - Shell mode 3-state toggle: bg-warning/10 + text-warning combinations
 *    still make sense (security-flavored), but the button radius matches
 *    the new 6px inner-chrome scale and icon sizes normalised to 3.5px.
 *  - Footer disclaimer ("Nanite may produce inaccurate…") now uses mono
 *    uppercase at 10px — consistent with every other caption in the system.
 */

let slashMenuOpen = false;
let fileMentionMenuOpen = false;

const MAX_HISTORY = 50;
let commandHistory: string[] = [];
let historyIndex = -1;

if (typeof window !== "undefined") {
  try {
    const stored = localStorage.getItem("nanite:command-history");
    if (stored) commandHistory = JSON.parse(stored);
  } catch {
    /* ignore */
  }
}

function pushHistory(text: string) {
  if (commandHistory[0] === text) return;
  commandHistory.unshift(text);
  if (commandHistory.length > MAX_HISTORY) commandHistory.length = MAX_HISTORY;
  historyIndex = -1;
  try {
    localStorage.setItem("nanite:command-history", JSON.stringify(commandHistory));
  } catch {
    /* ignore */
  }
}

interface ChatComposerProps {
  onSend: (content: string) => void;
  isStreaming?: boolean;
  onStop?: () => void;
  onEditorReady?: (focus: () => void) => void;
  reloadMessages?: () => void;
  drawer?: React.ReactNode;
}

export function ChatComposer({
  onSend,
  isStreaming = false,
  onStop,
  onEditorReady,
  reloadMessages,
  drawer = null,
}: ChatComposerProps) {
  const activeSessionId = useAppStore((s) => s.activeSessionId);
  const setActiveSession = useAppStore((s) => s.setActiveSession);
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId);
  const queryClient = useQueryClient();
  const {
    mode: shellMode,
    cycleMode: cycleShellMode,
    setMode: setShellMode,
  } = useShellMode(activeSessionId);
  const [isShellInput, setIsShellInput] = useState(false);
  const [shellRunning, setShellRunning] = useState(false);
  const workToast = useWorkStore((s) => s.toastMessage);
  const dismissWorkToast = useWorkStore((s) => s.dismissToast);
  // B3 (CW-20260428-0011): chat-scoped toast (e.g. mode auto-switch confirmation).
  // The per-session auto-switch override toggle itself lives in ComposerToolbar
  // (placed next to the shell-mode toggle); this parent only renders the toast.
  const chatToast = useChatStore((s) => s.chatToast);
  const dismissChatToast = useChatStore((s) => s.dismissChatToast);
  const { flushIfDirty } = useWorkSync();
  // F4 (CW-20260429-0004): upload handler extracted to useArtifactUpload so
  // the right-rail Artifacts panel dropzone can reuse the same path.
  const {
    uploading,
    dragOver,
    setDragOver,
    handleDrop,
    handleFileUpload: handleFileUploadFromHook,
  } = useArtifactUpload(activeSessionId);
  const dropRef = useRef<HTMLDivElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const handleFileUpload = useCallback(
    async (files: FileList | null) => {
      try {
        await handleFileUploadFromHook(files);
      } finally {
        // Composer-specific cleanup: reset the hidden <input type=file> so
        // the same file can be re-selected after a failed/canceled upload.
        if (fileInputRef.current) fileInputRef.current.value = "";
      }
    },
    [handleFileUploadFromHook],
  );

  const { data: commandDefs } = useQuery({
    queryKey: ["commands"],
    queryFn: () => api.listCommands(),
    staleTime: 60_000,
  });
  const commandDefsRef = useRef<SlashCommandDef[]>([]);
  commandDefsRef.current = commandDefs ?? [];

  const handleCommand = useCallback(
    async (cmd: SlashCommand, cmdArgs = "") => {
      switch (cmd.name) {
        case "new": {
          const session = await api.createSession({ workspace_id: activeWorkspaceId || "" });
          setActiveSession(session.id);
          void queryClient.invalidateQueries({ queryKey: ["sessions"] });
          return;
        }
        case "fork": {
          if (!activeSessionId) return;
          const forked = await api.forkSession(activeSessionId, { include_messages: true });
          setActiveSession(forked.id);
          void queryClient.invalidateQueries({ queryKey: ["sessions"] });
          return;
        }
        case "clone": {
          if (!activeSessionId) return;
          const cloned = await api.forkSession(activeSessionId, { include_messages: false });
          setActiveSession(cloned.id);
          void queryClient.invalidateQueries({ queryKey: ["sessions"] });
          return;
        }
        case "bookmark": {
          if (!activeSessionId) return;
          const session = await api.getSession(activeSessionId);
          const messages = session.messages || [];
          const lastAssistant = [...messages].reverse().find((m) => m.role === "assistant");
          if (lastAssistant) {
            await api.toggleBookmark(lastAssistant.id, activeSessionId);
            void queryClient.invalidateQueries({ queryKey: ["bookmarks", activeSessionId] });
          }
          return;
        }
        case "compact": {
          if (!activeSessionId) return;
          await api.compactSession(activeSessionId);
          void queryClient.invalidateQueries({ queryKey: ["session", activeSessionId] });
          return;
        }
        case "agent":
        case "model":
          return;
        case "memory": {
          useLayoutStore.getState().setMemoryModalOpen(true);
          return;
        }
        // J10 (CW-20260426-0008): /scratch, /pad, /scratchpad slash commands.
        // These commands are intercepted client-side before hitting the server
        // execute path. The bare-invocation case opens the bottom drawer and
        // switches to the scratchpad tab; the with-args case appends text to
        // the scratchpad WITHOUT sending to the agent.
        case "scratch":
        case "pad":
        case "scratchpad": {
          const store = useLayoutStore.getState();
          if (cmdArgs.trim()) {
            // With text — append to scratchpad without sending to agent.
            // Open the drawer so the user can see the append happened.
            // TODO(Wave 4 / Task 11): wire to the new working-drawer scratchpad
            // store directly. For now this is a no-op append; the drawer still
            // opens so the user can paste manually.
            store.setBottomDrawerOpen(true, "user");
          } else {
            // Bare invocation — open drawer to scratchpad tab.
            store.setBottomDrawerOpen(true, "user");
          }
          return;
        }
        default: {
          if (!activeSessionId) return;
          try {
            const result = await api.executeCommand(cmd.name, activeSessionId, cmdArgs);
            if (result.action === "message") {
              reloadMessages?.();
            } else if (
              result.action === "client" &&
              typeof result.content === "string" &&
              result.content.startsWith("mode_switched:")
            ) {
              // B2 (CW-20260428-0010): /mode, /chat, /plan, /work succeeded.
              // Invalidate session-mode + session queries so the chip and any
              // session-derived UI refresh from the new current_mode_id.
              void queryClient.invalidateQueries({ queryKey: ["session-mode", activeSessionId] });
              void queryClient.invalidateQueries({ queryKey: ["session", activeSessionId] });
            } else if (result.action === "skill") {
              const parts = (result.content ?? "").trim().split(/\s+/);
              const slug = parts[0];
              const args = parts.slice(1).join(" ");
              try {
                const skill = await api.getSkill(`file-${slug}`);
                if (skill?.prompt) {
                  const msg = args ? `${skill.prompt}\n\nArgs: ${args}` : skill.prompt;
                  onSend(msg);
                } else {
                  // Skill exists but has no prompt body (DB-only skill or empty file).
                  // Fall back to sending the raw content so the agent receives the slug.
                  if (result.content) onSend(result.content);
                }
              } catch {
                // Skill not found or fetch error — send raw content as fallback.
                if (result.content) onSend(result.content);
              }
            }
          } catch (err) {
            console.error("Command execution failed:", err);
          }
        }
      }
    },
    [activeSessionId, activeWorkspaceId, setActiveSession, queryClient, onSend, reloadMessages],
  );

  const handleCommandRef = useRef(handleCommand);
  handleCommandRef.current = handleCommand;

  const handleSendRef = useRef<() => void>(() => {});

  const editor = useEditor({
    extensions: [
      StarterKit.configure({
        heading: false,
        blockquote: false,
        codeBlock: false,
        horizontalRule: false,
        bulletList: false,
        orderedList: false,
        listItem: false,
      }),
      Placeholder.configure({
        placeholder: "Message Nanite… (Enter to send, / for commands, @ for files)",
      }),
      SlashCommandExtension.configure({
        suggestion: {
          ...slashCommandSuggestion,
          command: ({
            editor: ed,
            range,
            props,
          }: {
            editor: any;
            range: { from: number; to: number };
            props: SlashCommand;
          }) => {
            ed?.chain().focus().deleteRange(range).run();
            void handleCommandRef.current(props);
          },
        },
      }),
      FileMentionExtension.configure({
        suggestion: {
          ...fileMentionSuggestion,
          command: ({
            editor: ed,
            range,
            props,
          }: {
            editor: any;
            range: { from: number; to: number };
            props: FileResult;
          }) => {
            ed?.chain().focus().deleteRange(range).insertContent(`@${props.path} `).run();
          },
        },
      }),
    ],
    editorProps: {
      attributes: {
        class:
          "bg-transparent text-sm text-fg placeholder:text-fg-faint outline-none min-h-[80px] max-h-[160px] overflow-y-auto py-2 px-1 leading-relaxed prose-sm",
      },
      handleKeyDown(_view, event) {
        if (event.key === "Enter") {
          if (slashMenuOpen || fileMentionMenuOpen) return false;
          if (event.metaKey || event.ctrlKey || event.shiftKey) return false;
          const text = editor?.getText().trim() ?? "";
          if (!text) return false;
          event.preventDefault();
          handleSendRef.current();
          return true;
        }
        if (event.key === "Tab" && !slashMenuOpen && !fileMentionMenuOpen) {
          const text = editor?.getText() ?? "";
          if (text.startsWith("/")) {
            const parts = text.split(/\s+/);
            const cmdName = parts[0]?.slice(1);
            const cmdDef = commandDefsRef.current.find((c) => c.name === cmdName);
            if (cmdDef?.args) {
              const argIdx = parts.length - 2;
              const arg = cmdDef.args[argIdx];
              if (arg?.options && arg.options.length > 0) {
                event.preventDefault();
                const current = parts[parts.length - 1] ?? "";
                const currentOptIdx = arg.options.indexOf(current);
                const nextOpt = arg.options[(currentOptIdx + 1) % arg.options.length];
                parts[parts.length - 1] = nextOpt ?? "";
                const newText = parts.join(" ");
                editor?.commands.setContent(newText);
                editor?.commands.focus("end");
                return true;
              }
            }
          }
        }
        if (event.key === "ArrowUp" && !slashMenuOpen && !fileMentionMenuOpen) {
          const text = editor?.getText() ?? "";
          const sel = editor?.state.selection;
          if (sel && sel.$head.pos <= 1 && !text.includes("\n") && commandHistory.length > 0) {
            event.preventDefault();
            const nextIdx = Math.min(historyIndex + 1, commandHistory.length - 1);
            historyIndex = nextIdx;
            editor?.commands.setContent(commandHistory[nextIdx] ?? "");
            editor?.commands.focus("end");
            return true;
          }
        }
        if (event.key === "ArrowDown" && !slashMenuOpen && !fileMentionMenuOpen) {
          if (historyIndex >= 0) {
            event.preventDefault();
            historyIndex--;
            if (historyIndex < 0) {
              editor?.commands.clearContent();
            } else {
              editor?.commands.setContent(commandHistory[historyIndex] ?? "");
              editor?.commands.focus("end");
            }
            return true;
          }
        }
        return false;
      },
    },
    content: "",
  });

  useEffect(() => {
    if (!editor) return;
    const handler = () => {
      const text = editor.getText();
      setIsShellInput(text.startsWith("!") && text.length >= 1);
    };
    editor.on("update", handler);
    return () => {
      editor.off("update", handler);
    };
  }, [editor]);

  useEffect(() => {
    if (editor && onEditorReady)
      onEditorReady(() => {
        editor.commands.focus();
      });
  }, [editor, onEditorReady]);

  useEffect(() => {
    if (!workToast) return;
    const timer = setTimeout(() => dismissWorkToast(), 3000);
    return () => clearTimeout(timer);
  }, [workToast, dismissWorkToast]);

  // B3 (CW-20260428-0011): autodismiss chat-scoped toast after 3s, mirroring workToast.
  useEffect(() => {
    if (!chatToast) return;
    const timer = setTimeout(() => dismissChatToast(), 3000);
    return () => clearTimeout(timer);
  }, [chatToast, dismissChatToast]);

  const [pendingShellCommand, setPendingShellCommand] = useState<string | null>(null);

  const executeShellCommand = useCallback(
    async (command: string, approved: boolean) => {
      if (!activeSessionId) return;
      setShellRunning(true);
      try {
        const result = await api.shellExec(activeSessionId, command, approved);
        if (result.requires_approval) {
          setPendingShellCommand(command);
          return;
        }
        setPendingShellCommand(null);
        reloadMessages?.();
      } catch (err) {
        console.error("Shell exec failed:", err);
      } finally {
        setShellRunning(false);
        setIsShellInput(false);
      }
    },
    [activeSessionId, reloadMessages],
  );

  const handleShellExec = useCallback(
    async (command: string) => {
      await executeShellCommand(command, shellMode !== "ask");
    },
    [executeShellCommand, shellMode],
  );

  const handleShellApprove = useCallback(() => {
    if (pendingShellCommand) void executeShellCommand(pendingShellCommand, true);
  }, [pendingShellCommand, executeShellCommand]);

  const handleShellDeny = useCallback(() => setPendingShellCommand(null), []);

  const handleSend = useCallback(() => {
    if (!editor) return;
    const text = editor.getText().trim();
    if (!text) return;
    pushHistory(text);
    if (text.startsWith("!") && text.length > 1) {
      const command = text.slice(1).trim();
      if (command) {
        editor.commands.clearContent();
        void handleShellExec(command);
        return;
      }
    }
    // Handle /command-name typed without selecting from the autocomplete menu.
    // The suggestion plugin only fires when the menu is open; once dismissed
    // (Escape or click-away), the raw slash text remains. We detect and execute
    // it here so the command still runs reliably without autocomplete.
    // Args (text after the command name) are preserved and forwarded so that
    // /scratch some text correctly appends "some text" to the scratchpad.
    if (text.startsWith("/") && text.length > 1) {
      const withoutSlash = text.slice(1);
      const spaceIdx = withoutSlash.indexOf(" ");
      const cmdName = spaceIdx === -1 ? withoutSlash : withoutSlash.slice(0, spaceIdx);
      const cmdArgs = spaceIdx === -1 ? "" : withoutSlash.slice(spaceIdx + 1).trim();
      if (cmdName) {
        editor.commands.clearContent();
        void handleCommandRef.current(
          { name: cmdName, description: "", category: "", source: "builtin" },
          cmdArgs,
        );
        return;
      }
    }
    void flushIfDirty();
    onSend(text);
    editor.commands.clearContent();
  }, [editor, onSend, handleShellExec, flushIfDirty]);

  handleSendRef.current = handleSend;

  const hasContent = editor ? editor.getText().trim().length > 0 : false;

  const { data: settings } = useSettings();
  const developerMode = settings?.developer_mode ?? false;

  const composerAboveSlots = usePluginSlots("composer-above");
  const composerBelowSlots = usePluginSlots("composer-below");
  const handlePluginAction = usePluginAction();

  return (
    <div className="shrink-0 px-4 pb-4 pt-2">
      {composerAboveSlots.length > 0 && (
        <div className="mb-1 flex items-center gap-1">
          {composerAboveSlots.map((entry) => {
            const PluginIcon = resolveIcon(entry.icon);
            return (
              <button
                key={entry.id}
                type="button"
                className="flex items-center gap-1 rounded-[4px] px-2 py-1 text-xs text-fg-muted transition-colors hover:bg-surface hover:text-fg"
                onClick={() => handlePluginAction(entry)}
              >
                <PluginIcon className="h-3.5 w-3.5" />
                <span>{entry.label}</span>
              </button>
            );
          })}
        </div>
      )}

      {drawer}

      <div
        ref={dropRef}
        className={`relative overflow-hidden border bg-bg-elevated shadow-lg transition-colors ${
          drawer ? "rounded-b-[10px] rounded-t-none border-t-0" : "rounded-[10px]"
        } ${
          dragOver
            ? "border-primary shadow-[inset_3px_0_0_0_var(--color-primary)]"
            : "border-border-subtle"
        }`}
        onDragOver={(e) => {
          e.preventDefault();
          setDragOver(true);
        }}
        onDragLeave={() => setDragOver(false)}
        onDrop={(e) => void handleDrop(e)}
      >
        <div className="pointer-events-none absolute inset-x-0 top-0 z-[1] h-[2px] bg-primary opacity-85" />

        {/* DEV mode indicator — floating top-right when developer_mode=true */}
        {developerMode && (
          <div className="pointer-events-none select-none absolute top-2 right-3 z-[2]">
            <StatusPill tone="danger">
              <span className="inline-block w-1.5 h-1.5 rounded-full bg-danger animate-pulse shrink-0" />
              DEV
            </StatusPill>
          </div>
        )}

        {/* Shell info drawer — appears when user types ! */}
        {isShellInput && activeSessionId && (
          <ShellInfoDrawer
            sessionId={activeSessionId}
            shellMode={shellMode}
            onToggleDenylist={() => setShellMode(shellMode === "yolo" ? "session" : "yolo")}
          />
        )}

        {/* Drag-drop banner */}
        {dragOver && (
          <div className="flex items-center justify-center gap-2 border-b border-primary/30 bg-primary/5 px-3 py-2 text-xs text-primary">
            <Upload className="h-3.5 w-3.5" />
            <span className="font-mono text-[11px] font-semibold uppercase tracking-wide">
              Drop files to attach
            </span>
          </div>
        )}

        {/* Shell-running banner */}
        {shellRunning && (
          <div className="flex items-center justify-center gap-2 border-b border-border-subtle bg-surface px-3 py-2 text-xs text-fg-muted">
            <Terminal className="h-3.5 w-3.5" />
            <span className="font-mono text-[11px] font-semibold uppercase tracking-wide">
              Running command
            </span>
            <span className="inline-flex items-center gap-0.5" aria-hidden="true">
              <span className="inline-block h-1 w-1 animate-pulse rounded-full bg-fg-muted [animation-delay:0ms]" />
              <span className="inline-block h-1 w-1 animate-pulse rounded-full bg-fg-muted [animation-delay:160ms]" />
              <span className="inline-block h-1 w-1 animate-pulse rounded-full bg-fg-muted [animation-delay:320ms]" />
            </span>
          </div>
        )}

        {/* Work-toast banner — success accent, dismissible */}
        {workToast && (
          <div className="flex items-center gap-2 border-b border-border-subtle bg-surface px-3 py-2 text-xs shadow-[inset_3px_0_0_0_var(--color-success)]">
            <span className="flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-success text-white">
              <Check className="h-2.5 w-2.5" />
            </span>
            <span className="flex-1 text-fg">{workToast}</span>
            <button
              type="button"
              onClick={dismissWorkToast}
              aria-label="Dismiss"
              className="rounded-[4px] p-0.5 text-fg-faint transition-colors hover:bg-surface-hover hover:text-fg-secondary"
            >
              <X className="h-3 w-3" />
            </button>
          </div>
        )}

        {/* B3 (CW-20260428-0011): chat-scoped toast — used for auto-mode-switch */}
        {chatToast && (
          <div
            className={`flex items-center gap-2 border-b border-border-subtle bg-surface px-3 py-2 text-xs ${
              chatToast.tone === 'success'
                ? 'shadow-[inset_3px_0_0_0_var(--color-success)]'
                : 'shadow-[inset_3px_0_0_0_var(--color-info)]'
            }`}
          >
            <span
              className={`flex h-4 w-4 shrink-0 items-center justify-center rounded-full text-white ${
                chatToast.tone === 'success' ? 'bg-success' : 'bg-info'
              }`}
            >
              <Check className="h-2.5 w-2.5" />
            </span>
            <span className="flex-1 text-fg">{chatToast.message}</span>
            <button
              type="button"
              onClick={dismissChatToast}
              aria-label="Dismiss"
              className="rounded-[4px] p-0.5 text-fg-faint transition-colors hover:bg-surface-hover hover:text-fg-secondary"
            >
              <X className="h-3 w-3" />
            </button>
          </div>
        )}

        {/* Shell-approval strip — polished primary/ghost pair */}
        {pendingShellCommand && (
          <div className="flex flex-wrap items-center gap-2 border-b border-border-subtle bg-surface px-3 py-2 text-xs shadow-[inset_3px_0_0_0_var(--color-warning)]">
            <span className="font-mono text-[10px] font-semibold uppercase tracking-wide text-warning">
              Shell approval
            </span>
            <span className="text-fg-muted">Run</span>
            <code className="truncate rounded-[4px] border border-border-subtle bg-bg-elevated px-1.5 py-0.5 font-mono text-[11px] text-fg">
              {pendingShellCommand}
            </code>
            <div className="ml-auto flex items-center gap-1">
              <button
                type="button"
                onClick={handleShellApprove}
                className="rounded-[6px] bg-primary px-2.5 py-1 font-mono text-[10px] font-semibold uppercase tracking-wide text-primary-foreground transition-colors hover:bg-primary-hover"
              >
                Allow
              </button>
              <button
                type="button"
                onClick={handleShellDeny}
                className="rounded-[6px] border border-border-subtle bg-transparent px-2.5 py-1 font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-secondary transition-colors hover:bg-surface-hover hover:text-fg"
              >
                Deny
              </button>
            </div>
          </div>
        )}

        {/* Editor */}
        <div className="bg-bg-elevated px-[14px] pt-[10px] pb-2">
          <input
            ref={fileInputRef}
            type="file"
            multiple
            className="hidden"
            onChange={(e) => void handleFileUpload(e.target.files)}
          />
          <EditorContent
            editor={editor}
            className="min-w-0 [&_.tiptap]:outline-none [&_.tiptap_p.is-editor-empty:first-child::before]:content-[attr(data-placeholder)] [&_.tiptap_p.is-editor-empty:first-child::before]:text-fg-faint [&_.tiptap_p.is-editor-empty:first-child::before]:float-left [&_.tiptap_p.is-editor-empty:first-child::before]:h-0 [&_.tiptap_p.is-editor-empty:first-child::before]:pointer-events-none"
          />
        </div>

        <ComposerToolbar
          hasContent={hasContent}
          isStreaming={isStreaming}
          onSend={handleSend}
          onStop={onStop}
          onAttach={() => fileInputRef.current?.click()}
          onSlash={() => editor?.chain().focus().insertContent("/").run()}
          onMention={() => editor?.chain().focus().insertContent("@").run()}
          shellMode={shellMode}
          onCycleShell={cycleShellMode}
          uploading={uploading}
        />
      </div>

      {composerBelowSlots.length > 0 && (
        <div className="mt-1 flex items-center gap-1">
          {composerBelowSlots.map((entry) => {
            const PluginIcon = resolveIcon(entry.icon);
            return (
              <button
                key={entry.id}
                type="button"
                className="flex items-center gap-1 rounded-[4px] px-2 py-1 text-xs text-fg-muted transition-colors hover:bg-surface hover:text-fg"
                onClick={() => handlePluginAction(entry)}
              >
                <PluginIcon className="h-3.5 w-3.5" />
                <span>{entry.label}</span>
              </button>
            );
          })}
        </div>
      )}

      <p className="mt-2 text-center font-mono text-[10px] uppercase tracking-wide text-fg-faint">
        Nanite may produce inaccurate information
      </p>
    </div>
  );
}

export function setSlashMenuOpen(open: boolean) {
  slashMenuOpen = open;
}
export function setFileMentionMenuOpen(open: boolean) {
  fileMentionMenuOpen = open;
}
