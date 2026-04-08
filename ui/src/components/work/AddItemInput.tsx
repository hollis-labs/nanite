import { useState, useRef, useCallback } from 'react'
import { Plus } from 'lucide-react'

interface AddItemInputProps {
  placeholder?: string
  onAdd: (title: string) => void
  disabled?: boolean
}

export function AddItemInput({ placeholder = 'Add item...', onAdd, disabled }: AddItemInputProps) {
  const [editing, setEditing] = useState(false)
  const [value, setValue] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)

  const handleSubmit = useCallback(() => {
    const trimmed = value.trim()
    if (trimmed) {
      onAdd(trimmed)
    }
    setValue('')
    setEditing(false)
  }, [value, onAdd])

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Enter') {
        e.preventDefault()
        handleSubmit()
      } else if (e.key === 'Escape') {
        setValue('')
        setEditing(false)
      }
    },
    [handleSubmit],
  )

  if (!editing) {
    return (
      <button
        type="button"
        onClick={() => {
          setEditing(true)
          requestAnimationFrame(() => inputRef.current?.focus())
        }}
        disabled={disabled}
        className="flex items-center gap-1.5 w-full px-2 py-1.5 text-xs text-fg-faint hover:text-fg-muted border border-dashed border-border-subtle rounded-md transition-colors"
      >
        <Plus className="w-3 h-3" />
        <span>{placeholder}</span>
      </button>
    )
  }

  return (
    <input
      ref={inputRef}
      type="text"
      value={value}
      onChange={(e) => setValue(e.target.value)}
      onKeyDown={handleKeyDown}
      onBlur={handleSubmit}
      placeholder={placeholder}
      className="w-full bg-surface/50 border border-border rounded-md px-2 py-1.5 text-xs text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-primary"
    />
  )
}
