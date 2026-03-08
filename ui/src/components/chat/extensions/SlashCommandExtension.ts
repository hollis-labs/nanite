import { Extension } from '@tiptap/react'
import Suggestion, { type SuggestionOptions } from '@tiptap/suggestion'
import type { Editor } from '@tiptap/react'

export interface SlashCommand {
  name: string
  description: string
  category: string
}

export const COMMANDS: SlashCommand[] = [
  { name: 'mode', description: 'Switch agent mode', category: 'agent' },
  { name: 'architect', description: 'Switch to architect mode', category: 'agent' },
  { name: 'planner', description: 'Switch to planner mode', category: 'agent' },
  { name: 'writer', description: 'Switch to writer mode', category: 'agent' },
  { name: 'compact', description: 'Compact session context', category: 'session' },
  { name: 'new', description: 'Create new chat session', category: 'session' },
  { name: 'bookmark', description: 'Bookmark last message', category: 'tools' },
  { name: 'template', description: 'Apply a template to format the last response', category: 'tools' },
  { name: 'help', description: 'Show available commands', category: 'help' },
]

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
        command: ({ editor, range, props }: { editor: Editor; range: { from: number; to: number }; props: SlashCommand }) => {
          editor.chain().focus().deleteRange(range).run()
          editor.chain().focus().insertContent(`/${props.name} `).run()
        },
        items: ({ query }: { query: string }) => {
          return COMMANDS.filter((cmd) =>
            cmd.name.toLowerCase().includes(query.toLowerCase()) ||
            cmd.description.toLowerCase().includes(query.toLowerCase())
          ).slice(0, 8)
        },
      } as SlashCommandSuggestionOptions,
    }
  },

  addProseMirrorPlugins() {
    return [
      Suggestion({
        editor: this.editor,
        ...this.options.suggestion,
      }),
    ]
  },
})
