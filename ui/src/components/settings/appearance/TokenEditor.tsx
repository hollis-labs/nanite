import { useState } from 'react'
import { TOKEN_META, CATEGORIES } from '@/lib/theme/tokens'
import type { TokenKey, TokenValues } from '@/lib/theme/types'
import { ColorPickerPopover } from './ColorPickerPopover'

interface TokenEditorProps {
  values: TokenValues
  onChange: (key: TokenKey, value: string) => void
  readOnly?: boolean
}

export function TokenEditor({ values, onChange, readOnly }: TokenEditorProps) {
  const [openToken, setOpenToken] = useState<TokenKey | null>(null)

  return (
    <div className="space-y-5">
      {CATEGORIES.map((cat) => {
        const tokens = TOKEN_META.filter((t) => t.category === cat.id)
        if (tokens.length === 0) return null
        return (
          <div key={cat.id}>
            <div className="flex items-baseline gap-2 mb-2">
              <h4 className="text-xs font-semibold text-fg uppercase tracking-wider">{cat.label}</h4>
              {cat.description && (
                <span className="text-[10px] text-fg-muted">{cat.description}</span>
              )}
            </div>
            <div className="grid grid-cols-1 gap-1">
              {tokens.map((meta) => {
                const value = values[meta.key]
                const isOpen = openToken === meta.key
                return (
                  <div
                    key={meta.key}
                    className="relative flex items-center gap-3 px-2 py-1.5 rounded hover:bg-surface/30 group"
                  >
                    {/* Swatch button */}
                    <button
                      type="button"
                      disabled={readOnly}
                      onClick={() => setOpenToken(isOpen ? null : meta.key)}
                      className="w-7 h-7 rounded border border-border-subtle shrink-0 shadow-inner disabled:cursor-not-allowed disabled:opacity-50"
                      style={{ backgroundColor: value }}
                      title={readOnly ? 'Read-only — duplicate the theme to edit' : 'Edit color'}
                    />
                    {/* Label + key */}
                    <div className="flex-1 min-w-0">
                      <div className="flex items-baseline gap-2">
                        <span className="text-xs text-fg font-medium">{meta.label}</span>
                        <code className="text-[10px] text-fg-faint font-mono">--c-{meta.key}</code>
                      </div>
                      {meta.description && (
                        <div className="text-[10px] text-fg-muted leading-tight">{meta.description}</div>
                      )}
                    </div>
                    {/* Value */}
                    <code className="text-[10px] text-fg-muted font-mono shrink-0">{value}</code>

                    {isOpen && !readOnly && (
                      <div className="absolute top-full right-0 z-50">
                        <ColorPickerPopover
                          value={value}
                          allowAlpha={meta.allowsAlpha}
                          onChange={(next) => onChange(meta.key, next)}
                          onClose={() => setOpenToken(null)}
                        />
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          </div>
        )
      })}
    </div>
  )
}
