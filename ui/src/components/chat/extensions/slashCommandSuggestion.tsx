import { createRoot, type Root } from 'react-dom/client'
import type { SuggestionOptions, SuggestionProps } from '@tiptap/suggestion'
import { SlashCommandMenu, type SlashCommandMenuRef } from './SlashCommandMenu'
import type { SlashCommand } from './SlashCommandExtension'
import { setSlashMenuOpen } from '../ChatComposer'

// Cache fetched commands with a short TTL
let commandsCache: SlashCommand[] = []
let cacheTimestamp = 0
const CACHE_TTL_MS = 30_000

async function fetchCommands(): Promise<SlashCommand[]> {
  const now = Date.now()
  if (commandsCache.length > 0 && now - cacheTimestamp < CACHE_TTL_MS) {
    return commandsCache
  }
  try {
    const res = await fetch('/api/commands')
    if (!res.ok) return commandsCache
    const data = await res.json()
    commandsCache = data
    cacheTimestamp = now
    return data
  } catch {
    return commandsCache
  }
}

export const slashCommandSuggestion: Omit<SuggestionOptions<SlashCommand>, 'editor'> = {
  char: '/',
  startOfLine: false,

  items: async ({ query }) => {
    const commands = await fetchCommands()
    const q = query.toLowerCase()
    return commands.filter(
      (cmd) =>
        cmd.name.toLowerCase().includes(q) ||
        cmd.description.toLowerCase().includes(q) ||
        cmd.category.toLowerCase().includes(q)
    ).slice(0, 10)
  },

  render: () => {
    let popup: HTMLDivElement | null = null
    let root: Root | null = null
    let menuRef: SlashCommandMenuRef | null = null

    return {
      onStart: (props: SuggestionProps<SlashCommand>) => {
        setSlashMenuOpen(true)

        popup = document.createElement('div')
        popup.style.position = 'absolute'
        popup.style.zIndex = '9999'
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
      setSlashMenuOpen(false)
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
