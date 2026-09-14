import { ArrowBigUp, Square } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { Tooltip } from "@/components/ui/tooltip";
import { usePluginSlots } from "@/hooks/usePluginSlots";
import { api } from "@/lib/api";
import { resolveIcon } from "@/lib/icons";
import type { UISlotEntry } from "@/lib/types";
import { useAppStore } from "@/stores/useAppStore";
import { useActiveEffort, useChatStore } from "@/stores/useChatStore";
import { ComposerPlusMenu } from "./ComposerPlusMenu";
import { LayoutMenu } from "./LayoutMenu";

// F1 (CW-20260420-0014) — Effort levels for the per-turn budget + reasoning dial.
const EFFORT_LEVELS = [
  { value: "low", label: "Low", title: "Effort: Low — 0.5× token budget, minimum supported reasoning" },
  { value: "normal", label: "Norm", title: "Effort: Normal — 1.0× token budget (default)" },
  { value: "high", label: "High", title: "Effort: High — 2.0× token budget, reasoning on" },
  { value: "max", label: "Max", title: "Effort: Max — 4.0× token budget, intensive reasoning" },
] as const;

type EffortValue = (typeof EFFORT_LEVELS)[number]["value"];

interface ComposerToolbarProps {
  hasContent: boolean;
  isStreaming: boolean;
  onSend: () => void;
  onStop?: () => void;
  onAttach: () => void;
  onSlash: () => void;
  onMention: () => void;
  shellMode: "ask" | "session" | "yolo";
  onCycleShell: () => void;
  uploading?: boolean;
}

export function ComposerToolbar({
  hasContent,
  isStreaming,
  onSend,
  onStop,
  onAttach,
  onSlash,
  onMention,
  shellMode,
  onCycleShell,
  uploading = false,
}: ComposerToolbarProps) {
  // Layout menu state lives here (not inside ComposerPlusMenu) so the
  // global `⌘\` keyboard shortcut — which dispatches a
  // `toggle-layout-menu` window event — can still open the menu without
  // having to first open the `+` popover.
  const [layoutOpen, setLayoutOpen] = useState(false);
  const layoutAnchorRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function onToggle() {
      setLayoutOpen((o) => !o);
    }
    window.addEventListener("toggle-layout-menu", onToggle);
    return () => window.removeEventListener("toggle-layout-menu", onToggle);
  }, []);

  // F1 (CW-20260420-0014): per-session effort dial.
  const activeEffort = useActiveEffort() as EffortValue;
  const setActiveEffort = useChatStore((s) => s.setActiveEffort);
  const activeSessionId = useAppStore((s) => s.activeSessionId);
  const pluginButtons = usePluginSlots("composer-toolbar");

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
          window.dispatchEvent(new CustomEvent("plugin-modal", { detail: entry }));
          break;
      }
    },
    [activeSessionId],
  );

  const shellTitle =
    shellMode === "yolo"
      ? "Shell: YOLO — no restrictions"
      : shellMode === "session"
        ? "Shell: Session — auto-approve, denylist active"
        : "Shell: Ask — confirm each command";

  const shellClass =
    shellMode === "yolo" ? "text-warning" : shellMode === "session" ? "text-primary" : "";

  const cycleEffort = useCallback(() => {
    if (!activeSessionId) return;
    const idx = EFFORT_LEVELS.findIndex((l) => l.value === activeEffort);
    const next = EFFORT_LEVELS[(idx + 1) % EFFORT_LEVELS.length];
    setActiveEffort(activeSessionId, next.value);
  }, [activeSessionId, activeEffort, setActiveEffort]);

  const activeEffortLevel = EFFORT_LEVELS.find((l) => l.value === activeEffort);

  return (
    <div className="flex items-center justify-between border-t border-divider pt-2 pr-2.5 pb-4 pl-3">
      {/* Layout menu — rendered at the toolbar level so the global ⌘\
          keyboard shortcut can drive it independently of the +
          popover's open state. The anchor div is invisible; LayoutMenu
          centers itself via portal regardless. */}
      <div ref={layoutAnchorRef} className="hidden" />
      <LayoutMenu
        open={layoutOpen}
        onClose={() => setLayoutOpen(false)}
        anchorRef={layoutAnchorRef}
      />
      {/* ── Left: + popover, effort cycle pill, model picker ── */}
      <div className="relative flex items-center gap-2">
        <ComposerPlusMenu
          onAttach={onAttach}
          onSlash={onSlash}
          onMention={onMention}
          shellMode={shellMode}
          onCycleShell={onCycleShell}
          shellTitle={shellTitle}
          shellClass={shellClass}
          uploading={uploading}
          layoutOpen={layoutOpen}
          onToggleLayout={() => setLayoutOpen((o) => !o)}
          pluginButtons={
            pluginButtons.length > 0 ? (
              <>
                <span className="mx-1 h-4 w-px bg-divider" />
                {pluginButtons.map((entry) => {
                  const PluginIcon = resolveIcon(entry.icon);
                  return (
                    <button
                      key={entry.id}
                      type="button"
                      onClick={() => handlePluginAction(entry)}
                      className="flex items-center gap-1.5 rounded-[4px] px-2 py-1 text-xs text-fg-muted transition-colors hover:bg-surface hover:text-fg"
                      role="menuitem"
                    >
                      <PluginIcon size={14} />
                      <span>{entry.label}</span>
                    </button>
                  );
                })}
              </>
            ) : null
          }
        />

        <span className="h-4 w-px bg-divider" />

        {/* Effort cycle pill — uppercase, no glyph */}
        <Tooltip
          content={activeEffortLevel?.title ?? "Effort: per-turn token budget and reasoning dial"}
          side="top"
        >
          <button
            type="button"
            onClick={cycleEffort}
            className="rounded-[6px] border border-divider px-2.5 py-0.5 font-mono text-[10.5px] uppercase tracking-wider text-fg-secondary transition-colors hover:bg-surface"
          >
            {(activeEffortLevel?.label ?? "Norm").toUpperCase()}
          </button>
        </Tooltip>

      </div>

      {/* ── Right: send / stop ── */}
      <div className="flex items-center gap-2">
        {isStreaming ? (
          <Tooltip content="Stop generating" side="top">
            <button
              type="button"
              onClick={onStop}
              className="flex h-7 w-7 items-center justify-center rounded-[6px] text-danger transition-colors hover:bg-surface"
            >
              <Square size={14} />
            </button>
          </Tooltip>
        ) : (
          <Tooltip content="Send (Enter)" side="top">
            <button
              type="button"
              onClick={onSend}
              disabled={!hasContent}
              className={`flex h-7 w-7 items-center justify-center rounded-[6px] border transition-colors ${
                hasContent
                  ? "bg-brand text-brand-fg border-brand hover:bg-brand-hover hover:border-brand-hover"
                  : "cursor-default bg-brand-muted text-brand border-brand-muted"
              }`}
            >
              <ArrowBigUp size={16} strokeWidth={2.5} />
            </button>
          </Tooltip>
        )}
      </div>
    </div>
  );
}
