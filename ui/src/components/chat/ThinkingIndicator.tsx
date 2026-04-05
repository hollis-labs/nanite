import { useState, useEffect } from 'react'
import { Cog } from 'lucide-react'

const THINKING_MESSAGES = [
  'Grinding my gears…',
  'Warming up the circuits…',
  'Consulting the oracle…',
  'Crunching the numbers…',
  'Spinning up the hamster wheel…',
  'Loading the good stuff…',
  'Brewing something up…',
  'Connecting the dots…',
  'Sharpening the pencils…',
  'Calibrating the flux capacitor…',
]

export function ThinkingIndicator() {
  const [msgIndex, setMsgIndex] = useState(() =>
    Math.floor(Math.random() * THINKING_MESSAGES.length)
  )

  useEffect(() => {
    const interval = setInterval(() => {
      setMsgIndex(prev => (prev + 1) % THINKING_MESSAGES.length)
    }, 3000)
    return () => clearInterval(interval)
  }, [])

  return (
    <div className="flex items-center gap-2.5 py-2">
      <Cog className="w-4 h-4 text-primary animate-[spin_3s_linear_infinite] shrink-0" />
      <span className="text-sm text-fg-secondary animate-pulse">
        {THINKING_MESSAGES[msgIndex]}
      </span>
    </div>
  )
}
