import * as React from "react"
import { createPortal } from "react-dom"

interface TooltipProps {
  content: string
  side?: "top" | "right" | "bottom" | "left"
  children: React.ReactNode
}

export function Tooltip({ content, side = "right", children }: TooltipProps) {
  const [visible, setVisible] = React.useState(false)
  const triggerRef = React.useRef<HTMLDivElement>(null)
  const [style, setStyle] = React.useState<React.CSSProperties>({})

  React.useEffect(() => {
    if (!visible || !triggerRef.current) return

    const update = () => {
      if (!triggerRef.current) return
      const rect = triggerRef.current.getBoundingClientRect()
      const gap = 8
      let top: number
      let left: number

      switch (side) {
        case "top":
          top = rect.top - gap
          left = rect.left + rect.width / 2
          break
        case "bottom":
          top = rect.bottom + gap
          left = rect.left + rect.width / 2
          break
        case "left":
          top = rect.top + rect.height / 2
          left = rect.left - gap
          break
        case "right":
        default:
          top = rect.top + rect.height / 2
          left = rect.right + gap
          break
      }
      setStyle({ top, left })
    }

    update()
    window.addEventListener("scroll", update, true)
    window.addEventListener("resize", update)
    return () => {
      window.removeEventListener("scroll", update, true)
      window.removeEventListener("resize", update)
    }
  }, [visible, side])

  const transformMap: Record<string, string> = {
    top: "translate(-50%, -100%)",
    bottom: "translate(-50%, 0)",
    left: "translate(-100%, -50%)",
    right: "translate(0, -50%)",
  }

  return (
    <div
      ref={triggerRef}
      className="relative inline-flex"
      onMouseEnter={() => setVisible(true)}
      onMouseLeave={() => setVisible(false)}
      // Dismiss the tooltip on click so it doesn't get stuck behind a
      // popover that opens from the same trigger.
      onMouseDown={() => setVisible(false)}
    >
      {children}
      {visible &&
        createPortal(
          <div
            className="fixed z-50 px-2 py-1 text-xs font-medium text-fg bg-surface border border-border-subtle rounded-md shadow-lg whitespace-nowrap pointer-events-none"
            style={{ ...style, transform: transformMap[side] }}
          >
            {content}
          </div>,
          document.body
        )}
    </div>
  )
}
