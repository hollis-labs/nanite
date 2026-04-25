import {
  Activity,
  Bot,
  Brain,
  Building2,
  ChevronRight,
  Cpu,
  FileText,
  Keyboard,
  LayoutGrid,
  Palette,
  Puzzle,
  SlidersHorizontal,
  Sparkles,
  User,
  Wrench,
  Zap,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { Suspense, lazy, useEffect, useState } from "react";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Skeleton } from "@/components/ui/skeleton";
import {
  getInitialSettingsSection,
  setSettingsSectionCallback,
  updateSettingsHash,
} from "@/hooks/useHashRoute";
import { usePluginSlots } from "@/hooks/usePluginSlots";
import { resolveIcon } from "@/lib/icons";
import { getSlotComponent } from "@/lib/plugin-slot-lookup";
import { useNavigationStore } from "@/stores/useNavigationStore";
import { ActionsPanel } from "./ActionsPanel";
import { AgentProfileManager } from "./AgentProfileManager";
import { MemoryPanel } from "./MemoryPanel";

const AppearancePanel = lazy(() =>
  import("./appearance/AppearancePanel").then((m) => ({ default: m.AppearancePanel })),
);
import { ObservabilityDashboard } from "./observability/ObservabilityDashboard";
import { PluginManager } from "./PluginManager";
import { PreferencesPanel } from "./PreferencesPanel";
import { PromptTemplateEditor } from "./PromptTemplateEditor";
import { ProviderManager } from "./ProviderManager";
import { ShortcutsPanel } from "./ShortcutsPanel";
import { SkillsBrowser } from "./SkillsBrowser";
import { ToolDashboard } from "./ToolDashboard";
import { WidgetManager } from "./WidgetManager";
import { WorkspaceProjectManager } from "./WorkspaceProjectManager";
import { ProfilePanel } from "./ProfilePanel";

interface NavItem {
  id: string;
  label: string;
  icon: LucideIcon;
}

interface NavGroup {
  label: string;
  items: NavItem[];
}

const NAV_GROUPS: NavGroup[] = [
  {
    label: "You",
    items: [
      { id: "profile", label: "Profile", icon: User },
      { id: "preferences", label: "Preferences", icon: SlidersHorizontal },
      { id: "appearance", label: "Appearance", icon: Palette },
      { id: "shortcuts", label: "Shortcuts", icon: Keyboard },
    ],
  },
  {
    label: "AI",
    items: [
      { id: "providers", label: "Providers", icon: Cpu },
      { id: "agents", label: "Agents", icon: Bot },
      { id: "skills", label: "Skills", icon: Sparkles },
      { id: "prompts", label: "Prompts", icon: FileText },
      { id: "memory", label: "Memory", icon: Brain },
    ],
  },
  {
    label: "Workspace",
    items: [
      { id: "workspaces", label: "Workspaces", icon: Building2 },
      { id: "actions", label: "Actions", icon: Zap },
    ],
  },
  {
    label: "Extensions",
    items: [
      { id: "tools", label: "Tools", icon: Wrench },
      { id: "plugins", label: "Plugins", icon: Puzzle },
      { id: "widgets", label: "Widgets", icon: LayoutGrid },
    ],
  },
  {
    label: "System",
    items: [{ id: "observability", label: "Observability", icon: Activity }],
  },
];

function findItemInGroups(
  id: string,
  groups: NavGroup[],
): { item: NavItem; group: NavGroup } | undefined {
  for (const group of groups) {
    const item = group.items.find((i) => i.id === id);
    if (item) return { item, group };
  }
  return undefined;
}

export default function SettingsPage() {
  const [activeSection, setActiveSection] = useState<string>(
    () => getInitialSettingsSection() ?? "profile",
  );
  const [sectionKey, setSectionKey] = useState(0);
  const pluginTabs = usePluginSlots("settings-tab");

  const pluginItems: NavItem[] = pluginTabs.map((entry) => ({
    id: entry.id,
    label: entry.label,
    icon: resolveIcon(entry.icon),
  }));

  const allGroups: NavGroup[] = pluginItems.length
    ? [...NAV_GROUPS, { label: "Plugins", items: pluginItems }]
    : NAV_GROUPS;

  useEffect(() => {
    setSettingsSectionCallback((section) => setActiveSection(section));
    return () => setSettingsSectionCallback(null);
  }, []);

  const navPush = useNavigationStore((s) => s.push);

  const handleSectionChange = (section: string) => {
    if (section === activeSection) {
      setSectionKey((k) => k + 1);
    }
    setActiveSection(section);
    updateSettingsHash(section as any);
    navPush({ view: `settings/${section}`, label: section });
  };

  const renderActiveSection = () => {
    switch (activeSection) {
      case "profile":
        return <ProfilePanel />;
      case "preferences":
        return <PreferencesPanel />;
      case "appearance":
        return (
          <Suspense fallback={<Skeleton className="h-64 w-full" />}>
            <AppearancePanel />
          </Suspense>
        );
      case "providers":
        return <ProviderManager />;
      case "shortcuts":
        return <ShortcutsPanel />;
      case "actions":
        return <ActionsPanel />;
      case "agents":
        return <AgentProfileManager />;
      case "skills":
        return <SkillsBrowser />;
      case "prompts":
        return <PromptTemplateEditor />;
      case "tools":
        return <ToolDashboard />;
      case "plugins":
        return <PluginManager />;
      case "widgets":
        return <WidgetManager />;
      case "workspaces":
        return <WorkspaceProjectManager />;
      case "memory":
        return <MemoryPanel />;
      case "observability":
        return <ObservabilityDashboard />;
      default: {
        const pluginEntry = pluginTabs.find((e) => e.id === activeSection);
        if (pluginEntry?.component) {
          const PluginComponent = getSlotComponent(pluginEntry.component);
          if (PluginComponent) {
            return (
              <Suspense fallback={<Skeleton className="h-32 w-full" />}>
                <PluginComponent {...(pluginEntry.props ?? {})} />
              </Suspense>
            );
          }
        }
        return <PreferencesPanel />;
      }
    }
  };

  const activeMatch = findItemInGroups(activeSection, allGroups);

  return (
    <div className="flex-1 flex h-full bg-bg">
      {/* Sidebar nav */}
      <nav className="w-52 shrink-0 border-r border-border flex flex-col">
        <div className="h-[52px] px-[18px] flex items-center border-b border-border-subtle shrink-0">
          <h1 className="text-[13px] font-semibold text-fg">Settings</h1>
        </div>
        <ScrollArea className="flex-1">
          <div className="py-3 px-2">
            {allGroups.map((group, gi) => (
              <div key={group.label} className={gi > 0 ? "mt-3.5" : ""}>
                <div className="font-mono text-[10px] font-semibold uppercase tracking-[0.06em] text-fg-faint px-2.5 py-1.5">
                  {group.label}
                </div>
                {group.items.map((item) => {
                  const Icon = item.icon;
                  const isActive = activeSection === item.id;
                  return (
                    <button
                      key={item.id}
                      onClick={() => handleSectionChange(item.id)}
                      className={`mb-px w-full flex items-center gap-2.5 px-2.5 py-1.5 rounded-[6px] text-[13px] transition-colors ${
                        isActive
                          ? "bg-surface text-fg font-medium"
                          : "text-fg-secondary hover:bg-surface hover:text-fg"
                      }`}
                    >
                      <Icon
                        style={{ width: 14, height: 14 }}
                        className={`shrink-0 ${isActive ? "text-primary" : "text-fg-muted"}`}
                      />
                      {item.label}
                    </button>
                  );
                })}
              </div>
            ))}
          </div>
        </ScrollArea>
      </nav>

      {/* Content area */}
      <div className="flex-1 flex flex-col min-w-0">
        {/* Breadcrumb header */}
        <div className="h-[52px] px-7 flex items-center gap-1.5 border-b border-border-subtle shrink-0">
          {activeMatch ? (
            <>
              <span className="font-mono text-[12px] text-fg-faint uppercase tracking-[0.04em]">
                {activeMatch.group.label}
              </span>
              <ChevronRight style={{ width: 11, height: 11 }} className="text-fg-faint shrink-0" />
              <span className="text-[13px] text-fg font-medium">{activeMatch.item.label}</span>
            </>
          ) : (
            <span className="text-[13px] text-fg font-medium">{activeSection}</span>
          )}
        </div>
        <ScrollArea className="flex-1">
          <div
            key={`${activeSection}-${sectionKey}`}
            className="max-w-[720px] px-7 pt-7 pb-16"
          >
            {renderActiveSection()}
          </div>
        </ScrollArea>
      </div>
    </div>
  );
}
