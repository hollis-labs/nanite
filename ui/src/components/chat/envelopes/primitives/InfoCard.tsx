import { Info, CheckCircle, AlertTriangle, XCircle, type LucideIcon } from 'lucide-react'
import { Envelope, EnvelopeHeader, EnvelopeBody } from './Envelope'

type InfoVariant = 'info' | 'success' | 'warning' | 'danger'

interface InfoCardData {
  title: string
  body: string
  variant?: InfoVariant
}

interface InfoCardProps {
  data: InfoCardData
}

const VARIANT_CONFIG: Record<InfoVariant, { icon: LucideIcon; label: string }> = {
  info:    { icon: Info,           label: 'Info'    },
  success: { icon: CheckCircle,    label: 'Success' },
  warning: { icon: AlertTriangle,  label: 'Notice'  },
  danger:  { icon: XCircle,        label: 'Alert'   },
}

export function InfoCard({ data }: InfoCardProps) {
  const variant = data.variant || 'info'
  const { icon, label } = VARIANT_CONFIG[variant] ?? VARIANT_CONFIG.info

  return (
    <Envelope accent={variant}>
      <EnvelopeHeader icon={icon} label={label} tone={variant} />
      <EnvelopeBody title={data.title} description={data.body} />
    </Envelope>
  )
}
