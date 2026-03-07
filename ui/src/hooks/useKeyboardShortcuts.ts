import { useEffect } from 'react'
import { useLayoutStore } from '@/stores/useLayoutStore'

export function useKeyboardShortcuts() {
  const toggleLeftSidebar = useLayoutStore((s) => s.toggleLeftSidebar)
  const toggleRightRail = useLayoutStore((s) => s.toggleRightRail)

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
        console.log('[shortcut] focus composer')
      }

      if (mod && e.key === 'n') {
        e.preventDefault()
        console.log('[shortcut] new chat')
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [toggleLeftSidebar, toggleRightRail])
}
