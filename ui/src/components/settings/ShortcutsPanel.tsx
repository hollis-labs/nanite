import { Check, Keyboard, Lock, RotateCcw, X } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Kbd, KbdGroup } from "@/components/ui/kbd";
import { usePluginKeybindings } from "@/hooks/useKeyboardShortcuts";
import { useSettings, useSettingsMutation } from "@/hooks/useSettings";
import type { UserSettings } from "@/lib/types";

export const SHORTCUT_DEFS = [
  {
    key: "toggle_left_sidebar",
    label: "Toggle Sidebar",
    description: "Show or hide the sessions panel",
    default: "mod+b",
  },
  {
    key: "toggle_right_rail",
    label: "Toggle Widgets",
    description: "Show or hide the widgets panel",
    default: "mod+/",
  },
  {
    key: "focus_composer",
    label: "Focus Composer",
    description: "Jump to the message input",
    default: "mod+l",
  },
  {
    key: "new_session",
    label: "New Chat",
    description: "Create a new chat session",
    default: "mod+n",
  },
  {
    key: "command_palette",
    label: "Command Palette",
    description: "Open the command palette",
    default: "mod+k",
  },
  {
    key: "search",
    label: "Search Chats",
    description: "Quick search for chats (double-tap Shift)",
    default: "shift+shift",
  },
  {
    key: "next_session",
    label: "Next Session",
    description: "Switch to the next session",
    default: "mod+]",
  },
  {
    key: "prev_session",
    label: "Previous Session",
    description: "Switch to the previous session",
    default: "mod+[",
  },
  {
    key: "bookmark_last",
    label: "Bookmark Last",
    description: "Save the last assistant response",
    default: "mod+d",
  },
  {
    key: "toggle_artifacts",
    label: "Toggle Artifacts",
    description: "Show or hide the artifacts panel",
    default: "mod+.",
  },
] as const;

const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.userAgent);

export function formatKeys(binding: string): string[] {
  if (binding === "shift+shift") {
    const s = isMac ? "\u21E7" : "Shift";
    return [s, s];
  }
  return binding.split("+").map((part) => {
    if (part === "mod") return isMac ? "\u2318" : "Ctrl";
    if (part === "shift") return isMac ? "\u21E7" : "Shift";
    if (part === "alt") return isMac ? "\u2325" : "Alt";
    return part.toUpperCase();
  });
}

function KeyBadge({ keys, active }: { keys: string[]; active?: boolean }) {
  return (
    <KbdGroup>
      {keys.map((k, i) => (
        <Kbd
          key={i}
          className={`min-w-7 h-7 px-2 text-xs font-mono ${
            active ? "bg-primary/10 border-primary/30 text-primary" : ""
          }`}
        >
          {k}
        </Kbd>
      ))}
    </KbdGroup>
  );
}

/** Returns binding string, undefined for modifier-only (ignore), or null for cancel (Escape). */
function keyEventToBinding(e: KeyboardEvent): string | null | undefined {
  // Modifier-only presses — ignore, don't cancel
  if (["Meta", "Control", "Shift", "Alt"].includes(e.key)) return undefined;

  const parts: string[] = [];
  if (e.metaKey || e.ctrlKey) parts.push("mod");
  if (e.shiftKey) parts.push("shift");
  if (e.altKey) parts.push("alt");

  let key = e.key.toLowerCase();
  if (key === " ") key = "space";
  if (key === "escape") return null; // explicit cancel
  parts.push(key);

  return parts.join("+");
}

function ShortcutCard({
  label,
  description,
  binding,
  isEditing,
  onEdit,
  onSave,
  onCancel,
}: {
  label: string;
  description: string;
  binding: string;
  isEditing: boolean;
  onEdit: () => void;
  onSave: (newBinding: string) => void;
  onCancel: () => void;
}) {
  const [captured, setCaptured] = useState<string | null>(null);

  useEffect(() => {
    if (!isEditing) {
      setCaptured(null);
      return;
    }

    function handleKeyDown(e: KeyboardEvent) {
      e.preventDefault();
      e.stopPropagation();
      const result = keyEventToBinding(e);
      if (result === undefined) return; // modifier-only, ignore
      if (result === null) {
        onCancel();
        return;
      }
      setCaptured(result);
    }

    window.addEventListener("keydown", handleKeyDown, true);
    return () => window.removeEventListener("keydown", handleKeyDown, true);
  }, [isEditing, onCancel]);

  return (
    <div
      onClick={() => !isEditing && onEdit()}
      className={`rounded-xl border overflow-hidden transition-all cursor-pointer ${
        isEditing
          ? "border-primary/40 shadow-sm ring-1 ring-primary/20"
          : "border-border-subtle bg-white dark:bg-bg-elevated/60 hover:border-border-subtle hover:shadow-sm"
      }`}
    >
      <div className="flex items-center gap-3 px-3.5 py-3">
        <div className="flex-1 min-w-0">
          <div className="text-sm font-medium text-fg">{label}</div>
          <div className="text-[11px] text-fg-muted mt-0.5">{description}</div>
        </div>

        {isEditing ? (
          <div className="flex items-center gap-2 shrink-0">
            {captured ? (
              <>
                <KeyBadge keys={formatKeys(captured)} active />
                <Button
                  variant="ghost"
                  size="icon"
                  className="w-7 h-7 text-success hover:bg-success-muted"
                  onClick={(e) => {
                    e.stopPropagation();
                    onSave(captured);
                  }}
                >
                  <Check className="w-3.5 h-3.5" />
                </Button>
              </>
            ) : (
              <span className="text-xs text-fg-muted italic px-2">Press keys...</span>
            )}
            <Button
              variant="ghost"
              size="icon"
              className="w-7 h-7 text-fg-faint hover:text-fg-secondary"
              onClick={(e) => {
                e.stopPropagation();
                onCancel();
              }}
            >
              <X className="w-3.5 h-3.5" />
            </Button>
          </div>
        ) : (
          <KeyBadge keys={formatKeys(binding)} />
        )}
      </div>
    </div>
  );
}

function PluginKeybindingCard({
  label,
  description,
  binding,
}: {
  label: string;
  description: string;
  binding: string;
}) {
  return (
    <div className="rounded-xl border border-border-subtle bg-white dark:bg-bg-elevated/60 overflow-hidden opacity-75">
      <div className="flex items-center gap-3 px-3.5 py-3">
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-1.5">
            <span className="text-sm font-medium text-fg">{label}</span>
            <Lock className="w-3 h-3 text-fg-faint" />
          </div>
          <div className="text-[11px] text-fg-muted mt-0.5">{description}</div>
        </div>
        <KeyBadge keys={formatKeys(binding)} />
      </div>
    </div>
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

  const handleSave = useCallback(
    (key: string, newBinding: string) => {
      const updated = { ...shortcuts, [key]: newBinding };
      mutation.mutate({
        ext_settings: {
          ...settings?.ext_settings,
          shortcuts: updated,
        },
      } as Partial<UserSettings>);
      setEditingKey(null);
    },
    [shortcuts, settings?.ext_settings, mutation],
  );

  const handleResetAll = useCallback(() => {
    const defaults: Record<string, string> = {};
    for (const def of SHORTCUT_DEFS) {
      defaults[def.key] = def.default;
    }
    mutation.mutate({
      ext_settings: {
        ...settings?.ext_settings,
        shortcuts: defaults,
      },
    } as Partial<UserSettings>);
  }, [settings?.ext_settings, mutation]);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Keyboard className="w-4 h-4 text-fg-muted" />
          <p className="text-xs text-fg-muted">Click any shortcut to rebind it</p>
        </div>
        <Button
          variant="ghost"
          size="sm"
          className="text-xs text-fg-secondary hover:text-fg"
          onClick={handleResetAll}
        >
          <RotateCcw className="w-3 h-3 mr-1.5" />
          Reset All
        </Button>
      </div>

      <div className="grid grid-cols-2 gap-2">
        {SHORTCUT_DEFS.map((def) => (
          <ShortcutCard
            key={def.key}
            label={def.label}
            description={def.description}
            binding={getBinding(def.key, def.default)}
            isEditing={editingKey === def.key}
            onEdit={() => setEditingKey(def.key)}
            onSave={(newBinding) => handleSave(def.key, newBinding)}
            onCancel={() => setEditingKey(null)}
          />
        ))}
      </div>

      {/* Plugin keybindings (read-only) */}
      {pluginKeybindings && pluginKeybindings.length > 0 && (
        <>
          <div className="flex items-center gap-2 pt-2">
            <Lock className="w-3.5 h-3.5 text-fg-faint" />
            <p className="text-xs text-fg-muted">Plugin keybindings (read-only)</p>
          </div>
          <div className="grid grid-cols-2 gap-2">
            {pluginKeybindings.map((kb) => (
              <PluginKeybindingCard
                key={kb.id}
                label={kb.label}
                description={kb.description || kb.action_value}
                binding={kb.key}
              />
            ))}
          </div>
        </>
      )}

      <p className="text-xs text-fg-faint">
        {isMac ? "\u2318 = Command" : "Mod = Ctrl"} &middot; Changes are saved automatically
      </p>
    </div>
  );
}
