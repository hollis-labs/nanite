import { createRoot, type Root } from 'react-dom/client'
import type { SuggestionOptions, SuggestionProps } from '@tiptap/suggestion'
import { SlashCommandMenu, type SlashCommandMenuRef } from './SlashCommandMenu'
import { COMMANDS, type SlashCommand } from './SlashCommandExtension'

export const slashCommandSuggestion: Omit<SuggestionOptions<SlashCommand>, 'editor'> = {
  char: '/',
  startOfLine: false,

  items: ({ query }) => {
    return COMMANDS.filter(
      (cmd) =>
        cmd.name.toLowerCase().includes(query.toLowerCase()) ||
        cmd.description.toLowerCase().includes(query.toLowerCase())
    ).slice(0, 8)
  },

  render: () => {
    let popup: HTMLDivElement | null = null
    let root: Root | null = null
    let menuRef: SlashCommandMenuRef | null = null

    return {
      onStart: (props: SuggestionProps<SlashCommand>) => {
        popup = document.createElement('div')
        popup.style.position = 'absolute'
        popup.style.zIndex = '50'
        document.body.appendChild(popup)

        root = createRoot(popup)

        updatePosition(props, popup)
        renderMenu(root, props)
      },

      onUpdate: (props: SuggestionProps<SlashCommand>) => {
        if (!popup || !root) return
        updatePosition(props, popup)
        renderMenu(root, props)
      },

      onKeyDown: (props: { event: KeyboardEvent }) => {
        if (props.event.key === 'Escape') {
          cleanup()
          return true
        }
        return menuRef?.onKeyDown(props) ?? false
      },

      onExit: () => {
        cleanup()
      },
    }

    function renderMenu(r: Root, props: SuggestionProps<SlashCommand>) {
      r.render(
        <SlashCommandMenu
          items={props.items}
          command={props.command}
          ref={(ref) => {
            menuRef = ref
          }}
        />
      )
    }

    function updatePosition(props: SuggestionProps<SlashCommand>, el: HTMLDivElement) {
      const rect = props.clientRect?.()
      if (!rect) return
      el.style.left = `${rect.left}px`
      el.style.top = `${rect.top - 8}px`
      el.style.transform = 'translateY(-100%)'
    }

    function cleanup() {
      if (root) {
        root.unmount()
        root = null
      }
      if (popup) {
        popup.remove()
        popup = null
      }
      menuRef = null
    }
  },
}
