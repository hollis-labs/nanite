/**
 * ChatWorkingDrawer — bottom drawer of the 2026-05-01 chat-surface redesign.
 *
 * Working surface that lives ABOVE the composer. Pull-tab pattern: a
 * thin drag handle is always visible at the top of the drawer overlay;
 * dragging it down expands the body, double-click toggles, releasing
 * near 0 height auto-closes. The body is absolute-positioned so it
 * overlays the transcript above instead of pushing it; transparent
 * margins keep transcript content visible alongside.
 *
 * Body content is a 2-column layout: main tab content on the left,
 * vertical tab sidebar on the right. Tabs include a fixed set
 * (Scratchpad, Terminal 1, Terminal 2 [dev-only], Artifacts, Session
 * Context) plus a dynamic set of `card:<uuid>` tabs sourced from
 * `panelEnvelopes['bottom_chat_drawer']`.
 *
 * Alert overlay: when any of `sessionTakeover` / `circuitOpen` is
 * active, the drawer auto-opens (if closed), the body content fades
 * + becomes non-interactive, and a centered Banner overlay is rendered
 * with a glass backdrop. Drag is locked while the alert is up;
 * restored to the prior open/close state on dismiss.
 *
 * Mounted by `ChatMain.tsx`; receives alert flags + handlers from
 * `useChat`. Pinned-card lifecycle uses the existing
 * `api.pinDrawerCard` / `api.unpinDrawerCard` endpoints (cap of
 * `CHAT_DRAWER_PIN_CAP` per session, enforced server-side as
 * `DrawerPinCapError` on HTTP 409).
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { GripHorizontal, Pin, PinOff, X } from 'lucide-react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useAppStore } from '@/stores/useAppStore'
import { useShellStore } from '@/stores/useShellStore'
import { useChatStore } from '@/stores/useChatStore'
import { useSettings } from '@/hooks/useSettings'
import type { ChatDrawerTab } from '@/components/chat/ChatDrawerTabStrip'
import { EnvelopeRenderer } from '@/components/chat/envelopes/EnvelopeRenderer'
import { ArtifactsContent } from '@/components/drawers/ArtifactsContent'
import { Banner, type BannerProps } from '@/components/chat/Banner'
import { api, DrawerPinCapError } from '@/lib/api'
import { CHAT_DRAWER_PIN_CAP } from '@/lib/constants'
import type { DynamicCardTab, Envelope } from '@/lib/types'

const FIXED_TABS: { id: string; label: string; devOnly?: boolean }[] = [
  { id: 'scratchpad', label: 'Scratchpad' },
  { id: 'terminal-1', label: 'Terminal 1' },
  { id: 'terminal-2', label: 'Terminal 2', devOnly: true },
  { id: 'artifacts', label: 'Artifacts' },
  { id: 'session-context', label: 'Session Context' },
]

export interface ChatWorkingDrawerProps {
  /** Alert flags from useChat. When any is true the drawer auto-opens
   *  (if closed) and renders a Banner overlay over the body content. */
  sessionTakeover?: boolean
  circuitOpen?: boolean
  /** Action handlers. */
  onRetry?: () => void
  onDismissCircuit?: () => void
}

export function ChatWorkingDrawer({
  sessionTakeover = false,
  circuitOpen = false,
  onRetry,
  onDismissCircuit,
}: ChatWorkingDrawerProps = {}) {
  const drawer = useLayoutStore((s) => s.chatWorkingDrawer)
  const setDrawer = useLayoutStore((s) => s.setChatWorkingDrawer)
  const cardTabs = useLayoutStore((s) => s.chatWorkingDrawerCardTabs)
  const appendCardTab = useLayoutStore((s) => s.appendChatWorkingDrawerCardTab)
  const removeCardTab = useLayoutStore((s) => s.removeChatWorkingDrawerCardTab)
  const panelEnvelopes = useLayoutStore((s) => s.panelEnvelopes)
  const clearPanelEnvelopes = useLayoutStore((s) => s.clearPanelEnvelopes)
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const queryClient = useQueryClient()
  const { data: settings } = useSettings()
  const developerMode = settings?.developer_mode ?? false

  // Reactive: when a new envelope arrives in panelEnvelopes['bottom_chat_drawer'],
  // append it as a DynamicCardTab and clear the inbox slot.
  useEffect(() => {
    const incoming = panelEnvelopes['bottom_chat_drawer'] ?? []
    if (incoming.length === 0) return
    for (const env of incoming) {
      // `focused` isn't a declared field on Envelope but the routing layer
      // may stamp it; we treat absence as "default true" per the plan.
      const focusedFlag = (env as Envelope & { focused?: boolean }).focused
      const tab: DynamicCardTab = {
        id: `card:${env.id ?? crypto.randomUUID()}`,
        label: env.title || envelopeFallbackLabel(env),
        payload: env,
        focused: focusedFlag !== false,
        pinned: false,
        createdAt: Date.now(),
      }
      appendCardTab(tab)
    }
    clearPanelEnvelopes('bottom_chat_drawer')
  }, [panelEnvelopes, appendCardTab, clearPanelEnvelopes])

  const visibleFixedTabs = developerMode
    ? FIXED_TABS
    : FIXED_TABS.filter((t) => !t.devOnly)

  const tabs: ChatDrawerTab[] = useMemo(() => {
    const fixed: ChatDrawerTab[] = visibleFixedTabs.map((t) => ({
      id: t.id,
      label: t.label,
      active: drawer.activeTab === t.id,
    }))
    const dynamic: ChatDrawerTab[] = [...cardTabs]
      .sort((a, b) => a.createdAt - b.createdAt)
      .map((c) => ({
        id: c.id,
        label: c.label,
        active: drawer.activeTab === c.id,
        closeable: !c.pinned,
        pinnable: true,
        pinned: c.pinned,
      }))
    return [...fixed, ...dynamic]
  }, [visibleFixedTabs, cardTabs, drawer.activeTab])

  if (!activeSessionId) return null

  // ── Alert overlay state machine ─────────────────────────────────────────────
  // When any alert becomes active, snapshot the drawer's open state so we can
  // restore it on dismissal: if the drawer was closed when the alert fired,
  // close it again when the alert clears; if it was already open, leave it.
  const alertActive = sessionTakeover || circuitOpen
  const wasOpenBeforeAlertRef = useRef<boolean | null>(null)

  // Pull-tab drag mechanics. The drag-handle row is always visible; users
  // drag UP to grow the drawer, DOWN to shrink. Releasing at near-0 height
  // closes the drawer and snaps height back to a reasonable default.
  // While an alert overlay is active, the drag is locked so the drawer stays
  // at the size it had when the alert opened.
  const dragRef = useRef<{ y: number; height: number } | null>(null)
  const onPointerDown = (e: React.PointerEvent) => {
    if (alertActive) return
    dragRef.current = { y: e.clientY, height: drawer.height || 200 }
    e.currentTarget.setPointerCapture(e.pointerId)
    if (!drawer.open) setDrawer({ open: true })
  }
  const onPointerMove = (e: React.PointerEvent) => {
    if (alertActive) return
    const d = dragRef.current
    if (!d) return
    const next = Math.max(0, d.height - (e.clientY - d.y))
    setDrawer({ height: next })
  }
  const onPointerUp = (e: React.PointerEvent) => {
    if (!dragRef.current) return
    dragRef.current = null
    e.currentTarget.releasePointerCapture(e.pointerId)
    if (alertActive) return
    if (drawer.height < 24) setDrawer({ open: false, height: 200 })
  }
  const onDoubleClick = () => {
    if (alertActive) return
    setDrawer({ open: !drawer.open, height: drawer.height || 200 })
  }

  useEffect(() => {
    if (alertActive && wasOpenBeforeAlertRef.current === null) {
      wasOpenBeforeAlertRef.current = drawer.open
      if (!drawer.open) setDrawer({ open: true })
    } else if (!alertActive && wasOpenBeforeAlertRef.current !== null) {
      const wasOpen = wasOpenBeforeAlertRef.current
      wasOpenBeforeAlertRef.current = null
      if (!wasOpen) setDrawer({ open: false })
    }
  }, [alertActive, drawer.open, setDrawer])

  // Resolve which alert renders. Priority: takeover > circuit.
  const bannerProps: BannerProps | null = sessionTakeover
    ? {
        tone: 'info',
        title: 'This session is now active in another tab',
        body: 'The streaming connection moved to a newer tab. Reload to reconnect here.',
        actions: [{ label: 'Reload', onClick: () => window.location.reload(), primary: true, refresh: true }],
      }
    : circuitOpen
      ? {
          tone: 'warning',
          title: 'Provider rate limited after multiple retries',
          body: 'The API provider returned rate limit errors. Retry, or dismiss to keep the partial response.',
          actions: [
            ...(onRetry ? [{ label: 'Retry', onClick: onRetry, primary: true, refresh: true }] : []),
            ...(onDismissCircuit ? [{ label: 'Dismiss', onClick: onDismissCircuit, dismiss: true }] : []),
          ],
        }
      : null

  const showChatToast = useChatStore((s) => s.showChatToast)
  const onPinToggle = useCallback(async (id: string) => {
    const tab = cardTabs.find((t) => t.id === id)
    if (!tab || !activeSessionId) return
    if (tab.pinned) {
      const dbId = id.slice(5)
      try {
        await api.unpinDrawerCard(dbId)
        removeCardTab(id)
      } catch (err) {
        console.error('Unpin failed:', err)
        showChatToast('Failed to unpin card', 'info')
      }
    } else {
      // Stable content_ref: prefer the envelope's own id; fall back to the
      // dynamic-tab id (sans `card:` prefix) so pinned cards can still be
      // correlated even when the source envelope didn't carry an id.
      const contentRef = tab.payload.id ?? id.slice(5)
      try {
        await api.pinDrawerCard(activeSessionId, {
          card_type: 'agent-envelope',
          content_ref: contentRef,
          title: tab.label,
          payload: JSON.stringify(tab.payload),
        })
        queryClient.invalidateQueries({ queryKey: ['drawer-cards', activeSessionId] })
        removeCardTab(id)
      } catch (err) {
        if (err instanceof DrawerPinCapError) {
          showChatToast(`Pinned-card cap reached (${CHAT_DRAWER_PIN_CAP}). Unpin one to free a slot.`, 'info')
        } else {
          console.error('Pin failed:', err)
          showChatToast('Failed to pin card', 'info')
        }
      }
    }
  }, [cardTabs, activeSessionId, removeCardTab, queryClient, showChatToast])

  return (
    // Outer wrapper: column-width container. -mb-1.5 lets the in-flow
    // spacer's bottom tuck under the composer by ~6px, which (because the
    // overlay below is anchored to the wrapper's bottom) is the same edge
    // the drag handle visually tucks under.
    <div className="max-w-3xl w-full mx-auto relative -mb-1.5">
      <div className="w-[90%] mx-auto relative">
        {/* In-flow spacer — reserves the drag-handle's height (h-5 = 20px)
            so the chat layout always leaves that gap above the composer.
            The actual visible chrome lives in the absolute overlay below
            and is painted on top of this spacer. */}
        <div aria-hidden className="h-5 pointer-events-none" />

        {/* Absolute overlay — anchored to the inner wrapper's bottom. The
            drag handle is the TOP of this overlay; the body sits BELOW it.
            When open, the overlay extends UPWARD over the transcript above
            (the body grows up), keeping the drag handle attached to the
            overlay's top edge. The 5% margins on either side stay
            transparent so transcript content remains visible. */}
        <div className="absolute bottom-0 left-0 right-0 flex flex-col">
          {/* Drag handle — always visible, top of overlay. Rounded top
              corners + brand accent ribbon stop short of the edge. */}
          <div
            className="relative flex items-center justify-center h-5 overflow-hidden bg-bg-elevated border-x border-border-subtle border-t-2 border-t-primary rounded-t-[10px] cursor-row-resize select-none touch-none"
            onPointerDown={onPointerDown}
            onPointerMove={onPointerMove}
            onPointerUp={onPointerUp}
            onPointerCancel={onPointerUp}
            onDoubleClick={onDoubleClick}
            role="separator"
            aria-orientation="horizontal"
            aria-label="Drag to resize working drawer; double-click to toggle"
          >
            <GripHorizontal size={12} className="text-fg-muted pointer-events-none" />
          </div>

          {/* Body — below drag handle when active. 2-column layout (main
              content on the left, vertical tab sidebar on the right). When
              an alert/approval/notification is active, the body content is
              dimmed + non-interactive, and a Banner overlay floats on top. */}
          {drawer.open && (
            <div
              className="overflow-hidden border-x border-border-subtle bg-bg-elevated shadow-lg relative"
              style={{ height: drawer.height }}
            >
              {/* Content layer — fades when an alert is active. */}
              <div
                className={`flex h-full transition-opacity duration-200 ${
                  alertActive ? 'opacity-30 pointer-events-none' : 'opacity-100'
                }`}
              >
                <main className="flex-1 min-w-0 overflow-hidden">
                  <DrawerBody activeTab={drawer.activeTab} cardTabs={cardTabs} />
                </main>

                <aside className="w-[140px] shrink-0 border-l border-border-subtle bg-surface/30 overflow-y-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
                  <div className="flex flex-col gap-0.5 p-1.5">
                    {tabs.map((t) => (
                      <div
                        key={t.id}
                        className={`group relative flex items-center gap-1 px-2 py-1.5 rounded-[4px] font-mono text-[11px] tracking-wide transition-colors ${
                          t.active
                            ? 'bg-bg-elevated text-fg shadow-sm'
                            : 'text-fg-muted hover:bg-bg-elevated/60 hover:text-fg-secondary'
                        }`}
                      >
                        <button
                          type="button"
                          onClick={() => setDrawer({ activeTab: t.id })}
                          title={t.label}
                          className="flex-1 min-w-0 flex items-center gap-1.5 outline-none text-left"
                        >
                          <span className="truncate">{t.label}</span>
                          {t.runningPip && (
                            <span className="h-1.5 w-1.5 rounded-full bg-warning animate-pulse shrink-0" />
                          )}
                        </button>
                        {t.pinnable && (
                          <button
                            type="button"
                            onClick={() => onPinToggle(t.id)}
                            className="opacity-0 group-hover:opacity-100 transition-opacity shrink-0"
                            aria-label={t.pinned ? 'Unpin tab' : 'Pin tab'}
                          >
                            {t.pinned ? <PinOff size={10} /> : <Pin size={10} />}
                          </button>
                        )}
                        {t.closeable && (
                          <button
                            type="button"
                            onClick={() => removeCardTab(t.id)}
                            className="opacity-0 group-hover:opacity-100 transition-opacity shrink-0"
                            aria-label="Close tab"
                          >
                            <X size={10} />
                          </button>
                        )}
                      </div>
                    ))}
                  </div>
                </aside>
              </div>

              {/* Banner overlay — centered when an alert is active. A glass
                  backdrop sits between the dimmed body content and the
                  banner. inset-1.5 leaves a 6px gap between the frost and
                  the body's border on all four sides. bg-fg/8 keeps the
                  frost subtle and auto-adapts to the theme (dark fg on
                  light theme → faint dark glass; light fg on dark theme →
                  faint light glass). */}
              {alertActive && bannerProps && (
                <div className="absolute inset-0 flex items-center justify-center p-6">
                  <div className="absolute inset-1.5 rounded-[6px] bg-fg/8 backdrop-blur-[2px]" />
                  <div className="relative w-[85%]">
                    <Banner {...bannerProps} />
                  </div>
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

function DrawerBody({ activeTab, cardTabs }: { activeTab: string; cardTabs: DynamicCardTab[] }) {
  switch (activeTab) {
    case 'scratchpad':
      return <ScratchpadTab />
    case 'terminal-1':
      return <Terminal1Tab />
    case 'terminal-2':
      return <Terminal2Tab />
    case 'artifacts':
      return <ArtifactsTab />
    case 'session-context':
      return <SessionContextTab />
    default:
      if (activeTab.startsWith('card:')) {
        const tab = cardTabs.find((t) => t.id === activeTab)
        return tab ? <EnvelopeRenderer envelope={tab.payload} /> : null
      }
      return null
  }
}

function envelopeFallbackLabel(env: Envelope): string {
  const ts = new Date().toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })
  const typeLabel = env.kind ? env.kind.replace(/-/g, ' ') : 'Card'
  return `${typeLabel} ${ts}`
}

// ── Scratchpad tab ───────────────────────────────────────────────────────────
// Lifted verbatim from `BottomChatDrawer.tsx`'s `ScratchpadTab`. The
// `onControlsRef` plumbing (used by the legacy /scratch slash command to
// `append` text from the composer) is omitted here; Task 11 / Wave 4 will
// re-introduce the composer-side wiring against the new drawer.

const SCRATCH_STORAGE_KEY = 'nanite:scratchpad'

function ScratchpadTab() {
  const [content, setContent] = useState<string>(() => {
    try {
      return localStorage.getItem(SCRATCH_STORAGE_KEY) ?? ''
    } catch {
      return ''
    }
  })

  const handleChange = useCallback((e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const val = e.target.value
    setContent(val)
    try {
      localStorage.setItem(SCRATCH_STORAGE_KEY, val)
    } catch {
      // Storage unavailable — in-memory only.
    }
  }, [])

  const handleClear = useCallback(() => {
    setContent('')
    try {
      localStorage.removeItem(SCRATCH_STORAGE_KEY)
    } catch {
      /* ignore */
    }
  }, [])

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between px-3 py-1 shrink-0">
        <p className="text-xs text-fg-muted">
          Scratchpad — not sent to agent unless referenced
        </p>
        {content && (
          <button
            type="button"
            onClick={handleClear}
            className="text-xs text-fg-faint hover:text-fg-muted transition-colors"
          >
            Clear
          </button>
        )}
      </div>
      <textarea
        className="flex-1 w-full resize-none bg-transparent text-sm text-fg px-3 py-2 outline-none placeholder:text-fg-faint font-mono leading-relaxed [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
        placeholder="Type notes here… not sent to the agent"
        value={content}
        onChange={handleChange}
      />
    </div>
  )
}

// ── Terminal 1 tab ───────────────────────────────────────────────────────────
// Streamed output from `!command` shell-exec. Subscribes to `useShellStore`;
// the composer (Task 11) is responsible for setting that state. Display is
// read-only, monospace, auto-scroll-to-bottom.

function Terminal1Tab() {
  const chunks = useShellStore((s) => s.shellChunks)
  const running = useShellStore((s) => s.shellRunning)
  const output = useMemo(() => chunks.join(''), [chunks])
  const ref = useRef<HTMLPreElement>(null)
  useEffect(() => {
    ref.current?.scrollTo({ top: ref.current.scrollHeight })
  }, [output])
  return (
    <div className="h-full overflow-hidden">
      <pre
        ref={ref}
        className="h-full overflow-y-auto p-3 font-mono text-[11px] leading-relaxed text-fg-secondary whitespace-pre-wrap [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
      >
        {output ||
          (running
            ? 'Running…'
            : 'No shell output yet. Type ! followed by a command in the composer.')}
      </pre>
    </div>
  )
}

// ── Terminal 2 tab ───────────────────────────────────────────────────────────
// Full interactive shell — gated by developer_mode. Real implementation
// (xterm.js or similar) deferred to a follow-up task; ship with a placeholder
// per the plan's Step 1.

function Terminal2Tab() {
  return (
    <div className="p-3 font-mono text-xs">
      Terminal 2 (dev) — interactive shell, follow-up
    </div>
  )
}

// ── Artifacts tab ────────────────────────────────────────────────────────────
// PLACEHOLDER. The plan instructs "Copy `BottomChatDrawer.tsx`'s
// `case 'artifacts':` branch — it queries session artifacts and renders
// previews with download buttons." That branch does not exist in
// `BottomChatDrawer.tsx` (it has scratchpad / documents / context / pins /
// cards). The closest matching component in the codebase is
// `components/drawers/ArtifactsContent.tsx`, used today by `RightRail`.
// Lifting from there (vs. lifting "documents") is a real ambiguity flagged
// to the planner — wired up in a follow-up so we don't guess.

function ArtifactsTab() {
  // Reuse the existing ArtifactsContent panel. It's the same component the
  // RightRail uses for the Artifacts tab — full list / preview / download
  // / drag-drop upload behavior. Same component in two hosts is cheaper than
  // forking the implementation. `onTitleChange` is optional and only used by
  // hosts that surface a dynamic header title; ChatWorkingDrawer's tab strip
  // handles the label, so we don't pass it.
  return <ArtifactsContent />
}

// ── Session context tab ──────────────────────────────────────────────────────
// Lifted verbatim from `BottomChatDrawer.tsx`'s `SessionContextTab`.

function SessionContextTab() {
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const [localPrompt, setLocalPrompt] = useState('')
  const [saved, setSaved] = useState(false)
  const saveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const { data: fetchedPrompt } = useQuery({
    queryKey: ['session-context-prompt', activeSessionId],
    queryFn: () => api.getSessionContextPrompt(activeSessionId!),
    enabled: !!activeSessionId,
  })

  // Sync fetched value into local state once on load.
  useEffect(() => {
    if (fetchedPrompt !== undefined) {
      setLocalPrompt(fetchedPrompt)
    }
  }, [fetchedPrompt])

  const save = useCallback(
    async (value: string) => {
      if (!activeSessionId) return
      try {
        await api.setSessionContextPrompt(activeSessionId, value)
        setSaved(true)
        setTimeout(() => setSaved(false), 1500)
      } catch (err) {
        console.error('Failed to save session context prompt', err)
      }
    },
    [activeSessionId],
  )

  const handleChange = useCallback(
    (e: React.ChangeEvent<HTMLTextAreaElement>) => {
      const val = e.target.value
      setLocalPrompt(val)
      // Debounced auto-save.
      if (saveTimerRef.current) clearTimeout(saveTimerRef.current)
      saveTimerRef.current = setTimeout(() => void save(val), 1000)
    },
    [save],
  )

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between px-3 py-1 shrink-0">
        <p className="text-xs text-fg-muted">
          Session context — survives compaction · keep thin + pointer-style
        </p>
        {saved && <span className="text-xs text-success">Saved</span>}
      </div>
      <div className="flex-1 px-3 pb-1 min-h-0">
        <textarea
          className="w-full h-full resize-none bg-transparent text-sm text-fg outline-none placeholder:text-fg-faint leading-relaxed [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
          placeholder={`Thin, pointer-style context for this session.\nExamples:\n- Skills: use /capture-decision for design decisions\n- Tools: prefer memory_write over file writes\n- Reference: planning/myproject/scope.md`}
          value={localPrompt}
          onChange={handleChange}
        />
      </div>
    </div>
  )
}

