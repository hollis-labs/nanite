/**
 * BottomChatDrawer — J10 (CW-20260426-0008)
 *
 * Three-tab bottom drawer:
 *   Tab 0 — Scratchpad (extends P4 scratchpad infrastructure)
 *   Tab 1 — Documents (sidebar list + content view, per-doc include/exclude toggle)
 *   Tab 2 — Session Context (user-authored session-scoped prompt block)
 *
 * Opens via:
 *   - /scratch command (bare invocation)
 *   - Agent panel_open("bottom_chat_drawer") via envelope target
 *   - Direct user click on drawer toggle
 *
 * Dismiss policy: follows J8 state machine (setBottomDrawerOpen in useLayoutStore).
 */

import { useState, useCallback, useRef, useEffect } from 'react'
import { X, FileText, StickyNote, MessageSquare, Plus, Trash2, Eye, EyeOff, Maximize2, Minimize2, Pin, PinOff } from 'lucide-react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { ScrollArea } from '@/components/ui/scroll-area'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useAppStore } from '@/stores/useAppStore'
import { api } from '@/lib/api'
import type { Document, PinnedContent } from '@/lib/types'

// ── Tab IDs ──────────────────────────────────────────────────────────────────

type DrawerTab = 'scratchpad' | 'documents' | 'context' | 'pins'

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

export function BottomChatDrawer({ initialTab = 'scratchpad', onScratchpadRef }: BottomChatDrawerProps) {
  const isOpen = useLayoutStore((s) => s.bottomChatDrawerOpen)
  const setOpen = useLayoutStore((s) => s.setBottomDrawerOpen)
  const [activeTab, setActiveTab] = useState<DrawerTab>(initialTab)
  const [expanded, setExpanded] = useState(false)

  const handleClose = useCallback(() => setOpen(false, 'user'), [setOpen])

  // When the drawer is opened by a /scratch command, switch to scratchpad tab.
  useEffect(() => {
    if (isOpen && initialTab) {
      setActiveTab(initialTab)
    }
  }, [isOpen, initialTab])

  if (!isOpen) return null

  return (
    <div
      className={`border-t border-border bg-bg-elevated flex flex-col transition-all duration-200 ${
        expanded ? 'h-[60vh]' : 'h-72'
      }`}
      role="complementary"
      aria-label="Bottom drawer"
    >
      {/* Header */}
      <div className="flex items-center justify-between px-3 py-1.5 border-b border-border shrink-0">
        <div className="flex items-center gap-1">
          <TabButton
            active={activeTab === 'scratchpad'}
            icon={<StickyNote className="w-3.5 h-3.5" />}
            label="Scratchpad"
            onClick={() => setActiveTab('scratchpad')}
          />
          <TabButton
            active={activeTab === 'documents'}
            icon={<FileText className="w-3.5 h-3.5" />}
            label="Documents"
            onClick={() => setActiveTab('documents')}
          />
          <TabButton
            active={activeTab === 'context'}
            icon={<MessageSquare className="w-3.5 h-3.5" />}
            label="Session Context"
            onClick={() => setActiveTab('context')}
          />
          <TabButton
            active={activeTab === 'pins'}
            icon={<Pin className="w-3.5 h-3.5" />}
            label="Pins"
            onClick={() => setActiveTab('pins')}
          />
        </div>
        <div className="flex items-center gap-1">
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
      </div>
    </div>
  )
}

// ── Tab button ────────────────────────────────────────────────────────────────

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
      className={`flex items-center gap-1.5 px-2.5 py-1 rounded text-xs transition-colors ${
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
      case 'session': return 'text-blue-600'
      case 'cross_session': return 'text-violet-600'
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
