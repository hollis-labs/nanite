import { useEffect, useCallback, useState, useRef } from 'react'
import { useEditor, EditorContent } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import Placeholder from '@tiptap/extension-placeholder'
import { useQueryClient } from '@tanstack/react-query'
import { ComposerToolbar } from './ComposerToolbar'
import { SlashCommandExtension, type SlashCommand } from './extensions/SlashCommandExtension'
import { slashCommandSuggestion } from './extensions/slashCommandSuggestion'
import { FileMentionExtension, type FileResult } from './extensions/FileMentionExtension'
import { fileMentionSuggestion } from './extensions/fileMentionSuggestion'
import { useAppStore } from '@/stores/useAppStore'
import { api } from '@/lib/api'

interface ChatComposerProps {
  onSend: (content: string) => void
  isStreaming?: boolean
  onStop?: () => void
  onEditorReady?: (focus: () => void) => void
  reloadMessages?: () => void
}

// Module-level flags so the editor's stale handleKeyDown closure can check them.
// Set by the suggestion lifecycle callbacks (onStart/onExit).
let slashMenuOpen = false
let fileMentionMenuOpen = false

export function ChatComposer({ onSend, isStreaming = false, onStop, onEditorReady, reloadMessages }: ChatComposerProps) {
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const setActiveSession = useAppStore((s) => s.setActiveSession)
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId)
  const queryClient = useQueryClient()
  const [dragOver, setDragOver] = useState(false)
  const dropRef = useRef<HTMLDivElement>(null)

  const handleDrop = useCallback(async (e: React.DragEvent) => {
    e.preventDefault()
    setDragOver(false)
    if (!activeSessionId || !e.dataTransfer.files.length) return
    for (const file of Array.from(e.dataTransfer.files)) {
      await api.uploadArtifact(activeSessionId, file)
    }
    queryClient.invalidateQueries({ queryKey: ['artifacts', activeSessionId] })
  }, [activeSessionId, queryClient])

  // Handle slash command execution
  const handleCommand = useCallback(async (cmd: SlashCommand) => {
    switch (cmd.name) {
      case 'new': {
        const session = await api.createSession({ workspace_id: activeWorkspaceId || '' })
        setActiveSession(session.id)
        void queryClient.invalidateQueries({ queryKey: ['sessions'] })
        return
      }
      case 'fork': {
        if (!activeSessionId) return
        const forked = await api.forkSession(activeSessionId, { include_messages: true })
        setActiveSession(forked.id)
        void queryClient.invalidateQueries({ queryKey: ['sessions'] })
        return
      }
      case 'clone': {
        if (!activeSessionId) return
        const cloned = await api.forkSession(activeSessionId, { include_messages: false })
        setActiveSession(cloned.id)
        void queryClient.invalidateQueries({ queryKey: ['sessions'] })
        return
      }
      case 'bookmark': {
        if (!activeSessionId) return
        const session = await api.getSession(activeSessionId)
        const messages = session.messages || []
        const lastAssistant = [...messages].reverse().find((m) => m.role === 'assistant')
        if (lastAssistant) {
          await api.toggleBookmark(lastAssistant.id, activeSessionId)
          void queryClient.invalidateQueries({ queryKey: ['bookmarks', activeSessionId] })
        }
        return
      }
      case 'compact': {
        if (!activeSessionId) return
        await api.compactSession(activeSessionId)
        void queryClient.invalidateQueries({ queryKey: ['session', activeSessionId] })
        return
      }
      case 'agent':
      case 'model':
        return
      default: {
        if (!activeSessionId) return
        try {
          const result = await api.executeCommand(cmd.name, activeSessionId, '')
          if (result.action === 'message') {
            reloadMessages?.()
          }
        } catch (err) {
          console.error('Command execution failed:', err)
        }
      }
    }
  }, [activeSessionId, activeWorkspaceId, setActiveSession, queryClient, onSend, reloadMessages])

  const handleCommandRef = useRef(handleCommand)
  handleCommandRef.current = handleCommand

  const handleSendRef = useRef<() => void>(() => {})

  const editor = useEditor({
    extensions: [
      StarterKit.configure({
        heading: false,
        blockquote: false,
        codeBlock: false,
        horizontalRule: false,
        bulletList: false,
        orderedList: false,
        listItem: false,
      }),
      Placeholder.configure({
        placeholder: 'Message Conduit... (Enter to send, / for commands, @ for files)',
      }),
      SlashCommandExtension.configure({
        suggestion: {
          ...slashCommandSuggestion,
          command: ({ editor: ed, range, props }: { editor: any; range: { from: number; to: number }; props: SlashCommand }) => {
            ed?.chain().focus().deleteRange(range).run()
            void handleCommandRef.current(props)
          },
        },
      }),
      FileMentionExtension.configure({
        suggestion: {
          ...fileMentionSuggestion,
          command: ({ editor: ed, range, props }: { editor: any; range: { from: number; to: number }; props: FileResult }) => {
            // Delete the @query text and insert @path as plain text
            ed?.chain().focus().deleteRange(range).insertContent(`@${props.path} `).run()
          },
        },
      }),
    ],
    editorProps: {
      attributes: {
        class:
          'bg-transparent text-sm text-fg placeholder:text-fg-faint outline-none min-h-[40px] max-h-[120px] overflow-y-auto py-2 px-1 leading-relaxed prose-sm prose-invert',
      },
      handleKeyDown(_view, event) {
        if (event.key === 'Enter') {
          // If any suggestion menu is open, let the plugin handle Enter
          if (slashMenuOpen || fileMentionMenuOpen) {
            return false
          }
          if (event.metaKey || event.ctrlKey || event.shiftKey) {
            return false
          }
          const text = editor?.getText().trim() ?? ''
          if (!text) return false
          event.preventDefault()
          handleSendRef.current()
          return true
        }
        return false
      },
    },
    content: '',
  })

  useEffect(() => {
    if (editor && onEditorReady) {
      onEditorReady(() => {
        editor.commands.focus()
      })
    }
  }, [editor, onEditorReady])

  const handleSend = useCallback(() => {
    if (!editor) return
    const text = editor.getText().trim()
    if (!text) return
    onSend(text)
    editor.commands.clearContent()
  }, [editor, onSend])

  handleSendRef.current = handleSend

  const hasContent = editor ? editor.getText().trim().length > 0 : false

  return (
    <div className="px-4 pb-4 pt-2 shrink-0">
      <div
        ref={dropRef}
        className={`bg-bg-elevated border rounded-xl focus-within:border-border-subtle transition-colors shadow-lg shadow-black/20 ${
          dragOver ? 'border-accent bg-accent-muted' : 'border-border'
        }`}
        onDragOver={(e) => { e.preventDefault(); setDragOver(true) }}
        onDragLeave={() => setDragOver(false)}
        onDrop={(e) => void handleDrop(e)}
      >
        {dragOver && (
          <div className="px-3 py-1.5 text-xs text-accent text-center border-b border-accent/30">
            Drop files to attach
          </div>
        )}
        <div className="px-3 py-1">
          <EditorContent
            editor={editor}
            className="min-w-0 [&_.tiptap]:outline-none [&_.tiptap_p.is-editor-empty:first-child::before]:content-[attr(data-placeholder)] [&_.tiptap_p.is-editor-empty:first-child::before]:text-fg-faint [&_.tiptap_p.is-editor-empty:first-child::before]:float-left [&_.tiptap_p.is-editor-empty:first-child::before]:h-0 [&_.tiptap_p.is-editor-empty:first-child::before]:pointer-events-none"
          />
        </div>
        <ComposerToolbar
          hasContent={hasContent}
          isStreaming={isStreaming}
          onSend={handleSend}
          onStop={onStop}
        />
      </div>
      <p className="text-center text-xs text-fg-faint mt-2">
        Conduit may produce inaccurate information.
      </p>
    </div>
  )
}

// Exported setters for suggestion lifecycle callbacks
export function setSlashMenuOpen(open: boolean) {
  slashMenuOpen = open
}

export function setFileMentionMenuOpen(open: boolean) {
  fileMentionMenuOpen = open
}
