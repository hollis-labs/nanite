import { useState, useCallback } from 'react'
import { User, Sun, Moon, Keyboard, ChevronRight } from 'lucide-react'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { Separator } from '@/components/ui/separator'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useSettings, useSettingsMutation } from '@/hooks/useSettings'
import { updateSettingsHash } from '@/hooks/useHashRoute'
import { useNavigationStore } from '@/stores/useNavigationStore'
import type { UserSettings } from '@/lib/types'

const THEME_OPTIONS = [
  { value: 'dark', label: 'Dark', icon: Moon },
  { value: 'light', label: 'Light', icon: Sun },
] as const

export function UserProfileMenu() {
  const [open, setOpen] = useState(false)
  const theme = useLayoutStore((s) => s.theme)
  const setTheme = useLayoutStore((s) => s.setTheme)
  const setCurrentPage = useLayoutStore((s) => s.setCurrentPage)
  const navPush = useNavigationStore((s) => s.push)
  const { data: settings } = useSettings()
  const mutation = useSettingsMutation()

  const displayName = (settings?.ext_settings?.display_name as string) || ''
  const avatarUrl = (settings?.ext_settings?.avatar_url as string) || ''
  const avatar = (settings?.ext_settings?.avatar as string) || ''

  const goToSettings = useCallback((section: string) => {
    setCurrentPage('settings')
    updateSettingsHash(section as never)
    navPush({ view: `settings/${section}`, label: section })
    setOpen(false)
  }, [setCurrentPage, navPush])

  const handleNameChange = useCallback((name: string) => {
    mutation.mutate({
      ext_settings: {
        ...settings?.ext_settings,
        display_name: name,
      },
    } as Partial<UserSettings>)
  }, [settings?.ext_settings, mutation])

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          className="flex items-center justify-center size-6 rounded-sm bg-composer-hover text-composer-fg-secondary hover:text-composer-fg transition-colors"
          title="Profile & preferences"
        >
          {avatarUrl ? (
            <img src={avatarUrl} alt="" className="size-full rounded-sm object-cover" />
          ) : avatar ? (
            <span className="text-xs">{avatar}</span>
          ) : (
            <User className="size-3.5" />
          )}
        </button>
      </PopoverTrigger>
      <PopoverContent
        side="top"
        align="start"
        className="w-64 p-0 bg-bg-elevated border-border-subtle"
      >
        {/* User info */}
        <div className="px-3 py-3">
          <div className="flex items-center gap-2.5">
            <div className="flex items-center justify-center size-9 rounded-sm bg-surface text-fg-secondary">
              {avatarUrl ? (
                <img src={avatarUrl} alt="" className="size-full rounded-sm object-cover" />
              ) : avatar ? (
                <span className="text-base">{avatar}</span>
              ) : (
                <User className="size-4" />
              )}
            </div>
            <div className="flex-1 min-w-0">
              <input
                type="text"
                defaultValue={displayName}
                onBlur={(e) => handleNameChange(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    handleNameChange((e.target as HTMLInputElement).value)
                    ;(e.target as HTMLInputElement).blur()
                  }
                }}
                placeholder="Your name"
                className="w-full text-sm font-medium text-fg bg-transparent border-none outline-none placeholder:text-fg-faint"
              />
              <p className="text-[10px] text-fg-faint mt-0.5">Click to edit name</p>
            </div>
          </div>
        </div>

        <Separator />

        {/* Theme */}
        <div className="px-3 py-2">
          <p className="text-[10px] font-medium text-fg-muted uppercase tracking-wider mb-1.5">Theme</p>
          <div className="flex items-center gap-1">
            {THEME_OPTIONS.map((opt) => {
              const Icon = opt.icon
              const isActive = theme === opt.value
              return (
                <button
                  key={opt.value}
                  onClick={() => setTheme(opt.value as 'dark' | 'light')}
                  className={`flex items-center gap-1.5 px-2.5 py-1 rounded-md text-xs transition-colors ${
                    isActive
                      ? 'bg-surface text-fg'
                      : 'text-fg-muted hover:text-fg-secondary hover:bg-surface/50'
                  }`}
                >
                  <Icon className="size-3" />
                  {opt.label}
                </button>
              )
            })}
          </div>
        </div>

        <Separator />

        {/* Quick links */}
        <div className="py-1">
          <MenuLink icon={User} label="Profile" onClick={() => goToSettings('profile')} />
          <MenuLink icon={Keyboard} label="Keyboard Shortcuts" onClick={() => goToSettings('shortcuts')} />
        </div>
      </PopoverContent>
    </Popover>
  )
}

function MenuLink({ icon: Icon, label, onClick }: {
  icon: typeof User
  label: string
  onClick: () => void
}) {
  return (
    <button
      onClick={onClick}
      className="w-full flex items-center gap-2 px-3 py-1.5 text-xs text-fg-secondary hover:text-fg hover:bg-surface/50 transition-colors"
    >
      <Icon className="size-3.5 text-fg-muted" />
      <span className="flex-1 text-left">{label}</span>
      <ChevronRight className="size-3 text-fg-faint" />
    </button>
  )
}
