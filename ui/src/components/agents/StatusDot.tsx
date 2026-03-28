interface StatusDotProps {
  status: string
  size?: 'sm' | 'md'
}

const SIZE_CLASSES = {
  sm: 'w-2 h-2',
  md: 'w-3 h-3',
}

export function StatusDot({ status, size = 'sm' }: StatusDotProps) {
  const color = status === 'disabled' ? 'bg-zinc-600' : 'bg-emerald-500'
  return (
    <span
      className={`rounded-full ${color} ${SIZE_CLASSES[size]} inline-block`}
      title={status || 'active'}
    />
  )
}
