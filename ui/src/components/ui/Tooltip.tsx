import * as React from "react"
import { cn } from "@/lib/utils"

interface TooltipProps {
  content: string
  side?: "top" | "right" | "bottom" | "left"
  children: React.ReactNode
}

export function Tooltip({ content, side = "right", children }: TooltipProps) {
  const [visible, setVisible] = React.useState(false)

  const positionClasses: Record<string, string> = {
    top: "bottom-full left-1/2 -translate-x-1/2 mb-2",
    right: "left-full top-1/2 -translate-y-1/2 ml-2",
    bottom: "top-full left-1/2 -translate-x-1/2 mt-2",
    left: "right-full top-1/2 -translate-y-1/2 mr-2",
  }

  return (
    <div
      className="relative inline-flex"
      onMouseEnter={() => setVisible(true)}
      onMouseLeave={() => setVisible(false)}
    >
      {children}
      {visible && (
        <div
          className={cn(
            "absolute z-50 px-2 py-1 text-xs font-medium text-zinc-100 bg-zinc-800 border border-zinc-700 rounded-md shadow-lg whitespace-nowrap pointer-events-none",
            positionClasses[side]
          )}
        >
          {content}
        </div>
      )}
    </div>
  )
}
