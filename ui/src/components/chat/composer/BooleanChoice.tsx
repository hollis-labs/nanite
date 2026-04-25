import { Check, X } from 'lucide-react'

interface BooleanChoiceProps {
  value: boolean | null
  onChange: (next: boolean) => void
  yesLabel?: string
  noLabel?: string
}

export function BooleanChoice({
  value,
  onChange,
  yesLabel = 'Yes',
  noLabel = 'No',
}: BooleanChoiceProps) {
  return (
    <div className="flex gap-2">
      <Option val onClick={onChange} selected={value === true} label={yesLabel} accent="success" />
      <Option val={false} onClick={onChange} selected={value === false} label={noLabel} accent="neutral" />
    </div>
  )
}

interface OptionProps {
  val: boolean
  onClick: (v: boolean) => void
  selected: boolean
  label: string
  accent: 'success' | 'neutral'
}

function Option({ val, onClick, selected, label, accent }: OptionProps) {
  const isSuccess = accent === 'success'

  return (
    <button
      type="button"
      onClick={() => onClick(val)}
      className={[
        'flex flex-1 items-center justify-center gap-1.5 rounded-md px-3.5 py-3 text-[13px] font-semibold transition-colors',
        selected
          ? isSuccess
            ? 'border border-success bg-success/10 text-success'
            : 'border border-border bg-surface text-fg'
          : 'border border-border-subtle bg-bg-surface text-fg-secondary hover:border-border',
      ].join(' ')}
    >
      {isSuccess ? <Check className="h-3.5 w-3.5" strokeWidth={2.2} /> : <X className="h-3.5 w-3.5" strokeWidth={2.2} />}
      {label}
    </button>
  )
}
