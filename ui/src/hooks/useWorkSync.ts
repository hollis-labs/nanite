import { useCallback } from 'react'
import { useWorkStore } from '@/stores/useWorkStore'
import { api } from '@/lib/api'

/**
 * Provides the sync action and auto-inject helper.
 * Call `flush()` from the Send Changes button.
 * Call `flushIfDirty()` before sending a chat message to auto-inject.
 */
export function useWorkSync() {
  const dirty = useWorkStore((s) => s.dirty)
  const buildDiff = useWorkStore((s) => s.buildDiff)
  const clearChanges = useWorkStore((s) => s.clearChanges)
  const showToast = useWorkStore((s) => s.showToast)
  const changes = useWorkStore((s) => s.changes)

  const flush = useCallback(async () => {
    if (!dirty) return
    const diff = buildDiff()
    try {
      await api.syncWorkChanges(diff)
      clearChanges()
      showToast('Tasks updated \u2014 agent notified')
    } catch (err) {
      console.error('[useWorkSync] sync failed:', err)
    }
  }, [dirty, buildDiff, clearChanges, showToast])

  const flushIfDirty = useCallback(async () => {
    if (dirty) {
      await flush()
    }
  }, [dirty, flush])

  return {
    dirty,
    changeCount: changes.length,
    flush,
    flushIfDirty,
  }
}
