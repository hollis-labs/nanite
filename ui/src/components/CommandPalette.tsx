import { useState, useCallback, useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  MessageSquare,
  Plus,
  Settings,
  Bot,
  Sparkles,
  FileText,
  Wrench,
  Puzzle,
  LayoutGrid,
  Cpu,
  Keyboard,
  Activity,
  SlidersHorizontal,
  FolderOpen,
  ArrowLeft,
} from 'lucide-react'
import {
  CommandDialog,
  CommandInput,
  CommandList,
  CommandEmpty,
  CommandGroup,
  CommandItem,
  CommandSeparator,
  CommandShortcut,
} from '@/components/ui/command'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useAppStore } from '@/stores/useAppStore'
import { useNavigationStore } from '@/stores/useNavigationStore'
import { updateSettingsHash } from '@/hooks/useHashRoute'
import { usePluginSlots } from '@/hooks/usePluginSlots'
import { usePluginAction } from '@/hooks/usePluginAction'
import { resolveIcon } from '@/lib/icons'
import { api } from '@/lib/api'

interface CommandPaletteProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onNewSession?: () => void
}

type SubPage = 'root' | 'agents' | 'settings'

const isMac = typeof navigator !== 'undefined' && /Mac/i.test(navigator.userAgent)
const modKey = isMac ? '⌘' : 'Ctrl'

export function CommandPalette({ open, onOpenChange, onNewSession }: CommandPaletteProps) {
  const [subPage, setSubPage] = useState<SubPage>('root')
  const setCurrentPage = useLayoutStore((s) => s.setCurrentPage)
  const currentPage = useLayoutStore((s) => s.currentPage)
  const setActiveSession = useAppStore((s) => s.setActiveSession)
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId)
  const navPush = useNavigationStore((s) => s.push)

  const { data: sessions = [] } = useQuery({
    queryKey: ['sessions', activeWorkspaceId],
    queryFn: () => api.listSessions(activeWorkspaceId ?? undefined),
    enabled: open,
  })

  const { data: agents = [] } = useQuery({
    queryKey: ['agents'],
    queryFn: api.listAgents,
    enabled: open && subPage === 'agents',
  })

  const pluginCommands = usePluginSlots('command-palette')
  const handlePluginAction = usePluginAction()

  const close = useCallback(() => {
    onOpenChange(false)
    setSubPage('root')
  }, [onOpenChange])

  const goToChat = useCallback(() => {
    setCurrentPage('chat')
    window.location.hash = '#chat'
    close()
  }, [setCurrentPage, close])

  const goToSettings = useCallback((section: string) => {
    setCurrentPage('settings')
    updateSettingsHash(section as never)
    navPush({ view: `settings/${section}`, label: section })
    close()
  }, [setCurrentPage, navPush, close])

  const selectSession = useCallback((sessionId: string) => {
    setActiveSession(sessionId)
    setCurrentPage('chat')
    window.location.hash = '#chat'
    close()
  }, [setActiveSession, setCurrentPage, close])

  const handleNewSession = useCallback(() => {
    onNewSession?.()
    close()
  }, [onNewSession, close])

  // Recent sessions (top 8)
  const recentSessions = useMemo(() =>
    [...sessions]
      .sort((a, b) => new Date(b.last_activity).getTime() - new Date(a.last_activity).getTime())
      .slice(0, 8),
    [sessions]
  )

  const handleOpenChange = useCallback((v: boolean) => {
    if (!v) setSubPage('root')
    onOpenChange(v)
  }, [onOpenChange])

  return (
    <CommandDialog
      open={open}
      onOpenChange={handleOpenChange}
      title="Command Palette"
      description="Search for commands, chats, and settings"
      showCloseButton={false}
    >
      <CommandInput placeholder={subPage === 'root' ? 'Type a command or search...' : `Search ${subPage}...`} />
      <CommandList className="max-h-[400px]">
        <CommandEmpty className="text-fg-muted">No results found.</CommandEmpty>

        {subPage === 'root' && (
          <>
            {/* Quick Actions */}
            <CommandGroup heading="Actions">
              <CommandItem onSelect={handleNewSession}>
                <Plus />
                <span>New Chat</span>
                <CommandShortcut>{modKey}N</CommandShortcut>
              </CommandItem>
              {currentPage !== 'chat' && (
                <CommandItem onSelect={goToChat}>
                  <MessageSquare />
                  <span>Go to Chat</span>
                </CommandItem>
              )}
            </CommandGroup>

            <CommandSeparator />

            {/* Recent Chats */}
            {recentSessions.length > 0 && (
              <>
                <CommandGroup heading="Recent Chats">
                  {recentSessions.map((s) => (
                    <CommandItem key={s.id} onSelect={() => selectSession(s.id)}>
                      <MessageSquare />
                      <span className="truncate">{s.title || s.short_code || 'Untitled'}</span>
                      {s.short_code && (
                        <span className="ml-auto text-xs text-fg-faint font-mono">#{s.short_code}</span>
                      )}
                    </CommandItem>
                  ))}
                </CommandGroup>
                <CommandSeparator />
              </>
            )}

            {/* Navigate */}
            <CommandGroup heading="Navigate">
              <CommandItem onSelect={() => setSubPage('settings')}>
                <Settings />
                <span>Settings...</span>
              </CommandItem>
              <CommandItem onSelect={() => setSubPage('agents')}>
                <Bot />
                <span>Agents...</span>
              </CommandItem>
              <CommandItem onSelect={() => goToSettings('tools')}>
                <Wrench />
                <span>Tools &amp; Servers</span>
              </CommandItem>
              <CommandItem onSelect={() => goToSettings('plugins')}>
                <Puzzle />
                <span>Plugins</span>
              </CommandItem>
            </CommandGroup>

            {/* command-palette slot — plugin-registered commands */}
            {pluginCommands.length > 0 && (
              <>
                <CommandSeparator />
                <CommandGroup heading="Plugins">
                  {pluginCommands.map((entry) => {
                    const PluginIcon = resolveIcon(entry.icon)
                    return (
                      <CommandItem
                        key={entry.id}
                        onSelect={() => {
                          handlePluginAction(entry)
                          close()
                        }}
                      >
                        <PluginIcon />
                        <span>{entry.label}</span>
                      </CommandItem>
                    )
                  })}
                </CommandGroup>
              </>
            )}

            <CommandSeparator />

            {/* Shortcuts */}
            <CommandGroup heading="Shortcuts">
              <CommandItem onSelect={() => goToSettings('shortcuts')}>
                <Keyboard />
                <span>Keyboard Shortcuts</span>
              </CommandItem>
              <CommandItem onSelect={() => goToSettings('providers')}>
                <Cpu />
                <span>Manage Providers</span>
              </CommandItem>
            </CommandGroup>
          </>
        )}

        {/* Settings sub-page */}
        {subPage === 'settings' && (
          <>
            <CommandGroup heading="Settings">
              <CommandItem onSelect={() => setSubPage('root')}>
                <ArrowLeft />
                <span className="text-fg-muted">Back</span>
              </CommandItem>
              <CommandItem onSelect={() => goToSettings('preferences')}>
                <SlidersHorizontal />
                <span>Preferences</span>
              </CommandItem>
              <CommandItem onSelect={() => goToSettings('providers')}>
                <Cpu />
                <span>Providers</span>
              </CommandItem>
              <CommandItem onSelect={() => goToSettings('shortcuts')}>
                <Keyboard />
                <span>Shortcuts</span>
              </CommandItem>
              <CommandItem onSelect={() => goToSettings('agents')}>
                <Bot />
                <span>Agents</span>
              </CommandItem>
              <CommandItem onSelect={() => goToSettings('skills')}>
                <Sparkles />
                <span>Skills</span>
              </CommandItem>
              <CommandItem onSelect={() => goToSettings('prompts')}>
                <FileText />
                <span>Prompts</span>
              </CommandItem>
              <CommandItem onSelect={() => goToSettings('tools')}>
                <Wrench />
                <span>Tools</span>
              </CommandItem>
              <CommandItem onSelect={() => goToSettings('plugins')}>
                <Puzzle />
                <span>Plugins</span>
              </CommandItem>
              <CommandItem onSelect={() => goToSettings('widgets')}>
                <LayoutGrid />
                <span>Widgets</span>
              </CommandItem>
              <CommandItem onSelect={() => goToSettings('observability')}>
                <Activity />
                <span>Observability</span>
              </CommandItem>
            </CommandGroup>
          </>
        )}

        {/* Agents sub-page */}
        {subPage === 'agents' && (
          <>
            <CommandGroup heading="Agents">
              <CommandItem onSelect={() => setSubPage('root')}>
                <ArrowLeft />
                <span className="text-fg-muted">Back</span>
              </CommandItem>
              <CommandItem onSelect={() => goToSettings('agents')}>
                <FolderOpen />
                <span>Manage All Agents</span>
              </CommandItem>
            </CommandGroup>
            {agents.length > 0 && (
              <CommandGroup heading="Available Agents">
                {agents.map((a) => (
                  <CommandItem key={a.id} onSelect={() => goToSettings('agents')}>
                    <span className="flex items-center justify-center size-5 rounded text-xs">
                      {a.avatar || '🤖'}
                    </span>
                    <span>{a.name}</span>
                    <span className="ml-auto text-xs text-fg-faint">{a.source}</span>
                  </CommandItem>
                ))}
              </CommandGroup>
            )}
          </>
        )}
      </CommandList>
    </CommandDialog>
  )
}
