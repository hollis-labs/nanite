import { Info, Bookmark, Gauge } from 'lucide-react'
import { ScrollArea } from '@/components/ui/ScrollArea'
import { useLayoutStore } from '@/stores/useLayoutStore'

const widgets = [
  {
    id: 'session-info',
    title: 'Session Info',
    icon: Info,
    content: (
      <div className="space-y-2 text-xs text-zinc-400">
        <div className="flex justify-between">
          <span>Created</span>
          <span className="text-zinc-300">2 min ago</span>
        </div>
        <div className="flex justify-between">
          <span>Messages</span>
          <span className="text-zinc-300">3</span>
        </div>
        <div className="flex justify-between">
          <span>Model</span>
          <span className="text-zinc-300">Claude Opus</span>
        </div>
      </div>
    ),
  },
  {
    id: 'bookmarks',
    title: 'Bookmarks',
    icon: Bookmark,
    content: (
      <p className="text-xs text-zinc-500 italic">No bookmarked messages yet.</p>
    ),
  },
  {
    id: 'context-budget',
    title: 'Context Budget',
    icon: Gauge,
    content: (
      <div className="space-y-2">
        <div className="flex justify-between text-xs text-zinc-400">
          <span>Used</span>
          <span className="text-zinc-300">12,400 / 200,000</span>
        </div>
        <div className="w-full bg-zinc-800 rounded-full h-1.5">
          <div
            className="bg-indigo-500 h-1.5 rounded-full transition-all"
            style={{ width: '6%' }}
          />
        </div>
      </div>
    ),
  },
]

export function RightRail() {
  const open = useLayoutStore((s) => s.rightRailOpen)

  return (
    <aside
      className={`h-full bg-zinc-950 border-l border-zinc-800 flex flex-col transition-all duration-200 ease-in-out overflow-hidden ${
        open ? 'w-96' : 'w-0'
      }`}
    >
      <div className="min-w-96">
        {/* Header */}
        <div className="px-4 py-3 border-b border-zinc-800">
          <h2 className="text-sm font-semibold text-zinc-100">Widgets</h2>
        </div>

        {/* Widget cards */}
        <ScrollArea className="flex-1 h-[calc(100%-48px)]">
          <div className="p-3 space-y-3">
            {widgets.map(({ id, title, icon: Icon, content }) => (
              <div
                key={id}
                className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-3"
              >
                <div className="flex items-center gap-2 mb-3">
                  <Icon className="w-3.5 h-3.5 text-zinc-500" />
                  <h3 className="text-xs font-medium text-zinc-300 uppercase tracking-wider">
                    {title}
                  </h3>
                </div>
                {content}
              </div>
            ))}
          </div>
        </ScrollArea>
      </div>
    </aside>
  )
}
