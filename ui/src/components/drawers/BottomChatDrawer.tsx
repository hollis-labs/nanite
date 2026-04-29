/**
 * BottomChatDrawer — J10 + A2 + C1 (CW-20260428-0012)
 *
 * Two layers of tabs:
 *   1. Built-in tabs:  Scratchpad / Documents / Context / Pins / Cards
 *      (Cards = the A2 transient slot for `render_target=bottom_chat_drawer`
 *       envelopes; the latest envelope replaces the previous transient.)
 *   2. Pinned cards:   per-session DB-backed tabs, each rendering a typed
 *      card payload (markdown / diff / image / scratchpad / artifact-mini /
 *      agent-envelope). Capped at BOTTOM_DRAWER_PIN_CAP.
 *
 * Tab strip horizontally scrolls when total tabs exceed viewport width.
 *
 * Default-tab setting: persisted in the layout store (`defaultDrawerTab`),
 * editable in Settings → Bottom Drawer (PreferencesPanel). When the drawer
 * is opened with no specific target, this tab activates.
 *
 * Pin lifecycle:
 *   transient (FE-only) → user pin → POST /drawer-cards → pinned (server-side)
 *   pinned (server-side) → user unpin → DELETE /drawer-cards/{id} → gone
 *
 * Pin cap: 10 per session. 11th pin → toast via showChatToast.
 *
 * Opens via:
 *   - /scratch slash command (bare invocation)
 *   - Agent panel_open("bottom_chat_drawer")
 *   - Agent envelope.render_target = "bottom_chat_drawer" (A2)
 *   - Direct user click on drawer toggle
 *
 * Dismiss policy: follows J8 state machine (setBottomDrawerOpen).
 */

import { useState, useCallback, useRef, useEffect, useMemo } from 'react'
import {
  X,
  FileText,
  StickyNote,
  MessageSquare,
  Plus,
  Trash2,
  Eye,
  EyeOff,
  Maximize2,
  Minimize2,
  Pin,
  PinOff,
  Inbox,
  Image as ImageIcon,
  GitCompare,
  Package,
} from 'lucide-react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { ScrollArea } from '@/components/ui/scroll-area'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useAppStore } from '@/stores/useAppStore'
import { useChatStore } from '@/stores/useChatStore'
import { api, DrawerPinCapError } from '@/lib/api'
import type { Document, PinnedContent, Envelope, DrawerCardType, DrawerPinnedCard } from '@/lib/types'
import { EnvelopeRenderer } from '@/components/chat/envelopes/EnvelopeRenderer'

// ── Constants ────────────────────────────────────────────────────────────────

/** UI policy cap matching store.BottomDrawerPinCap. Surfaced as a constant so
 *  tests can reference the same number. */
export const BOTTOM_DRAWER_PIN_CAP = 10

/** Built-in tab IDs. Pinned-card tab IDs are dynamic (`pin:<uuid>`). */
const BUILTIN_TABS = ['scratchpad', 'documents', 'context', 'pins', 'cards'] as const
type BuiltinTab = (typeof BUILTIN_TABS)[number]
type DrawerTab = BuiltinTab | string // `pin:<uuid>` for pinned-card tabs

const BUILTIN_LABELS: Record<BuiltinTab, string> = {
  scratchpad: 'Scratchpad',
  documents: 'Documents',
  context: 'Session Context',
  pins: 'Pins',
  cards: 'Cards',
}

// ── Main component ───────────────────────────────────────────────────────────

interface BottomChatDrawerProps {
  /** Initial tab to show when opened. Caller can set this to 'scratchpad' when
   *  /scratch is invoked bare so the pad is visible immediately. */
  initialTab?: DrawerTab
  /** Ref to control the scratchpad from outside (e.g. slash command append). */
  onScratchpadRef?: (controls: ScratchpadControls) => void
}

export interface ScratchpadControls {
  append: (text: string) => void
  open: () => void
}

export function BottomChatDrawer({ initialTab, onScratchpadRef }: BottomChatDrawerProps) {
  const isOpen = useLayoutStore((s) => s.bottomChatDrawerOpen)
  const setOpen = useLayoutStore((s) => s.setBottomDrawerOpen)
  const defaultTab = useLayoutStore((s) => s.defaultDrawerTab)
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const showChatToast = useChatStore((s) => s.showChatToast)

  // Pinned cards (server-backed)
  const { data: pinnedCards = [] } = useQuery({
    queryKey: ['drawer-cards', activeSessionId],
    queryFn: () => api.listDrawerCards(activeSessionId!),
    enabled: !!activeSessionId,
  })

  const queryClient = useQueryClient()

  const pinMutation = useMutation({
    mutationFn: (input: { card_type: DrawerCardType; content_ref?: string; title?: string; payload?: string }) =>
      api.pinDrawerCard(activeSessionId!, input),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['drawer-cards', activeSessionId] })
    },
    onError: (err) => {
      if (err instanceof DrawerPinCapError) {
        showChatToast(`${BOTTOM_DRAWER_PIN_CAP}-tab limit; unpin one first`, 'info')
      } else {
        showChatToast('Failed to pin card', 'info')
      }
    },
  })

  const unpinMutation = useMutation({
    mutationFn: (id: string) => api.unpinDrawerCard(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['drawer-cards', activeSessionId] })
    },
  })

  // Active tab — defaults to `defaultTab` (per-user setting). When `initialTab`
  // is explicitly provided (e.g. by /scratch), it wins for one open cycle.
  const [activeTab, setActiveTab] = useState<DrawerTab>(initialTab ?? defaultTab ?? 'scratchpad')
  const [expanded, setExpanded] = useState(false)

  const handleClose = useCallback(() => setOpen(false, 'user'), [setOpen])

  // When the drawer opens, activate the requested tab (initialTab > defaultTab > scratchpad).
  useEffect(() => {
    if (isOpen) {
      setActiveTab(initialTab ?? defaultTab ?? 'scratchpad')
    }
  }, [isOpen, initialTab, defaultTab])

  // If the active tab points at a pinned card that no longer exists (e.g. just
  // unpinned), fall back to the default tab.
  useEffect(() => {
    if (activeTab.startsWith('pin:')) {
      const pinId = activeTab.slice(4)
      if (!pinnedCards.find((c) => c.id === pinId)) {
        setActiveTab(defaultTab ?? 'scratchpad')
      }
    }
  }, [pinnedCards, activeTab, defaultTab])

  const handlePin = useCallback(
    (input: { card_type: DrawerCardType; content_ref?: string; title?: string; payload?: string }) => {
      if (pinnedCards.length >= BOTTOM_DRAWER_PIN_CAP) {
        // Pre-flight check — avoid a server round-trip when we know we'll fail.
        showChatToast(`${BOTTOM_DRAWER_PIN_CAP}-tab limit; unpin one first`, 'info')
        return
      }
      pinMutation.mutate(input)
    },
    [pinnedCards.length, pinMutation, showChatToast],
  )

  const handleUnpin = useCallback(
    (id: string) => {
      unpinMutation.mutate(id)
    },
    [unpinMutation],
  )

  if (!isOpen) return null

  return (
    <div
      className={`border-t border-border bg-bg-elevated flex flex-col transition-all duration-200 ${
        expanded ? 'h-[60vh]' : 'h-72'
      }`}
      role="complementary"
      aria-label="Bottom drawer"
    >
      {/* Header — horizontal-scroll tab strip + chrome */}
      <div className="flex items-center justify-between px-3 py-1.5 border-b border-border shrink-0 gap-2">
        <div
          className="flex items-center gap-1 overflow-x-auto scrollbar-hide min-w-0 flex-1"
          data-testid="bottom-drawer-tabstrip"
        >
          {/* Built-in tabs */}
          <TabButton
            active={activeTab === 'scratchpad'}
            icon={<StickyNote className="w-3.5 h-3.5" />}
            label={BUILTIN_LABELS.scratchpad}
            onClick={() => setActiveTab('scratchpad')}
          />
          <TabButton
            active={activeTab === 'documents'}
            icon={<FileText className="w-3.5 h-3.5" />}
            label={BUILTIN_LABELS.documents}
            onClick={() => setActiveTab('documents')}
          />
          <TabButton
            active={activeTab === 'context'}
            icon={<MessageSquare className="w-3.5 h-3.5" />}
            label={BUILTIN_LABELS.context}
            onClick={() => setActiveTab('context')}
          />
          <TabButton
            active={activeTab === 'pins'}
            icon={<Pin className="w-3.5 h-3.5" />}
            label={BUILTIN_LABELS.pins}
            onClick={() => setActiveTab('pins')}
          />
          <TabButton
            active={activeTab === 'cards'}
            icon={<Inbox className="w-3.5 h-3.5" />}
            label={BUILTIN_LABELS.cards}
            onClick={() => setActiveTab('cards')}
          />

          {/* Dynamic pinned-card tabs */}
          {pinnedCards.length > 0 && (
            <div className="w-px h-4 bg-border mx-1 shrink-0" aria-hidden="true" />
          )}
          {pinnedCards.map((card) => {
            const tabId = `pin:${card.id}`
            return (
              <PinnedTabButton
                key={card.id}
                active={activeTab === tabId}
                card={card}
                onClick={() => setActiveTab(tabId)}
                onUnpin={() => handleUnpin(card.id)}
              />
            )
          })}
        </div>
        <div className="flex items-center gap-1 shrink-0">
          <button
            type="button"
            onClick={() => setExpanded((v) => !v)}
            className="p-1 rounded text-fg-muted hover:text-fg hover:bg-surface transition-colors"
            aria-label={expanded ? 'Collapse drawer' : 'Expand drawer'}
          >
            {expanded ? <Minimize2 className="w-3.5 h-3.5" /> : <Maximize2 className="w-3.5 h-3.5" />}
          </button>
          <button
            type="button"
            onClick={handleClose}
            className="p-1 rounded text-fg-muted hover:text-fg hover:bg-surface transition-colors"
            aria-label="Close drawer"
          >
            <X className="w-3.5 h-3.5" />
          </button>
        </div>
      </div>

      {/* Content */}
      <div className="flex-1 min-h-0 overflow-hidden">
        {activeTab === 'scratchpad' && (
          <ScratchpadTab onControlsRef={onScratchpadRef} />
        )}
        {activeTab === 'documents' && <DocumentsTab />}
        {activeTab === 'context' && <SessionContextTab />}
        {activeTab === 'pins' && <PinsTab />}
        {activeTab === 'cards' && <CardsTab onPin={handlePin} canPin={pinnedCards.length < BOTTOM_DRAWER_PIN_CAP} />}
        {activeTab.startsWith('pin:') && (
          <PinnedCardContent
            card={pinnedCards.find((c) => c.id === activeTab.slice(4)) ?? null}
            onUnpin={() => handleUnpin(activeTab.slice(4))}
          />
        )}
      </div>
    </div>
  )
}

// ── Cards tab ────────────────────────────────────────────────────────────────
// A2 inbox slot — routed envelopes (envelope.render_target = "bottom_chat_drawer").
// One transient slot — latest envelope replaces the previous. User can pin the
// transient to promote it into a dedicated tab.

function CardsTab({
  onPin,
  canPin,
}: {
  onPin: (input: { card_type: DrawerCardType; content_ref?: string; title?: string; payload?: string }) => void
  canPin: boolean
}) {
  const envelopes = useLayoutStore((s) => s.panelEnvelopes['bottom_chat_drawer'] ?? [])
  const clearPanelEnvelopes = useLayoutStore((s) => s.clearPanelEnvelopes)
  const latest: Envelope | null = envelopes.length > 0 ? envelopes[envelopes.length - 1] : null
  const history = envelopes.slice(0, -1).slice(-5).reverse()

  const handlePinLatest = useCallback(() => {
    if (!latest) return
    const title = latest.title ?? latest.type ?? 'Pinned card'
    onPin({
      card_type: 'agent-envelope',
      content_ref: latest.id ?? '',
      title,
      payload: JSON.stringify(latest),
    })
  }, [latest, onPin])

  if (!latest) {
    return (
      <EmptyState message="No active card — agent-emitted cards routed to the bottom drawer will land here." />
    )
  }

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between px-3 py-1 shrink-0 gap-2">
        <p className="text-xs text-fg-muted truncate">
          Routed cards — transient. Pin to keep across messages.
        </p>
        <div className="flex items-center gap-2 shrink-0">
          <button
            type="button"
            onClick={handlePinLatest}
            disabled={!canPin}
            className="flex items-center gap-1 text-xs text-primary hover:text-primary-hover disabled:text-fg-faint disabled:cursor-not-allowed transition-colors"
            title={canPin ? 'Pin this card' : `${BOTTOM_DRAWER_PIN_CAP}-pin cap reached`}
          >
            <Pin className="w-3 h-3" />
            Pin
          </button>
          <button
            type="button"
            onClick={() => clearPanelEnvelopes('bottom_chat_drawer')}
            className="text-xs text-fg-faint hover:text-fg-muted transition-colors"
          >
            Clear
          </button>
        </div>
      </div>
      <ScrollArea className="flex-1">
        <div className="p-3 space-y-3">
          <EnvelopeRenderer envelope={latest} />
          {history.length > 0 && (
            <div className="pt-3 border-t border-border space-y-2">
              <p className="text-[11px] text-fg-faint uppercase tracking-wide">Earlier this session</p>
              {history.map((env, i) => (
                <details key={i} className="text-xs text-fg-muted">
                  <summary className="cursor-pointer hover:text-fg">
                    {env.title ? `${env.type}: ${env.title}` : env.type}
                  </summary>
                  <div className="mt-2">
                    <EnvelopeRenderer envelope={env} />
                  </div>
                </details>
              ))}
            </div>
          )}
        </div>
      </ScrollArea>
    </div>
  )
}

// ── Pinned card content ──────────────────────────────────────────────────────

function PinnedCardContent({
  card,
  onUnpin,
}: {
  card: DrawerPinnedCard | null
  onUnpin: () => void
}) {
  if (!card) {
    return <EmptyState message="Pinned card not found" />
  }

  const payload = useMemo<unknown>(() => {
    if (!card.payload) return null
    try {
      return JSON.parse(card.payload)
    } catch {
      return null
    }
  }, [card.payload])

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between px-3 py-1 shrink-0 gap-2">
        <p className="text-xs text-fg-muted truncate">
          <CardTypeIcon type={card.card_type} />
          <span className="ml-1.5">{card.title || card.card_type}</span>
        </p>
        <button
          type="button"
          onClick={onUnpin}
          className="flex items-center gap-1 text-xs text-fg-faint hover:text-danger transition-colors"
          title="Unpin"
        >
          <PinOff className="w-3 h-3" />
          Unpin
        </button>
      </div>
      <ScrollArea className="flex-1">
        <div className="p-3">
          <PinnedCardBody card={card} payload={payload} />
        </div>
      </ScrollArea>
    </div>
  )
}

function PinnedCardBody({ card, payload }: { card: DrawerPinnedCard; payload: unknown }) {
  switch (card.card_type) {
    case 'agent-envelope':
      if (payload && typeof payload === 'object') {
        return <EnvelopeRenderer envelope={payload as Envelope} />
      }
      return <UnknownCardBody card={card} />

    case 'markdown': {
      const text = typeof payload === 'object' && payload !== null && 'content' in payload
        ? String((payload as { content: unknown }).content ?? '')
        : ''
      return (
        <pre className="text-xs font-mono text-fg-secondary whitespace-pre-wrap break-words leading-relaxed">
          {text || <span className="text-fg-faint italic">No content</span>}
        </pre>
      )
    }

    case 'image': {
      const src = typeof payload === 'object' && payload !== null && 'src' in payload
        ? String((payload as { src: unknown }).src ?? '')
        : ''
      if (!src) return <UnknownCardBody card={card} />
      return (
        <div className="flex items-center justify-center">
          <img src={src} alt={card.title} className="max-w-full max-h-[60vh] rounded-sm border border-border" />
        </div>
      )
    }

    case 'diff': {
      const text = typeof payload === 'object' && payload !== null && 'diff' in payload
        ? String((payload as { diff: unknown }).diff ?? '')
        : ''
      return (
        <pre className="text-xs font-mono text-fg-secondary whitespace-pre-wrap break-words leading-relaxed bg-surface rounded-sm p-2 border border-border">
          {text || <span className="text-fg-faint italic">No diff</span>}
        </pre>
      )
    }

    case 'scratchpad': {
      const text = typeof payload === 'object' && payload !== null && 'content' in payload
        ? String((payload as { content: unknown }).content ?? '')
        : ''
      return (
        <pre className="text-xs font-mono text-fg whitespace-pre-wrap break-words leading-relaxed">
          {text || <span className="text-fg-faint italic">Empty scratchpad snapshot</span>}
        </pre>
      )
    }

    case 'artifact-mini':
      return <ArtifactMiniBody card={card} />

    default:
      return <UnknownCardBody card={card} />
  }
}

function UnknownCardBody({ card }: { card: DrawerPinnedCard }) {
  return (
    <div className="text-xs text-fg-muted">
      <p className="mb-2">
        Unknown card type <code className="font-mono text-fg-faint">{card.card_type}</code>.
      </p>
      <pre className="bg-surface p-2 rounded-sm text-[11px] text-fg-faint break-all whitespace-pre-wrap">
        {card.payload || '(empty payload)'}
      </pre>
    </div>
  )
}

function ArtifactMiniBody({ card }: { card: DrawerPinnedCard }) {
  // Pinned artifact-mini cards keep enough info for a download CTA without
  // re-fetching: name / mime / size live in the payload snapshot. content_ref
  // is the artifact ID.
  const meta = useMemo<{ name?: string; mime_type?: string; size?: number } | null>(() => {
    if (!card.payload) return null
    try {
      const parsed = JSON.parse(card.payload)
      if (parsed && typeof parsed === 'object') {
        return parsed as { name?: string; mime_type?: string; size?: number }
      }
    } catch {
      // fall through
    }
    return null
  }, [card.payload])

  const downloadUrl = card.content_ref ? `/api/artifacts/${card.content_ref}/download` : ''

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-3 px-3 py-2 rounded-sm bg-bg-elevated/50 border border-border">
        <Package className="w-4 h-4 text-fg-muted shrink-0" />
        <div className="flex-1 min-w-0">
          <p className="text-sm text-fg truncate">{meta?.name ?? card.title ?? 'Artifact'}</p>
          <div className="flex items-center gap-2 mt-0.5 text-xs text-fg-faint">
            {meta?.mime_type && <span>{meta.mime_type}</span>}
            {typeof meta?.size === 'number' && <span>{formatSize(meta.size)}</span>}
          </div>
        </div>
        {downloadUrl && (
          <a
            href={downloadUrl}
            download
            className="text-xs text-primary hover:text-primary-hover transition-colors"
          >
            Download
          </a>
        )}
      </div>
    </div>
  )
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

function CardTypeIcon({ type }: { type: DrawerCardType }) {
  switch (type) {
    case 'markdown':
      return <FileText className="w-3 h-3 inline-block" />
    case 'diff':
      return <GitCompare className="w-3 h-3 inline-block" />
    case 'image':
      return <ImageIcon className="w-3 h-3 inline-block" />
    case 'scratchpad':
      return <StickyNote className="w-3 h-3 inline-block" />
    case 'artifact-mini':
      return <Package className="w-3 h-3 inline-block" />
    case 'agent-envelope':
      return <Inbox className="w-3 h-3 inline-block" />
    default:
      return <FileText className="w-3 h-3 inline-block" />
  }
}

// ── Tab buttons ──────────────────────────────────────────────────────────────

function TabButton({
  active,
  icon,
  label,
  onClick,
}: {
  active: boolean
  icon: React.ReactNode
  label: string
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`flex items-center gap-1.5 px-2.5 py-1 rounded text-xs transition-colors shrink-0 ${
        active
          ? 'bg-surface text-fg font-medium'
          : 'text-fg-muted hover:text-fg hover:bg-surface/50'
      }`}
    >
      {icon}
      {label}
    </button>
  )
}

function PinnedTabButton({
  active,
  card,
  onClick,
  onUnpin,
}: {
  active: boolean
  card: DrawerPinnedCard
  onClick: () => void
  onUnpin: () => void
}) {
  return (
    <div
      className={`group flex items-center gap-1 px-2 py-1 rounded text-xs transition-colors shrink-0 max-w-[160px] ${
        active
          ? 'bg-surface text-fg font-medium'
          : 'text-fg-muted hover:text-fg hover:bg-surface/50'
      }`}
    >
      <button
        type="button"
        onClick={onClick}
        className="flex items-center gap-1 min-w-0"
        title={card.title || card.card_type}
      >
        <CardTypeIcon type={card.card_type} />
        <span className="truncate">{card.title || card.card_type}</span>
      </button>
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation()
          onUnpin()
        }}
        className="ml-0.5 p-0.5 rounded text-fg-faint opacity-0 group-hover:opacity-100 hover:text-danger transition-colors"
        aria-label={`Unpin ${card.title || card.card_type}`}
      >
        <X className="w-3 h-3" />
      </button>
    </div>
  )
}

// ── Scratchpad tab ────────────────────────────────────────────────────────────
// Extends P4 scratchpad (CW-20260419-0025). This is the USER-facing scratchpad
// (bottom drawer), NOT the agent-facing in-loop scratchpad (loopState).
// Content is excluded from agent context by default.

interface ScratchpadTabProps {
  onControlsRef?: (controls: ScratchpadControls) => void
}

const SCRATCH_STORAGE_KEY = 'nanite:scratchpad'

function ScratchpadTab({ onControlsRef }: ScratchpadTabProps) {
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

  const append = useCallback((text: string) => {
    setContent((prev) => {
      const sep = prev.length > 0 && !prev.endsWith('\n') ? '\n' : ''
      const next = prev + sep + text
      try {
        localStorage.setItem(SCRATCH_STORAGE_KEY, next)
      } catch {
        /* ignore */
      }
      return next
    })
  }, [])

  const open = useCallback(() => {
    // Controls exposed for slash command handler — opening is done by parent.
  }, [])

  // Expose controls to parent (ChatComposer slash command handler).
  useEffect(() => {
    onControlsRef?.({ append, open })
  }, [onControlsRef, append, open])

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

// ── Documents tab ─────────────────────────────────────────────────────────────

function DocumentsTab() {
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const queryClient = useQueryClient()
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [importing, setImporting] = useState(false)
  const [pasteMode, setPasteMode] = useState(false)
  const [pasteName, setPasteName] = useState('')
  const [pasteContent, setPasteContent] = useState('')
  const fileInputRef = useRef<HTMLInputElement>(null)

  const { data: documents = [], isLoading } = useQuery({
    queryKey: ['documents', activeSessionId],
    queryFn: () => api.listDocuments(activeSessionId!),
    enabled: !!activeSessionId,
  })

  const selectedDoc = documents.find((d) => d.id === selectedId) ?? null

  // Auto-select first doc when list arrives and nothing is selected.
  useEffect(() => {
    if (documents.length > 0 && !selectedId) {
      setSelectedId(documents[0].id)
    }
  }, [documents, selectedId])

  const toggleMutation = useMutation({
    mutationFn: ({ id, included, fullContent }: { id: string; included: boolean; fullContent: boolean }) =>
      api.updateDocument(id, { included, full_content: fullContent }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['documents', activeSessionId] })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.deleteDocument(id),
    onSuccess: (_, id) => {
      void queryClient.invalidateQueries({ queryKey: ['documents', activeSessionId] })
      if (selectedId === id) setSelectedId(null)
    },
  })

  const createMutation = useMutation({
    mutationFn: (doc: { name: string; content: string; mime_type?: string }) =>
      api.createDocument(activeSessionId!, doc),
    onSuccess: (created) => {
      void queryClient.invalidateQueries({ queryKey: ['documents', activeSessionId] })
      setSelectedId(created.id)
      setPasteMode(false)
      setPasteName('')
      setPasteContent('')
    },
  })

  const handleFileUpload = useCallback(
    async (files: FileList | null) => {
      if (!files || !activeSessionId) return
      setImporting(true)
      try {
        for (const file of Array.from(files)) {
          const text = await file.text()
          await createMutation.mutateAsync({
            name: file.name,
            content: text,
            mime_type: file.type || 'text/plain',
          })
        }
      } finally {
        setImporting(false)
        if (fileInputRef.current) fileInputRef.current.value = ''
      }
    },
    [activeSessionId, createMutation],
  )

  const handlePasteSubmit = useCallback(() => {
    if (!pasteName.trim() || !pasteContent.trim()) return
    createMutation.mutate({ name: pasteName.trim(), content: pasteContent.trim() })
  }, [pasteName, pasteContent, createMutation])

  if (!activeSessionId) {
    return <EmptyState message="No active session" />
  }

  return (
    <div className="flex h-full">
      {/* Sidebar */}
      <div className="w-48 shrink-0 border-r border-border flex flex-col">
        <div className="flex items-center gap-1 p-2 shrink-0">
          <button
            type="button"
            onClick={() => fileInputRef.current?.click()}
            disabled={importing}
            className="flex items-center gap-1 px-2 py-1 text-xs text-fg-muted hover:text-fg hover:bg-surface rounded transition-colors"
            title="Upload file"
          >
            <Plus className="w-3 h-3" />
            Upload
          </button>
          <button
            type="button"
            onClick={() => setPasteMode((v) => !v)}
            className="flex items-center gap-1 px-2 py-1 text-xs text-fg-muted hover:text-fg hover:bg-surface rounded transition-colors"
            title="Paste content"
          >
            Paste
          </button>
          <input
            ref={fileInputRef}
            type="file"
            multiple
            accept="text/*,.md,.txt,.json,.yaml,.yml,.csv"
            className="hidden"
            onChange={(e) => void handleFileUpload(e.target.files)}
          />
        </div>
        <ScrollArea className="flex-1">
          {isLoading && (
            <div className="p-3 text-xs text-fg-faint">Loading…</div>
          )}
          {!isLoading && documents.length === 0 && (
            <div className="p-3 text-xs text-fg-faint">No documents yet</div>
          )}
          {documents.map((doc) => (
            <DocumentRow
              key={doc.id}
              doc={doc}
              selected={selectedId === doc.id}
              onSelect={() => setSelectedId(doc.id)}
              onToggleInclude={() =>
                toggleMutation.mutate({ id: doc.id, included: !doc.included, fullContent: doc.full_content })
              }
              onDelete={() => deleteMutation.mutate(doc.id)}
            />
          ))}
        </ScrollArea>
      </div>

      {/* Content pane */}
      <div className="flex-1 min-w-0 flex flex-col">
        {pasteMode ? (
          <PasteForm
            name={pasteName}
            content={pasteContent}
            onNameChange={setPasteName}
            onContentChange={setPasteContent}
            onSubmit={handlePasteSubmit}
            onCancel={() => setPasteMode(false)}
            submitting={createMutation.isPending}
          />
        ) : selectedDoc ? (
          <DocumentDetail
            doc={selectedDoc}
            onToggleInclude={() =>
              toggleMutation.mutate({
                id: selectedDoc.id,
                included: !selectedDoc.included,
                fullContent: selectedDoc.full_content,
              })
            }
            onToggleFullContent={() =>
              toggleMutation.mutate({
                id: selectedDoc.id,
                included: selectedDoc.included,
                fullContent: !selectedDoc.full_content,
              })
            }
          />
        ) : (
          <EmptyState message="Select or upload a document" />
        )}
      </div>
    </div>
  )
}

function DocumentRow({
  doc,
  selected,
  onSelect,
  onToggleInclude,
  onDelete,
}: {
  doc: Document
  selected: boolean
  onSelect: () => void
  onToggleInclude: () => void
  onDelete: () => void
}) {
  return (
    <div
      className={`group flex items-center gap-1.5 px-2 py-1.5 cursor-pointer transition-colors ${
        selected ? 'bg-surface text-fg' : 'text-fg-muted hover:bg-surface/60 hover:text-fg'
      }`}
      onClick={onSelect}
    >
      <FileText className="w-3.5 h-3.5 shrink-0 text-fg-faint" />
      <span className="flex-1 text-xs truncate">{doc.name}</span>
      <div className="flex items-center gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity">
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation()
            onToggleInclude()
          }}
          className={`p-0.5 rounded transition-colors ${
            doc.included ? 'text-primary hover:text-primary-hover' : 'text-fg-faint hover:text-fg-muted'
          }`}
          title={doc.included ? 'Exclude from context' : 'Include in context'}
        >
          {doc.included ? <Eye className="w-3 h-3" /> : <EyeOff className="w-3 h-3" />}
        </button>
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation()
            onDelete()
          }}
          className="p-0.5 rounded text-fg-faint hover:text-danger transition-colors"
          title="Delete document"
        >
          <Trash2 className="w-3 h-3" />
        </button>
      </div>
    </div>
  )
}

function DocumentDetail({
  doc,
  onToggleInclude,
  onToggleFullContent,
}: {
  doc: Document
  onToggleInclude: () => void
  onToggleFullContent: () => void
}) {
  return (
    <div className="flex flex-col h-full">
      {/* Toolbar */}
      <div className="flex items-center gap-2 px-3 py-1.5 border-b border-border shrink-0">
        <span className="text-xs text-fg font-medium truncate flex-1">{doc.name}</span>
        <div className="flex items-center gap-2 shrink-0">
          {/* Include toggle */}
          <label className="flex items-center gap-1.5 cursor-pointer">
            <span className="text-xs text-fg-muted">Include</span>
            <button
              type="button"
              role="switch"
              aria-checked={doc.included}
              onClick={onToggleInclude}
              className={`relative inline-flex w-7 h-4 rounded-full transition-colors ${
                doc.included ? 'bg-primary' : 'bg-border'
              }`}
            >
              <span
                className={`inline-block w-3 h-3 rounded-full bg-white shadow transform transition-transform mt-0.5 ${
                  doc.included ? 'translate-x-3.5' : 'translate-x-0.5'
                }`}
              />
            </button>
          </label>
          {/* Pointer vs full toggle — only relevant when included */}
          {doc.included && (
            <label className="flex items-center gap-1.5 cursor-pointer">
              <span className="text-xs text-fg-muted">Full</span>
              <button
                type="button"
                role="switch"
                aria-checked={doc.full_content}
                onClick={onToggleFullContent}
                className={`relative inline-flex w-7 h-4 rounded-full transition-colors ${
                  doc.full_content ? 'bg-primary' : 'bg-border'
                }`}
              >
                <span
                  className={`inline-block w-3 h-3 rounded-full bg-white shadow transform transition-transform mt-0.5 ${
                    doc.full_content ? 'translate-x-3.5' : 'translate-x-0.5'
                  }`}
                />
              </button>
            </label>
          )}
          {doc.included && !doc.full_content && (
            <span className="text-xs text-fg-faint">(pointer mode)</span>
          )}
        </div>
      </div>
      <ScrollArea className="flex-1">
        <pre className="p-3 text-xs font-mono text-fg-secondary whitespace-pre-wrap break-words leading-relaxed">
          {doc.content || <span className="text-fg-faint italic">Empty document</span>}
        </pre>
      </ScrollArea>
    </div>
  )
}

function PasteForm({
  name,
  content,
  onNameChange,
  onContentChange,
  onSubmit,
  onCancel,
  submitting,
}: {
  name: string
  content: string
  onNameChange: (v: string) => void
  onContentChange: (v: string) => void
  onSubmit: () => void
  onCancel: () => void
  submitting: boolean
}) {
  return (
    <div className="flex flex-col h-full p-3 gap-2">
      <input
        type="text"
        placeholder="Document name"
        value={name}
        onChange={(e) => onNameChange(e.target.value)}
        className="w-full px-2 py-1.5 rounded text-sm bg-surface border border-border text-fg placeholder:text-fg-faint outline-none"
      />
      <textarea
        placeholder="Paste content here…"
        value={content}
        onChange={(e) => onContentChange(e.target.value)}
        className="flex-1 w-full px-2 py-1.5 rounded text-xs font-mono bg-surface border border-border text-fg placeholder:text-fg-faint outline-none resize-none"
      />
      <div className="flex items-center gap-2 justify-end shrink-0">
        <button
          type="button"
          onClick={onCancel}
          className="px-3 py-1 text-xs rounded bg-surface text-fg-muted hover:text-fg transition-colors"
        >
          Cancel
        </button>
        <button
          type="button"
          onClick={onSubmit}
          disabled={!name.trim() || !content.trim() || submitting}
          className="px-3 py-1 text-xs rounded bg-primary text-white hover:bg-primary-hover disabled:opacity-50 transition-colors"
        >
          {submitting ? 'Saving…' : 'Save'}
        </button>
      </div>
    </div>
  )
}

// ── Session context tab ───────────────────────────────────────────────────────

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

// ── Pins tab ─────────────────────────────────────────────────────────────────
// J11 (CW-20260426-0009): lists agent-pinned content with unpin button,
// per-pin scope label, and source-agent attribution.

function PinsTab() {
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const queryClient = useQueryClient()

  const { data: pins = [], isLoading } = useQuery({
    queryKey: ['pins', activeSessionId],
    queryFn: () => api.listPins(activeSessionId!),
    enabled: !!activeSessionId,
    refetchInterval: 5000, // refresh frequently — pins can be set during a turn
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.deletePin(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['pins', activeSessionId] })
    },
  })

  if (!activeSessionId) {
    return <EmptyState message="No active session" />
  }

  const scopeLabel = (scope: PinnedContent['scope']) => {
    switch (scope) {
      case 'turn': return 'Turn'
      case 'session': return 'Session'
      case 'cross_session': return 'Cross-session'
      default: return scope
    }
  }

  const scopeColour = (scope: PinnedContent['scope']) => {
    switch (scope) {
      case 'turn': return 'text-fg-faint'
      case 'session': return 'text-info'
      case 'cross_session': return 'text-primary'
      default: return 'text-fg-muted'
    }
  }

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between px-3 py-1 shrink-0">
        <p className="text-xs text-fg-muted">
          Pinned context — survives compaction · set by agent via <code className="font-mono text-fg-faint">nanite_pin</code>
        </p>
      </div>
      <ScrollArea className="flex-1">
        {isLoading && (
          <div className="px-3 py-2 text-xs text-fg-faint">Loading…</div>
        )}
        {!isLoading && pins.length === 0 && (
          <div className="px-3 py-4 text-xs text-fg-faint italic">
            No pinned content yet. The agent can pin content using <code className="font-mono">nanite_pin</code>.
          </div>
        )}
        <div className="space-y-1 p-2">
          {pins.map((pin) => (
            <div
              key={pin.id}
              className="group flex items-start gap-2 px-2 py-2 rounded-lg border border-border bg-surface text-xs"
            >
              <Pin className="w-3 h-3 shrink-0 mt-0.5 text-fg-faint" />
              <div className="flex-1 min-w-0">
                <p className="text-fg leading-relaxed break-words">{pin.content}</p>
                <div className="flex items-center gap-2 mt-1">
                  <span className={`text-[10px] font-medium ${scopeColour(pin.scope)}`}>
                    {scopeLabel(pin.scope)}
                  </span>
                  {pin.agent_id && (
                    <span className="text-[10px] text-fg-faint">
                      by {pin.agent_id}
                    </span>
                  )}
                </div>
              </div>
              <button
                type="button"
                onClick={() => deleteMutation.mutate(pin.id)}
                disabled={deleteMutation.isPending}
                className="p-0.5 rounded text-fg-faint hover:text-danger transition-colors opacity-0 group-hover:opacity-100"
                title="Unpin"
              >
                <PinOff className="w-3 h-3" />
              </button>
            </div>
          ))}
        </div>
      </ScrollArea>
    </div>
  )
}

// ── Helpers ──────────────────────────────────────────────────────────────────

function EmptyState({ message }: { message: string }) {
  return (
    <div className="flex items-center justify-center h-full">
      <p className="text-xs text-fg-faint">{message}</p>
    </div>
  )
}
