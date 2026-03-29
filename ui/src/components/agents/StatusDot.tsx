interface StatusDotProps {
  status: string
  size?: 'sm' | 'md'
}

const SIZE_CLASSES = {
  sm: 'w-1.5 h-1.5',
  md: 'w-2 h-2',
}

export function StatusDot({ status, size = 'sm' }: StatusDotProps) {
  const color = status === 'disabled' ? 'bg-fg-faint' : 'bg-success'
  return (
    <span
      className={`rounded-full ${color} ${SIZE_CLASSES[size]} inline-block shrink-0`}
      title={status || 'active'}
    />
  )
}
