import { useEffect, useCallback } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useAppStore } from '@/stores/useAppStore'
import { api } from '@/lib/api'

interface KeyboardShortcutsOptions {
  focusComposer?: () => void
  sessions?: Array<{ id: string }>
}

export function useKeyboardShortcuts(options: KeyboardShortcutsOptions = {}) {
  const toggleLeftSidebar = useLayoutStore((s) => s.toggleLeftSidebar)
  const toggleRightRail = useLayoutStore((s) => s.toggleRightRail)
  const toggleArtifactsDrawer = useLayoutStore((s) => s.toggleArtifactsDrawer)
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId)
  const setActiveSession = useAppStore((s) => s.setActiveSession)
  const queryClient = useQueryClient()

  const { focusComposer, sessions = [] } = options

  const handleNewSession = useCallback(async () => {
    if (!activeWorkspaceId) return
    try {
      const newSession = await api.createSession({ workspace_id: activeWorkspaceId })
      void queryClient.invalidateQueries({ queryKey: ['sessions'] })
      setActiveSession(newSession.id)
    } catch (err) {
      console.error('Failed to create session:', err)
    }
  }, [activeWorkspaceId, queryClient, setActiveSession])

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
      const mod = e.metaKey || e.ctrlKey

      if (mod && e.key === 'b') {
        e.preventDefault()
        toggleLeftSidebar()
      }

      if (mod && e.key === '/') {
        e.preventDefault()
        toggleRightRail()
      }

      if (mod && e.key === 'l') {
        e.preventDefault()
        focusComposer?.()
      }

      if (mod && e.key === 'n') {
        e.preventDefault()
        void handleNewSession()
      }

      if (mod && e.key === 'k') {
        e.preventDefault()
        // Focus search — for now, toggle sidebar to show sessions
        const sidebarOpen = useLayoutStore.getState().leftSidebarOpen
        if (!sidebarOpen) {
          toggleLeftSidebar()
        }
      }

      if (mod && e.key === ']') {
        e.preventDefault()
        navigateSession('next')
      }

      if (mod && e.key === '[') {
        e.preventDefault()
        navigateSession('prev')
      }

      if (mod && e.key === 'd') {
        e.preventDefault()
        void handleBookmarkLast()
      }

      if (mod && e.key === '.') {
        e.preventDefault()
        toggleArtifactsDrawer()
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [
    toggleLeftSidebar,
    toggleRightRail,
    toggleArtifactsDrawer,
    focusComposer,
    handleNewSession,
    handleBookmarkLast,
    navigateSession,
  ])
}
