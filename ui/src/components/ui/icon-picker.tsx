import { icons, type LucideIcon } from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { ScrollArea } from "@/components/ui/scroll-area";

// Curated set of useful icons for entities (agents, skills, prompts, plugins, workspaces, projects)
const CURATED_ICONS = [
  // People & agents
  "Bot",
  "User",
  "Users",
  "UserCog",
  "Brain",
  "Sparkles",
  "Zap",
  "Shield",
  // Dev & tools
  "Code2",
  "Terminal",
  "Wrench",
  "Settings",
  "Cog",
  "Hammer",
  "Puzzle",
  "Blocks",
  // Files & content
  "FileText",
  "FileCode",
  "FolderKanban",
  "Folder",
  "BookOpen",
  "Notebook",
  "Scroll",
  "Clipboard",
  // Communication
  "MessageSquare",
  "MessageCircle",
  "Mail",
  "Send",
  "Megaphone",
  "Bell",
  "Radio",
  // Data & analysis
  "Database",
  "BarChart3",
  "LineChart",
  "PieChart",
  "Activity",
  "Gauge",
  "Search",
  "Filter",
  // Navigation & UI
  "Compass",
  "Map",
  "Globe",
  "Layers",
  "Layout",
  "Grid3x3",
  "Boxes",
  "Package",
  // Actions
  "Play",
  "Rocket",
  "Target",
  "Flag",
  "Award",
  "Star",
  "Heart",
  "Bookmark",
  // Nature & misc
  "Sun",
  "Moon",
  "Cloud",
  "Flame",
  "Leaf",
  "Mountain",
  "Gem",
  "Crown",
  // Arrows & flow
  "GitBranch",
  "GitMerge",
  "Workflow",
  "Route",
  "Repeat",
  "RefreshCw",
  "Shuffle",
  // Security & auth
  "Lock",
  "Key",
  "Eye",
  "ShieldCheck",
  "Fingerprint",
  // Media
  "Image",
  "Camera",
  "Video",
  "Music",
  "Palette",
] as const;

function getIcon(name: string): LucideIcon | null {
  return (icons as Record<string, LucideIcon>)[name] ?? null;
}

interface IconPickerProps {
  value: string;
  onChange: (iconName: string) => void;
  disabled?: boolean;
}

export function IconPicker({ value, onChange, disabled }: IconPickerProps) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");

  const filteredIcons = useMemo(() => {
    if (!search) return CURATED_ICONS as unknown as string[];
    const q = search.toLowerCase();
    return (CURATED_ICONS as unknown as string[]).filter((name) => name.toLowerCase().includes(q));
  }, [search]);

  const handleSelect = useCallback(
    (name: string) => {
      onChange(name);
      setOpen(false);
      setSearch("");
    },
    [onChange],
  );

  const handleClear = useCallback(() => {
    onChange("");
    setOpen(false);
  }, [onChange]);

  const SelectedIcon = value ? getIcon(value) : null;

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          disabled={disabled}
          className="w-9 h-9 rounded-lg border border-border-subtle bg-white dark:bg-bg-elevated/60"
          title={value || "Choose icon"}
        >
          {SelectedIcon ? (
            <SelectedIcon className="w-4 h-4 text-fg-secondary" />
          ) : (
            <span className="text-xs text-fg-faint">?</span>
          )}
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-72 p-0" align="start">
        <div className="p-2 border-b border-border">
          <input
            type="text"
            placeholder="Search icons..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="w-full bg-surface/50 border border-border rounded-md px-2.5 py-1.5 text-xs text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-accent/50"
            autoFocus
          />
        </div>
        <ScrollArea className="h-56">
          <div className="grid grid-cols-8 gap-0.5 p-2">
            {filteredIcons.map((name) => {
              const Icon = getIcon(name);
              if (!Icon) return null;
              const isSelected = value === name;
              return (
                <button
                  key={name}
                  type="button"
                  onClick={() => handleSelect(name)}
                  className={`w-8 h-8 flex items-center justify-center rounded-md transition-colors ${
                    isSelected
                      ? "bg-accent/15 text-accent ring-1 ring-accent/30"
                      : "text-fg-secondary hover:bg-surface-hover hover:text-fg"
                  }`}
                  title={name}
                >
                  <Icon className="w-4 h-4" />
                </button>
              );
            })}
          </div>
          {filteredIcons.length === 0 && (
            <p className="text-xs text-fg-muted text-center py-4">No icons match "{search}"</p>
          )}
        </ScrollArea>
        {value && (
          <div className="p-2 border-t border-border flex items-center justify-between">
            <span className="text-xs text-fg-muted">{value}</span>
            <button
              type="button"
              onClick={handleClear}
              className="text-xs text-fg-faint hover:text-fg transition-colors"
            >
              Clear
            </button>
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}

/** Render a Lucide icon by name string, with fallback. */
export function DynamicIcon({
  name,
  className,
  fallback,
}: {
  name: string;
  className?: string;
  fallback?: LucideIcon;
}) {
  const Icon = name ? getIcon(name) : null;
  const FallbackIcon = fallback;
  if (Icon) return <Icon className={className} />;
  if (FallbackIcon) return <FallbackIcon className={className} />;
  return null;
}
