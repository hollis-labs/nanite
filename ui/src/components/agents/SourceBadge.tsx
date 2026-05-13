import type { StatusTone } from '@/components/chat/envelopes/primitives/StatusPill'
import { StatusPill } from '@/components/chat/envelopes/primitives/StatusPill'

export const SOURCE_LABELS: Record<string, string> = {
  seed: 'system',
  api: 'api',
  agentrc: 'agentrc',
  builtin: 'built-in',
  // CW-20260512-0111: 'internal' provenance marks file-source-of-truth
  // agents whose body is hydrated from internal/agent/builtin/profiles/
  // at every Nanite boot. The UI renders these read-only.
  internal: 'internal',
  project: 'project',
  user: 'user',
  plugin: 'plugin',
  claude: 'claude',
  nanite: 'nanite',
  db: 'custom',
}

const SOURCE_TONE: Record<string, StatusTone> = {
  seed:     'neutral',
  builtin:  'success',
  internal: 'success',
  agentrc:  'info',
  plugin:   'primary',
  project:  'warning',
  user:     'neutral',
  api:      'neutral',
  claude:   'brand',
  nanite:   'info',
  db:       'neutral',
}

interface SourceBadgeProps {
  source: string
  className?: string
}

export function SourceBadge({ source, className = '' }: SourceBadgeProps) {
  if (!source) return null
  const label = SOURCE_LABELS[source] ?? source
  const tone = SOURCE_TONE[source] ?? 'neutral'
  return (
    <StatusPill tone={tone} className={className}>
      {label}
    </StatusPill>
  )
}
