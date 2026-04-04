import { useEffect, useRef, useState } from 'react'
import { HexAlphaColorPicker, HexColorPicker } from 'react-colorful'
import { matchSwatch } from '@/lib/theme/tailwind-swatches'
import { TailwindSwatchGrid } from './TailwindSwatchGrid'

interface ColorPickerPopoverProps {
  value: string
  onChange: (next: string) => void
  allowAlpha?: boolean
  onClose: () => void
}

/**
 * Converts rgba(r,g,b,a) to 8-digit hex (#rrggbbaa) that react-colorful understands.
 * Falls back to the original string if it doesn't match.
 */
function rgbaToHex8(input: string): string {
  const m = input.match(/^rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*(?:,\s*([\d.]+))?\s*\)$/i)
  if (!m) return input
  const r = parseInt(m[1], 10)
  const g = parseInt(m[2], 10)
  const b = parseInt(m[3], 10)
  const a = m[4] !== undefined ? Math.round(parseFloat(m[4]) * 255) : 255
  const toHex = (n: number) => n.toString(16).padStart(2, '0')
  return `#${toHex(r)}${toHex(g)}${toHex(b)}${toHex(a)}`
}

/**
 * Converts a hex8 (#rrggbbaa) back to rgba() for storage, so our tokens
 * remain readable in the source.
 */
function hex8ToRgba(hex: string): string {
  const m = hex.match(/^#?([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i)
  if (!m) return hex
  const r = parseInt(m[1], 16)
  const g = parseInt(m[2], 16)
  const b = parseInt(m[3], 16)
  const a = parseInt(m[4], 16) / 255
  return `rgba(${r}, ${g}, ${b}, ${a.toFixed(2)})`
}

export function ColorPickerPopover({ value, onChange, allowAlpha, onClose }: ColorPickerPopoverProps) {
  const ref = useRef<HTMLDivElement>(null)
  const [hexInput, setHexInput] = useState(value)

  useEffect(() => {
    setHexInput(value)
  }, [value])

  // Close on outside click
  useEffect(() => {
    function handler(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        onClose()
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [onClose])

  const isRgba = value.startsWith('rgba(') || value.startsWith('rgb(')
  const pickerValue = isRgba ? rgbaToHex8(value) : value

  const handlePickerChange = (next: string) => {
    // If we started as rgba, round-trip back to rgba for readability
    const out = isRgba || allowAlpha ? hex8ToRgba(next) : next
    onChange(out)
  }

  const handleHexSubmit = () => {
    const trimmed = hexInput.trim()
    // Accept #rgb, #rrggbb, #rrggbbaa, or rgba(...)
    if (/^#[0-9a-f]{3}$|^#[0-9a-f]{6}$|^#[0-9a-f]{8}$/i.test(trimmed)) {
      onChange(trimmed)
    } else if (/^rgba?\(/i.test(trimmed)) {
      onChange(trimmed)
    }
  }

  const matched = matchSwatch(pickerValue.slice(0, 7))

  return (
    <div
      ref={ref}
      className="absolute z-50 mt-1 bg-bg-elevated border border-border-subtle rounded-md shadow-xl p-3 w-[280px]"
    >
      {allowAlpha || isRgba ? (
        <HexAlphaColorPicker color={pickerValue} onChange={handlePickerChange} />
      ) : (
        <HexColorPicker color={pickerValue} onChange={handlePickerChange} />
      )}

      <div className="mt-3 flex items-center gap-2">
        <input
          type="text"
          value={hexInput}
          onChange={(e) => setHexInput(e.target.value)}
          onBlur={handleHexSubmit}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              handleHexSubmit()
            }
          }}
          className="flex-1 bg-surface border border-border-subtle rounded px-2 py-1 text-xs font-mono text-fg outline-none focus:border-primary"
          placeholder="#rrggbb or rgba(...)"
        />
        {matched && (
          <span className="text-[10px] text-fg-muted font-mono shrink-0">
            {matched.family}-{matched.shade}
          </span>
        )}
      </div>

      <div className="mt-3 pt-3 border-t border-border-subtle">
        <div className="text-[10px] text-fg-muted uppercase tracking-wider mb-2">Tailwind swatches</div>
        <TailwindSwatchGrid
          onPick={(hex: string) => {
            // Preserve alpha if the existing value had it
            if (isRgba || allowAlpha) {
              // Convert hex to rgba with existing alpha (default 1.0)
              const m = hex.match(/^#?([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i)
              if (m) {
                const r = parseInt(m[1], 16)
                const g = parseInt(m[2], 16)
                const b = parseInt(m[3], 16)
                // Keep prior alpha if available
                const prior = value.match(/rgba?\([^)]*,\s*([\d.]+)\s*\)$/)
                const a = prior ? parseFloat(prior[1]) : allowAlpha ? 0.15 : 1.0
                onChange(`rgba(${r}, ${g}, ${b}, ${a.toFixed(2)})`)
                return
              }
            }
            onChange(hex)
          }}
        />
      </div>
    </div>
  )
}
