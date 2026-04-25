import { useEffect, useCallback, useMemo, useRef } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useAppStore } from '@/stores/useAppStore'
import { useNavigationStore } from '@/stores/useNavigationStore'
import { useSettings } from '@/hooks/useSettings'
import { api } from '@/lib/api'

interface KeyboardShortcutsOptions {
  focusComposer?: () => void
  sessions?: Array<{ id: string }>
  openCommandPalette?: () => void
  openSearch?: () => void
}

const DEFAULT_BINDINGS: Record<string, string> = {
  toggle_left_sidebar: 'mod+b',
  toggle_right_rail: 'mod+/',
  focus_composer: 'mod+l',
  new_session: 'mod+n',
  command_palette: 'mod+k',
  search: 'shift+shift',
  next_session: 'mod+]',
  prev_session: 'mod+[',
  bookmark_last: 'mod+d',
  toggle_artifacts: 'mod+.',
}

/** Human-readable labels for shortcuts */
export const SHORTCUT_LABELS: Record<string, string> = {
  toggle_left_sidebar: 'Toggle sidebar',
  toggle_right_rail: 'Toggle widgets',
  focus_composer: 'Focus composer',
  new_session: 'New chat',
  command_palette: 'Command palette',
  search: 'Search chats',
  next_session: 'Next session',
  prev_session: 'Previous session',
  bookmark_last: 'Bookmark last message',
  toggle_artifacts: 'Toggle artifacts',
}

/** Check if a keyboard event matches a binding string like "mod+b" or "mod+shift+k". */
export function matchesBinding(e: KeyboardEvent, binding: string): boolean {
  const parts = binding.split('+')
  const key = parts[parts.length - 1]
  const needsMod = parts.includes('mod')
  const needsShift = parts.includes('shift')
  const needsAlt = parts.includes('alt')

  const hasMod = e.metaKey || e.ctrlKey
  if (needsMod !== hasMod) return false
  if (needsShift !== e.shiftKey) return false
  if (needsAlt !== e.altKey) return false

  return e.key.toLowerCase() === key
}

/** Hook to fetch plugin keybindings */
export function usePluginKeybindings() {
  return useQuery({
    queryKey: ['plugin-keybindings'],
    queryFn: async () => {
      const data = await api.listPluginKeybindings()
      return data.keybindings
    },
    staleTime: 60_000,
  })
}

/** Hook to fetch custom actions (for keybinding registration) */
export function useActionKeybindings() {
  return useQuery({
    queryKey: ['actions'],
    queryFn: async () => {
      const data = await api.listActions()
      return data.actions.filter((a) => a.enabled && a.keybinding)
    },
    staleTime: 60_000,
  })
}

export function useKeyboardShortcuts(options: KeyboardShortcutsOptions = {}) {
  const toggleLeftSidebar = useLayoutStore((s) => s.toggleLeftSidebar)
  const toggleRightRail = useLayoutStore((s) => s.toggleRightRail)
  const toggleArtifactsDrawer = useLayoutStore((s) => s.toggleArtifactsDrawer)
  const toggleToolDrawer = useLayoutStore((s) => s.toggleToolDrawer)
  const toggleHeaderChips = useLayoutStore((s) => s.toggleHeaderChips)
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId)
  const setActiveSession = useAppStore((s) => s.setActiveSession)
  const queryClient = useQueryClient()

  const { data: userSettings } = useSettings()
  const { focusComposer, sessions = [], openCommandPalette, openSearch } = options

  // Fetch plugin keybindings and action keybindings
  const { data: pluginKeybindings } = usePluginKeybindings()
  const { data: actionBindings } = useActionKeybindings()

  // Merge user-customized shortcuts with defaults.
  const bindings = useMemo(() => {
    const custom = (userSettings?.ext_settings?.shortcuts as Record<string, string>) ?? {}
    return { ...DEFAULT_BINDINGS, ...custom }
  }, [userSettings?.ext_settings])

  const handleNewSession = useCallback(async () => {
    if (!activeWorkspaceId) return
    try {
      const newSession = await api.createSession({
        workspace_id: activeWorkspaceId,
        provider: userSettings?.default_provider || undefined,
        model: userSettings?.default_model || undefined,
      })
      void queryClient.invalidateQueries({ queryKey: ['sessions'] })
      setActiveSession(newSession.id)
    } catch (err) {
      console.error('Failed to create session:', err)
    }
  }, [activeWorkspaceId, queryClient, setActiveSession, userSettings])

  const handleBookmarkLast = useCallback(async () => {
    if (!activeSessionId) return
    try {
      const messages = await api.getMessages(activeSessionId, 50)
      const lastAssistant = [...(messages ?? [])].reverse().find((m) => m.role === 'assistant')
      if (lastAssistant) {
        await api.toggleBookmark(lastAssistant.id, activeSessionId)
        void queryClient.invalidateQueries({ queryKey: ['bookmarks', activeSessionId] })
      }
    } catch (err) {
      console.error('Failed to bookmark:', err)
    }
  }, [activeSessionId, queryClient])

  const navigateSession = useCallback(
    (direction: 'next' | 'prev') => {
      if (!activeSessionId || sessions.length === 0) return
      const idx = sessions.findIndex((s) => s.id === activeSessionId)
      if (idx === -1) return
      const newIdx = direction === 'next'
        ? Math.min(idx + 1, sessions.length - 1)
        : Math.max(idx - 1, 0)
      if (newIdx !== idx) {
        setActiveSession(sessions[newIdx]!.id)
      }
    },
    [activeSessionId, sessions, setActiveSession]
  )

  const navPop = useNavigationStore((s) => s.pop)
  const navDepth = useNavigationStore((s) => s.depth)
  const setCurrentPage = useLayoutStore((s) => s.setCurrentPage)
  const currentPage = useLayoutStore((s) => s.currentPage)

  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      // Escape: go back through navigation stack
      if (e.key === 'Escape' && !e.metaKey && !e.ctrlKey && !e.altKey && !e.shiftKey) {
        // Let Radix dialogs handle their own Escape first
        const hasOpenDialog = document.querySelector('[data-state="open"][role="dialog"]')
        if (hasOpenDialog) return

        // Let focused inputs/textareas release focus first
        const active = document.activeElement
        if (active && (active.tagName === 'INPUT' || active.tagName === 'TEXTAREA' || (active as HTMLElement).isContentEditable)) {
          ;(active as HTMLElement).blur()
          e.preventDefault()
          return
        }

        e.preventDefault()
        const depth = navDepth()
        if (depth > 0) {
          navPop()
        } else if (currentPage === 'settings') {
          setCurrentPage('chat')
          window.location.hash = '#chat'
        }
        return
      }

      // Core bindings
      if (matchesBinding(e, bindings.toggle_left_sidebar)) {
        e.preventDefault()
        toggleLeftSidebar()
      } else if (matchesBinding(e, bindings.toggle_right_rail)) {
        e.preventDefault()
        toggleRightRail()
      } else if (matchesBinding(e, bindings.focus_composer)) {
        e.preventDefault()
        focusComposer?.()
      } else if (matchesBinding(e, bindings.new_session)) {
        e.preventDefault()
        void handleNewSession()
      } else if (matchesBinding(e, bindings.command_palette)) {
        e.preventDefault()
        openCommandPalette?.()
      } else if (matchesBinding(e, bindings.next_session)) {
        e.preventDefault()
        navigateSession('next')
      } else if (matchesBinding(e, bindings.prev_session)) {
        e.preventDefault()
        navigateSession('prev')
      } else if (matchesBinding(e, bindings.bookmark_last)) {
        e.preventDefault()
        void handleBookmarkLast()
      } else if (matchesBinding(e, bindings.toggle_artifacts)) {
        e.preventDefault()
        toggleArtifactsDrawer()
      } else if ((e.metaKey || e.ctrlKey) && e.key === '\\') {
        e.preventDefault()
        window.dispatchEvent(new CustomEvent('toggle-layout-menu'))
      } else if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 't' && !e.shiftKey) {
        e.preventDefault()
        toggleToolDrawer()
      } else if ((e.metaKey || e.ctrlKey) && e.shiftKey && e.key.toLowerCase() === 'h') {
        e.preventDefault()
        toggleHeaderChips()
      } else {
        // Check custom action keybindings
        if (actionBindings && activeSessionId) {
          for (const action of actionBindings) {
            if (matchesBinding(e, action.keybinding)) {
              e.preventDefault()
              void api.executeAction(action.id, activeSessionId)
              return
            }
          }
        }
        // Check plugin keybindings
        if (pluginKeybindings) {
          for (const kb of pluginKeybindings) {
            if (matchesBinding(e, kb.key)) {
              e.preventDefault()
              // Plugin keybindings with action="command" execute the named command
              if (kb.action === 'command' && kb.action_value && activeSessionId) {
                void api.executeCommand(kb.action_value, activeSessionId, '')
              }
              return
            }
          }
        }
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [
    bindings,
    toggleLeftSidebar,
    toggleRightRail,
    toggleArtifactsDrawer,
    toggleToolDrawer,
    toggleHeaderChips,
    focusComposer,
    handleNewSession,
    handleBookmarkLast,
    navigateSession,
    navPop,
    navDepth,
    currentPage,
    setCurrentPage,
    openCommandPalette,
    actionBindings,
    pluginKeybindings,
    activeSessionId,
  ])

  // Double-shift to open search
  const lastShiftTime = useRef(0)
  useEffect(() => {
    function handleShiftShift(e: KeyboardEvent) {
      if (e.key !== 'Shift' || e.metaKey || e.ctrlKey || e.altKey) return
      const now = Date.now()
      if (now - lastShiftTime.current < 400) {
        e.preventDefault()
        openSearch?.()
        lastShiftTime.current = 0
      } else {
        lastShiftTime.current = now
      }
    }
    window.addEventListener('keydown', handleShiftShift)
    return () => window.removeEventListener('keydown', handleShiftShift)
  }, [openSearch])
}
