import { useEffect, useCallback, useMemo } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useAppStore } from '@/stores/useAppStore'
import { useSettings } from '@/hooks/useSettings'
import { api } from '@/lib/api'

interface KeyboardShortcutsOptions {
  focusComposer?: () => void
  sessions?: Array<{ id: string }>
}

const DEFAULT_BINDINGS: Record<string, string> = {
  toggle_left_sidebar: 'mod+b',
  toggle_right_rail: 'mod+/',
  focus_composer: 'mod+l',
  new_session: 'mod+n',
  search: 'mod+k',
  next_session: 'mod+]',
  prev_session: 'mod+[',
  bookmark_last: 'mod+d',
  toggle_artifacts: 'mod+.',
}

/** Check if a keyboard event matches a binding string like "mod+b" or "mod+shift+k". */
function matchesBinding(e: KeyboardEvent, binding: string): boolean {
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

export function useKeyboardShortcuts(options: KeyboardShortcutsOptions = {}) {
  const toggleLeftSidebar = useLayoutStore((s) => s.toggleLeftSidebar)
  const toggleRightRail = useLayoutStore((s) => s.toggleRightRail)
  const toggleArtifactsDrawer = useLayoutStore((s) => s.toggleArtifactsDrawer)
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId)
  const setActiveSession = useAppStore((s) => s.setActiveSession)
  const queryClient = useQueryClient()

  const { data: userSettings } = useSettings()
  const { focusComposer, sessions = [] } = options

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

  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
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
      } else if (matchesBinding(e, bindings.search)) {
        e.preventDefault()
        const sidebarOpen = useLayoutStore.getState().leftSidebarOpen
        if (!sidebarOpen) toggleLeftSidebar()
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
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [
    bindings,
    toggleLeftSidebar,
    toggleRightRail,
    toggleArtifactsDrawer,
    focusComposer,
    handleNewSession,
    handleBookmarkLast,
    navigateSession,
  ])
}
