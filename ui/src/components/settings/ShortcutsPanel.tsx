import { Check, Lock, RotateCcw, X } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { usePluginKeybindings } from "@/hooks/useKeyboardShortcuts";
import { useSettings, useSettingsMutation } from "@/hooks/useSettings";
import type { UserSettings } from "@/lib/types";
import { Kbd, KbdGroup, SCard, SRow } from "./primitives";

export const SHORTCUT_DEFS = [
  { key: "toggle_left_sidebar", group: "navigation", label: "Toggle Sidebar", description: "Show or hide the sessions panel", default: "mod+b" },
  { key: "toggle_right_rail", group: "navigation", label: "Toggle Widgets", description: "Show or hide the widgets panel", default: "mod+/" },
  { key: "next_session", group: "navigation", label: "Next Session", description: "Switch to the next session", default: "mod+]" },
  { key: "prev_session", group: "navigation", label: "Previous Session", description: "Switch to the previous session", default: "mod+[" },
  { key: "toggle_artifacts", group: "navigation", label: "Toggle Artifacts", description: "Show or hide the artifacts panel", default: "mod+." },
  { key: "new_session", group: "sessions", label: "New Chat", description: "Create a new chat session", default: "mod+n" },
  { key: "search", group: "sessions", label: "Search Chats", description: "Quick search for chats (double-tap Shift)", default: "shift+shift" },
  { key: "command_palette", group: "sessions", label: "Command Palette", description: "Open the command palette", default: "mod+k" },
  { key: "focus_composer", group: "actions", label: "Focus Composer", description: "Jump to the message input", default: "mod+l" },
  { key: "bookmark_last", group: "actions", label: "Bookmark Last", description: "Save the last assistant response", default: "mod+d" },
] as const;

const SHORTCUT_GROUPS = [
  { id: "navigation", label: "Navigation" },
  { id: "sessions", label: "Sessions" },
  { id: "actions", label: "Actions" },
] as const;

const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.userAgent);

export function formatKeys(binding: string): string[] {
  if (binding === "shift+shift") {
    const s = isMac ? "⇧" : "Shift";
    return [s, s];
  }
  return binding.split("+").map((part) => {
    if (part === "mod") return isMac ? "⌘" : "Ctrl";
    if (part === "shift") return isMac ? "⇧" : "Shift";
    if (part === "alt") return isMac ? "⌥" : "Alt";
    return part.toUpperCase();
  });
}

function keyEventToBinding(e: KeyboardEvent): string | null | undefined {
  if (["Meta", "Control", "Shift", "Alt"].includes(e.key)) return undefined;
  const parts: string[] = [];
  if (e.metaKey || e.ctrlKey) parts.push("mod");
  if (e.shiftKey) parts.push("shift");
  if (e.altKey) parts.push("alt");
  let key = e.key.toLowerCase();
  if (key === " ") key = "space";
  if (key === "escape") return null;
  parts.push(key);
  return parts.join("+");
}

function ShortcutRow({
  label, description, binding, isEditing, onEdit, onSave, onCancel,
}: {
  label: string; description: string; binding: string;
  isEditing: boolean; onEdit: () => void; onSave: (b: string) => void; onCancel: () => void;
}) {
  const [captured, setCaptured] = useState<string | null>(null);

  useEffect(() => {
    if (!isEditing) { setCaptured(null); return; }
    function handleKeyDown(e: KeyboardEvent) {
      e.preventDefault(); e.stopPropagation();
      const result = keyEventToBinding(e);
      if (result === undefined) return;
      if (result === null) { onCancel(); return; }
      setCaptured(result);
    }
    window.addEventListener("keydown", handleKeyDown, true);
    return () => window.removeEventListener("keydown", handleKeyDown, true);
  }, [isEditing, onCancel]);

  return (
    <SRow
      label={label}
      description={description}
      className={isEditing ? "ring-inset ring-1 ring-primary/30 bg-primary-muted" : "cursor-pointer hover:bg-surface"}
      onClick={() => { if (!isEditing) onEdit(); }}
    >
      {isEditing ? (
        <div className="flex items-center gap-2">
          {captured ? (
            <>
              <KbdGroup>
                {formatKeys(captured).map((k, i) => <Kbd key={i} active>{k}</Kbd>)}
              </KbdGroup>
              <Button variant="ghost" size="icon" className="w-6 h-6 text-success hover:bg-success-muted"
                onClick={(e) => { e.stopPropagation(); onSave(captured); }}>
                <Check className="w-3 h-3" />
              </Button>
            </>
          ) : (
            <span className="text-xs text-fg-muted italic">Press keys…</span>
          )}
          <Button variant="ghost" size="icon" className="w-6 h-6 text-fg-faint hover:text-fg-secondary"
            onClick={(e) => { e.stopPropagation(); onCancel(); }}>
            <X className="w-3 h-3" />
          </Button>
        </div>
      ) : (
        <KbdGroup>
          {formatKeys(binding).map((k, i) => <Kbd key={i}>{k}</Kbd>)}
        </KbdGroup>
      )}
    </SRow>
  );
}

export function ShortcutsPanel() {
  const { data: settings } = useSettings();
  const mutation = useSettingsMutation();
  const [editingKey, setEditingKey] = useState<string | null>(null);
  const { data: pluginKeybindings } = usePluginKeybindings();

  const shortcuts: Record<string, string> =
    (settings?.ext_settings?.shortcuts as Record<string, string>) ?? {};

  const getBinding = useCallback(
    (key: string, defaultValue: string) => shortcuts[key] ?? defaultValue,
    [shortcuts],
  );

  const handleSave = useCallback((key: string, newBinding: string) => {
    mutation.mutate({
      ext_settings: { ...settings?.ext_settings, shortcuts: { ...shortcuts, [key]: newBinding } },
    } as Partial<UserSettings>);
    setEditingKey(null);
  }, [shortcuts, settings?.ext_settings, mutation]);

  const handleResetAll = useCallback(() => {
    const defaults: Record<string, string> = {};
    for (const def of SHORTCUT_DEFS) defaults[def.key] = def.default;
    mutation.mutate({
      ext_settings: { ...settings?.ext_settings, shortcuts: defaults },
    } as Partial<UserSettings>);
  }, [settings?.ext_settings, mutation]);

  return (
    <div className="space-y-0">
      {SHORTCUT_GROUPS.map((group) => {
        const groupDefs = SHORTCUT_DEFS.filter((d) => d.group === group.id);
        return (
          <SCard key={group.id} title={group.label}>
            {groupDefs.map((def) => (
              <ShortcutRow
                key={def.key}
                label={def.label}
                description={def.description}
                binding={getBinding(def.key, def.default)}
                isEditing={editingKey === def.key}
                onEdit={() => setEditingKey(def.key)}
                onSave={(b) => handleSave(def.key, b)}
                onCancel={() => setEditingKey(null)}
              />
            ))}
          </SCard>
        );
      })}

      {pluginKeybindings && pluginKeybindings.length > 0 && (
        <SCard title="Plugin Shortcuts" meta="read-only">
          {pluginKeybindings.map((kb) => (
            <SRow key={kb.id} label={kb.label} description={kb.description || kb.action_value} className="opacity-75">
              <div className="flex items-center gap-1.5">
                <Lock className="w-3 h-3 text-fg-faint" />
                <KbdGroup>
                  {formatKeys(kb.key).map((k, i) => <Kbd key={i}>{k}</Kbd>)}
                </KbdGroup>
              </div>
            </SRow>
          ))}
        </SCard>
      )}

      <div className="border border-dashed border-border rounded-[8px] p-3 flex justify-between items-center">
        <p className="text-xs text-fg-muted">
          {isMac ? "⌘ = Command" : "Mod = Ctrl"} · Click any shortcut to rebind · Changes save automatically
        </p>
        <button
          onClick={handleResetAll}
          className="flex items-center gap-1.5 text-xs text-fg-secondary hover:text-fg transition-colors"
        >
          <RotateCcw className="w-3 h-3" />
          Reset All
        </button>
      </div>
    </div>
  );
}
