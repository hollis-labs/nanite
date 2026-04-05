import { TW_FAMILIES, TW_SHADES, getSwatch } from '@/lib/theme/tailwind-swatches'

interface TailwindSwatchGridProps {
  onPick: (hex: string) => void
}

export function TailwindSwatchGrid({ onPick }: TailwindSwatchGridProps) {
  return (
    <div className="space-y-0.5 max-h-[220px] overflow-y-auto chat-scroll pr-1">
      {TW_FAMILIES.map((family) => (
        <div key={family} className="flex items-center gap-1">
          <span className="w-14 text-[9px] text-fg-muted font-mono shrink-0">{family}</span>
          <div className="flex gap-0.5 flex-1">
            {TW_SHADES.map((shade) => {
              const hex = getSwatch(family, shade)
              if (!hex) return null
              return (
                <button
                  key={shade}
                  type="button"
                  onClick={() => onPick(hex)}
                  className="flex-1 h-4 rounded-sm border border-border-subtle/30 hover:border-fg/50 hover:scale-110 transition-all cursor-pointer"
                  style={{ backgroundColor: hex }}
                  title={`${family}-${shade} · ${hex}`}
                />
              )
            })}
          </div>
        </div>
      ))}
    </div>
  )
}
