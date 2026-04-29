import { useState } from 'react'
import { Trash2, ArrowUpRight, ArrowDownLeft, Bell } from 'lucide-react'
import type { Reminder, AgentStateScope } from '@/lib/types'
import { ScopeChip } from './ScopeChip'

/**
 * Single-row renderer for a reminder in the Work panel (D2, CW-20260428-0015).
 * Surfaces the reminder text, scope chip, and the promote/demote/delete
 * actions inline (kebab-style on hover to keep the row compact).
 */
export interface ReminderItemProps {
  reminder: Reminder
  /** Project ID for the active session — required to enable "promote to project". */
  activeProjectId: string | null
  onDelete: (id: string) => void
  /** Promote/demote handlers return a Promise so the row's `busy` flag stays
   *  set until the mutation resolves (no double-submits on slow networks). */
  onPromote: (id: string, projectId: string) => Promise<void>
  onDemote: (id: string, sessionId: string) => Promise<void>
}

function formatTrigger(triggerJSON: string): string {
  try {
    const t = JSON.parse(triggerJSON) as { type?: string; at?: string; n?: number }
    if (t.type === 'time' && t.at) return `at ${t.at}`
    if (t.type === 'turn_count' && typeof t.n === 'number') return `in ${t.n} turn${t.n === 1 ? '' : 's'}`
    return t.type ?? 'unknown trigger'
  } catch {
    return 'unknown trigger'
  }
}

export function ReminderItem({ reminder, activeProjectId, onDelete, onPromote, onDemote }: ReminderItemProps) {
  const [busy, setBusy] = useState(false)

  const canPromote = reminder.scope === 'session' && !!activeProjectId
  const canDemote = reminder.scope === 'project' && !!reminder.session_id

  const promote = async () => {
    if (!activeProjectId) return
    setBusy(true)
    try {
      await onPromote(reminder.id, activeProjectId)
    } finally {
      setBusy(false)
    }
  }
  const demote = async () => {
    if (!reminder.session_id) return
    setBusy(true)
    try {
      await onDemote(reminder.id, reminder.session_id)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="group flex items-start gap-2 px-2 py-1.5 rounded-md border border-border-subtle bg-bg-elevated text-xs">
      <Bell className="w-3 h-3 shrink-0 mt-0.5 text-fg-faint" aria-hidden />
      <div className="flex-1 min-w-0">
        <p className="text-fg leading-snug break-words">{reminder.text}</p>
        <div className="flex items-center gap-2 mt-1">
          <ScopeChip scope={reminder.scope as AgentStateScope} />
          <span className="text-[10px] text-fg-faint">{formatTrigger(reminder.trigger_json)}</span>
        </div>
      </div>
      <div className="flex items-center gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity">
        {canPromote && (
          <button
            type="button"
            onClick={promote}
            disabled={busy}
            className="p-1 rounded text-fg-faint hover:text-primary disabled:opacity-50"
            title="Promote to project"
          >
            <ArrowUpRight className="w-3 h-3" />
          </button>
        )}
        {canDemote && (
          <button
            type="button"
            onClick={demote}
            disabled={busy}
            className="p-1 rounded text-fg-faint hover:text-fg disabled:opacity-50"
            title="Demote to session"
          >
            <ArrowDownLeft className="w-3 h-3" />
          </button>
        )}
        <button
          type="button"
          onClick={() => onDelete(reminder.id)}
          className="p-1 rounded text-fg-faint hover:text-danger"
          title="Delete reminder"
        >
          <Trash2 className="w-3 h-3" />
        </button>
      </div>
    </div>
  )
}
