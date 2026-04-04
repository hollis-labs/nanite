import type { LucideIcon } from "lucide-react";
import {
  Activity,
  Bot,
  Building2,
  Calendar,
  ClipboardList,
  Code,
  Cpu,
  FileText,
  GitBranch,
  Keyboard,
  LayoutGrid,
  MessageSquare,
  Notebook,
  Package,
  Plus,
  Puzzle,
  Search,
  Settings,
  SlidersHorizontal,
  Sparkles,
  Wrench,
} from "lucide-react";

/** Map of Lucide icon names (kebab-case) to their component. */
const ICON_MAP: Record<string, LucideIcon> = {
  activity: Activity,
  bot: Bot,
  building2: Building2,
  calendar: Calendar,
  "clipboard-list": ClipboardList,
  code: Code,
  cpu: Cpu,
  "file-text": FileText,
  "git-branch": GitBranch,
  keyboard: Keyboard,
  "layout-grid": LayoutGrid,
  "message-square": MessageSquare,
  notebook: Notebook,
  package: Package,
  plus: Plus,
  puzzle: Puzzle,
  search: Search,
  settings: Settings,
  "sliders-horizontal": SlidersHorizontal,
  sparkles: Sparkles,
  wrench: Wrench,
};

/**
 * Resolve a Lucide icon name string to a component.
 * Returns the Puzzle icon as fallback for unknown names.
 */
export function resolveIcon(name: string | undefined): LucideIcon {
  if (!name) return Puzzle;
  return ICON_MAP[name] ?? Puzzle;
}
