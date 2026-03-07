import { NavRail } from './NavRail'
import { LeftSidebar } from './sidebar/LeftSidebar'
import { ChatMain } from './chat/ChatMain'
import { RightRail } from './RightRail'
import { useKeyboardShortcuts } from '@/hooks/useKeyboardShortcuts'

export function AppShell() {
  useKeyboardShortcuts()

  return (
    <div className="flex h-screen w-screen overflow-hidden bg-zinc-950">
      <NavRail />
      <LeftSidebar />
      <ChatMain />
      <RightRail />
    </div>
  )
}
