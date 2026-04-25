import { Check } from 'lucide-react'

export interface CardRadioOption<T extends string> {
  value: T
  letter?: string
  label: string
  description?: string
  suggested?: boolean
}

interface CardRadioProps<T extends string> {
  options: CardRadioOption<T>[]
  value: T
  onChange: (next: T) => void
  dense?: boolean
}

export function CardRadio<T extends string>({
  options,
  value,
  onChange,
  dense = false,
}: CardRadioProps<T>) {
  return (
    <div className="flex flex-col gap-1.5">
      {options.map((opt) => {
        const selected = value === opt.value
        return (
          <button
            key={opt.value}
            type="button"
            onClick={() => onChange(opt.value)}
            className={[
              'flex items-start gap-2.5 rounded-md text-left transition-colors',
              dense ? 'px-2.5 py-2' : 'px-3 py-2.5',
              selected
                ? 'border border-primary bg-primary/10'
                : 'border border-border-subtle bg-bg-surface hover:border-border',
            ].join(' ')}
          >
            <span
              className={[
                'flex h-5 w-5 shrink-0 items-center justify-center rounded-sm font-mono text-[11px] font-semibold',
                selected ? 'bg-primary text-primary-foreground' : 'bg-surface text-fg-secondary',
              ].join(' ')}
            >
              {opt.letter ?? '•'}
            </span>
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-1.5 text-[13px] text-fg">
                <span className={selected ? 'font-semibold' : 'font-medium'}>{opt.label}</span>
                {opt.suggested && (
                  <span
                    className={[
                      'font-mono text-[10px] font-semibold uppercase tracking-wide',
                      selected ? 'text-primary' : 'text-info',
                    ].join(' ')}
                  >
                    Suggested
                  </span>
                )}
              </div>
              {opt.description && (
                <div className="mt-0.5 text-xs leading-[1.5] text-fg-secondary">
                  {opt.description}
                </div>
              )}
            </div>
            {selected && (
              <span className="mt-0.5 shrink-0 text-primary">
                <Check className="h-3.5 w-3.5" strokeWidth={2.2} />
              </span>
            )}
          </button>
        )
      })}
    </div>
  )
}
