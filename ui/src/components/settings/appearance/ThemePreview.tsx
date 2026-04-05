import { Bot, Check, Folder, Terminal, AlertTriangle, X, Puzzle } from 'lucide-react'

/**
 * Representative preview of the token system. Intentionally uses the exact
 * same semantic classes that the rest of the app uses — so any token change
 * reflects here immediately.
 */
export function ThemePreview() {
  return (
    <div className="rounded-lg border border-border-subtle bg-bg-elevated/40 p-4 space-y-3">
      <div className="text-[10px] text-fg-muted uppercase tracking-wider">Live preview</div>

      {/* Typography scale */}
      <div className="space-y-1">
        <div className="text-sm text-fg">Primary text on background</div>
        <div className="text-sm text-fg-secondary">Secondary text — slightly dimmed</div>
        <div className="text-xs text-fg-muted">Muted text — labels and metadata</div>
        <div className="text-xs text-fg-faint">Faint text — hints and timestamps</div>
      </div>

      {/* Surfaces */}
      <div className="grid grid-cols-3 gap-2">
        <div className="rounded-md bg-bg border border-border-subtle px-3 py-2 text-xs text-fg-secondary">bg</div>
        <div className="rounded-md bg-surface border border-border px-3 py-2 text-xs text-fg">surface</div>
        <div className="rounded-md bg-surface-hover border border-border px-3 py-2 text-xs text-fg">surface-hover</div>
      </div>

      {/* Buttons */}
      <div className="flex flex-wrap items-center gap-2">
        <button className="bg-primary hover:bg-primary-hover text-primary-foreground text-xs px-3 py-1.5 rounded-md font-medium">
          Primary action
        </button>
        <button className="bg-surface hover:bg-surface-hover text-fg text-xs px-3 py-1.5 rounded-md font-medium border border-border-subtle">
          Secondary
        </button>
        <button className="bg-danger hover:bg-danger-hover text-danger-fg text-xs px-3 py-1.5 rounded-md font-medium">
          Delete
        </button>
        <button className="bg-brand hover:bg-brand-hover text-brand-fg text-xs px-3 py-1.5 rounded-md font-medium">
          Brand
        </button>
      </div>

      {/* Status indicators */}
      <div className="flex flex-wrap items-center gap-3 text-xs">
        <span className="flex items-center gap-1.5">
          <span className="w-2 h-2 rounded-full bg-success animate-pulse" />
          <span className="text-success">Streaming</span>
        </span>
        <span className="flex items-center gap-1.5">
          <span className="w-2 h-2 rounded-full bg-info" />
          <span className="text-info">CLI active</span>
        </span>
        <span className="flex items-center gap-1.5">
          <span className="w-2 h-2 rounded-full bg-warning" />
          <span className="text-warning">Pending</span>
        </span>
        <span className="flex items-center gap-1.5">
          <span className="w-2 h-2 rounded-full bg-danger" />
          <span className="text-danger">Failed</span>
        </span>
      </div>

      {/* Sidebar list simulation */}
      <div className="rounded-md border border-border-subtle bg-bg overflow-hidden">
        <div className="px-3 py-2 text-xs text-fg-muted border-b border-border-subtle flex items-center gap-2">
          <Folder className="w-3 h-3" /> Project sidebar
        </div>
        <div className="px-2 py-1 space-y-0.5">
          <div className="px-2 py-1 rounded text-xs text-fg-secondary hover:bg-surface/40 cursor-default flex items-center gap-2">
            <Bot className="w-3 h-3" /> Unselected item
          </div>
          <div className="px-2 py-1 rounded text-xs text-fg bg-surface/60 cursor-default flex items-center gap-2">
            <Check className="w-3 h-3 text-success" /> Selected item
          </div>
          <div className="px-2 py-1 rounded text-xs text-fg bg-selection cursor-default flex items-center gap-2">
            <Terminal className="w-3 h-3" /> Hover (selection)
          </div>
        </div>
      </div>

      {/* Banners */}
      <div className="space-y-2">
        <div className="flex items-center gap-2 rounded-md border border-warning/30 bg-warning/10 px-3 py-2 text-xs text-warning">
          <AlertTriangle className="w-3.5 h-3.5 shrink-0" />
          Warning banner — rate limit or caution
        </div>
        <div className="flex items-center gap-2 rounded-md border border-danger/30 bg-danger/10 px-3 py-2 text-xs text-danger">
          <X className="w-3.5 h-3.5 shrink-0" />
          Error banner — something failed
        </div>
        <div className="flex items-center gap-2 rounded-md border border-primary/30 bg-primary/10 px-3 py-2 text-xs text-primary">
          <Puzzle className="w-3.5 h-3.5 shrink-0" />
          Info banner — primary callout
        </div>
      </div>

      {/* Agent mode badges */}
      <div className="flex flex-wrap gap-2 text-xs">
        <span className="px-2 py-0.5 rounded bg-mode-default/15 text-mode-default">default</span>
        <span className="px-2 py-0.5 rounded bg-mode-architect/15 text-mode-architect">architect</span>
        <span className="px-2 py-0.5 rounded bg-mode-planner/15 text-mode-planner">planner</span>
        <span className="px-2 py-0.5 rounded bg-mode-writer/15 text-mode-writer">writer</span>
      </div>
    </div>
  )
}
