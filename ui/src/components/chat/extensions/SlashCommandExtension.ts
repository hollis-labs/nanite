import { Extension } from '@tiptap/react'
import Suggestion, { type SuggestionOptions } from '@tiptap/suggestion'
import { PluginKey } from '@tiptap/pm/state'
import type { Editor } from '@tiptap/react'

const slashCommandPluginKey = new PluginKey('slashCommand')

export interface SlashCommand {
  name: string
  description: string
  category: string
  source: string
}

export type SlashCommandSuggestionOptions = Omit<SuggestionOptions<SlashCommand>, 'editor'>

interface SlashCommandOptions {
  suggestion: SlashCommandSuggestionOptions
}

export const SlashCommandExtension = Extension.create<SlashCommandOptions>({
  name: 'slashCommand',

  addOptions() {
    return {
      suggestion: {
        char: '/',
        startOfLine: false,
        command: ({ editor, range }: { editor: Editor; range: { from: number; to: number }; props: SlashCommand }) => {
          // Delete the /query text — execution is handled by the composer via onCommand callback
          editor.chain().focus().deleteRange(range).run()
        },
        items: () => [],
      } as SlashCommandSuggestionOptions,
    }
  },

  addProseMirrorPlugins() {
    return [
      Suggestion({
        editor: this.editor,
        pluginKey: slashCommandPluginKey,
        ...this.options.suggestion,
      }),
    ]
  },
})
