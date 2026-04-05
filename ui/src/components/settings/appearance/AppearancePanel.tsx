import { useEffect, useMemo, useState } from 'react'
import { Check, Copy, Download, Palette, RotateCcw, Save, Trash2, Upload } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { useTheme } from '@/hooks/useTheme'
import { applyTheme } from '@/lib/theme/apply'
import type { Theme, TokenKey } from '@/lib/theme/types'
import { NANITE_DEFAULT } from '@/lib/theme/defaults'
import { TokenEditor } from './TokenEditor'
import { ThemePreview } from './ThemePreview'
import { useLayoutStore } from '@/stores/useLayoutStore'

type Mode = 'dark' | 'light'

export function AppearancePanel() {
  const {
    activeTheme,
    activeThemeId,
    allThemes,
    customThemes,
    setActiveTheme,
    saveCustomTheme,
    deleteCustomTheme,
    duplicateTheme,
  } = useTheme()
  const currentMode = useLayoutStore((s) => s.theme)
  const toggleGlobalTheme = useLayoutStore((s) => s.toggleTheme)

  const resolvedMode: Mode = useMemo(() => {
    if (currentMode === 'system') {
      return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
    }
    return currentMode
  }, [currentMode])

  // Draft state — what the editor is currently showing.
  // Initialized from the active theme; reset when the active theme changes.
  const [draft, setDraft] = useState<Theme>(activeTheme)
  const [editorMode, setEditorMode] = useState<Mode>(resolvedMode)

  useEffect(() => {
    setDraft(activeTheme)
  }, [activeTheme])

  useEffect(() => {
    setEditorMode(resolvedMode)
  }, [resolvedMode])

  const isDirty = useMemo(() => JSON.stringify(draft) !== JSON.stringify(activeTheme), [draft, activeTheme])
  const isBuiltin = !!draft.builtin

  const handleTokenChange = (key: TokenKey, value: string) => {
    const next: Theme = {
      ...draft,
      tokens: {
        ...draft.tokens,
        [editorMode]: {
          ...draft.tokens[editorMode],
          [key]: value,
        },
      },
    }
    setDraft(next)
    // Live-apply the draft so the user sees changes instantly
    applyTheme(next)
  }

  const handleSave = () => {
    if (isBuiltin) {
      // Duplicate first — can't edit built-ins
      const copy = duplicateTheme(draft, `${draft.name} (custom)`)
      // Copy user edits across both modes from the current draft
      copy.tokens = {
        dark: { ...draft.tokens.dark },
        light: { ...draft.tokens.light },
      }
      saveCustomTheme(copy)
      setDraft(copy)
    } else {
      saveCustomTheme(draft)
    }
  }

  const handleReset = () => {
    setDraft(activeTheme)
    applyTheme(activeTheme)
  }

  const handleDuplicate = () => {
    const copy = duplicateTheme(draft, `${draft.name} (copy)`)
    saveCustomTheme(copy)
    setDraft(copy)
  }

  const handleDelete = () => {
    if (isBuiltin) return
    if (!window.confirm(`Delete theme "${draft.name}"?`)) return
    deleteCustomTheme(draft.id)
  }

  const handleExport = () => {
    const blob = new Blob([JSON.stringify(draft, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${draft.id}.theme.json`
    a.click()
    URL.revokeObjectURL(url)
  }

  const handleImport = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => {
      try {
        const parsed = JSON.parse(reader.result as string) as Theme
        if (!parsed.id || !parsed.name || !parsed.tokens?.dark || !parsed.tokens?.light) {
          alert('Invalid theme file — missing required fields.')
          return
        }
        const imported: Theme = { ...parsed, id: `custom-${Date.now()}`, builtin: false }
        saveCustomTheme(imported)
        setDraft(imported)
      } catch (err) {
        alert(`Failed to parse theme: ${(err as Error).message}`)
      }
    }
    reader.readAsText(file)
    e.target.value = ''
  }

  const handleResetToDefault = () => {
    if (!window.confirm('Discard current edits and reset to Nanite Default?')) return
    const fresh: Theme = { ...NANITE_DEFAULT, tokens: { dark: { ...NANITE_DEFAULT.tokens.dark }, light: { ...NANITE_DEFAULT.tokens.light } } }
    setDraft(fresh)
    applyTheme(fresh)
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <div className="flex items-center gap-2 mb-1">
          <Palette className="w-4 h-4 text-primary" />
          <h3 className="text-sm font-semibold text-fg">Appearance</h3>
        </div>
        <p className="text-xs text-fg-muted">
          Customize colors, typography, and themes. Changes apply immediately and persist to your account.
        </p>
      </div>

      {/* Theme picker */}
      <section>
        <div className="text-xs font-semibold text-fg uppercase tracking-wider mb-2">Themes</div>
        <div className="grid grid-cols-2 gap-2">
          {allThemes.map((theme) => {
            const isActive = theme.id === activeThemeId
            return (
              <button
                key={theme.id}
                type="button"
                onClick={() => setActiveTheme(theme.id)}
                className={`text-left rounded-md border p-3 transition-colors ${
                  isActive
                    ? 'border-primary bg-primary/5'
                    : 'border-border-subtle hover:border-border hover:bg-surface/30'
                }`}
              >
                <div className="flex items-center gap-2 mb-1">
                  <span className="text-sm font-medium text-fg">{theme.name}</span>
                  {theme.builtin && (
                    <span className="text-[9px] px-1.5 py-0.5 rounded bg-surface text-fg-muted uppercase tracking-wider">Built-in</span>
                  )}
                  {isActive && <Check className="w-3.5 h-3.5 text-primary ml-auto" />}
                </div>
                {theme.description && <p className="text-[11px] text-fg-muted">{theme.description}</p>}
                {/* Color chips preview */}
                <div className="flex items-center gap-1 mt-2">
                  {(['brand', 'primary', 'danger', 'success', 'warning', 'info'] as const).map((k) => (
                    <div
                      key={k}
                      className="w-4 h-4 rounded border border-border-subtle/50"
                      style={{ backgroundColor: theme.tokens.dark[k] }}
                      title={k}
                    />
                  ))}
                </div>
              </button>
            )
          })}
        </div>
        {customThemes.length === 0 && (
          <p className="text-[11px] text-fg-muted mt-2">
            Customize the active theme below, then click <strong>Save</strong> to create your own.
          </p>
        )}
      </section>

      {/* Editor controls */}
      <section>
        <div className="flex items-center justify-between mb-3">
          <div>
            <div className="text-xs font-semibold text-fg uppercase tracking-wider">Editing</div>
            <div className="text-sm text-fg mt-0.5">
              {draft.name}
              {isBuiltin && (
                <span className="ml-2 text-[10px] text-fg-muted">(save to create a custom copy)</span>
              )}
              {isDirty && !isBuiltin && (
                <span className="ml-2 text-[10px] text-warning">● unsaved</span>
              )}
            </div>
          </div>
          <div className="flex items-center gap-1">
            {/* Mode switcher — controls which set we're editing */}
            <div className="flex bg-surface/50 rounded-md p-0.5 mr-2">
              <button
                type="button"
                onClick={() => setEditorMode('dark')}
                className={`text-[11px] px-2 py-0.5 rounded ${
                  editorMode === 'dark' ? 'bg-bg-elevated text-fg shadow-sm' : 'text-fg-muted'
                }`}
              >
                Dark
              </button>
              <button
                type="button"
                onClick={() => setEditorMode('light')}
                className={`text-[11px] px-2 py-0.5 rounded ${
                  editorMode === 'light' ? 'bg-bg-elevated text-fg shadow-sm' : 'text-fg-muted'
                }`}
              >
                Light
              </button>
            </div>
            <Button variant="ghost" size="sm" onClick={toggleGlobalTheme} title="Toggle app dark/light">
              Preview {resolvedMode === 'dark' ? 'light' : 'dark'}
            </Button>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-2 mb-4">
          <Button variant="default" size="sm" onClick={handleSave} disabled={!isDirty && !isBuiltin}>
            <Save className="w-3.5 h-3.5 mr-1.5" />
            {isBuiltin ? 'Save as copy' : 'Save'}
          </Button>
          <Button variant="outline" size="sm" onClick={handleReset} disabled={!isDirty}>
            <RotateCcw className="w-3.5 h-3.5 mr-1.5" />
            Revert
          </Button>
          <Button variant="outline" size="sm" onClick={handleDuplicate}>
            <Copy className="w-3.5 h-3.5 mr-1.5" />
            Duplicate
          </Button>
          <Button variant="outline" size="sm" onClick={handleExport}>
            <Download className="w-3.5 h-3.5 mr-1.5" />
            Export JSON
          </Button>
          <label className="inline-flex items-center h-8 px-3 text-xs font-medium rounded-md border border-border-subtle bg-transparent shadow-sm hover:bg-surface hover:text-fg cursor-pointer">
            <Upload className="w-3.5 h-3.5 mr-1.5" />
            Import
            <input type="file" accept="application/json,.json" onChange={handleImport} className="hidden" />
          </label>
          {!isBuiltin && (
            <Button variant="destructive" size="sm" onClick={handleDelete}>
              <Trash2 className="w-3.5 h-3.5 mr-1.5" />
              Delete
            </Button>
          )}
          <div className="flex-1" />
          <Button variant="ghost" size="sm" onClick={handleResetToDefault}>
            Reset to Nanite Default
          </Button>
        </div>
      </section>

      <div className="grid grid-cols-1 lg:grid-cols-[minmax(0,1fr)_380px] gap-6">
        {/* Token editor */}
        <section>
          <TokenEditor
            values={draft.tokens[editorMode]}
            onChange={handleTokenChange}
            readOnly={isBuiltin}
          />
        </section>

        {/* Preview pane (sticky on wide screens) */}
        <section className="lg:sticky lg:top-0 self-start">
          <ThemePreview />
        </section>
      </div>
    </div>
  )
}
