import { forwardRef, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import {
  Building2, ChevronDown, Eye, EyeOff,
  GitBranch, LayoutGrid, ListTodo, Mail, Moon,
  Package, Plus, Search, Sun,
} from 'lucide-react'
import { useLayoutStore, type LayoutPreset } from '@/stores/useLayoutStore'
import { usePluginSlots } from '@/hooks/usePluginSlots'
import { resolveIcon } from '@/lib/icons'
import { useTheme } from '@/hooks/useTheme'
import { BUILTIN_THEMES } from '@/lib/theme/defaults'

// ── SVG wireframe icons ────────────────────────────────────────────────────

function WireIcon({ children, size = 13 }: { children: React.ReactNode; size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor"
      strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round">
      {children}
    </svg>
  )
}

const WI = {
  focus:    () => <WireIcon><rect x="6" y="4" width="12" height="16" rx="2"/></WireIcon>,
  default:  () => <WireIcon><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18"/></WireIcon>,
  full:     () => <WireIcon><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18M15 3v18"/></WireIcon>,
  reading:  () => <WireIcon><rect x="6" y="3" width="12" height="18" rx="2"/><path d="M9 8h6M9 12h6M9 16h4"/></WireIcon>,
  drawer:   () => <WireIcon size={11}><rect x="3" y="6" width="18" height="14" rx="2"/><path d="M3 12h18"/></WireIcon>,
  layout:   () => <WireIcon size={13}><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18M3 12h6"/></WireIcon>,
}

// ── Kbd badge ──────────────────────────────────────────────────────────────

function LMKbd({ children }: { children: React.ReactNode }) {
  return (
    <span className="rounded-[3px] border border-border-subtle bg-surface px-[5px] py-px font-mono text-[10px] leading-none text-fg-muted">
      {children}
    </span>
  )
}

// ── Eye toggle button ──────────────────────────────────────────────────────

function EyeBtn({ on, onClick, size = 11 }: { on: boolean; onClick?: () => void; size?: number }) {
  const Icon = on ? Eye : EyeOff
  return (
    <button
      type="button"
      onClick={(e) => { e.stopPropagation(); onClick?.() }}
      className="flex items-center justify-center w-[18px] h-[18px] rounded-[3px] transition-colors text-fg-muted hover:bg-surface hover:text-fg"
    >
      <Icon style={{ width: size, height: size }} />
    </button>
  )
}

// ── Left rail feature row ─────────────────────────────────────────────────

function LeftRailRow({
  icon: Icon, label, on, onToggle, railOn,
}: {
  icon: React.ElementType; label: string; on: boolean; onToggle: () => void; railOn: boolean
}) {
  return (
    <div
      className={`flex items-center gap-1.5 px-1.5 py-[3.5px] rounded-[4px] transition-all ${
        on ? 'border border-border-subtle bg-surface' : 'border border-transparent'
      } ${!railOn ? 'opacity-30' : on ? '' : 'opacity-55'}`}
    >
      <Icon size={10} className="text-fg-muted shrink-0" />
      <span className={`flex-1 text-[9.5px] truncate leading-none ${on ? 'text-fg' : 'text-fg-muted'}`}>
        {label}
      </span>
      <EyeBtn on={on} onClick={railOn ? onToggle : undefined} size={10} />
    </div>
  )
}

// ── Right rail layer row ──────────────────────────────────────────────────

function RightLayerRow({
  icon: Icon, label, active, onSelect, railOn,
}: {
  icon: React.ElementType; label: string; active: boolean; onSelect: () => void; railOn: boolean
}) {
  return (
    <button
      type="button"
      onClick={railOn ? onSelect : undefined}
      className={`flex w-full items-center gap-1.5 px-1.5 py-[3.5px] rounded-[4px] border transition-all text-left ${
        active && railOn ? 'border-primary bg-primary-muted' : 'border-transparent'
      } ${!railOn ? 'opacity-30 cursor-default' : 'cursor-pointer'}`}
    >
      <Icon size={10} className={active && railOn ? 'text-primary' : 'text-fg-muted'} />
      <span className={`flex-1 text-[9.5px] truncate ${active && railOn ? 'text-primary font-semibold' : 'text-fg-secondary'}`}>
        {label}
      </span>
      {active && railOn && (
        <div className="w-1.5 h-1.5 rounded-full bg-primary shrink-0" />
      )}
    </button>
  )
}

// ── Theme + mode dropdown ─────────────────────────────────────────────────


function useIsDark() {
  const theme = useLayoutStore((s) => s.theme)
  return theme === 'dark' || (theme === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches)
}

function ThemeTrigger({ onClick, expanded }: { onClick: () => void; expanded: boolean }) {
  const { activeThemeId } = useTheme()
  const isDark = useIsDark()
  const activeBuiltin = BUILTIN_THEMES.find((t) => t.id === activeThemeId) ?? BUILTIN_THEMES[0]
  const swatchBg = activeBuiltin.tokens.light['bg-elevated'] ?? '#ffffff'
  const swatchAccent = activeBuiltin.tokens.light['primary'] ?? '#000000'

  return (
    <button
      type="button"
      onClick={onClick}
      className={`flex items-center gap-2 rounded-[6px] border px-2.5 py-1.5 transition-colors min-w-[140px] ${
        expanded ? 'border-border bg-surface' : 'border-border-subtle hover:border-border hover:bg-surface'
      }`}
    >
      <div
        className="relative overflow-hidden rounded-[3px] shrink-0"
        style={{ width: 16, height: 16, background: swatchBg, border: '1px solid rgba(0,0,0,0.12)' }}
      >
        <div className="absolute bottom-[1px] inset-x-[1px] h-[4px] rounded-[1px]" style={{ background: swatchAccent }} />
      </div>
      <span className="font-mono text-[11px] font-semibold text-fg-secondary flex-1 text-left truncate">
        {activeBuiltin.name.split(' ')[0]}·{isDark ? 'D' : 'L'}
      </span>
      <ChevronDown className="h-[10px] w-[10px] text-fg-faint shrink-0" />
    </button>
  )
}

function LightDarkToggle() {
  const isDark = useIsDark()
  const setTheme = useLayoutStore((s) => s.setTheme)
  const Icon = isDark ? Moon : Sun
  return (
    <button
      type="button"
      onClick={() => setTheme(isDark ? 'light' : 'dark')}
      title={isDark ? 'Switch to light' : 'Switch to dark'}
      aria-label={isDark ? 'Switch to light mode' : 'Switch to dark mode'}
      className="flex items-center justify-center rounded-[6px] border border-border-subtle px-2 py-1.5 text-fg-secondary transition-colors hover:border-border hover:bg-surface hover:text-fg"
    >
      <Icon className="h-3.5 w-3.5" />
    </button>
  )
}

function ThemeOverlay({ onClose }: { onClose: () => void }) {
  const { activeThemeId, setActiveTheme } = useTheme()
  const isDark = useIsDark()

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <div className="absolute inset-0 z-20 flex items-center justify-center">
      <button
        type="button"
        aria-label="Close theme picker"
        onClick={onClose}
        className="absolute inset-0 cursor-default bg-black/40 backdrop-blur-[2px]"
      />
      <div
        className="no-scrollbar relative h-[80%] w-[80%] overflow-y-auto rounded-[12px] border border-border bg-bg-elevated p-4 shadow-[0_16px_48px_rgba(15,17,22,0.32)]"
      >
        <div className="mb-3 flex items-center justify-between">
          <span className="font-mono text-[10px] font-semibold uppercase tracking-[0.05em] text-fg-muted">
            Theme
          </span>
          <button
            type="button"
            onClick={onClose}
            className="rounded-[4px] px-1.5 py-0.5 font-mono text-[10px] text-fg-muted transition-colors hover:bg-surface hover:text-fg"
          >
            Esc
          </button>
        </div>
        <div className="grid grid-cols-2 gap-2">
          {BUILTIN_THEMES.map((t) => {
            const bg      = isDark ? (t.tokens.dark['bg-elevated']  ?? '#111') : (t.tokens.light['bg-elevated']  ?? '#fff')
            const surface = isDark ? (t.tokens.dark['surface']      ?? '#222') : (t.tokens.light['surface']      ?? '#eee')
            const brand   = isDark ? (t.tokens.dark['brand']        ?? '#888') : (t.tokens.light['brand']        ?? '#888')
            const primary = isDark ? (t.tokens.dark['primary']      ?? '#fff') : (t.tokens.light['primary']      ?? '#000')
            const fg2     = isDark ? (t.tokens.dark['fg-secondary'] ?? '#aaa') : (t.tokens.light['fg-secondary'] ?? '#555')
            const isActive = t.id === activeThemeId
            return (
              <button
                key={t.id}
                type="button"
                onClick={() => { setActiveTheme(t.id); onClose() }}
                className={`flex items-center gap-2.5 rounded-[7px] border px-2.5 py-2 text-left transition-colors ${
                  isActive ? 'border-primary bg-primary-muted' : 'border-border-subtle hover:border-border hover:bg-surface'
                }`}
              >
                <div
                  className="shrink-0 rounded-[5px] overflow-hidden flex flex-col gap-[3px] p-[5px]"
                  style={{ width: 48, height: 40, background: bg, border: '1px solid rgba(0,0,0,0.10)' }}
                >
                  <div className="flex items-center gap-[3px]">
                    <div className="w-[8px] h-[8px] rounded-[2px]" style={{ background: brand }} />
                    <div className="h-[4px] rounded-[2px] flex-1" style={{ background: surface }} />
                  </div>
                  <div className="h-[3px] rounded-[2px]" style={{ background: surface, width: '80%' }} />
                  <div className="h-[3px] rounded-[2px]" style={{ background: surface, width: '60%' }} />
                  <div className="mt-auto flex items-center gap-[3px]">
                    <div className="h-[5px] rounded-[2px] flex-1" style={{ background: surface }} />
                    <div className="w-[8px] h-[5px] rounded-[2px]" style={{ background: primary }} />
                  </div>
                </div>
                <div className="flex flex-col gap-1.5 min-w-0 flex-1">
                  <span className={`text-[11px] font-medium leading-tight ${isActive ? 'text-primary' : 'text-fg'}`}>
                    {t.name.split(' ')[0]}
                  </span>
                  <div className="flex items-center gap-1">
                    <div className="w-3 h-3 rounded-full border border-black/10" style={{ background: brand }} title="Brand" />
                    <div className="w-3 h-3 rounded-full border border-black/10" style={{ background: primary }} title="Primary" />
                    <div className="w-3 h-3 rounded-full border border-black/10" style={{ background: fg2 }} title="Secondary" />
                  </div>
                </div>
              </button>
            )
          })}
        </div>
      </div>
    </div>
  )
}

// ── Data ───────────────────────────────────────────────────────────────────

const PRESETS: { id: LayoutPreset; icon: () => React.ReactNode; label: string; left: boolean; right: boolean; drawer: boolean; chips: boolean }[] = [
  { id: 'focus',     icon: WI.focus,   label: 'Focus',   left: false, right: false, drawer: false, chips: false },
  { id: 'default',   icon: WI.default, label: 'Default', left: true,  right: false, drawer: false, chips: true },
  { id: 'workspace', icon: WI.full,    label: 'Full',    left: true,  right: true,  drawer: true,  chips: true },
  { id: 'reading',   icon: WI.reading, label: 'Reading', left: false, right: false, drawer: false, chips: true },
]

const CORE_RAIL_TABS = [
  { id: 'widgets',   Icon: LayoutGrid, label: 'Widgets' },
  { id: 'inbox',     Icon: Mail,       label: 'Inbox' },
  { id: 'work',      Icon: ListTodo,   label: 'Plan' },
  { id: 'workflows', Icon: GitBranch,  label: 'Workflows' },
  { id: 'artifacts', Icon: Package,    label: 'Artifacts' },
] as const

// ── Main component ─────────────────────────────────────────────────────────

interface LayoutMenuProps {
  open: boolean
  onClose: () => void
  anchorRef: React.RefObject<HTMLElement | null>
}

export function LayoutMenu({ open, onClose, anchorRef }: LayoutMenuProps) {
  const leftOpen          = useLayoutStore((s) => s.leftSidebarOpen)
  const rightOpen         = useLayoutStore((s) => s.rightRailOpen)
  const toolDrawerEnabled = useLayoutStore((s) => s.toolDrawerEnabled)
  const chipsVisible      = useLayoutStore((s) => s.headerChipsVisible)
  const activeRailTab     = useLayoutStore((s) => s.rightRailTab)
  const workspaceVisible  = useLayoutStore((s) => s.leftRailWorkspaceVisible)
  const newChatVisible    = useLayoutStore((s) => s.leftRailNewChatVisible)
  const searchVisible     = useLayoutStore((s) => s.leftRailSearchVisible)
  const toggleWorkspace   = useLayoutStore((s) => s.toggleLeftRailWorkspace)
  const toggleNewChat     = useLayoutStore((s) => s.toggleLeftRailNewChat)
  const toggleSearchField = useLayoutStore((s) => s.toggleLeftRailSearch)
  const toggleLeft        = useLayoutStore((s) => s.toggleLeftSidebar)
  const toggleRight       = useLayoutStore((s) => s.toggleRightRail)
  const toggleDrawer      = useLayoutStore((s) => s.toggleToolDrawer)
  const toggleChips       = useLayoutStore((s) => s.toggleHeaderChips)
  const applyPreset       = useLayoutStore((s) => s.applyLayoutPreset)
  const setRightRail      = useLayoutStore((s) => s.setRightRail)
  const setRailTab        = useLayoutStore((s) => s.setRightRailTab)
  const pluginTabs        = usePluginSlots('right-rail-tab')

  const activePreset = PRESETS.find((p) =>
    p.left === leftOpen && p.right === rightOpen &&
    p.drawer === toolDrawerEnabled && p.chips === chipsVisible
  )?.id

  const [themeOverlayOpen, setThemeOverlayOpen] = useState(false)

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

  useEffect(() => {
    if (!open) setThemeOverlayOpen(false)
  }, [open])

  if (!open) return null

  const allRailTabs = [
    ...CORE_RAIL_TABS,
    ...pluginTabs.map((e) => ({ id: e.id, Icon: resolveIcon(e.icon), label: e.label })),
  ]

  const leftColWidth = leftOpen ? 168 : 18
  const rightColWidth = rightOpen ? 152 : 18

  const leftFeatures = [
    { id: 'workspace', Icon: Building2, label: 'Workspace',     on: workspaceVisible, toggle: toggleWorkspace },
    { id: 'newchat',   Icon: Plus,      label: 'New chat',      on: newChatVisible,   toggle: toggleNewChat },
    { id: 'search',    Icon: Search,    label: 'Search',        on: searchVisible,    toggle: toggleSearchField },
  ]

  return createPortal(
    <div
      ref={menuRef}
      className="fixed left-1/2 top-1/2 z-[9999] -translate-x-1/2 -translate-y-1/2"
      style={{ width: 696 }}
    >
      {/* ── Main panel ── */}
      <div className="relative rounded-[12px] border border-border bg-bg-elevated shadow-[0_16px_48px_rgba(15,17,22,0.18)] overflow-hidden">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-divider px-3 py-2.5">
          <div className="flex items-center gap-2">
            <WI.layout />
            <span className="font-mono text-[10px] font-semibold uppercase tracking-[0.05em] text-fg-muted">
              Layout
            </span>
            <LMKbd>⌘\</LMKbd>
          </div>
          <div className="flex items-center gap-1.5">
            <ThemeTrigger
              onClick={() => setThemeOverlayOpen((v) => !v)}
              expanded={themeOverlayOpen}
            />
            <LightDarkToggle />
          </div>
        </div>

        {/* 3-column body */}
        <div
          className="bg-bg overflow-hidden"
          style={{
            display: 'grid',
            gridTemplateColumns: `${leftColWidth}px 1fr ${rightColWidth}px`,
            minHeight: 288,
            transition: 'grid-template-columns 200ms',
          }}
        >
          {/* ─── Left rail column ─── */}
          <div
            className="border-r border-divider flex flex-col transition-all"
            style={{
              background: leftOpen ? 'var(--c-bg)' : 'var(--c-surface)',
              padding: leftOpen ? '8px 6px' : 0,
              alignItems: leftOpen ? 'stretch' : 'center',
              justifyContent: leftOpen ? 'flex-start' : 'center',
              gap: leftOpen ? 4 : 0,
            }}
          >
            {leftOpen ? (
              <>
                <div className="flex items-center justify-between px-1.5 pb-1.5">
                  <span className="font-mono text-[8.5px] font-semibold uppercase tracking-[0.05em] text-fg-faint">
                    Left Rail
                  </span>
                  <EyeBtn on={true} onClick={toggleLeft} size={11} />
                </div>
                {leftFeatures.map(({ id, Icon, label, on, toggle }) => (
                  <LeftRailRow
                    key={id}
                    icon={Icon}
                    label={label}
                    on={on}
                    onToggle={toggle}
                    railOn={leftOpen}
                  />
                ))}
              </>
            ) : (
              <button
                type="button"
                onClick={toggleLeft}
                className="flex items-center justify-center w-full h-full text-fg-muted hover:text-fg transition-colors"
              >
                <EyeOff size={11} />
              </button>
            )}
          </div>

          {/* ─── Center wireframe ─── */}
          <div className="flex flex-col gap-2 p-2.5 min-w-0">
            {/* Header strip */}
            <div className="flex items-center gap-1.5 rounded-[5px] border border-divider bg-bg-elevated px-2 py-1.5 min-h-[26px]">
              <div className="w-3.5 h-3.5 rounded-[3px] bg-brand shrink-0" />
              <span className="text-[9px] font-medium text-fg-secondary">Nanite</span>
              <div className="w-px h-2.5 bg-divider" />
              {chipsVisible ? (
                <>
                  <div className="h-3 px-1.5 rounded-[3px] bg-surface flex items-center font-mono text-[8px] text-fg-muted shrink-0">claude</div>
                  <div className="h-3 px-1.5 rounded-[3px] bg-surface flex items-center font-mono text-[8px] text-fg-muted shrink-0">153t</div>
                </>
              ) : (
                <span className="font-mono text-[8px] text-fg-faint uppercase tracking-wide opacity-60">chips hidden</span>
              )}
              <div className="flex-1" />
              <EyeBtn on={chipsVisible} onClick={toggleChips} size={10} />
            </div>

            {/* Tool drawer */}
            {toolDrawerEnabled ? (
              <div className="flex items-center gap-1.5 rounded-[5px] border border-divider bg-bg-elevated px-2 py-1.5">
                <WI.drawer />
                <span className="font-mono text-[9px] font-semibold uppercase tracking-[0.04em] text-fg-muted flex-1">
                  Tool Drawer
                </span>
                <div className="flex-1 h-px bg-divider mx-1" />
                <EyeBtn on={true} onClick={toggleDrawer} size={10} />
              </div>
            ) : (
              <div className="flex items-center gap-1.5 rounded-[5px] border border-dashed border-border-subtle px-2 py-1.5 opacity-60">
                <WI.drawer />
                <span className="font-mono text-[9px] font-semibold uppercase tracking-[0.04em] text-fg-faint flex-1">
                  Drawer hidden
                </span>
                <EyeBtn on={false} onClick={toggleDrawer} size={10} />
              </div>
            )}

            {/* Conversation skeleton */}
            <div className="flex-1 flex flex-col gap-1.5 py-1 min-h-[40px]">
              <div className="h-[5px] rounded-[2px] bg-surface" style={{ width: '70%' }} />
              <div className="h-[5px] rounded-[2px] bg-surface" style={{ width: '55%' }} />
              <div className="h-[5px] rounded-[2px] bg-surface mt-1" style={{ width: '62%' }} />
            </div>

            {/* Composer skeleton */}
            <div className="rounded-[5px] border border-divider bg-bg-elevated px-2 py-2">
              <div className="h-[5px] rounded-[2px] bg-surface mb-2" style={{ width: '70%' }} />
              <div className="flex items-center justify-between">
                <div className="w-3 h-3 rounded-[3px] bg-surface" />
                <div className="w-4 h-3 rounded-[3px] bg-primary" />
              </div>
            </div>
          </div>

          {/* ─── Right rail column ─── */}
          <div
            className="border-l border-divider flex flex-col transition-all"
            style={{
              background: rightOpen ? 'var(--c-bg)' : 'var(--c-surface)',
              padding: rightOpen ? '8px 6px' : 0,
              alignItems: rightOpen ? 'stretch' : 'center',
              justifyContent: rightOpen ? 'flex-start' : 'center',
              gap: rightOpen ? 2 : 0,
            }}
          >
            {rightOpen ? (
              <>
                <div className="flex items-center justify-between px-1.5 pb-1">
                  <span className="font-mono text-[8.5px] font-semibold uppercase tracking-[0.05em] text-fg-faint">
                    Right Rail
                  </span>
                  <EyeBtn on={true} onClick={toggleRight} size={11} />
                </div>
                <div className="px-1.5 pb-2 text-[8px] italic text-fg-faint leading-tight">
                  Pick which rail is shown
                </div>
                {allRailTabs.map(({ id, Icon, label }) => (
                  <RightLayerRow
                    key={id}
                    icon={Icon}
                    label={label}
                    active={activeRailTab === id}
                    onSelect={() => { setRightRail(true); setRailTab(id) }}
                    railOn={rightOpen}
                  />
                ))}
              </>
            ) : (
              <button
                type="button"
                onClick={toggleRight}
                className="flex items-center justify-center w-full h-full text-fg-muted hover:text-fg transition-colors"
              >
                <EyeOff size={11} />
              </button>
            )}
          </div>
        </div>
      </div>

      {/* ── Presets footer — sits below the main panel ── */}
      <div className="mx-auto flex gap-1.5 rounded-b-[10px] border border-t-0 border-border bg-bg-elevated px-2 py-2 shadow-[0_12px_28px_rgba(15,17,22,0.06)]"
        style={{ width: '92%' }}>
        {PRESETS.map((p) => {
          const isActive = activePreset === p.id
          return (
            <button
              key={p.id}
              type="button"
              onClick={() => applyPreset(p.id)}
              className={`flex flex-1 flex-col items-center gap-1 rounded-[5px] border px-1 py-1.5 transition-colors ${
                isActive
                  ? 'border-primary bg-primary-muted text-primary'
                  : 'border-border-subtle text-fg-secondary hover:border-border hover:bg-surface'
              }`}
            >
              <p.icon />
              <span className="font-mono text-[8.5px] font-semibold uppercase tracking-[0.04em]">
                {p.label}
              </span>
            </button>
          )
        })}
      </div>

      {themeOverlayOpen && <ThemeOverlay onClose={() => setThemeOverlayOpen(false)} />}
    </div>,
    document.body,
  )
}

// ── Trigger button ─────────────────────────────────────────────────────────

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
        <WI.layout />
      </button>
    )
  }
)
