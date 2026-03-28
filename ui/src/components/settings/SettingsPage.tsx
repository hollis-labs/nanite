import { useState } from 'react'
import { PreferencesPanel } from './PreferencesPanel'
import { ShortcutsPanel } from './ShortcutsPanel'
import { AgentProfileManager } from './AgentProfileManager'
import { PromptTemplateEditor } from './PromptTemplateEditor'
import { SkillsBrowser } from './SkillsBrowser'
import { ToolDashboard } from './ToolDashboard'
import { PluginManager } from './PluginManager'
import { Button } from '@/components/ui/Button'

type SettingsSection = 'preferences' | 'shortcuts' | 'agents' | 'skills' | 'prompts' | 'tools' | 'plugins'

export default function SettingsPage() {
  const [activeSection, setActiveSection] = useState<SettingsSection>('preferences')

  const sections = [
    { id: 'preferences' as SettingsSection, label: 'Preferences' },
    { id: 'shortcuts' as SettingsSection, label: 'Shortcuts' },
    { id: 'agents' as SettingsSection, label: 'Agents' },
    { id: 'skills' as SettingsSection, label: 'Skills' },
    { id: 'prompts' as SettingsSection, label: 'Prompts' },
    { id: 'tools' as SettingsSection, label: 'Tools' },
    { id: 'plugins' as SettingsSection, label: 'Plugins' },
  ]

  const renderActiveSection = () => {
    switch (activeSection) {
      case 'preferences':
        return <PreferencesPanel />
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
      default:
        return <PreferencesPanel />
    }
  }

  return (
    <div className="flex-1 flex flex-col h-full bg-zinc-950">
      {/* Header with tabs */}
      <div className="border-b border-zinc-800 px-6 py-4">
        <h1 className="text-2xl font-semibold text-zinc-100 mb-4">Settings</h1>
        <div className="flex gap-1">
          {sections.map((section) => (
            <Button
              key={section.id}
              variant={activeSection === section.id ? "default" : "ghost"}
              size="sm"
              onClick={() => setActiveSection(section.id)}
              className={`${
                activeSection === section.id
                  ? 'bg-indigo-600 text-white'
                  : 'text-zinc-400 hover:text-zinc-100'
              }`}
            >
              {section.label}
            </Button>
          ))}
        </div>
      </div>

      {/* Content */}
      <div className="flex-1 overflow-y-auto">
        <div className="p-6">
          {renderActiveSection()}
        </div>
      </div>
    </div>
  );
}
