import { MessageSquare, Search, Plus, Settings, User } from 'lucide-react'
import { useState, useCallback } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Tooltip } from '@/components/ui/tooltip'
import { useAppStore } from '@/stores/useAppStore'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useSettings } from '@/hooks/useSettings'
import { usePluginSlots } from '@/hooks/usePluginSlots'
import { useNavigationStore } from '@/stores/useNavigationStore'
import { updateSettingsHash } from '@/hooks/useHashRoute'
import { resolveIcon } from '@/lib/icons'
import { api } from '@/lib/api'

const CORE_NAV_ITEMS = [
  { icon: MessageSquare, label: 'Chat', id: 'chat' },
  { icon: Search, label: 'Search', id: 'search' },
  { icon: Plus, label: 'New Chat', id: 'new' },
  { icon: Settings, label: 'Settings', id: 'settings' },
] as const

export function NavRail() {
  const [activeItem, setActiveItem] = useState<string>('chat')
  const pluginNavItems = usePluginSlots('nav-rail')

  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId)
  const setActiveSession = useAppStore((s) => s.setActiveSession)
  const currentPage = useLayoutStore((s) => s.currentPage)
  const setCurrentPage = useLayoutStore((s) => s.setCurrentPage)
  const queryClient = useQueryClient()
  const navPush = useNavigationStore((s) => s.push)
  const { data: userSettings } = useSettings()

  const createSessionMutation = useMutation({
    mutationFn: () => api.createSession({
      workspace_id: activeWorkspaceId!,
      provider: userSettings?.default_provider || undefined,
      model: userSettings?.default_model || undefined,
      agent_id: userSettings?.default_agent || undefined,
    }),
    onSuccess: (newSession) => {
      void queryClient.invalidateQueries({ queryKey: ['sessions'] })
      setActiveSession(newSession.id)
    },
  })

  const handleGoHome = useCallback(() => {
    setCurrentPage('chat')
    setActiveSession(null)
    setActiveItem('chat')
  }, [setCurrentPage, setActiveSession])

  return (
    <nav className="flex flex-col items-center w-16 h-full bg-bg-elevated border-r border-border py-3 shrink-0">
      {/* Brand logo — click to go home */}
      <Tooltip content="Home" side="right">
        <button
          onClick={handleGoHome}
          className="w-10 h-10 rounded-lg flex items-center justify-center bg-brand text-brand-fg hover:bg-brand-hover transition-colors mb-3 leading-none"
          style={{ fontSize: '34px', fontWeight: 900, fontFamily: 'system-ui, -apple-system, BlinkMacSystemFont, sans-serif' }}
        >
          N
        </button>
      </Tooltip>

      <div className="w-8 border-t border-border-subtle mb-2" />

      <div className="flex flex-col items-center gap-1 flex-1">
        {/* Core nav items */}
        {CORE_NAV_ITEMS.map(({ icon: Icon, label, id }) => {
          const isActive = id === 'settings' ? currentPage === 'settings' :
                          id === 'chat' ? currentPage === 'chat' :
                          activeItem === id
          return (
            <Tooltip key={id} content={label} side="right">
              <Button
                variant="ghost"
                size="icon"
                className={`w-10 h-10 rounded-lg ${
                  isActive
                    ? 'bg-surface text-fg'
                    : 'text-fg-secondary hover:bg-surface hover:text-fg'
                }`}
                onClick={() => {
                  if (id === 'new') {
                    if (activeWorkspaceId) createSessionMutation.mutate()
                  } else if (id === 'search') {
                    window.dispatchEvent(new CustomEvent('open-search'))
                  } else if (id === 'settings') {
                    setCurrentPage('settings')
                    setActiveItem(id)
                  } else if (id === 'chat') {
                    setCurrentPage('chat')
                    setActiveItem(id)
                  } else {
                    setActiveItem(id)
                  }
                }}
              >
                <Icon className="w-5 h-5" />
              </Button>
            </Tooltip>
          )
        })}

        {/* Plugin-registered nav items */}
        {pluginNavItems.map((entry) => {
          const PluginIcon = resolveIcon(entry.icon)
          const isActive = currentPage === entry.id
          return (
            <Tooltip key={entry.id} content={entry.label} side="right">
              <Button
                variant="ghost"
                size="icon"
                className={`w-10 h-10 rounded-lg ${
                  isActive
                    ? 'bg-surface text-fg'
                    : 'text-fg-secondary hover:bg-surface hover:text-fg'
                }`}
                onClick={() => {
                  setCurrentPage(entry.id as any)
                  setActiveItem(entry.id)
                }}
              >
                <PluginIcon className="w-5 h-5" />
              </Button>
            </Tooltip>
          )
        })}
      </div>
      <div className="mt-auto flex flex-col items-center gap-1">
        <Tooltip content="Profile" side="right">
          <Button
            variant="ghost"
            size="icon"
            className={`w-10 h-10 rounded-lg ${
              currentPage === 'settings' ? 'text-fg-secondary hover:text-fg' : 'text-fg-secondary hover:text-fg'
            }`}
            onClick={() => {
              setCurrentPage('settings')
              updateSettingsHash('profile' as never)
              navPush({ view: 'settings/profile', label: 'Profile' })
            }}
          >
            {userSettings?.ext_settings?.avatar_url ? (
              <img
                src={userSettings.ext_settings.avatar_url as string}
                alt="Profile"
                className="size-6 rounded-md object-cover"
              />
            ) : (
              <User className="w-5 h-5" />
            )}
          </Button>
        </Tooltip>
      </div>
    </nav>
  )
}
