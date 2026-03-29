import { useState, useEffect } from 'react'
import {
  SlidersHorizontal,
  Cpu,
  Keyboard,
  Bot,
  Sparkles,
  FileText,
  Wrench,
  Puzzle,
  LayoutGrid,
  Activity,
} from 'lucide-react'
import {
  getInitialSettingsSection,
  setSettingsSectionCallback,
  updateSettingsHash,
} from '@/hooks/useHashRoute'
import { PreferencesPanel } from './PreferencesPanel'
import { ShortcutsPanel } from './ShortcutsPanel'
import { AgentProfileManager } from './AgentProfileManager'
import { PromptTemplateEditor } from './PromptTemplateEditor'
import { SkillsBrowser } from './SkillsBrowser'
import { ToolDashboard } from './ToolDashboard'
import { PluginManager } from './PluginManager'
import { ProviderManager } from './ProviderManager'
import { WidgetManager } from './WidgetManager'
import { ObservabilityDashboard } from './observability/ObservabilityDashboard'
import { ScrollArea } from '@/components/ui/ScrollArea'

type SettingsSection = 'preferences' | 'providers' | 'shortcuts' | 'agents' | 'skills' | 'prompts' | 'tools' | 'plugins' | 'widgets' | 'observability'

const sections: { id: SettingsSection; label: string; icon: typeof SlidersHorizontal }[] = [
  { id: 'preferences', label: 'Preferences', icon: SlidersHorizontal },
  { id: 'providers', label: 'Providers', icon: Cpu },
  { id: 'shortcuts', label: 'Shortcuts', icon: Keyboard },
  { id: 'agents', label: 'Agents', icon: Bot },
  { id: 'skills', label: 'Skills', icon: Sparkles },
  { id: 'prompts', label: 'Prompts', icon: FileText },
  { id: 'tools', label: 'Tools', icon: Wrench },
  { id: 'plugins', label: 'Plugins', icon: Puzzle },
  { id: 'widgets', label: 'Widgets', icon: LayoutGrid },
  { id: 'observability', label: 'Observability', icon: Activity },
]

export default function SettingsPage() {
  const [activeSection, setActiveSection] = useState<SettingsSection>(
    () => getInitialSettingsSection() ?? 'preferences',
  )

  useEffect(() => {
    setSettingsSectionCallback((section) => setActiveSection(section))
    return () => setSettingsSectionCallback(null)
  }, [])

  const handleSectionChange = (section: SettingsSection) => {
    setActiveSection(section)
    updateSettingsHash(section)
  }

  const renderActiveSection = () => {
    switch (activeSection) {
      case 'preferences':
        return <PreferencesPanel />
      case 'providers':
        return <ProviderManager />
      case 'shortcuts':
        return <ShortcutsPanel />
      case 'agents':
        return <AgentProfileManager />
      case 'skills':
        return <SkillsBrowser />
      case 'prompts':
        return <PromptTemplateEditor />
      case 'tools':
        return <ToolDashboard />
      case 'plugins':
        return <PluginManager />
      case 'widgets':
        return <WidgetManager />
      case 'observability':
        return <ObservabilityDashboard />
      default:
        return <PreferencesPanel />
    }
  }

  const active = sections.find((s) => s.id === activeSection) ?? sections[0]

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
              const Icon = section.icon
              const isActive = activeSection === section.id
              return (
                <button
                  key={section.id}
                  onClick={() => handleSectionChange(section.id)}
                  className={`w-full flex items-center gap-2.5 px-3 py-2 rounded-md text-sm transition-colors ${
                    isActive
                      ? 'bg-surface text-fg'
                      : 'text-fg-secondary hover:text-fg hover:bg-surface/50'
                  }`}
                >
                  <Icon className={`w-4 h-4 shrink-0 ${isActive ? 'text-accent' : ''}`} />
                  {section.label}
                </button>
              )
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
          <div className="p-6 max-w-4xl">
            {renderActiveSection()}
          </div>
        </ScrollArea>
      </div>
    </div>
  )
}
