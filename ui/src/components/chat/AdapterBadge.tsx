import { Terminal, Globe } from 'lucide-react'

const PTY_CONFIG = { label: 'PTY', icon: Terminal, bg: 'bg-violet-500/10', text: 'text-violet-400', border: 'border-violet-500/20' }
const API_CONFIG = { label: 'API', icon: Globe, bg: 'bg-success/10', text: 'text-success', border: 'border-success/20' }

function getConfig(provider: string) {
  if (provider.startsWith('pty')) return PTY_CONFIG
  return API_CONFIG
}

interface AdapterBadgeProps {
  provider: string
  size?: 'sm' | 'md'
}

export function AdapterBadge({ provider, size = 'sm' }: AdapterBadgeProps) {
  if (!provider) return null

  const config = getConfig(provider)
  const Icon = config.icon
  const isPTY = provider.startsWith('pty')

  if (size === 'sm') {
    return (
      <span
        className={`inline-flex items-center gap-0.5 px-1 py-0 rounded text-[10px] font-medium ${config.bg} ${config.text} border ${config.border} leading-relaxed`}
        title={isPTY ? 'CLI (PTY) session' : 'API session'}
      >
        <Icon className="w-2.5 h-2.5" />
        {config.label}
      </span>
    )
  }

  return (
    <span
      className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-xs font-medium ${config.bg} ${config.text} border ${config.border}`}
      title={isPTY ? 'CLI (PTY) session' : 'API session'}
    >
      <Icon className="w-3 h-3" />
      {config.label}
    </span>
  )
}
