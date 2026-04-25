import { AlertTriangle, RefreshCw } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Envelope, EnvelopeBody, EnvelopeFooter, EnvelopeHeader } from './primitives/Envelope'
import { StatusPill } from './primitives/StatusPill'

interface PluginLoadErrorCardProps {
  pluginId?: string
  reason: string
  onRetry?: () => void
  title?: string
  retryLabel?: string
}

export function PluginLoadErrorCard({
  pluginId,
  reason,
  onRetry,
  title,
  retryLabel = 'Retry',
}: PluginLoadErrorCardProps) {
  const resolvedTitle = title ?? (pluginId ? `Plugin failed to load: ${pluginId}` : 'Envelope failed to render')

  return (
    <Envelope accent="danger">
      <EnvelopeHeader
        icon={AlertTriangle}
        label="Load error"
        tone="danger"
        action={<StatusPill tone="danger">Unavailable</StatusPill>}
      />
      <EnvelopeBody title={resolvedTitle}>
        <p className="text-[13px] leading-relaxed text-fg-secondary">{reason}</p>
      </EnvelopeBody>
      {onRetry && (
        <EnvelopeFooter>
          <Button size="sm" variant="outline" onClick={onRetry}>
            <RefreshCw className="h-3 w-3" />
            {retryLabel}
          </Button>
        </EnvelopeFooter>
      )}
    </Envelope>
  )
}
