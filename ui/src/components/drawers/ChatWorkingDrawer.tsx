/**
 * ChatWorkingDrawer — Task 6 of the 2026-05-01 chat-surface redesign.
 *
 * Working surface that lives ABOVE the composer (square bottom corners,
 * flush against it). Hosts a fixed set of tabs (Scratchpad, Terminal 1,
 * Terminal 2 [dev-only], Artifacts, Session Context) plus a dynamic set
 * of `card:<uuid>` tabs sourced from `panelEnvelopes['bottom_chat_drawer']`.
 *
 * Mounting is deferred to Task 8 (Wave 3). This file only adds the
 * component + its companion `useShellStore`; nothing in the running app
 * references it yet.
 *
 * Tab body lift status (Task 6 / Step 2):
 *   - Scratchpad      — lifted verbatim from BottomChatDrawer (sans
 *                       `onControlsRef` plumbing, which is external wiring
 *                       Task 11 will revisit).
 *   - Session Context — lifted verbatim from BottomChatDrawer.
 *   - Artifacts       — placeholder. See `ArtifactsTab` note below;
 *                       BottomChatDrawer has no `case 'artifacts':`
 *                       branch to lift from. Tracked as a follow-up.
 *   - Terminal 1      — new, subscribes to `useShellStore`.
 *   - Terminal 2      — placeholder per plan (interactive shell deferred).
 *
 * The pinned-card lifecycle (transient → DB-backed) reuses the existing
 * `api.pinDrawerCard` / `api.unpinDrawerCard` endpoints. The plan's draft
 * referenced `api.createDrawerCard` / `api.deleteDrawerCard` which don't
 * exist; the rename is 1:1 against the same routes.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useAppStore } from '@/stores/useAppStore'
import { useShellStore } from '@/stores/useShellStore'
import { useSettings } from '@/hooks/useSettings'
import { ChatDrawerTabStrip, type ChatDrawerTab } from '@/components/chat/ChatDrawerTabStrip'
import { EnvelopeRenderer } from '@/components/chat/envelopes/EnvelopeRenderer'
import { api } from '@/lib/api'
import type { DynamicCardTab, Envelope } from '@/lib/types'

const FIXED_TABS: { id: string; label: string; devOnly?: boolean }[] = [
  { id: 'scratchpad', label: 'Scratchpad' },
  { id: 'terminal-1', label: 'Terminal 1' },
  { id: 'terminal-2', label: 'Terminal 2', devOnly: true },
  { id: 'artifacts', label: 'Artifacts' },
  { id: 'session-context', label: 'Session Context' },
]

export function ChatWorkingDrawer() {
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

  return (
    <div className="w-[85%] max-w-7xl mx-auto px-4 relative">
      <ChatDrawerTabStrip
        tabs={tabs}
        dock="top"
        onSelect={(id) => {
          if (drawer.activeTab === id && drawer.open) {
            setDrawer({ open: false })
            return
          }
          setDrawer({ open: true, activeTab: id })
        }}
        onClose={(id) => removeCardTab(id)}
        onTogglePin={async (id) => {
          const tab = cardTabs.find((t) => t.id === id)
          if (!tab) return
          if (tab.pinned) {
            // Promoted card — DELETE; refresh pinned cards in ChatPrimaryDrawer.
            const dbId = id.slice(5) // strip 'card:' prefix
            await api.unpinDrawerCard(dbId)
            removeCardTab(id)
          } else {
            // Promote: POST /drawer-cards via existing API. The plan's
            // shape (`type` / `label`) maps 1:1 onto the live API's
            // `card_type` / `title` field names.
            await api.pinDrawerCard(activeSessionId, {
              card_type: 'agent-envelope',
              content_ref: tab.payload.id ?? '',
              title: tab.label,
              payload: JSON.stringify(tab.payload),
            })
            queryClient.invalidateQueries({ queryKey: ['drawer-cards', activeSessionId] })
            removeCardTab(id) // tab now lives in ChatPrimaryDrawer's pinned-tabs list
          }
        }}
      />
      <div
        className="overflow-hidden border-x border-b border-border-subtle bg-bg-elevated"
        style={{
          height: drawer.open ? drawer.height : 0,
          // Square bottom corners — flush against composer.
          borderRadius: '10px 10px 0 0',
        }}
      >
        {/* Top accent ribbon — success color (differentiates from ChatPrimaryDrawer) */}
        <div className="pointer-events-none absolute inset-x-0 top-0 h-[2px] bg-success opacity-65" />
        {drawer.open && (
          <DrawerBody activeTab={drawer.activeTab} cardTabs={cardTabs} />
        )}
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
        className="flex-1 w-full resize-none bg-transparent text-sm text-fg px-3 py-2 outline-none placeholder:text-fg-faint font-mono leading-relaxed"
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
  const output = useShellStore((s) => s.shellOutput)
  const running = useShellStore((s) => s.shellRunning)
  const ref = useRef<HTMLPreElement>(null)
  useEffect(() => {
    ref.current?.scrollTo({ top: ref.current.scrollHeight })
  }, [output])
  return (
    <div className="h-full overflow-hidden">
      <pre
        ref={ref}
        className="h-full overflow-y-auto p-3 font-mono text-[11px] leading-relaxed text-fg-secondary whitespace-pre-wrap"
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
  return (
    <div className="p-3 text-xs text-fg-faint italic">
      Artifacts — pending lift from ArtifactsContent.tsx (follow-up).
    </div>
  )
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
          className="w-full h-full resize-none bg-transparent text-sm text-fg outline-none placeholder:text-fg-faint leading-relaxed"
          placeholder={`Thin, pointer-style context for this session.\nExamples:\n- Skills: use /capture-decision for design decisions\n- Tools: prefer memory_write over file writes\n- Reference: planning/myproject/scope.md`}
          value={localPrompt}
          onChange={handleChange}
        />
      </div>
    </div>
  )
}

