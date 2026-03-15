import { useEffect, useCallback } from 'react'
import { useEditor, EditorContent } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import Placeholder from '@tiptap/extension-placeholder'
import { ComposerToolbar } from './ComposerToolbar'
import { SlashCommandExtension } from './extensions/SlashCommandExtension'
import { slashCommandSuggestion } from './extensions/slashCommandSuggestion'

interface ChatComposerProps {
  onSend: (content: string) => void
  isStreaming?: boolean
  onStop?: () => void
  onEditorReady?: (focus: () => void) => void
}

export function ChatComposer({ onSend, isStreaming = false, onStop, onEditorReady }: ChatComposerProps) {
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
      <div className="bg-zinc-900 border border-zinc-800 rounded-xl focus-within:border-zinc-700 transition-colors shadow-lg shadow-black/20">
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
