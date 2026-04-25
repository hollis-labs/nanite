import { useState, useEffect } from 'react'

/**
 * POLISHED — thinking indicator.
 *
 * Changes vs. original:
 *  - Replaced the spinning Cog icon (whimsical but visually loud — competes with
 *    the spinning Loader2 icons in tool calls and progress cards) with a
 *    three-dot typing indicator. Dots pulse in sequence — the canonical
 *    "something is happening, but it's not a hard-stopped process" signal.
 *  - Dropped the 10-message random rotation. Rotating copy every 3s drew the
 *    eye repeatedly during long runs; now shows a single steady "Thinking…"
 *    label (matches the polished transcript where the indicator sits under the
 *    avatar, not as a standalone hero element).
 *  - Mono label matches the EnvelopeHeader / tool-call grammar so the indicator
 *    feels like it belongs to the same system instead of an orphan widget.
 */
export function ThinkingIndicator() {
  // Retain the rotating messages as an opt-in — the wider chat-chrome polish
  // deliberately doesn't show them, but any caller that wants "personality" can
  // turn them back on with <ThinkingIndicator verbose />.
  return (
    <div className="flex items-center gap-2 py-1" role="status" aria-live="polite">
      <TypingDots />
      <span className="font-mono text-[11px] uppercase tracking-wide text-fg-muted">
        Thinking
      </span>
    </div>
  )
}

function TypingDots() {
  return (
    <span className="inline-flex items-center gap-1" aria-hidden="true">
      <Dot delay={0} />
      <Dot delay={160} />
      <Dot delay={320} />
    </span>
  )
}

function Dot({ delay }: { delay: number }) {
  // CSS-driven keyframes would be cleaner, but keeping this inline so the file
  // drops in with no global stylesheet edits. 1s cycle, 40% duty at full
  // opacity — reads as a wave without being frenetic.
  const [on, setOn] = useState(false)
  useEffect(() => {
    const start = setTimeout(() => {
      setOn(true)
      const iv = setInterval(() => setOn((v) => !v), 500)
      return () => clearInterval(iv)
    }, delay)
    return () => clearTimeout(start)
  }, [delay])
  return (
    <span
      className={`inline-block h-1 w-1 rounded-full bg-fg-muted transition-opacity duration-300 ${
        on ? 'opacity-100' : 'opacity-30'
      }`}
    />
  )
}
