import { createRoot, type Root } from 'react-dom/client'
import type { SuggestionOptions, SuggestionProps } from '@tiptap/suggestion'
import { FileMentionMenu, type FileMentionMenuRef } from './FileMentionMenu'
import type { FileResult } from './FileMentionExtension'
import { setFileMentionMenuOpen } from '../ChatComposer'

// Debounce timer for API calls
let debounceTimer: ReturnType<typeof setTimeout> | null = null

async function fetchFiles(query: string, sessionId: string): Promise<FileResult[]> {
  try {
    const params = new URLSearchParams({ q: query })
    if (sessionId) params.set('session_id', sessionId)
    const res = await fetch(`/api/autocomplete/files?${params}`)
    if (!res.ok) return []
    return res.json()
  } catch {
    return []
  }
}

// Get active session ID from the app store (read directly to avoid hook dependency)
function getActiveSessionId(): string {
  try {
    // Access Zustand store directly — it's a module singleton
    const storeState = JSON.parse(localStorage.getItem('conduit-app') || '{}')
    return storeState?.state?.activeSessionId || ''
  } catch {
    return ''
  }
}

export const fileMentionSuggestion: Omit<SuggestionOptions<FileResult>, 'editor'> = {
  char: '@',
  startOfLine: false,

  items: ({ query }) => {
    // Return a promise that debounces the API call
    return new Promise<FileResult[]>((resolve) => {
      if (debounceTimer) clearTimeout(debounceTimer)
      debounceTimer = setTimeout(async () => {
        const sessionId = getActiveSessionId()
        const results = await fetchFiles(query, sessionId)
        resolve(results)
      }, 150)
    })
  },

  render: () => {
    let popup: HTMLDivElement | null = null
    let root: Root | null = null
    let menuRef: FileMentionMenuRef | null = null

    return {
      onStart: (props: SuggestionProps<FileResult>) => {
        setFileMentionMenuOpen(true)

        popup = document.createElement('div')
        popup.style.position = 'absolute'
        popup.style.zIndex = '50'
        document.body.appendChild(popup)

        root = createRoot(popup)

        updatePosition(props, popup)
        renderMenu(root, props)
      },

      onUpdate: (props: SuggestionProps<FileResult>) => {
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

    function renderMenu(r: Root, props: SuggestionProps<FileResult>) {
      r.render(
        <FileMentionMenu
          items={props.items}
          command={props.command}
          ref={(ref) => {
            menuRef = ref
          }}
        />
      )
    }

    function updatePosition(props: SuggestionProps<FileResult>, el: HTMLDivElement) {
      const rect = props.clientRect?.()
      if (!rect) return
      el.style.left = `${rect.left}px`
      el.style.top = `${rect.top - 8}px`
      el.style.transform = 'translateY(-100%)'
    }

    function cleanup() {
      setFileMentionMenuOpen(false)
      if (debounceTimer) {
        clearTimeout(debounceTimer)
        debounceTimer = null
      }
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
