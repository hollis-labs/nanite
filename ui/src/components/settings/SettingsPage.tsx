import {
  Activity,
  Bot,
  Building2,
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

const CORE_SECTIONS: { id: string; label: string; icon: LucideIcon }[] = [
  { id: "profile", label: "Profile", icon: User },
  { id: "preferences", label: "Preferences", icon: SlidersHorizontal },
  { id: "appearance", label: "Appearance", icon: Palette },
  { id: "providers", label: "Providers", icon: Cpu },
  { id: "shortcuts", label: "Shortcuts", icon: Keyboard },
  { id: "actions", label: "Actions", icon: Zap },
  { id: "workspaces", label: "Workspaces", icon: Building2 },
  { id: "agents", label: "Agents", icon: Bot },
  { id: "skills", label: "Skills", icon: Sparkles },
  { id: "prompts", label: "Prompts", icon: FileText },
  { id: "tools", label: "Tools", icon: Wrench },
  { id: "plugins", label: "Plugins", icon: Puzzle },
  { id: "widgets", label: "Widgets", icon: LayoutGrid },
  { id: "observability", label: "Observability", icon: Activity },
];

export default function SettingsPage() {
  const [activeSection, setActiveSection] = useState<string>(
    () => getInitialSettingsSection() ?? "profile",
  );
  // Bump key to force remount when clicking the same section (resets sub-views)
  const [sectionKey, setSectionKey] = useState(0);
  const pluginTabs = usePluginSlots("settings-tab");

  // Merge core + plugin tabs
  const sections: { id: string; label: string; icon: LucideIcon }[] = [
    ...CORE_SECTIONS,
    ...pluginTabs.map((entry) => ({
      id: entry.id,
      label: entry.label,
      icon: resolveIcon(entry.icon),
    })),
  ];

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
      case "observability":
        return <ObservabilityDashboard />;
      default: {
        // Check for plugin-registered settings tab component
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

  const active = sections.find((s) => s.id === activeSection) ?? sections[0];

  return (
    <div className="flex-1 flex h-full bg-bg">
      {/* Sidebar nav */}
      <nav className="w-52 shrink-0 border-r border-border flex flex-col">
        <div className="px-5 h-12 flex items-center border-b border-border shrink-0">
          <h1 className="text-sm font-semibold text-fg">Settings</h1>
        </div>
        <ScrollArea className="flex-1">
          <div className="py-2 px-2">
            {sections.map((section) => {
              const Icon = section.icon;
              const isActive = activeSection === section.id;
              return (
                <button
                  key={section.id}
                  onClick={() => handleSectionChange(section.id)}
                  className={`w-full flex items-center gap-2.5 px-3 py-2 rounded-md text-sm transition-colors ${
                    isActive
                      ? "bg-surface text-fg"
                      : "text-fg-secondary hover:text-fg hover:bg-surface/50"
                  }`}
                >
                  <Icon className={`w-4 h-4 shrink-0 ${isActive ? "text-primary" : ""}`} />
                  {section.label}
                </button>
              );
            })}
          </div>
        </ScrollArea>
      </nav>

      {/* Content area */}
      <div className="flex-1 flex flex-col min-w-0">
        <div className="px-6 h-12 flex items-center border-b border-border shrink-0">
          <h2 className="text-sm font-semibold text-fg">{active.label}</h2>
        </div>
        <ScrollArea className="flex-1">
          <div key={`${activeSection}-${sectionKey}`} className="p-6 max-w-4xl">
            {renderActiveSection()}
          </div>
        </ScrollArea>
      </div>
    </div>
  );
}
