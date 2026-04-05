// Metadata for every theme token. Drives the grouped editor UI.
import type { TokenMeta } from './types'

export const TOKEN_META: TokenMeta[] = [
  // Surfaces
  { key: 'bg', label: 'Background', category: 'surfaces', description: 'Main app background' },
  { key: 'bg-elevated', label: 'Elevated surface', category: 'surfaces', description: 'Cards, panels, modals' },
  { key: 'surface', label: 'Surface', category: 'surfaces', description: 'Buttons, inputs, chips' },
  { key: 'surface-hover', label: 'Surface hover', category: 'surfaces' },
  { key: 'surface-active', label: 'Surface active', category: 'surfaces' },
  { key: 'border', label: 'Border', category: 'surfaces' },
  { key: 'border-subtle', label: 'Border subtle', category: 'surfaces' },

  // Text
  { key: 'fg', label: 'Primary text', category: 'text' },
  { key: 'fg-secondary', label: 'Secondary text', category: 'text' },
  { key: 'fg-muted', label: 'Muted text', category: 'text' },
  { key: 'fg-faint', label: 'Faint text', category: 'text' },

  // Brand
  { key: 'brand', label: 'Brand', category: 'brand', description: 'Rare identity moments (logo, architect mode)' },
  { key: 'brand-hover', label: 'Brand hover', category: 'brand' },
  { key: 'brand-active', label: 'Brand active', category: 'brand' },
  { key: 'brand-muted', label: 'Brand muted', category: 'brand', allowsAlpha: true },
  { key: 'brand-fg', label: 'Brand foreground', category: 'brand', description: 'Text on brand backgrounds' },

  // Primary
  { key: 'primary', label: 'Primary', category: 'primary', description: 'Main highlight — CTAs, links, focus rings, toggles' },
  { key: 'primary-hover', label: 'Primary hover', category: 'primary' },
  { key: 'primary-active', label: 'Primary active', category: 'primary' },
  { key: 'primary-muted', label: 'Primary muted', category: 'primary', allowsAlpha: true },
  { key: 'primary-fg', label: 'Primary foreground', category: 'primary', description: 'Text on primary backgrounds' },

  // Danger
  { key: 'danger', label: 'Danger', category: 'danger', description: 'Destructive actions, errors' },
  { key: 'danger-hover', label: 'Danger hover', category: 'danger' },
  { key: 'danger-muted', label: 'Danger muted', category: 'danger', allowsAlpha: true },
  { key: 'danger-fg', label: 'Danger foreground', category: 'danger' },

  // Info
  { key: 'info', label: 'Info', category: 'info', description: 'Neutral informational state (was "success" blue)' },
  { key: 'info-muted', label: 'Info muted', category: 'info', allowsAlpha: true },

  // Success
  { key: 'success', label: 'Success', category: 'success', description: 'Completion, healthy state' },
  { key: 'success-muted', label: 'Success muted', category: 'success', allowsAlpha: true },
  { key: 'success-fg', label: 'Success foreground', category: 'success' },

  // Warning
  { key: 'warning', label: 'Warning', category: 'warning', description: 'Caution, pending approval, rate limits' },
  { key: 'warning-muted', label: 'Warning muted', category: 'warning', allowsAlpha: true },

  // Selection
  { key: 'selection', label: 'Selection', category: 'selection', description: 'Neutral hover/focus surface on menus and lists', allowsAlpha: true },

  // Modes
  { key: 'mode-default', label: 'Default mode', category: 'modes' },
  { key: 'mode-architect', label: 'Architect mode', category: 'modes' },
  { key: 'mode-planner', label: 'Planner mode', category: 'modes' },
  { key: 'mode-writer', label: 'Writer mode', category: 'modes' },

  // Status
  { key: 'status-ok', label: 'Status OK', category: 'status' },
  { key: 'status-warn', label: 'Status warn', category: 'status' },
  { key: 'status-danger', label: 'Status danger', category: 'status' },
]

export const CATEGORIES: Array<{ id: TokenMeta['category']; label: string; description?: string }> = [
  { id: 'surfaces', label: 'Surfaces', description: 'Backgrounds, panels, borders' },
  { id: 'text', label: 'Text', description: 'Text hierarchy' },
  { id: 'brand', label: 'Brand', description: 'Identity — use sparingly' },
  { id: 'primary', label: 'Primary', description: 'Main highlight for actions' },
  { id: 'danger', label: 'Danger', description: 'Destructive, errors' },
  { id: 'success', label: 'Success', description: 'Completion, healthy' },
  { id: 'warning', label: 'Warning', description: 'Caution' },
  { id: 'info', label: 'Info', description: 'Informational' },
  { id: 'selection', label: 'Selection', description: 'Neutral hover' },
  { id: 'modes', label: 'Agent modes', description: 'Mode indicators' },
  { id: 'status', label: 'Widget status', description: 'Muted status dots' },
]
