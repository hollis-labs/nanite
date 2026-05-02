/**
 * ChatPrimaryDrawer — top drawer (chat-surface redesign 2026-05-01).
 *
 * Replaces the old `BottomChatDrawer` chrome at the top of the chat column.
 * Tab strip docks to the bottom edge so the strip itself is the handle when
 * the drawer is closed. Documents / Pins / PinnedCardTab bodies are lifted
 * verbatim from `BottomChatDrawer.tsx`. Reports / Diffs are net-new
 * empty-state placeholders. Tools renders the existing `ToolCallItem`
 * list, matching today's `ToolCallDrawer` body layout.
 */

import { useMemo, useRef, useState, useCallback, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  ClipboardList,
  Eye,
  EyeOff,
  FileText,
  GitCompare,
  GripHorizontal,
  Image as ImageIcon,
  Inbox,
  Package,
  Pin,
  PinOff,
  Plus,
  StickyNote,
  Trash2,
  Wrench,
  X,
  ArrowUpRight,
  ArrowDownLeft,
} from 'lucide-react'
import { ScrollArea } from '@/components/ui/scroll-area'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useAppStore } from '@/stores/useAppStore'
import { useChatStore } from '@/stores/useChatStore'
import { api } from '@/lib/api'
import type {
  Document,
  PinnedContent,
  Envelope,
  DrawerCardType,
  DrawerPinnedCard,
  AgentStateScope,
} from '@/lib/types'
import { EnvelopeRenderer } from '@/components/chat/envelopes/EnvelopeRenderer'
import { ToolCallItem } from '@/components/chat/ToolCallItem'
import { ScopeChip, ScopeFilterChip, type ScopeFilter } from '@/components/work/ScopeChip'

export function ChatPrimaryDrawer() {
  const drawer = useLayoutStore((s) => s.chatPrimaryDrawer)
  const setDrawer = useLayoutStore((s) => s.setChatPrimaryDrawer)
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const toolCalls = useChatStore((s) => s.toolCalls)
  const isStreaming = useChatStore((s) => s.isStreaming)
  const queryClient = useQueryClient()

  const hasRunningTool = toolCalls.some((tc) => tc.status === 'running')

  // DB-backed pinned cards (existing API).
  const { data: pinnedCards = [] } = useQuery<DrawerPinnedCard[]>({
    queryKey: ['drawer-cards', activeSessionId],
    queryFn: () => api.listDrawerCards(activeSessionId!),
    enabled: !!activeSessionId,
  })

  if (!activeSessionId) return null

  // Pull-tab drag mechanics — top-drawer variant. The drag-handle row is
  // always visible at the bottom of the drawer. Drag DOWN to grow (body
  // extends downward into the transcript area), drag UP to shrink. Auto-
  // closes when released near 0 height. Double-click toggles open/closed.
  const dragRef = useRef<{ y: number; height: number } | null>(null)
  const onPointerDown = (e: React.PointerEvent) => {
    dragRef.current = { y: e.clientY, height: drawer.height || 240 }
    ;(e.target as HTMLElement).setPointerCapture(e.pointerId)
    if (!drawer.open) setDrawer({ open: true })
  }
  const onPointerMove = (e: React.PointerEvent) => {
    const d = dragRef.current
    if (!d) return
    const next = Math.max(0, d.height + (e.clientY - d.y))
    setDrawer({ height: next })
  }
  const onPointerUp = (e: React.PointerEvent) => {
    if (!dragRef.current) return
    dragRef.current = null
    ;(e.target as HTMLElement).releasePointerCapture(e.pointerId)
    if (drawer.height < 24) setDrawer({ open: false, height: 240 })
  }
  const onDoubleClick = () => {
    setDrawer({ open: !drawer.open, height: drawer.height || 240 })
  }

  return (
    <div className="max-w-3xl w-full mx-auto relative">
      {/* Body — top of drawer, opens downward toward the transcript when
          active. Mirrors the body region of the original BottomChatDrawer. */}
      {drawer.open && (
        <div
          className="overflow-hidden border-x border-t border-border bg-bg-elevated"
          style={{ height: drawer.height }}
        >
          <DrawerBody activeTab={drawer.activeTab} pinnedCards={pinnedCards} />
        </div>
      )}

      {/* Tab row — visible only when drawer is open.  Footer row containing
          tabs (left, horizontally scrollable) + close button (right). The
          original BottomChatDrawer had this as a HEADER row at the top of
          the drawer; mirrored here to the bottom for the top-drawer layout. */}
      {drawer.open && (
        <div className="flex items-center justify-between gap-2 px-3 py-0.5 shrink-0 border-x border-t border-border bg-bg-elevated">
          <div className="flex items-center gap-1 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden min-w-0 flex-1">
            <PrimaryTabButton
              active={drawer.activeTab === 'documents'}
              icon={<FileText className="w-3.5 h-3.5" />}
              label="Documents"
              onClick={() => setDrawer({ activeTab: 'documents' })}
            />
            <PrimaryTabButton
              active={drawer.activeTab === 'reports'}
              icon={<ClipboardList className="w-3.5 h-3.5" />}
              label="Reports"
              onClick={() => setDrawer({ activeTab: 'reports' })}
            />
            <PrimaryTabButton
              active={drawer.activeTab === 'diffs'}
              icon={<GitCompare className="w-3.5 h-3.5" />}
              label="Diffs"
              onClick={() => setDrawer({ activeTab: 'diffs' })}
            />
            <PrimaryTabButton
              active={drawer.activeTab === 'tools'}
              icon={<Wrench className="w-3.5 h-3.5" />}
              label="Tools"
              runningPip={hasRunningTool && isStreaming}
              onClick={() => setDrawer({ activeTab: 'tools' })}
            />
            <PrimaryTabButton
              active={drawer.activeTab === 'pins'}
              icon={<Pin className="w-3.5 h-3.5" />}
              label="Pins"
              onClick={() => setDrawer({ activeTab: 'pins' })}
            />
            {pinnedCards.length > 0 && (
              <div className="w-px h-4 bg-border mx-1 shrink-0" aria-hidden="true" />
            )}
            {pinnedCards.map((card) => (
              <PrimaryPinnedTabButton
                key={card.id}
                active={drawer.activeTab === `pin:${card.id}`}
                card={card}
                onClick={() => setDrawer({ activeTab: `pin:${card.id}` })}
                onUnpin={async () => {
                  await api.unpinDrawerCard(card.id)
                  void queryClient.invalidateQueries({ queryKey: ['drawer-cards', activeSessionId] })
                }}
              />
            ))}
          </div>
          <div className="flex items-center gap-1 shrink-0">
            <button
              type="button"
              onClick={() => setDrawer({ open: false })}
              className="p-1 rounded text-fg-muted hover:text-fg hover:bg-surface transition-colors"
              aria-label="Close drawer"
            >
              <X className="w-3.5 h-3.5" />
            </button>
          </div>
        </div>
      )}

      {/* Drag-handle row — ALWAYS visible at the bottom of the drawer. Brand
          accent at bottom edge, rounded bottom corners (mirrors the bottom
          drawer's pattern). Drag DOWN to grow; double-click to toggle. */}
      <div
        className="relative flex items-center justify-center h-5 overflow-hidden bg-bg-elevated border-x border-border-subtle border-b-2 border-b-surface rounded-b-[10px] cursor-row-resize select-none touch-none"
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={onPointerUp}
        onDoubleClick={onDoubleClick}
        role="separator"
        aria-orientation="horizontal"
        aria-label="Drag to resize primary drawer; double-click to toggle"
      >
        <GripHorizontal size={12} className="text-fg-muted pointer-events-none" />
      </div>
    </div>
  )
}

// ── Tab button components ───────────────────────────────────────────────────
// Mirrors the original BottomChatDrawer.TabButton / PinnedTabButton style.

function PrimaryTabButton({
  active,
  icon,
  label,
  onClick,
  runningPip,
}: {
  active: boolean
  icon: React.ReactNode
  label: string
  onClick: () => void
  runningPip?: boolean
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`flex items-center gap-1.5 px-2 py-0.5 rounded text-xs transition-colors shrink-0 ${
        active
          ? 'bg-surface/60 text-fg'
          : 'text-fg-muted hover:text-fg-secondary hover:bg-surface/40'
      }`}
    >
      {icon}
      {label}
      {runningPip && (
        <span className="inline-block h-1.5 w-1.5 rounded-full bg-warning animate-pulse" />
      )}
    </button>
  )
}

function PrimaryPinnedTabButton({
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
      className={`group flex items-center gap-1 px-2 py-0.5 rounded text-xs transition-colors shrink-0 max-w-[160px] ${
        active
          ? 'bg-surface/60 text-fg'
          : 'text-fg-muted hover:text-fg-secondary hover:bg-surface/40'
      }`}
    >
      <button
        type="button"
        onClick={onClick}
        className="flex items-center gap-1 min-w-0"
        title={card.title || card.card_type}
      >
        <Package className="w-3 h-3 shrink-0" />
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

function DrawerBody({
  activeTab,
  pinnedCards,
}: {
  activeTab: string
  pinnedCards: DrawerPinnedCard[]
}) {
  switch (activeTab) {
    case 'documents': return <DocumentsTab />
    case 'reports': return <ReportsTab />
    case 'diffs': return <DiffsTab />
    case 'tools': return <ToolsTab />
    case 'pins': return <PinsTab />
    default:
      if (activeTab.startsWith('pin:')) {
        const card = pinnedCards.find((c) => `pin:${c.id}` === activeTab)
        return card ? <PinnedCardTab card={card} /> : null
      }
      return null
  }
}

// ── Documents tab ─────────────────────────────────────────────────────────────
// Lifted verbatim from BottomChatDrawer.tsx's `DocumentsTab` (and its
// helper components: DocumentRow, DocumentDetail, PasteForm). No logic
// changes.

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

// ── Reports tab (empty state) ────────────────────────────────────────────────

function ReportsTab() {
  return (
    <div className="flex h-full items-center justify-center p-6 text-xs text-fg-muted">
      No reports yet — they'll appear here as agents produce them.
    </div>
  )
}

// ── Diffs tab (empty state) ──────────────────────────────────────────────────

function DiffsTab() {
  return (
    <div className="flex h-full items-center justify-center p-6 text-xs text-fg-muted">
      No diffs in this session yet.
    </div>
  )
}

// ── Tools tab ────────────────────────────────────────────────────────────────
// Renders the existing ToolCallItem list — same UI as today's
// ToolCallDrawer body.

function ToolsTab() {
  const toolCalls = useChatStore((s) => s.toolCalls)
  return (
    <div className="chat-scroll min-h-0 h-full overflow-y-auto">
      {toolCalls.map((tc) => (
        <ToolCallItem key={tc.id} toolCall={tc} variant="drawer" />
      ))}
    </div>
  )
}

// ── Pins tab ─────────────────────────────────────────────────────────────────
// Lifted verbatim from BottomChatDrawer.tsx's `PinsTab` (and its helpers
// PinsScopeSection, PinRow). No logic changes.

function PinsTab() {
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const activeProjectId = useAppStore((s) => s.activeProjectId)
  const queryClient = useQueryClient()
  const [filter, setFilter] = useState<ScopeFilter>('all')

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

  const scopeMutation = useMutation({
    mutationFn: ({ id, scope, projectId }: { id: string; scope: AgentStateScope; projectId?: string }) =>
      api.updatePinScope(id, scope, projectId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['pins', activeSessionId] })
    },
  })

  if (!activeSessionId) {
    return <EmptyState message="No active session" />
  }

  // Partition pins by scope. Filter narrows to a specific tier when set.
  const sessionPins = pins.filter((p) => p.scope === 'session')
  const projectPins = pins.filter((p) => p.scope === 'project')
  const showSession = filter === 'all' || filter === 'session'
  const showProject = filter === 'all' || filter === 'project'

  const promote = (id: string) => {
    if (!activeProjectId) return
    scopeMutation.mutate({ id, scope: 'project', projectId: activeProjectId })
  }
  const demote = (id: string) => {
    scopeMutation.mutate({ id, scope: 'session', projectId: '' })
  }

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between px-3 py-1 shrink-0 gap-2">
        <p className="text-xs text-fg-muted">
          Pinned context — survives compaction · set by agent via <code className="font-mono text-fg-faint">nanite_pin</code>
        </p>
        <ScopeFilterChip filter={filter} onChange={setFilter} />
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
        {showSession && sessionPins.length > 0 && (
          <PinsScopeSection label="This Session">
            <div className="space-y-1">
              {sessionPins.map((pin) => (
                <PinRow
                  key={pin.id}
                  pin={pin}
                  canPromote={!!activeProjectId}
                  canDemote={false}
                  onPromote={promote}
                  onDemote={demote}
                  onDelete={(id) => deleteMutation.mutate(id)}
                />
              ))}
            </div>
          </PinsScopeSection>
        )}
        {showProject && projectPins.length > 0 && (
          <PinsScopeSection label="This Project">
            <div className="space-y-1">
              {projectPins.map((pin) => (
                <PinRow
                  key={pin.id}
                  pin={pin}
                  canPromote={false}
                  canDemote
                  onPromote={promote}
                  onDemote={demote}
                  onDelete={(id) => deleteMutation.mutate(id)}
                />
              ))}
            </div>
          </PinsScopeSection>
        )}
      </ScrollArea>
    </div>
  )
}

function PinsScopeSection({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="px-2 pt-2">
      <div className="px-1 mb-1 text-[10px] uppercase tracking-wider text-fg-faint">{label}</div>
      {children}
    </div>
  )
}

interface PinRowProps {
  pin: PinnedContent
  canPromote: boolean
  canDemote: boolean
  onPromote: (id: string) => void
  onDemote: (id: string) => void
  onDelete: (id: string) => void
}

function PinRow({ pin, canPromote, canDemote, onPromote, onDemote, onDelete }: PinRowProps) {
  return (
    <div className="group flex items-start gap-2 px-2 py-2 rounded-lg border border-border bg-surface text-xs">
      <Pin className="w-3 h-3 shrink-0 mt-0.5 text-fg-faint" />
      <div className="flex-1 min-w-0">
        <p className="text-fg leading-relaxed break-words">{pin.content}</p>
        <div className="flex items-center gap-2 mt-1">
          <ScopeChip scope={pin.scope} />
          {pin.agent_id && (
            <span className="text-[10px] text-fg-faint">by {pin.agent_id}</span>
          )}
        </div>
      </div>
      <div className="flex items-center gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity">
        {canPromote && (
          <button
            type="button"
            onClick={() => onPromote(pin.id)}
            className="p-0.5 rounded text-fg-faint hover:text-primary"
            title="Promote to project"
          >
            <ArrowUpRight className="w-3 h-3" />
          </button>
        )}
        {canDemote && (
          <button
            type="button"
            onClick={() => onDemote(pin.id)}
            className="p-0.5 rounded text-fg-faint hover:text-fg"
            title="Demote to session"
          >
            <ArrowDownLeft className="w-3 h-3" />
          </button>
        )}
        <button
          type="button"
          onClick={() => onDelete(pin.id)}
          className="p-0.5 rounded text-fg-faint hover:text-danger transition-colors"
          title="Unpin"
        >
          <PinOff className="w-3 h-3" />
        </button>
      </div>
    </div>
  )
}

// ── Pinned card tab ──────────────────────────────────────────────────────────
// Lifted from BottomChatDrawer.tsx's `PinnedCardContent` (and its helpers
// PinnedCardBody, UnknownCardBody, ArtifactMiniBody, formatSize,
// CardTypeIcon). The plan signature is `({ card }: { card: DrawerPinnedCard })`,
// so the unpin mutation is wired locally here to preserve the in-pane
// Unpin button from the lifted body.

function PinnedCardTab({ card }: { card: DrawerPinnedCard }) {
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const queryClient = useQueryClient()
  const unpinMutation = useMutation({
    mutationFn: (id: string) => api.unpinDrawerCard(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['drawer-cards', activeSessionId] })
    },
  })

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
          onClick={() => unpinMutation.mutate(card.id)}
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

// ── Helpers ──────────────────────────────────────────────────────────────────

function EmptyState({ message }: { message: string }) {
  return (
    <div className="flex items-center justify-center h-full">
      <p className="text-xs text-fg-faint">{message}</p>
    </div>
  )
}

