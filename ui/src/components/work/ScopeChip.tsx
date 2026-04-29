import type { AgentStateScope } from '@/lib/types'

/**
 * Renders a small inline scope tag (turn / session / project) used by the
 * Work panel, the bottom-drawer Pins tab, and the I1 dev-mode inspector to
 * distinguish project-scoped agent state from session-scoped state at a
 * glance.
 *
 * D2 (CW-20260428-0015). Uses semantic theme tokens only — no raw Tailwind
 * palette colors.
 */
export interface ScopeChipProps {
  scope: AgentStateScope
  /** Optional extra classes (e.g. ml-2 to space it from sibling text). */
  className?: string
}

const SCOPE_LABELS: Record<AgentStateScope, string> = {
  turn: 'Turn',
  session: 'Session',
  project: 'Project',
}

const SCOPE_CLASSES: Record<AgentStateScope, string> = {
  turn: 'bg-bg-elevated text-fg-faint border-border-subtle',
  session: 'bg-info-muted text-fg border-info',
  project: 'bg-primary-muted text-fg border-primary',
}

export function ScopeChip({ scope, className = '' }: ScopeChipProps) {
  const label = SCOPE_LABELS[scope] ?? scope
  const cls = SCOPE_CLASSES[scope] ?? 'bg-bg-elevated text-fg-faint border-border-subtle'
  return (
    <span
      className={`inline-flex items-center px-1.5 py-px rounded-full border text-[9px] uppercase tracking-wide font-medium ${cls} ${className}`.trim()}
    >
      {label}
    </span>
  )
}

/**
 * Filter chip for "All / Session / Project" selection. Turn-scoped state is
 * intentionally excluded from the filter (only persisted scopes are filterable).
 */
export type ScopeFilter = 'all' | 'session' | 'project'

export interface ScopeFilterChipProps {
  filter: ScopeFilter
  onChange: (filter: ScopeFilter) => void
}

const FILTER_OPTIONS: ScopeFilter[] = ['all', 'session', 'project']
const FILTER_LABELS: Record<ScopeFilter, string> = {
  all: 'All',
  session: 'Session',
  project: 'Project',
}

export function ScopeFilterChip({ filter, onChange }: ScopeFilterChipProps) {
  return (
    <div className="flex bg-bg-elevated rounded p-0.5 gap-0.5">
      {FILTER_OPTIONS.map((opt) => (
        <button
          key={opt}
          type="button"
          onClick={() => onChange(opt)}
          className={`text-[10px] px-2 py-0.5 rounded transition-colors ${
            filter === opt ? 'bg-surface text-fg' : 'text-fg-faint hover:text-fg-muted'
          }`}
        >
          {FILTER_LABELS[opt]}
        </button>
      ))}
    </div>
  )
}
