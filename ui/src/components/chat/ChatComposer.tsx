import { useEditor, EditorContent } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import Placeholder from '@tiptap/extension-placeholder'
import { SendHorizonal, Square } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { ComposerToolbar } from './ComposerToolbar'

interface ChatComposerProps {
  onSend: (content: string) => void
  isStreaming?: boolean
  onStop?: () => void
}

export function ChatComposer({ onSend, isStreaming = false, onStop }: ChatComposerProps) {
  const editor = useEditor({
    extensions: [
      StarterKit.configure({
        // Disable features we don't need in the composer
        heading: false,
        blockquote: false,
        codeBlock: false,
        horizontalRule: false,
        bulletList: false,
        orderedList: false,
        listItem: false,
      }),
      Placeholder.configure({
        placeholder: 'Message Mentat... (Enter to send, Shift+Enter for new line)',
      }),
    ],
    editorProps: {
      attributes: {
        class:
          'flex-1 bg-transparent text-sm text-zinc-100 placeholder:text-zinc-600 outline-none min-h-[40px] max-h-[200px] overflow-y-auto py-2 px-1 leading-relaxed prose-sm prose-invert',
      },
      handleKeyDown(_view, event) {
        if (event.key === 'Enter' && !event.shiftKey) {
          event.preventDefault()
          handleSend()
          return true
        }
        return false
      },
    },
    content: '',
  })

  const handleSend = () => {
    if (!editor) return
    const text = editor.getText().trim()
    if (!text) return
    onSend(text)
    editor.commands.clearContent()
  }

  const hasContent = editor ? editor.getText().trim().length > 0 : false

  return (
    <div className="px-4 pb-4 pt-2 shrink-0">
      <div className="bg-zinc-900 border border-zinc-800 rounded-xl focus-within:border-zinc-700 transition-colors">
        <div className="flex items-end gap-2 px-3 py-1">
          <EditorContent
            editor={editor}
            className="flex-1 min-w-0 [&_.tiptap]:outline-none [&_.tiptap_p.is-editor-empty:first-child::before]:content-[attr(data-placeholder)] [&_.tiptap_p.is-editor-empty:first-child::before]:text-zinc-600 [&_.tiptap_p.is-editor-empty:first-child::before]:float-left [&_.tiptap_p.is-editor-empty:first-child::before]:h-0 [&_.tiptap_p.is-editor-empty:first-child::before]:pointer-events-none"
          />
          {isStreaming ? (
            <Button
              variant="ghost"
              size="icon"
              className="w-8 h-8 shrink-0 mb-0.5 text-red-400 hover:text-red-300 hover:bg-red-500/10 transition-colors"
              onClick={onStop}
            >
              <Square className="w-4 h-4" />
            </Button>
          ) : (
            <Button
              variant="ghost"
              size="icon"
              className={`w-8 h-8 shrink-0 mb-0.5 transition-colors ${
                hasContent
                  ? 'text-indigo-400 hover:text-indigo-300 hover:bg-indigo-500/10'
                  : 'text-zinc-600'
              }`}
              disabled={!hasContent}
              onClick={handleSend}
            >
              <SendHorizonal className="w-4 h-4" />
            </Button>
          )}
        </div>
        <ComposerToolbar />
      </div>
      <p className="text-center text-xs text-zinc-600 mt-2">
        Mentat Chat may produce inaccurate information.
      </p>
    </div>
  )
}
