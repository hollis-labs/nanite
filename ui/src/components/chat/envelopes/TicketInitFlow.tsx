import { useState } from 'react'
import { MessageSquare, ArrowRight, Loader2, CheckCircle, AlertCircle } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { buildTicketDataMarker, buildTicketMessage } from './ticket-utils'
import { Envelope, EnvelopeBody, EnvelopeFooter, EnvelopeHeader, EnvelopeSection } from './primitives/Envelope'
import { StatusPill } from './primitives/StatusPill'

interface TicketInitFlowProps {
  onSendMessage?: (content: string) => void
  query?: string       // original user query for prefilling
  kbCategory?: string  // category from KB search results (most relevant article)
}

const CATEGORIES = [
  'network', 'access', 'vpn', 'jira', 'confluence', 'sso',
  'aws', 'tableau', 'snowflake', 'email', 'software', 'hardware', 'general',
]

// Map KB categories (which may be verbose like "VPN / GlobalProtect") to our short form
const KB_CATEGORY_MAP: Record<string, string> = {
  'vpn / globalprotect': 'vpn',
  'active directory': 'access',
  'sso / authentication': 'sso',
  'aws / cloud': 'aws',
  'networking': 'network',
  'email': 'email',
  'general it': 'general',
}

function guessCategory(text: string, kbCategory?: string): string {
  // First: use KB category if available (most accurate)
  if (kbCategory) {
    const mapped = KB_CATEGORY_MAP[kbCategory.toLowerCase()]
    if (mapped) return mapped
    // Try matching the KB category directly against our list
    const lower = kbCategory.toLowerCase()
    const direct = CATEGORIES.find(c => lower.includes(c))
    if (direct) return direct
  }

  // Second: keyword matching on the user's text
  const lower = text.toLowerCase()
  const patterns: [RegExp, string][] = [
    [/vpn|globalprotect|tunnel/i, 'vpn'],
    [/jira|atlassian.*jira/i, 'jira'],
    [/confluence|wiki/i, 'confluence'],
    [/aws|amazon|ec2|s3|rds|cloudwatch|iam.*role/i, 'aws'],
    [/snowflake|warehouse|dbt/i, 'snowflake'],
    [/tableau|dashboard|workbook/i, 'tableau'],
    [/wifi|network|ethernet|dns|firewall|printer/i, 'network'],
    [/password|locked.*out|account.*disabled|mfa|2fa|active.*directory|ad\s/i, 'access'],
    [/sso|login|sign.in|okta|saml|authentication/i, 'sso'],
    [/email|mailbox|distribution.*list|outlook|exchange/i, 'email'],
    [/install|software|license|app/i, 'software'],
    [/monitor|dock|laptop|keyboard|mouse|hardware/i, 'hardware'],
  ]
  for (const [pattern, cat] of patterns) {
    if (pattern.test(lower)) return cat
  }
  return 'general'
}

type FlowStep = 'describe' | 'form' | 'submitting' | 'done' | 'error'

export function TicketInitFlow({ onSendMessage, query, kbCategory }: TicketInitFlowProps) {
  const [step, setStep] = useState<FlowStep>('describe')
  const [description, setDescription] = useState('')

  // Form fields
  const [title, setTitle] = useState('')
  const [category, setCategory] = useState('')
  const [priority, setPriority] = useState('medium')
  const [fullDescription, setFullDescription] = useState('')
  const [stepsTried, setStepsTried] = useState('')

  // Result
  const [ticketId, setTicketId] = useState('')
  const [errorMsg, setErrorMsg] = useState('')

  const handleDescribe = () => {
    const desc = description.trim()
    if (!desc) return
    setTitle(desc.length > 80 ? desc.slice(0, 80) + '...' : desc)
    setFullDescription(query ? `Original issue: ${query}\n\nAdditional details: ${desc}` : desc)
    // Guess category from description + KB context
    setCategory(guessCategory(desc + ' ' + (query || ''), kbCategory))
    setStep('form')
  }

  const handleSubmit = async () => {
    if (!title.trim() || !category || !fullDescription.trim()) return
    setStep('submitting')
    setErrorMsg('')

    try {
      const res = await fetch('/api/plugins/tickets', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title: title.trim(),
          category,
          priority,
          description: fullDescription.trim(),
          steps_tried: stepsTried.trim() || undefined,
        }),
      })
      if (!res.ok) {
        const body = await res.text()
        throw new Error(body || `HTTP ${res.status}`)
      }
      const result = await res.json() as Record<string, unknown>
      const tid = (result['id'] || result['ticket_id'] || '') as string
      const rt = (result['routing'] || 'IT Service Desk — Triage') as string
      setTicketId(tid)
      setStep('done')

      if (onSendMessage) {
        const msg = buildTicketMessage(tid, title.trim(), category, priority, rt)
        const marker = buildTicketDataMarker({
          id: tid, title: title.trim(), description: fullDescription.trim(),
          category, priority, routing: rt,
        })
        onSendMessage(msg + marker)
      }
    } catch (err) {
      setStep('error')
      setErrorMsg(err instanceof Error ? err.message : 'Failed to create ticket')
    }
  }

  const inputCls = 'w-full rounded-[6px] border border-border-subtle bg-surface px-2.5 py-1.5 text-sm text-fg outline-none transition-colors focus:border-primary placeholder:text-fg-faint'

  // Step 1: Brief description
  if (step === 'describe') {
    return (
      <Envelope>
        <EnvelopeHeader
          icon={MessageSquare}
          label="Support ticket"
          action={<StatusPill tone="info">Draft</StatusPill>}
        />
        <EnvelopeBody
          title="Open a support ticket"
          description="Briefly describe your issue and we'll prepare a ticket for you to review."
        >
          <textarea
            className={`${inputCls} min-h-[60px] resize-y`}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="What's the issue you're experiencing?"
            rows={2}
            autoFocus
          />
        </EnvelopeBody>
        <EnvelopeFooter>
          <Button
            size="sm"
            className="bg-primary hover:bg-primary-hover text-white"
            onClick={handleDescribe}
            disabled={!description.trim()}
          >
            <ArrowRight className="h-3 w-3" />
            Prepare ticket
          </Button>
        </EnvelopeFooter>
      </Envelope>
    )
  }

  // Step 2: Review & edit prefilled form
  if (step === 'form' || step === 'error') {
    return (
      <Envelope>
        <EnvelopeHeader
          icon={MessageSquare}
          label="Support ticket"
          action={<StatusPill tone={step === 'error' ? 'danger' : 'warning'}>{step === 'error' ? 'Retry required' : 'Review'}</StatusPill>}
        />

        <EnvelopeBody title="Review and submit ticket">
          <div className="space-y-4">
            <EnvelopeSection label="Issue summary">
              <input type="text" className={inputCls} value={title} onChange={(e) => setTitle(e.target.value)} />
            </EnvelopeSection>

            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <EnvelopeSection label="Category">
                <select className={inputCls} value={category} onChange={(e) => setCategory(e.target.value)}>
                  <option value="">Select...</option>
                  {CATEGORIES.map((cat) => (
                    <option key={cat} value={cat}>{cat.charAt(0).toUpperCase() + cat.slice(1)}</option>
                  ))}
                </select>
              </EnvelopeSection>

              <EnvelopeSection label="Priority">
                <div className="flex flex-wrap gap-3 pt-1">
                  {(['low', 'medium', 'high'] as const).map((p) => (
                    <label key={p} className="flex cursor-pointer items-center gap-1.5">
                      <input type="radio" name="ticket-priority" value={p} checked={priority === p}
                        onChange={() => setPriority(p)} className="accent-accent" />
                      <span className="text-xs capitalize text-fg-secondary">{p}</span>
                    </label>
                  ))}
                </div>
              </EnvelopeSection>
            </div>

            <EnvelopeSection label="Description">
              <textarea className={`${inputCls} min-h-[80px] resize-y`} value={fullDescription}
                onChange={(e) => setFullDescription(e.target.value)} rows={3} />
            </EnvelopeSection>

            <EnvelopeSection label="Steps already tried">
              <textarea className={`${inputCls} min-h-[50px] resize-y`} value={stepsTried}
                onChange={(e) => setStepsTried(e.target.value)} rows={2}
                placeholder="What have you already tried?" />
            </EnvelopeSection>

            {step === 'error' && (
              <div className="flex items-center gap-2 rounded-[6px] border border-danger/30 bg-danger/5 px-3 py-2">
                <AlertCircle className="h-4 w-4 shrink-0 text-danger" />
                <span className="text-xs text-danger">{errorMsg}</span>
              </div>
            )}
          </div>
        </EnvelopeBody>

        <EnvelopeFooter>
          <Button
            size="sm"
            className="bg-primary hover:bg-primary-hover text-white"
            onClick={() => void handleSubmit()}
            disabled={!title.trim() || !category || !fullDescription.trim()}
          >
            Submit ticket
          </Button>
        </EnvelopeFooter>
      </Envelope>
    )
  }

  // Step 2.5: Submitting
  if (step === 'submitting') {
    return (
      <Envelope accent="primary">
        <EnvelopeHeader
          icon={Loader2}
          label="Support ticket"
          tone="primary"
          action={<StatusPill tone="info">Submitting</StatusPill>}
        />
        <EnvelopeBody>
          <div className="flex items-center gap-2">
            <Loader2 className="h-4 w-4 animate-spin text-primary" />
            <span className="text-sm text-fg">Submitting ticket...</span>
          </div>
        </EnvelopeBody>
      </Envelope>
    )
  }

  // Step 3: Simple confirmation (the rich card is injected by the system after the agent responds)
  return (
    <Envelope accent="success" muted>
      <EnvelopeHeader
        icon={CheckCircle}
        label="Support ticket"
        tone="success"
        action={<StatusPill tone="success">Submitted</StatusPill>}
      />
      <EnvelopeBody>
        <div className="flex items-center gap-2">
          <CheckCircle className="h-4 w-4 text-success" />
          <span className="text-sm text-fg">
            Ticket submitted{ticketId ? ` — ${ticketId}` : ''}
          </span>
        </div>
      </EnvelopeBody>
    </Envelope>
  )
}
