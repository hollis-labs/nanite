import { useState, useCallback } from 'react'
import { Copy, Check } from 'lucide-react'

interface ContentActionsProps {
  content: string
  visible: boolean
  className?: string
}

export function ContentActions({
  content,
  visible,
  className = '',
}: ContentActionsProps) {
  const [copied, setCopied] = useState(false)

  const handleCopy = useCallback(() => {
    void navigator.clipboard.writeText(content)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }, [content])

  return (
    <div className={`flex items-center gap-1 transition-opacity duration-150 ${visible ? 'opacity-100' : 'opacity-0'} ${className}`}>
      <button
        onClick={handleCopy}
        className="p-1 rounded text-fg-faint hover:text-fg-secondary hover:bg-surface transition-colors"
        aria-label="Copy"
        tabIndex={visible ? 0 : -1}
      >
        {copied ? <Check className="w-3.5 h-3.5" /> : <Copy className="w-3.5 h-3.5" />}
      </button>

    </div>
  )
}
