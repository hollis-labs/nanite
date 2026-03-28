import { useEffect, useCallback, useState, useRef } from 'react'
import { useEditor, EditorContent } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import Placeholder from '@tiptap/extension-placeholder'
import { useQueryClient } from '@tanstack/react-query'
import { ComposerToolbar } from './ComposerToolbar'
import { SlashCommandExtension } from './extensions/SlashCommandExtension'
import { slashCommandSuggestion } from './extensions/slashCommandSuggestion'
import { useAppStore } from '@/stores/useAppStore'
import { api } from '@/lib/api'

interface ChatComposerProps {
  onSend: (content: string) => void
  isStreaming?: boolean
  onStop?: () => void
  onEditorReady?: (focus: () => void) => void
}

export function ChatComposer({ onSend, isStreaming = false, onStop, onEditorReady }: ChatComposerProps) {
  const activeSessionId = useAppStore((s) => s.activeSessionId)
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
        placeholder: 'Message Conduit... (Enter to send, / for commands)',
      }),
      SlashCommandExtension.configure({
        suggestion: slashCommandSuggestion,
      }),
    ],
    editorProps: {
      attributes: {
        class:
          'bg-transparent text-sm text-zinc-100 placeholder:text-zinc-600 outline-none min-h-[40px] max-h-[120px] overflow-y-auto py-2 px-1 leading-relaxed prose-sm prose-invert',
      },
      handleKeyDown(_view, event) {
        if (event.key === 'Enter') {
          // Cmd+Enter or Shift+Enter inserts newline
          if (event.metaKey || event.ctrlKey || event.shiftKey) {
            return false // let TipTap handle newline
          }
          // Plain Enter sends
          const text = editor?.getText().trim() ?? ''
          if (!text) return false
          event.preventDefault()
          handleSend()
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

  const hasContent = editor ? editor.getText().trim().length > 0 : false

  return (
    <div className="px-4 pb-4 pt-2 shrink-0">
      <div
        ref={dropRef}
        className={`bg-zinc-900 border rounded-xl focus-within:border-zinc-700 transition-colors shadow-lg shadow-black/20 ${
          dragOver ? 'border-indigo-500 bg-indigo-500/5' : 'border-zinc-800'
        }`}
        onDragOver={(e) => { e.preventDefault(); setDragOver(true) }}
        onDragLeave={() => setDragOver(false)}
        onDrop={(e) => void handleDrop(e)}
      >
        {dragOver && (
          <div className="px-3 py-1.5 text-xs text-indigo-400 text-center border-b border-indigo-500/30">
            Drop files to attach
          </div>
        )}
        {/* Editor area */}
        <div className="px-3 py-1">
          <EditorContent
            editor={editor}
            className="min-w-0 [&_.tiptap]:outline-none [&_.tiptap_p.is-editor-empty:first-child::before]:content-[attr(data-placeholder)] [&_.tiptap_p.is-editor-empty:first-child::before]:text-zinc-600 [&_.tiptap_p.is-editor-empty:first-child::before]:float-left [&_.tiptap_p.is-editor-empty:first-child::before]:h-0 [&_.tiptap_p.is-editor-empty:first-child::before]:pointer-events-none"
          />
        </div>
        {/* Toolbar with send button */}
        <ComposerToolbar
          hasContent={hasContent}
          isStreaming={isStreaming}
          onSend={handleSend}
          onStop={onStop}
        />
      </div>
      <p className="text-center text-xs text-zinc-600 mt-2">
        Conduit may produce inaccurate information.
      </p>
    </div>
  )
}
