import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { ChevronDown, LayoutGrid, ListTodo, GitBranch, Mail, Moon, Package, Building2, Plus, Search, Sun } from 'lucide-react'
import { useLayoutStore, type LayoutPreset } from '@/stores/useLayoutStore'
import { usePluginSlots } from '@/hooks/usePluginSlots'
import { resolveIcon } from '@/lib/icons'
import { useTheme } from '@/hooks/useTheme'
import { BUILTIN_THEMES } from '@/lib/theme/defaults'

// ── SVG icon helpers (match design reference paths exactly) ──────────────────

function LMIcon({ children, size = 14, style }: { children: React.ReactNode; size?: number; style?: React.CSSProperties }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" style={style}>
      {children}
    </svg>
  )
}

const Icons = {
  layout:    (p: { size?: number }) => <LMIcon size={p.size}><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18M3 12h6"/></LMIcon>,
  focus:     (p: { size?: number }) => <LMIcon size={p.size}><rect x="6" y="4" width="12" height="16" rx="2"/></LMIcon>,
  default:   (p: { size?: number }) => <LMIcon size={p.size}><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18"/></LMIcon>,
  workspace: (p: { size?: number }) => <LMIcon size={p.size}><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18M15 3v18"/></LMIcon>,
  reading:   (p: { size?: number }) => <LMIcon size={p.size}><rect x="6" y="3" width="12" height="18" rx="2"/><path d="M9 8h6M9 12h6M9 16h4"/></LMIcon>,
  panelL:    (p: { size?: number }) => <LMIcon size={p.size}><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18"/></LMIcon>,
  panelR:    (p: { size?: number }) => <LMIcon size={p.size}><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M15 3v18"/></LMIcon>,
  drawer:    (p: { size?: number }) => <LMIcon size={p.size}><rect x="3" y="6" width="18" height="14" rx="2"/><path d="M3 12h18"/></LMIcon>,
  chips:     (p: { size?: number }) => <LMIcon size={p.size}><rect x="3" y="9" width="6" height="6" rx="1"/><rect x="11" y="9" width="6" height="6" rx="1"/><rect x="19" y="9" width="2" height="6" rx="1"/></LMIcon>,
}

// ── Sub-components ───────────────────────────────────────────────────────────

function LMKbd({ children }: { children: React.ReactNode }) {
  return (
    <span className="rounded-[3px] border border-border-subtle bg-surface px-[5px] py-px font-mono text-[10px] leading-none text-fg-muted">
      {children}
    </span>
  )
}

function LMToggle({ on }: { on: boolean }) {
  return (
    <div className={`relative h-[14px] w-6 shrink-0 rounded-full transition-colors duration-120 ${on ? 'bg-primary' : 'bg-surface'}`}>
      <div className={`absolute top-px h-3 w-3 rounded-full transition-all duration-120 ${on ? 'left-[11px] bg-primary-fg' : 'left-px bg-fg-muted'}`} />
    </div>
  )
}

function LMRow({
  icon: Ic,
  label,
  kbd,
  on,
  onClick,
}: {
  icon: (p: { size?: number }) => React.ReactNode
  label: string
  kbd: string
  on: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex w-full items-center gap-2.5 rounded-[6px] px-2.5 py-2 text-left transition-colors hover:bg-surface"
    >
      <span className="shrink-0 text-fg-muted"><Ic size={14} /></span>
      <span className="flex-1 text-[12px] text-fg">{label}</span>
      <LMKbd>{kbd}</LMKbd>
      <LMToggle on={on} />
    </button>
  )
}

function LMPreset({
  icon: Ic,
  label,
  active,
  onClick,
}: {
  icon: (p: { size?: number }) => React.ReactNode
  label: string
  active: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`flex flex-1 flex-col items-center gap-1.5 rounded-[6px] border px-1 py-2.5 transition-all duration-120 ${
        active
          ? 'border-primary bg-primary-muted text-primary'
          : 'border-border-subtle bg-transparent text-fg-secondary hover:border-border hover:bg-surface'
      }`}
    >
      <Ic size={16} />
      <span className="font-mono text-[9px] font-semibold uppercase tracking-wide">{label}</span>
    </button>
  )
}

// ── Theme dropdown ───────────────────────────────────────────────────────────

function ThemeDropdown() {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const { activeThemeId, setActiveTheme } = useTheme()
  const theme = useLayoutStore((s) => s.theme)
  const setTheme = useLayoutStore((s) => s.setTheme)

  const activeBuiltin = BUILTIN_THEMES.find((t) => t.id === activeThemeId) ?? BUILTIN_THEMES[0]
  // Swatch preview uses light tokens regardless of current mode
  const swatchBg = activeBuiltin.tokens.light['bg-elevated'] ?? '#ffffff'
  const swatchAccent = activeBuiltin.tokens.light['primary'] ?? '#000000'
  const isDark = theme === 'dark' || (theme === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches)

  useEffect(() => {
    if (!open) return
    function onDown(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className={`flex items-center gap-1.5 rounded-[5px] border px-1.5 py-1 transition-colors ${
          open ? 'border-border bg-surface' : 'border-border-subtle hover:border-border hover:bg-surface'
        }`}
      >
        {/* Mini swatch */}
        <div
          className="relative overflow-hidden rounded-[3px]"
          style={{ width: 14, height: 14, background: swatchBg, border: '1px solid rgba(0,0,0,0.10)' }}
        >
          <div
            className="absolute bottom-[1px] inset-x-[1px] h-[3px] rounded-[1px]"
            style={{ background: swatchAccent }}
          />
        </div>
        <span className="font-mono text-[10px] font-semibold text-fg-secondary">
          {activeBuiltin.name.split(' ')[0]}·{isDark ? 'D' : 'L'}
        </span>
        <ChevronDown className="h-[9px] w-[9px] text-fg-faint" />
      </button>

      {open && (
        <div className="absolute bottom-full right-0 mb-1.5 z-10 w-[220px] rounded-[8px] border border-border bg-bg-elevated p-2 shadow-[0_8px_24px_rgba(0,0,0,0.14)]">
          {/* Light / Dark toggle */}
          <div className="mb-2 flex rounded-[5px] border border-border-subtle bg-surface p-[2px]">
            {(['light', 'dark'] as const).map((m) => (
              <button
                key={m}
                type="button"
                onClick={() => setTheme(m)}
                className={`flex flex-1 items-center justify-center gap-1 rounded-[3px] py-1 text-[10px] font-medium capitalize transition-colors ${
                  (m === 'dark') === isDark
                    ? 'bg-bg-elevated text-fg shadow-sm'
                    : 'text-fg-muted hover:text-fg-secondary'
                }`}
              >
                {m === 'light' ? <Sun className="h-[9px] w-[9px]" /> : <Moon className="h-[9px] w-[9px]" />}
                {m}
              </button>
            ))}
          </div>

          {/* Theme palette grid */}
          <div className="grid grid-cols-3 gap-1.5">
            {BUILTIN_THEMES.map((t) => {
              const bg = t.tokens.light['bg-elevated'] ?? '#ffffff'
              const accent = t.tokens.light['primary'] ?? '#000000'
              const brand = t.tokens.light['brand'] ?? accent
              const isActive = t.id === activeThemeId
              return (
                <button
                  key={t.id}
                  type="button"
                  onClick={() => { setActiveTheme(t.id); setOpen(false) }}
                  className={`flex flex-col items-center gap-1 rounded-[5px] border p-1.5 transition-colors ${
                    isActive ? 'border-primary bg-primary-muted' : 'border-transparent hover:border-border-subtle hover:bg-surface'
                  }`}
                >
                  <div
                    className="relative overflow-hidden rounded-[4px]"
                    style={{ width: 26, height: 26, background: bg, border: '1px solid rgba(0,0,0,0.08)' }}
                  >
                    <div className="absolute bottom-[2px] inset-x-[2px] h-[3px] rounded-[1px]" style={{ background: accent }} />
                    <div className="absolute left-[2px] top-[6px] h-[6px] w-[6px] rounded-full" style={{ background: brand }} />
                  </div>
                  <span className={`font-mono text-[9px] font-semibold ${isActive ? 'text-primary' : 'text-fg-muted'}`}>
                    {t.name.split(' ')[0]}
                  </span>
                </button>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}

// ── Main component ───────────────────────────────────────────────────────────

interface LayoutMenuProps {
  open: boolean
  onClose: () => void
  anchorRef: React.RefObject<HTMLElement | null>
}

const CORE_RAIL_TABS = [
  { id: 'widgets',   Icon: LayoutGrid, label: 'Widgets' },
  { id: 'work',      Icon: ListTodo,   label: 'Work' },
  { id: 'workflows', Icon: GitBranch,  label: 'Workflows' },
  { id: 'inbox',     Icon: Mail,       label: 'Inbox' },
  { id: 'artifacts', Icon: Package,    label: 'Artifacts' },
] as const

const PRESETS: { id: LayoutPreset; icon: (p: { size?: number }) => React.ReactNode; label: string; left: boolean; right: boolean; drawer: boolean; chips: boolean }[] = [
  { id: 'focus',     icon: Icons.focus,     label: 'Focus',     left: false, right: false, drawer: false, chips: false },
  { id: 'default',   icon: Icons.default,   label: 'Default',   left: true,  right: false, drawer: false, chips: true  },
  { id: 'workspace', icon: Icons.workspace, label: 'Workspace', left: true,  right: true,  drawer: true,  chips: true  },
  { id: 'reading',   icon: Icons.reading,   label: 'Reading',   left: false, right: false, drawer: false, chips: true  },
]

export function LayoutMenu({ open, onClose, anchorRef }: LayoutMenuProps) {
  const leftOpen        = useLayoutStore((s) => s.leftSidebarOpen)
  const rightOpen       = useLayoutStore((s) => s.rightRailOpen)
  const toolDrawerEnabled = useLayoutStore((s) => s.toolDrawerEnabled)
  const chipsVisible    = useLayoutStore((s) => s.headerChipsVisible)
  const activeRailTab   = useLayoutStore((s) => s.rightRailTab)
  const workspaceVisible   = useLayoutStore((s) => s.leftRailWorkspaceVisible)
  const newChatVisible     = useLayoutStore((s) => s.leftRailNewChatVisible)
  const searchVisible      = useLayoutStore((s) => s.leftRailSearchVisible)
  const toggleWorkspace    = useLayoutStore((s) => s.toggleLeftRailWorkspace)
  const toggleNewChat      = useLayoutStore((s) => s.toggleLeftRailNewChat)
  const toggleSearchField  = useLayoutStore((s) => s.toggleLeftRailSearch)
  const toggleLeft         = useLayoutStore((s) => s.toggleLeftSidebar)
  const toggleRight     = useLayoutStore((s) => s.toggleRightRail)
  const toggleDrawer    = useLayoutStore((s) => s.toggleToolDrawer)
  const toggleChips     = useLayoutStore((s) => s.toggleHeaderChips)
  const applyPreset     = useLayoutStore((s) => s.applyLayoutPreset)
  const setRightRail    = useLayoutStore((s) => s.setRightRail)
  const setRailTab      = useLayoutStore((s) => s.setRightRailTab)
  const pluginTabs      = usePluginSlots('right-rail-tab')

  const drawerOn = toolDrawerEnabled
  const activePreset = PRESETS.find((p) =>
    p.left === leftOpen && p.right === rightOpen &&
    p.drawer === drawerOn && p.chips === chipsVisible
  )?.id

  // Fixed position — computed from the anchor button's bounding rect
  const [pos, setPos] = useState<{ left: number; bottom: number } | null>(null)
  useEffect(() => {
    if (!open || !anchorRef.current) return
    const r = anchorRef.current.getBoundingClientRect()
    setPos({ left: r.left, bottom: window.innerHeight - r.top + 8 })
  }, [open, anchorRef])

  const menuRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    function onDown(e: MouseEvent) {
      if (
        menuRef.current && !menuRef.current.contains(e.target as Node) &&
        anchorRef.current && !anchorRef.current.contains(e.target as Node)
      ) onClose()
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open, onClose, anchorRef])

  if (!open || !pos) return null

  const allRailTabs = [
    ...CORE_RAIL_TABS,
    ...pluginTabs.map((e) => ({ id: e.id, Icon: resolveIcon(e.icon), label: e.label })),
  ]

  const leftRailFeatures = [
    { id: 'workspace', Icon: Building2, label: 'Workspace header', on: workspaceVisible, toggle: toggleWorkspace },
    { id: 'newchat',   Icon: Plus,      label: 'New chat button',  on: newChatVisible,   toggle: toggleNewChat },
    { id: 'search',    Icon: Search,    label: 'Search field',     on: searchVisible,    toggle: toggleSearchField },
  ]

  return createPortal(
    <div
      ref={menuRef}
      className="fixed z-[9999] flex overflow-hidden rounded-[10px] border border-border bg-bg-elevated shadow-[0_12px_40px_rgba(0,0,0,0.35)]"
      style={{ left: pos.left, bottom: pos.bottom }}
    >
      {/* ── Left strip — left rail feature toggles ── */}
      <div className="flex flex-col border-r border-divider">
        {leftRailFeatures.map(({ id, Icon, label, on, toggle }) => (
          <button
            key={id}
            type="button"
            title={label}
            onClick={toggle}
            className={`flex h-9 w-9 items-center justify-center transition-colors ${
              on ? 'text-primary bg-primary-muted' : 'text-fg-muted hover:bg-surface hover:text-fg'
            }`}
          >
            <Icon className="h-[15px] w-[15px]" />
          </button>
        ))}
      </div>

      {/* ── Main panel ── */}
      <div className="w-[280px]">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-divider px-3 py-2.5">
          <div className="flex items-center gap-2">
            <span className="font-mono text-[10px] font-semibold uppercase tracking-[0.05em] text-fg-muted">
              Layout
            </span>
            <LMKbd>⌘\</LMKbd>
          </div>
          <ThemeDropdown />
        </div>

        {/* Toggle rows */}
        <div className="p-1.5">
          <LMRow icon={Icons.panelL} label="Left rail"    kbd="⌘B"   on={leftOpen}     onClick={toggleLeft} />
          <LMRow icon={Icons.panelR} label="Right rail"   kbd="⌘/"   on={rightOpen}    onClick={toggleRight} />
          <LMRow icon={Icons.drawer} label="Tool drawer"  kbd="⌘T"   on={drawerOn}     onClick={toggleDrawer} />
          <LMRow icon={Icons.chips}  label="Header chips" kbd="⌘⇧H"  on={chipsVisible} onClick={toggleChips} />
        </div>

        {/* Presets */}
        <div className="flex gap-1.5 border-t border-divider bg-surface p-2">
          {PRESETS.map((p) => (
            <LMPreset
              key={p.id}
              icon={p.icon}
              label={p.label}
              active={activePreset === p.id}
              onClick={() => applyPreset(p.id)}
            />
          ))}
        </div>
      </div>

      {/* ── Right-side tab strip ── */}
      <div className="flex flex-col border-l border-divider">
        {allRailTabs.map(({ id, Icon, label }) => {
          const isActive = activeRailTab === id
          return (
            <button
              key={id}
              type="button"
              title={label}
              onClick={() => { setRightRail(true); setRailTab(id) }}
              className={`flex h-9 w-9 items-center justify-center transition-colors ${
                isActive
                  ? 'text-primary bg-primary-muted'
                  : 'text-fg-muted hover:bg-surface hover:text-fg'
              }`}
            >
              <Icon className="h-[15px] w-[15px]" />
            </button>
          )
        })}
      </div>
    </div>,
    document.body,
  )
}

// ── Trigger button (rendered in ComposerToolbar) ─────────────────────────────

import { forwardRef } from 'react'

export const LayoutMenuTrigger = forwardRef<HTMLButtonElement, { open: boolean; onClick: () => void }>(
  function LayoutMenuTrigger({ open, onClick }, ref) {
    return (
      <button
        ref={ref}
        type="button"
        onClick={onClick}
        title="Layout (⌘\)"
        className={`flex h-[22px] w-[22px] items-center justify-center rounded-[4px] transition-all duration-120 ${
          open
            ? 'bg-primary-muted text-primary'
            : 'bg-transparent text-fg-muted hover:bg-surface hover:text-fg'
        }`}
      >
        <Icons.layout size={13} />
      </button>
    )
  }
)
