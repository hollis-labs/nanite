import { Terminal, Globe } from 'lucide-react'

const ADAPTER_CONFIG: Record<string, { label: string; icon: typeof Terminal; bg: string; text: string; border: string }> = {
  pty: { label: 'PTY', icon: Terminal, bg: 'bg-emerald-500/10', text: 'text-emerald-400', border: 'border-emerald-500/20' },
  'pty-claude': { label: 'PTY', icon: Terminal, bg: 'bg-emerald-500/10', text: 'text-emerald-400', border: 'border-emerald-500/20' },
  'pty-codex': { label: 'PTY', icon: Terminal, bg: 'bg-emerald-500/10', text: 'text-emerald-400', border: 'border-emerald-500/20' },
  'pty-gemini': { label: 'PTY', icon: Terminal, bg: 'bg-emerald-500/10', text: 'text-emerald-400', border: 'border-emerald-500/20' },
  anthropic: { label: 'API', icon: Globe, bg: 'bg-blue-500/10', text: 'text-blue-400', border: 'border-blue-500/20' },
  openai: { label: 'API', icon: Globe, bg: 'bg-blue-500/10', text: 'text-blue-400', border: 'border-blue-500/20' },
  ollama: { label: 'API', icon: Globe, bg: 'bg-blue-500/10', text: 'text-blue-400', border: 'border-blue-500/20' },
}

const DEFAULT_CONFIG = { label: 'API', icon: Globe, bg: 'bg-zinc-500/10', text: 'text-fg-secondary', border: 'border-zinc-500/20' }

function isPTYProvider(provider: string): boolean {
  return provider.startsWith('pty')
}

interface AdapterBadgeProps {
  provider: string
  size?: 'sm' | 'md'
}

export function AdapterBadge({ provider, size = 'sm' }: AdapterBadgeProps) {
  if (!provider) return null

  const config = ADAPTER_CONFIG[provider] ?? DEFAULT_CONFIG
  const Icon = config.icon

  if (size === 'sm') {
    return (
      <span
        className={`inline-flex items-center gap-0.5 px-1 py-0 rounded text-[10px] font-medium ${config.bg} ${config.text} border ${config.border} leading-relaxed`}
        title={isPTYProvider(provider) ? 'CLI (PTY) session' : 'API session'}
      >
        <Icon className="w-2.5 h-2.5" />
        {config.label}
      </span>
    )
  }

  return (
    <span
      className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-xs font-medium ${config.bg} ${config.text} border ${config.border}`}
      title={isPTYProvider(provider) ? 'CLI (PTY) session' : 'API session'}
    >
      <Icon className="w-3 h-3" />
      {config.label}
    </span>
  )
}
