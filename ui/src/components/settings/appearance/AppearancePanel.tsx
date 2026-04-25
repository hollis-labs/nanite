import { useEffect, useMemo, useState } from 'react'
import { Check, Copy, Download, Palette, RotateCcw, Save, Trash2, Upload } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
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
    applyTheme(next)
  }

  const handleSave = () => {
    if (isBuiltin) {
      const copy = duplicateTheme(draft, `${draft.name} (custom)`)
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
    if (!window.confirm('Discard current edits and reset to Concrete & Signal?')) return
    const fresh: Theme = { ...NANITE_DEFAULT, tokens: { dark: { ...NANITE_DEFAULT.tokens.dark }, light: { ...NANITE_DEFAULT.tokens.light } } }
    setDraft(fresh)
    applyTheme(fresh)
  }

  return (
    <div className="space-y-4">
      {/* Header */}
      <div>
        <div className="flex items-center gap-2 mb-1">
          <Palette className="w-4 h-4 text-brand" />
          <h3 className="text-sm font-semibold text-fg">Appearance</h3>
        </div>
        <p className="text-xs text-fg-muted">
          Customize colors, typography, and themes. Changes apply immediately.
        </p>
      </div>

      <Tabs defaultValue="themes">
        <TabsList variant="line" className="w-full justify-start border-b border-border">
          <TabsTrigger value="themes" className="gap-1.5 text-xs">
            <Palette className="w-3.5 h-3.5" /> Themes
            <span className="text-[10px] text-fg-faint">({allThemes.length})</span>
          </TabsTrigger>
          <TabsTrigger value="edit" className="gap-1.5 text-xs">
            Edit
            {isDirty && !isBuiltin && (
              <span className="ml-1 text-[10px] text-warning">●</span>
            )}
          </TabsTrigger>
        </TabsList>

        {/* ── Themes Tab ──────────────────────────────────────── */}
        <TabsContent value="themes" className="pt-5 space-y-4">
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
            {allThemes.map((theme) => {
              const isActive = theme.id === activeThemeId
              return (
                <button
                  key={theme.id}
                  type="button"
                  onClick={() => setActiveTheme(theme.id)}
                  className={`text-left rounded-lg border p-3 transition-all ${
                    isActive
                      ? 'border-brand bg-brand-muted'
                      : 'border-border-subtle hover:border-border hover:bg-surface'
                  }`}
                >
                  <div className="flex items-center gap-2 mb-1.5">
                    <span className="text-sm font-medium text-fg leading-tight">{theme.name}</span>
                    {isActive && <Check className="w-3.5 h-3.5 text-brand ml-auto shrink-0" />}
                  </div>
                  {theme.builtin ? (
                    <span className="inline-flex items-center gap-1 rounded-[4px] border border-border-subtle px-1.5 py-0.5 font-mono text-[9px] font-semibold uppercase tracking-wide bg-surface text-fg-secondary">
                      built-in
                    </span>
                  ) : (
                    <span className="inline-flex items-center gap-1 rounded-[4px] border border-border-subtle px-1.5 py-0.5 font-mono text-[9px] font-semibold uppercase tracking-wide bg-surface text-brand">
                      custom
                    </span>
                  )}
                  {theme.description && (
                    <p className="text-[11px] text-fg-muted mt-1.5 line-clamp-2">{theme.description}</p>
                  )}
                  {/* Color swatches */}
                  <div className="flex items-center gap-1 mt-2">
                    {(['brand', 'primary', 'danger', 'success', 'warning', 'info'] as const).map((k) => (
                      <div
                        key={k}
                        className="w-3.5 h-3.5 rounded-sm border border-border-subtle/50"
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
            <p className="text-[11px] text-fg-muted">
              Switch to the <strong className="text-fg">Edit</strong> tab to customize the active theme and save your own copy.
            </p>
          )}

          {/* Import */}
          <div className="flex items-center gap-2 pt-1">
            <label className="inline-flex items-center h-8 px-3 text-xs font-medium rounded-md border border-border-subtle bg-transparent shadow-sm hover:bg-surface hover:text-fg cursor-pointer text-fg-secondary transition-colors">
              <Upload className="w-3.5 h-3.5 mr-1.5" />
              Import JSON
              <input type="file" accept="application/json,.json" onChange={handleImport} className="hidden" />
            </label>
          </div>
        </TabsContent>

        {/* ── Edit Tab ────────────────────────────────────────── */}
        <TabsContent value="edit" className="pt-5 space-y-5">
          {/* Editing context + controls */}
          <div className="flex items-start justify-between gap-4">
            <div>
              <div className="text-xs font-semibold text-fg-secondary uppercase tracking-wider mb-0.5">Editing</div>
              <div className="text-sm text-fg">
                {draft.name}
                {isBuiltin && (
                  <span className="ml-2 text-[10px] text-fg-muted">(save to create a custom copy)</span>
                )}
                {isDirty && !isBuiltin && (
                  <span className="ml-2 text-[10px] text-warning">● unsaved</span>
                )}
              </div>
            </div>
            {/* Mode switcher */}
            <div className="flex bg-surface rounded-md p-0.5 shrink-0">
              <button
                type="button"
                onClick={() => setEditorMode('dark')}
                className={`text-[11px] px-2.5 py-1 rounded transition-colors ${
                  editorMode === 'dark' ? 'bg-bg-elevated text-fg shadow-sm' : 'text-fg-muted hover:text-fg'
                }`}
              >
                Dark
              </button>
              <button
                type="button"
                onClick={() => setEditorMode('light')}
                className={`text-[11px] px-2.5 py-1 rounded transition-colors ${
                  editorMode === 'light' ? 'bg-bg-elevated text-fg shadow-sm' : 'text-fg-muted hover:text-fg'
                }`}
              >
                Light
              </button>
            </div>
          </div>

          {/* Action bar */}
          <div className="flex flex-wrap items-center gap-2">
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
              Export
            </Button>
            {!isBuiltin && (
              <Button variant="destructive" size="sm" onClick={handleDelete}>
                <Trash2 className="w-3.5 h-3.5 mr-1.5" />
                Delete
              </Button>
            )}
            <div className="flex-1" />
            <Button variant="ghost" size="sm" onClick={toggleGlobalTheme} className="text-fg-muted hover:text-fg">
              Preview {resolvedMode === 'dark' ? 'light' : 'dark'}
            </Button>
            <Button variant="ghost" size="sm" onClick={handleResetToDefault} className="text-fg-muted hover:text-fg">
              Reset to default
            </Button>
          </div>

          {/* Editor + Preview */}
          <div className="grid grid-cols-1 lg:grid-cols-[minmax(0,1fr)_360px] gap-6">
            <section>
              <TokenEditor
                values={draft.tokens[editorMode]}
                onChange={handleTokenChange}
                readOnly={isBuiltin}
              />
            </section>
            <section className="lg:sticky lg:top-0 self-start">
              <ThemePreview />
            </section>
          </div>
        </TabsContent>
      </Tabs>
    </div>
  )
}
