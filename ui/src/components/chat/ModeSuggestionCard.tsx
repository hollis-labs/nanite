import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Sparkles } from 'lucide-react'
import { useState } from 'react'
import { api } from '@/lib/api'
import type { ModeSuggestion } from '@/lib/types'
import { useChatStore } from '@/stores/useChatStore'
import { Envelope, EnvelopeBody, EnvelopeFooter, EnvelopeHeader } from './envelopes/primitives/Envelope'

/**
 * B3 (CW-20260428-0011) — inline card surfacing classifier mode suggestions.
 *
 * Two variants:
 *   - "firstUse"  → 5-option card. Shown when the global pref is unset, so
 *                   the user picks a behavior (always / ask / never) at the
 *                   same time as deciding whether to switch this turn.
 *   - "compact"   → 2-option strip. Shown when the global pref is "ask".
 *                   Just Switch / Stay.
 *
 * The "always" / "off" effective behaviors do NOT render this card — they
 * are handled by transcript-level effects (auto-apply or discard).
 */

type Variant = 'firstUse' | 'compact'

interface ModeSuggestionCardProps {
  suggestion: ModeSuggestion
  sessionId: string
  variant: Variant
}

export function ModeSuggestionCard({ suggestion, sessionId, variant }: ModeSuggestionCardProps) {
  const queryClient = useQueryClient()
  const clearModeSuggestion = useChatStore((s) => s.clearModeSuggestion)
  const showChatToast = useChatStore((s) => s.showChatToast)
  const [errMsg, setErrMsg] = useState<string | null>(null)

  const setSessionMode = useMutation({
    mutationFn: (slug: string) => api.setSessionMode(sessionId, { slug }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['session-mode', sessionId] })
      void queryClient.invalidateQueries({ queryKey: ['session', sessionId] })
    },
  })

  const setPref = useMutation({
    mutationFn: (pref: '' | 'always' | 'ask' | 'never') => api.setModeAutoSwitchPref(pref),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['settings'] })
    },
  })

  const finish = () => {
    setErrMsg(null)
    clearModeSuggestion()
  }

  const applyOnly = async () => {
    try {
      await setSessionMode.mutateAsync(suggestion.suggested)
      showChatToast(`Switched to ${suggestion.suggested} mode`, 'success')
      finish()
    } catch (err) {
      setErrMsg(err instanceof Error ? err.message : 'Failed to switch mode')
    }
  }

  const applyAndSetPref = async (pref: '' | 'always' | 'ask' | 'never') => {
    try {
      await Promise.all([setSessionMode.mutateAsync(suggestion.suggested), setPref.mutateAsync(pref)])
      showChatToast(`Switched to ${suggestion.suggested} mode`, 'success')
      finish()
    } catch (err) {
      setErrMsg(err instanceof Error ? err.message : 'Failed to apply preference')
    }
  }

  const setPrefOnly = async (pref: '' | 'always' | 'ask' | 'never') => {
    try {
      await setPref.mutateAsync(pref)
      finish()
    } catch (err) {
      setErrMsg(err instanceof Error ? err.message : 'Failed to save preference')
    }
  }

  const dismiss = () => finish()

  const busy = setSessionMode.isPending || setPref.isPending
  const firstSignal = suggestion.signals?.[0]
  const subline = firstSignal
    ? `We detected '${firstSignal}' in your message.`
    : `Confidence ${(suggestion.confidence * 100).toFixed(0)}%.`

  if (variant === 'compact') {
    return (
      <Envelope accent="primary">
        <div className="flex flex-wrap items-center gap-2 px-4 py-2.5 text-[13px] text-fg">
          <Sparkles className="h-3.5 w-3.5 shrink-0 text-primary" />
          <span className="font-mono text-[11px] font-semibold uppercase tracking-wide text-fg-muted">
            Mode suggestion
          </span>
          <span className="text-fg-secondary">
            Switch to <span className="font-medium text-fg">{suggestion.suggested}</span>?
          </span>
          <div className="ml-auto flex items-center gap-1.5">
            <button
              type="button"
              onClick={() => void applyOnly()}
              disabled={busy}
              className="rounded-[6px] bg-primary px-2.5 py-1 font-mono text-[10px] font-semibold uppercase tracking-wide text-primary-foreground transition-colors hover:bg-primary-hover disabled:opacity-50"
            >
              Switch
            </button>
            <button
              type="button"
              onClick={dismiss}
              disabled={busy}
              className="rounded-[6px] border border-border-subtle bg-transparent px-2.5 py-1 font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-secondary transition-colors hover:bg-surface-hover hover:text-fg disabled:opacity-50"
            >
              Stay
            </button>
          </div>
        </div>
        {errMsg && (
          <div role="alert" className="mx-4 mb-3 rounded-[6px] border border-danger/30 bg-danger/5 px-3 py-1.5 text-[12px] text-danger">
            {errMsg}
          </div>
        )}
      </Envelope>
    )
  }

  // firstUse — full 5-option card
  return (
    <Envelope accent="primary">
      <EnvelopeHeader icon={Sparkles} label="Mode suggestion" tone="primary" />
      <EnvelopeBody
        title={`Switch to ${suggestion.suggested} mode?`}
        description={subline}
      />
      {errMsg && (
        <div role="alert" className="mx-4 mb-3 rounded-[6px] border border-danger/30 bg-danger/5 px-3 py-1.5 text-[12px] text-danger">
          {errMsg}
        </div>
      )}
      <EnvelopeFooter className="flex flex-col gap-2 sm:flex-row sm:flex-wrap">
        <button
          type="button"
          onClick={() => void applyOnly()}
          disabled={busy}
          className="rounded-[6px] bg-primary px-3 py-1.5 font-mono text-[11px] font-semibold uppercase tracking-wide text-primary-foreground transition-colors hover:bg-primary-hover disabled:opacity-50"
        >
          Yes, switch
        </button>
        <button
          type="button"
          onClick={dismiss}
          disabled={busy}
          className="rounded-[6px] border border-border-subtle bg-transparent px-3 py-1.5 font-mono text-[11px] font-semibold uppercase tracking-wide text-fg-secondary transition-colors hover:bg-surface-hover hover:text-fg disabled:opacity-50"
        >
          Not now
        </button>
        <button
          type="button"
          onClick={() => void applyAndSetPref('always')}
          disabled={busy}
          className="rounded-[6px] border border-border-subtle bg-surface px-3 py-1.5 font-mono text-[11px] font-semibold uppercase tracking-wide text-fg transition-colors hover:bg-surface-hover disabled:opacity-50"
        >
          Always switch automatically
        </button>
        <button
          type="button"
          onClick={() => void applyAndSetPref('ask')}
          disabled={busy}
          className="rounded-[6px] border border-border-subtle bg-surface px-3 py-1.5 font-mono text-[11px] font-semibold uppercase tracking-wide text-fg transition-colors hover:bg-surface-hover disabled:opacity-50"
        >
          Always ask
        </button>
        <button
          type="button"
          onClick={() => void setPrefOnly('never')}
          disabled={busy}
          className="rounded-[6px] border border-border-subtle bg-transparent px-3 py-1.5 font-mono text-[11px] font-semibold uppercase tracking-wide text-fg-muted transition-colors hover:bg-surface-hover hover:text-fg-secondary disabled:opacity-50"
        >
          Never auto-switch
        </button>
      </EnvelopeFooter>
    </Envelope>
  )
}
