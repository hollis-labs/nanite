/**
 * ElicitationPromptCard — renders an MCP elicitation/create prompt inline in chat.
 *
 * Two widget modes (v1 scope, CW-20260420-0018 D3):
 *   - boolean: accept/decline buttons
 *   - string:  text input + submit / decline buttons
 *
 * On user action the card submits a ResponseV1 via onRespond:
 *   { action: "accept" | "decline" | "cancel", content?, elicitation_id }
 *
 * The elicitation_id is echoed back so the backend Service.Respond() can
 * unblock the in-flight tool call.
 */
import { HelpCircle, CheckCircle2, XCircle } from 'lucide-react'
import { useState, useRef } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ResponseStatus } from '@/lib/envelope-response'
import type { Envelope as EnvelopeType } from '@/lib/types'
import {
  Envelope,
  EnvelopeBody,
  EnvelopeFooter,
  EnvelopeHeader,
} from './primitives/Envelope'
import type { EnvelopeResponder } from './EnvelopeRenderer'

// ---------------------------------------------------------------------------
// Data shape mirrors ElicitationPrompt envelope schema (Go: elicitation.EnvelopeData)
// ---------------------------------------------------------------------------

export interface ElicitationPromptData {
  elicitation_id: string
  message: string
  schema_type: 'boolean' | 'string'
  schema_title?: string
  schema_description?: string
  tool_call_id?: string
  origin: 'server' | 'client'
  timeout_at: string // ISO 8601
}

interface ElicitationPromptCardProps {
  envelope: EnvelopeType
  onRespond?: EnvelopeResponder
}

type ElicitationState = 'pending' | 'accepted' | 'declined' | 'canceled'

/**
 * Hydrate the card state from a persisted response so it keeps its resolved
 * state after a page reload. `respond()` echoes the chosen `action` into
 * `data.action` ('accept' | 'decline' | 'cancel'). CW-20260517-0006.
 */
function hydrateState(prior: EnvelopeType['prior_response']): ElicitationState {
  if (!prior) return 'pending'
  const action = prior.data?.action
  if (action === 'decline') return 'declined'
  if (action === 'cancel') return 'canceled'
  return 'accepted'
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function ElicitationPromptCard({ envelope, onRespond }: ElicitationPromptCardProps) {
  const data = (envelope.data ?? {}) as unknown as ElicitationPromptData
  const prior = envelope.prior_response
  const [state, setState] = useState<ElicitationState>(() => hydrateState(prior))
  const [inputValue, setInputValue] = useState<string>(() =>
    typeof prior?.data?.content === 'string' ? prior.data.content : '',
  )
  const [submitError, setSubmitError] = useState<string | null>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  const isBoolean = data.schema_type === 'boolean'

  const respond = async (action: 'accept' | 'decline' | 'cancel', content?: string) => {
    setSubmitError(null)
    setState(action === 'accept' ? 'accepted' : action === 'decline' ? 'declined' : 'canceled')
    if (!onRespond) return
    try {
      await onRespond({
        status: ResponseStatus.Submitted,
        data: {
          action,
          elicitation_id: data.elicitation_id,
          ...(content !== undefined ? { content } : {}),
        },
      })
    } catch (err) {
      // Revert state on failure so the user can retry.
      setState('pending')
      setSubmitError(err instanceof Error ? err.message : 'Failed to submit response')
    }
  }

  const handleAccept = () => {
    if (isBoolean) {
      void respond('accept')
    } else {
      const val = inputValue.trim()
      if (!val) {
        setSubmitError('Please enter a response before submitting.')
        inputRef.current?.focus()
        return
      }
      void respond('accept', val)
    }
  }

  const handleDecline = () => void respond('decline')

  // Terminal states — show a compact settled view.
  if (state === 'accepted') {
    return (
      <Envelope accent="success" muted>
        <EnvelopeBody>
          <div className="flex items-center gap-2">
            <CheckCircle2 className="h-4 w-4 text-success shrink-0" />
            <span className="text-[13px] text-fg">Response submitted</span>
            {!isBoolean && inputValue && (
              <span className="text-[13px] text-fg-secondary">— {inputValue}</span>
            )}
          </div>
        </EnvelopeBody>
      </Envelope>
    )
  }

  if (state === 'declined' || state === 'canceled') {
    return (
      <Envelope accent="neutral" muted>
        <EnvelopeBody>
          <div className="flex items-center gap-2">
            <XCircle className="h-4 w-4 text-fg-muted shrink-0" />
            <span className="text-[13px] text-fg-secondary">
              {state === 'declined' ? 'Declined' : 'Canceled'} — {data.message}
            </span>
          </div>
        </EnvelopeBody>
      </Envelope>
    )
  }

  // Active state — render input widget.
  const originLabel = data.origin === 'client' ? 'external tool' : 'tool'

  return (
    <Envelope accent="info">
      <EnvelopeHeader
        icon={HelpCircle}
        label={`${originLabel} is asking`}
        tone="info"
      />

      <EnvelopeBody
        title={data.schema_title ?? data.message}
        description={data.schema_title ? data.message : data.schema_description}
      />

      {/* String input widget — only shown for string schema. */}
      {!isBoolean && (
        <div className="px-4 pb-2">
          <Input
            ref={inputRef}
            placeholder={data.schema_description ?? 'Enter your response…'}
            value={inputValue}
            onChange={(e) => setInputValue(e.target.value)}
            onKeyDown={(e) => { if (e.key === 'Enter') handleAccept() }}
            className="text-[13px]"
            autoFocus
          />
        </div>
      )}

      {submitError && (
        <div
          role="alert"
          className="mx-4 mb-3 rounded-md border border-danger/30 bg-danger/5 px-3 py-2 text-[12px] text-danger"
        >
          {submitError}
        </div>
      )}

      <EnvelopeFooter>
        <Button size="sm" onClick={handleAccept}>
          {isBoolean ? 'Accept' : 'Submit'}
        </Button>
        <Button
          size="sm"
          variant="outline"
          className="text-fg-secondary hover:text-fg"
          onClick={handleDecline}
        >
          Decline
        </Button>
      </EnvelopeFooter>
    </Envelope>
  )
}
