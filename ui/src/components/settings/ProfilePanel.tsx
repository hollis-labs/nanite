import { useState, useCallback, useRef } from 'react'
import {
  Camera,
  ChevronDown,
  Globe,
  Languages,
  Monitor,
  Moon,
  Sun,
  Trash2,
  User,
} from 'lucide-react'
import { useSettings, useSettingsMutation } from '@/hooks/useSettings'
import { useLayoutStore } from '@/stores/useLayoutStore'

// --- Shared sub-components (same pattern as PreferencesPanel) ---

function SettingsCard({
  title,
  description,
  children,
}: {
  title: string
  description?: string
  children: React.ReactNode
}) {
  return (
    <div className="rounded-xl border border-border-subtle bg-white dark:bg-bg-elevated/60 shadow-sm overflow-hidden">
      <div className="px-4 py-3 border-b border-border/50">
        <h3 className="text-sm font-semibold text-fg">{title}</h3>
        {description && (
          <p className="text-[11px] text-fg-muted mt-0.5">{description}</p>
        )}
      </div>
      <div className="px-4 py-2">{children}</div>
    </div>
  )
}

function SettingsRow({
  label,
  description,
  children,
  vertical,
}: {
  label: string
  description?: string
  children: React.ReactNode
  vertical?: boolean
}) {
  if (vertical) {
    return (
      <div className="py-2.5 space-y-2">
        <div className="min-w-0">
          <div className="text-sm text-fg">{label}</div>
          {description && (
            <div className="text-[11px] text-fg-muted mt-0.5">{description}</div>
          )}
        </div>
        <div>{children}</div>
      </div>
    )
  }
  return (
    <div className="flex items-center justify-between gap-4 py-2.5">
      <div className="min-w-0">
        <div className="text-sm text-fg">{label}</div>
        {description && (
          <div className="text-[11px] text-fg-muted mt-0.5">{description}</div>
        )}
      </div>
      <div className="shrink-0">{children}</div>
    </div>
  )
}

function SettingsInput({
  value,
  placeholder,
  onChange,
  type = 'text',
}: {
  value: string
  placeholder?: string
  onChange: (value: string) => void
  type?: string
}) {
  return (
    <input
      type={type}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      placeholder={placeholder}
      className="w-48 bg-bg-elevated border border-border-subtle rounded-lg px-3 py-1.5 text-sm text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent"
    />
  )
}

// --- Theme Selector ---

type ThemeOption = 'system' | 'light' | 'dark'

const THEME_OPTIONS: { value: ThemeOption; label: string; icon: typeof Sun }[] = [
  { value: 'system', label: 'System', icon: Monitor },
  { value: 'light', label: 'Light', icon: Sun },
  { value: 'dark', label: 'Dark', icon: Moon },
]

function ThemeSelector({
  value,
  onChange,
}: {
  value: ThemeOption
  onChange: (value: ThemeOption) => void
}) {
  return (
    <div className="flex items-center gap-1 bg-surface/50 rounded-lg p-0.5">
      {THEME_OPTIONS.map((opt) => {
        const Icon = opt.icon
        const isActive = value === opt.value
        return (
          <button
            key={opt.value}
            onClick={() => onChange(opt.value)}
            className={`flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium transition-colors ${
              isActive
                ? 'bg-bg-elevated text-fg shadow-sm'
                : 'text-fg-muted hover:text-fg-secondary'
            }`}
          >
            <Icon className="size-3.5" />
            {opt.label}
          </button>
        )
      })}
    </div>
  )
}

// --- Avatar uploader ---

function AvatarUploader({
  avatarUrl,
  displayName,
  onUpload,
  onRemove,
}: {
  avatarUrl: string
  displayName: string
  onUpload: (dataUrl: string) => void
  onRemove: () => void
}) {
  const fileRef = useRef<HTMLInputElement>(null)

  const handleFile = useCallback(
    (file: File) => {
      if (!file.type.startsWith('image/')) return
      // Cap at 128x128 and convert to data-URL for ext_settings storage
      const reader = new FileReader()
      reader.onload = () => {
        const img = new Image()
        img.onload = () => {
          const canvas = document.createElement('canvas')
          const size = 128
          canvas.width = size
          canvas.height = size
          const ctx = canvas.getContext('2d')!
          // Center-crop to square
          const min = Math.min(img.width, img.height)
          const sx = (img.width - min) / 2
          const sy = (img.height - min) / 2
          ctx.drawImage(img, sx, sy, min, min, 0, 0, size, size)
          onUpload(canvas.toDataURL('image/webp', 0.85))
        }
        img.src = reader.result as string
      }
      reader.readAsDataURL(file)
    },
    [onUpload],
  )

  const initials = displayName
    ? displayName
        .split(' ')
        .map((w) => w[0])
        .join('')
        .slice(0, 2)
        .toUpperCase()
    : ''

  return (
    <div className="flex items-center gap-4">
      <input
        ref={fileRef}
        type="file"
        accept="image/*"
        className="hidden"
        onChange={(e) => {
          const file = e.target.files?.[0]
          if (file) handleFile(file)
          e.target.value = ''
        }}
      />
      <button
        onClick={() => fileRef.current?.click()}
        className="relative group size-16 rounded-xl bg-surface border border-border-subtle overflow-hidden flex items-center justify-center shrink-0 transition-all hover:ring-2 hover:ring-accent/40"
      >
        {avatarUrl ? (
          <img src={avatarUrl} alt="Avatar" className="size-full object-cover" />
        ) : (
          <span className="text-lg font-semibold text-fg-secondary">
            {initials || <User className="size-6 text-fg-muted" />}
          </span>
        )}
        <div className="absolute inset-0 bg-black/40 opacity-0 group-hover:opacity-100 transition-opacity flex items-center justify-center">
          <Camera className="size-5 text-white" />
        </div>
      </button>
      <div className="space-y-1">
        <button
          onClick={() => fileRef.current?.click()}
          className="text-xs text-accent hover:text-accent-hover font-medium"
        >
          Upload photo
        </button>
        {avatarUrl && (
          <button
            onClick={onRemove}
            className="flex items-center gap-1 text-xs text-fg-muted hover:text-fg-secondary transition-colors"
          >
            <Trash2 className="size-3" />
            Remove
          </button>
        )}
        <p className="text-[10px] text-fg-faint">128×128 WebP. Used in chat and menus.</p>
      </div>
    </div>
  )
}

// --- Timezone/Language data ---

const TIMEZONE_OPTIONS = (() => {
  const zones = Intl.supportedValuesOf('timeZone')
  const userTz = Intl.DateTimeFormat().resolvedOptions().timeZone
  const favorites = [userTz, 'UTC']
  const sorted = [
    ...favorites.filter((tz) => zones.includes(tz)),
    ...zones.filter((tz) => !favorites.includes(tz)),
  ]
  return sorted.map((tz) => ({ value: tz, label: tz.replace(/_/g, ' ') }))
})()

const LANGUAGE_OPTIONS = [
  { value: 'en', label: 'English' },
  { value: 'de', label: 'Deutsch' },
  { value: 'es', label: 'Español' },
]

// --- Main panel ---

export function ProfilePanel() {
  const { data: settings } = useSettings()
  const mutation = useSettingsMutation()
  const setTheme = useLayoutStore((s) => s.setTheme)

  const ext = settings?.ext_settings ?? {}
  const displayName = (ext.display_name as string) || ''
  const avatarUrl = (ext.avatar_url as string) || ''
  const email = (ext.email as string) || ''
  const timezone = (ext.timezone as string) || Intl.DateTimeFormat().resolvedOptions().timeZone
  const language = (ext.language as string) || 'en'
  const themePreference = (ext.theme_preference as ThemeOption) || 'system'
  const userContext = (ext.user_context as string) || ''

  // Debounced text saves
  const [localName, setLocalName] = useState<string | null>(null)
  const [localEmail, setLocalEmail] = useState<string | null>(null)
  const [localContext, setLocalContext] = useState<string | null>(null)
  const nameRef = useRef<ReturnType<typeof setTimeout>>(undefined)
  const emailRef = useRef<ReturnType<typeof setTimeout>>(undefined)
  const contextRef = useRef<ReturnType<typeof setTimeout>>(undefined)

  const updateExt = useCallback(
    (fields: Record<string, unknown>) => {
      mutation.mutate({
        ext_settings: {
          ...settings?.ext_settings,
          ...fields,
        },
      })
    },
    [settings?.ext_settings, mutation],
  )

  const handleNameChange = useCallback(
    (value: string) => {
      setLocalName(value)
      clearTimeout(nameRef.current)
      nameRef.current = setTimeout(() => {
        updateExt({ display_name: value })
        setLocalName(null)
      }, 500)
    },
    [updateExt],
  )

  const handleEmailChange = useCallback(
    (value: string) => {
      setLocalEmail(value)
      clearTimeout(emailRef.current)
      emailRef.current = setTimeout(() => {
        updateExt({ email: value })
        setLocalEmail(null)
      }, 500)
    },
    [updateExt],
  )

  const handleContextChange = useCallback(
    (value: string) => {
      setLocalContext(value)
      clearTimeout(contextRef.current)
      contextRef.current = setTimeout(() => {
        updateExt({ user_context: value })
        setLocalContext(null)
      }, 800)
    },
    [updateExt],
  )

  const handleThemeChange = useCallback(
    (value: ThemeOption) => {
      updateExt({ theme_preference: value })
      if (value === 'system') {
        const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches
        setTheme(prefersDark ? 'dark' : 'light')
      } else {
        setTheme(value)
      }
    },
    [updateExt, setTheme],
  )

  return (
    <div className="space-y-3">
      {/* Identity */}
      <SettingsCard title="Identity" description="Your personal information visible to agents and in chat">
        <div className="py-3">
          <AvatarUploader
            avatarUrl={avatarUrl}
            displayName={localName ?? displayName}
            onUpload={(dataUrl) => updateExt({ avatar_url: dataUrl })}
            onRemove={() => updateExt({ avatar_url: '' })}
          />
        </div>
        <SettingsRow label="Display Name" description="Shown in chat messages and agent interactions">
          <SettingsInput
            value={localName ?? displayName}
            placeholder="Your name"
            onChange={handleNameChange}
          />
        </SettingsRow>
        <SettingsRow label="Email" description="Optional — used for notifications and integrations">
          <SettingsInput
            value={localEmail ?? email}
            placeholder="you@example.com"
            onChange={handleEmailChange}
            type="email"
          />
        </SettingsRow>
      </SettingsCard>

      {/* Locale */}
      <SettingsCard title="Locale" description="Regional preferences for display and formatting">
        <SettingsRow label="Timezone" description="Used for scheduling and time display">
          <div className="relative">
            <Globe className="absolute left-2.5 top-1/2 -translate-y-1/2 size-3.5 text-fg-faint pointer-events-none" />
            <select
              value={timezone}
              onChange={(e) => updateExt({ timezone: e.target.value })}
              className="appearance-none w-48 bg-bg-elevated border border-border-subtle rounded-lg pl-8 pr-8 py-1.5 text-sm text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent cursor-pointer"
            >
              {TIMEZONE_OPTIONS.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>
            <ChevronDown className="absolute right-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-fg-faint pointer-events-none" />
          </div>
        </SettingsRow>
        <SettingsRow label="Language" description="Interface language (translations coming soon)">
          <div className="relative">
            <Languages className="absolute left-2.5 top-1/2 -translate-y-1/2 size-3.5 text-fg-faint pointer-events-none" />
            <select
              value={language}
              onChange={(e) => updateExt({ language: e.target.value })}
              className="appearance-none w-48 bg-bg-elevated border border-border-subtle rounded-lg pl-8 pr-8 py-1.5 text-sm text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent cursor-pointer"
            >
              {LANGUAGE_OPTIONS.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>
            <ChevronDown className="absolute right-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-fg-faint pointer-events-none" />
          </div>
        </SettingsRow>
      </SettingsCard>

      {/* Appearance */}
      <SettingsCard title="Appearance" description="Theme and visual preferences">
        <SettingsRow label="Theme" description="System follows your OS preference">
          <ThemeSelector value={themePreference} onChange={handleThemeChange} />
        </SettingsRow>
      </SettingsCard>

      {/* Agent Context */}
      <SettingsCard
        title="Agent Context"
        description="Tell agents about yourself — goals, projects, preferences, background. This context is included in every conversation."
      >
        <SettingsRow
          label="About You"
          description="Free-form text that agents can reference during conversations"
          vertical
        >
          <textarea
            value={localContext ?? userContext}
            onChange={(e) => handleContextChange(e.target.value)}
            placeholder={"Example: I'm a senior engineer working on a Go + React chat platform. Currently focused on plugin architecture. I prefer concise answers and working code over long explanations. My timezone is US/Central."}
            rows={6}
            className="w-full bg-bg-elevated border border-border-subtle rounded-lg px-3 py-2 text-sm text-fg placeholder:text-fg-faint/60 focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent resize-y min-h-[120px]"
          />
          <p className="text-[10px] text-fg-faint mt-1">
            {(localContext ?? userContext).length} characters
          </p>
        </SettingsRow>
      </SettingsCard>
    </div>
  )
}
