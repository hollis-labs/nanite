// Theme system types — a Theme is a complete set of token values for
// both dark and light modes. TokenKey covers the user-editable semantic
// tokens (bg, fg, brand, primary, danger, modes, etc.). Non-editable
// --c-* variables in index.css (composer chrome, status-ok/warn/danger
// pair helpers) are intentionally excluded from the editor surface.

export type TokenKey =
  // Surfaces
  | 'bg'
  | 'bg-elevated'
  | 'surface'
  | 'surface-hover'
  | 'surface-active'
  | 'border'
  | 'border-subtle'
  // Text
  | 'fg'
  | 'fg-secondary'
  | 'fg-muted'
  | 'fg-faint'
  // Brand
  | 'brand'
  | 'brand-hover'
  | 'brand-active'
  | 'brand-muted'
  | 'brand-fg'
  // Primary
  | 'primary'
  | 'primary-hover'
  | 'primary-active'
  | 'primary-muted'
  | 'primary-fg'
  // Danger
  | 'danger'
  | 'danger-hover'
  | 'danger-muted'
  | 'danger-fg'
  // Info
  | 'info'
  | 'info-muted'
  // Success
  | 'success'
  | 'success-muted'
  | 'success-fg'
  // Warning
  | 'warning'
  | 'warning-muted'
  // Selection (neutral hover/focus surface)
  | 'selection'
  // Agent modes
  | 'mode-default'
  | 'mode-architect'
  | 'mode-planner'
  | 'mode-writer'
  // Widget status
  | 'status-ok'
  | 'status-warn'
  | 'status-danger'

export type TokenValues = Record<TokenKey, string>

export interface Theme {
  id: string
  name: string
  description?: string
  author?: string
  version?: string
  /** Whether this theme is shipped with the app (read-only, must duplicate to edit) */
  builtin?: boolean
  tokens: {
    dark: TokenValues
    light: TokenValues
  }
}

export interface TokenMeta {
  key: TokenKey
  label: string
  description?: string
  category: 'surfaces' | 'text' | 'brand' | 'primary' | 'danger' | 'info' | 'success' | 'warning' | 'selection' | 'modes' | 'status'
  /** If true, this token typically uses rgba with alpha (muted variants) */
  allowsAlpha?: boolean
}
