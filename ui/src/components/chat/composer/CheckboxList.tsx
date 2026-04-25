import { Check } from 'lucide-react'

export interface CheckboxOption<T extends string> {
  value: T
  label: string
  hint?: string
}

interface CheckboxListProps<T extends string> {
  options: CheckboxOption<T>[]
  value: T[]
  onChange: (next: T[]) => void
}

export function CheckboxList<T extends string>({
  options,
  value,
  onChange,
}: CheckboxListProps<T>) {
  const toggle = (nextValue: T) => {
    onChange(value.includes(nextValue) ? value.filter((item) => item !== nextValue) : [...value, nextValue])
  }

  return (
    <div className="flex flex-col gap-1">
      {options.map((opt) => {
        const checked = value.includes(opt.value)
        return (
          <button
            key={opt.value}
            type="button"
            onClick={() => toggle(opt.value)}
            className={[
              'flex items-center gap-2.5 rounded-md px-2.5 py-2 text-left transition-colors',
              checked ? 'border border-primary bg-primary/10' : 'border border-transparent hover:bg-surface',
            ].join(' ')}
          >
            <span
              className={[
                'flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded-sm border-[1.5px]',
                checked ? 'border-primary bg-primary text-primary-foreground' : 'border-border bg-transparent',
              ].join(' ')}
            >
              {checked && <Check className="h-2.5 w-2.5" strokeWidth={3} />}
            </span>
            <span className={['flex-1 text-[13px] text-fg', checked ? 'font-medium' : 'font-normal'].join(' ')}>
              {opt.label}
            </span>
            {opt.hint && <span className="font-mono text-[11px] text-fg-muted">{opt.hint}</span>}
          </button>
        )
      })}
    </div>
  )
}
