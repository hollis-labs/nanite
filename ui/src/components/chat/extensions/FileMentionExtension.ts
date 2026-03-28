import { Extension } from '@tiptap/react'
import Suggestion, { type SuggestionOptions } from '@tiptap/suggestion'
import { PluginKey } from '@tiptap/pm/state'

const fileMentionPluginKey = new PluginKey('fileMention')

export interface FileResult {
  path: string
  name: string
  is_dir: boolean
  size: number
  mod_time: string
}

export type FileMentionSuggestionOptions = Omit<SuggestionOptions<FileResult>, 'editor'>

interface FileMentionOptions {
  suggestion: FileMentionSuggestionOptions
}

export const FileMentionExtension = Extension.create<FileMentionOptions>({
  name: 'fileMention',

  addOptions() {
    return {
      suggestion: {
        char: '@',
        startOfLine: false,
        // Allow file path characters in the query (letters, numbers, dots, slashes, hyphens, underscores)
        allowedPrefixes: [' ', '\n'],
        command: ({ editor, range }: { editor: any; range: { from: number; to: number }; props: FileResult }) => {
          editor?.chain().focus().deleteRange(range).run()
        },
        items: () => [],
      } as FileMentionSuggestionOptions,
    }
  },

  addProseMirrorPlugins() {
    return [
      Suggestion({
        editor: this.editor,
        pluginKey: fileMentionPluginKey,
        ...this.options.suggestion,
      }),
    ]
  },
})
