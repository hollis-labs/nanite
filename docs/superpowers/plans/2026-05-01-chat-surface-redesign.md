# Chat Surface Redesign Implementation Plan

**Goal:** Replace the chat surface's drawer model (bottom-anchored single drawer + inline alerts) with three named regions — `ChatPrimaryDrawer` (top), `ChatWorkingDrawer` (bottom mini-cards), `ChatAlertOverlay` (z-stacked, no-reflow alerts) — and consolidate the composer chrome (toolbar `+` popover, effort cycle pill, bolder send icon, width unification).

**Architecture:** Frontend-only redesign. New drawer components live in `ui/src/components/drawers/` alongside the existing files; old `BottomChatDrawer.tsx` and `ToolCallDrawer.tsx` get retired after the new components are wired and the condensed banner sub-component is extracted for reuse. Routing key `render_target=bottom_chat_drawer` is preserved; behavior changes from "replace a slot" to "append a dynamic tab." No backend or API changes required.

**Tech Stack:** React 19, TypeScript, Zustand (with persist middleware), TanStack React Query, Tailwind CSS v4, lucide-react, TipTap.

**Mode:** Full

**Status:** Draft

**Related spec:** `docs/superpowers/specs/2026-05-01-chat-surface-redesign-design.md`

---

## File Map

| File | Responsibility |
|------|----------------|
| `ui/src/lib/types.ts` | Add `DynamicCardTab` interface |
| `ui/src/stores/useLayoutStore.ts` | Add `chatPrimaryDrawer*` and `chatWorkingDrawer*` state; remove `bottomChatDrawerOpen` and `setBottomDrawerOpen` (or rename); update `persist` config |
| `ui/src/components/chat/ChatDrawerTabStrip.tsx` | NEW shared tab strip with horizontal scroll, hidden scrollbar, pagination arrows, label truncation |
| `ui/src/components/chat/ToolCallBanner.tsx` | NEW extracted from `ToolCallDrawer.tsx` (condensed-header bar) — preserves the running/done summary UI for future reuse |
| `ui/src/components/drawers/ChatPrimaryDrawer.tsx` | NEW — relocated chrome from `BottomChatDrawer.tsx`, tab strip flipped to bottom edge, tabs: Documents / Reports / Diffs / Tools / Pins / dynamic pin-tabs |
| `ui/src/components/drawers/ChatWorkingDrawer.tsx` | NEW — alert-banner chrome, tabs: Scratchpad / Terminal 1 / Terminal 2 (dev-mode) / Artifacts / Session Context + dynamic card-tabs |
| `ui/src/components/drawers/ChatAlertOverlay.tsx` | NEW — absolute-positioned alert overlay, reads sessionTakeover / streamStalled / circuitOpen / statusMessage from `useChat` |
| `ui/src/components/chat/ChatMain.tsx` | Mount new drawers + overlay; remove 4 inline alert blocks; update wrappers to `w-[85%] max-w-7xl mx-auto`; halve bottom padding |
| `ui/src/components/chat/ChatComposer.tsx` | DEV pill `border-radius: 4px`; conditional editor `pr-[88px]` when developer_mode; remove shell-running banner |
| `ui/src/components/chat/ComposerToolbar.tsx` | `+` popover trigger replacing 6 icons; effort 4-segment → cycle pill (uppercase); remove `⌘↵ to send` hint; bolder send icon |
| `ui/src/components/chat/ChatHeader.tsx` | Update wrapper to `w-[85%] max-w-7xl mx-auto` |
| `ui/src/components/chat/ChatTranscript.tsx` | Update inner wrapper to `w-[85%] max-w-7xl mx-auto` |

**Deleted files:**

- `ui/src/components/drawers/BottomChatDrawer.tsx` (after Task 8 confirms wire-up)
- `ui/src/components/chat/ToolCallDrawer.tsx` (after Task 4 extracts banner + Task 8 wires Tools tab)

---

## Task ordering rationale

Foundation first (state, types, shared components) → new components built in parallel with old ones still working → ChatMain wired to new components → composer cleanup → retirement of old files → bug fix + verification. Old files stay live until new wiring is verified, so the app never breaks mid-implementation.

---

### Task 1: Layout store — add new drawer state

**Files:**

- Modify: `ui/src/stores/useLayoutStore.ts`

- [ ] **Step 1: Add new state types and interface fields**

In `useLayoutStore.ts`, before the `LayoutState` interface, add a type alias:

```ts
import type { Envelope, DynamicCardTab } from '@/lib/types'

type ChatDrawerState = {
  open: boolean
  height: number
  activeTab: string
}
```

In the `LayoutState` interface, add after `defaultDrawerTab`:

```ts
  // Chat surface redesign 2026-05-01
  chatPrimaryDrawer: ChatDrawerState
  chatWorkingDrawer: ChatDrawerState
  /** FE-only state for transient card-tabs in ChatWorkingDrawer.
   *  Populated from panelEnvelopes['bottom_chat_drawer'] reactively
   *  and augmented with focused/pinned/createdAt metadata. */
  chatWorkingDrawerCardTabs: DynamicCardTab[]

  setChatPrimaryDrawer: (patch: Partial<ChatDrawerState>) => void
  setChatWorkingDrawer: (patch: Partial<ChatDrawerState>) => void
  appendChatWorkingDrawerCardTab: (tab: DynamicCardTab) => void
  removeChatWorkingDrawerCardTab: (id: string) => void
  focusChatWorkingDrawerCardTab: (id: string) => void
```

- [ ] **Step 2: Add the action implementations in the store body**

Inside the `create<LayoutState>()(persist(...))` body, after `defaultDrawerTab` initialization:

```ts
      chatPrimaryDrawer: { open: false, height: 280, activeTab: 'documents' },
      chatWorkingDrawer: { open: false, height: 200, activeTab: 'scratchpad' },
      chatWorkingDrawerCardTabs: [],

      setChatPrimaryDrawer: (patch) =>
        set((s) => ({ chatPrimaryDrawer: { ...s.chatPrimaryDrawer, ...patch } })),
      setChatWorkingDrawer: (patch) =>
        set((s) => ({ chatWorkingDrawer: { ...s.chatWorkingDrawer, ...patch } })),
      appendChatWorkingDrawerCardTab: (tab) =>
        set((s) => ({
          chatWorkingDrawerCardTabs: [...s.chatWorkingDrawerCardTabs, tab],
          // Newly arriving focused cards become the active tab.
          chatWorkingDrawer: tab.focused
            ? { ...s.chatWorkingDrawer, activeTab: tab.id }
            : s.chatWorkingDrawer,
        })),
      removeChatWorkingDrawerCardTab: (id) =>
        set((s) => {
          const next = s.chatWorkingDrawerCardTabs.filter((t) => t.id !== id)
          // If the active tab was removed, fall back to the last fixed tab.
          const activeWas = s.chatWorkingDrawer.activeTab === id
          return {
            chatWorkingDrawerCardTabs: next,
            chatWorkingDrawer: activeWas
              ? { ...s.chatWorkingDrawer, activeTab: 'scratchpad' }
              : s.chatWorkingDrawer,
          }
        }),
      focusChatWorkingDrawerCardTab: (id) =>
        set((s) => ({
          chatWorkingDrawer: { ...s.chatWorkingDrawer, activeTab: id },
        })),
```

- [ ] **Step 3: Update `onRehydrateStorage` to reset card-tabs on reload**

In the `onRehydrateStorage` callback, alongside `state.panelEnvelopes = {}`, add:

```ts
        // Transient card-tabs do not survive reload (FE-only state).
        // Pinned cards still survive via the DB-backed POST /drawer-cards path.
        state.chatWorkingDrawerCardTabs = []
```

- [ ] **Step 4: Build to confirm no type errors**

```bash
cd ui && npm run build
```

Expected: build passes.

- [ ] **Step 5: Commit**

```bash
git add ui/src/stores/useLayoutStore.ts
git commit -m "feat(layout): add chat-surface drawer state for redesign"
```

---

### Task 2: Types — `DynamicCardTab` interface

**Files:**

- Modify: `ui/src/lib/types.ts`

- [ ] **Step 1: Add the `DynamicCardTab` interface**

Append to `ui/src/lib/types.ts` (after the existing drawer-card types, near the other card-related interfaces):

```ts
/**
 * Per-tab data for transient card-tabs in ChatWorkingDrawer.
 *
 * Each agent-emitted envelope routed via `render_target=bottom_chat_drawer`
 * becomes one of these tabs. `pinned: true` promotes via the existing
 * POST /drawer-cards endpoint and survives session reload as a DB-backed
 * pinned card; transient (pinned=false) tabs live only in the layout store
 * for the session.
 */
export interface DynamicCardTab {
  /** Stable ID. Format: `card:<uuid>`. */
  id: string
  /** Display label. Derived from envelope.title when present;
   *  fallback = envelope-type display name + short timestamp. */
  label: string
  /** The full envelope payload — render via EnvelopeRenderer. */
  payload: Envelope
  /** Agent-emitted defaults true. When true, the tab promotes to active
   *  on the next drawer-open. Manual user selection overrides until the
   *  next focused-true arrival. */
  focused: boolean
  /** When true, has been promoted to a DB-backed pinned card via the
   *  existing API. Transient tabs default false. */
  pinned: boolean
  /** Epoch ms. Used for stable sort order in the tab strip. */
  createdAt: number
}
```

- [ ] **Step 2: Build to confirm no type errors**

```bash
cd ui && npm run build
```

Expected: build passes.

- [ ] **Step 3: Commit**

```bash
git add ui/src/lib/types.ts
git commit -m "feat(types): add DynamicCardTab for chat working drawer"
```

---

### Task 3: `ChatDrawerTabStrip` shared component

**Files:**

- Create: `ui/src/components/chat/ChatDrawerTabStrip.tsx`

- [ ] **Step 1: Create the shared tab strip component**

```tsx
import { ChevronLeft, ChevronRight, X, Pin, PinOff } from 'lucide-react'
import { useEffect, useLayoutEffect, useRef, useState } from 'react'

export interface ChatDrawerTab {
  id: string
  label: string
  /** True for the currently selected tab. */
  active: boolean
  /** Optional pulsing-pip indicator (e.g. Tools tab during a stream). */
  runningPip?: boolean
  /** Closeable tabs render an `×` on hover. Used for dynamic card-tabs. */
  closeable?: boolean
  /** Pinnable tabs render a Pin / PinOff toggle on hover. */
  pinnable?: boolean
  pinned?: boolean
}

interface Props {
  tabs: ChatDrawerTab[]
  /** Where the strip docks: 'top' (default, used by ChatWorkingDrawer) or
   *  'bottom' (used by ChatPrimaryDrawer — strip is the handle). */
  dock?: 'top' | 'bottom'
  onSelect: (id: string) => void
  onClose?: (id: string) => void
  onTogglePin?: (id: string) => void
}

const TAB_LABEL_MAX = 14

function truncate(s: string): string {
  return s.length > TAB_LABEL_MAX ? s.slice(0, TAB_LABEL_MAX - 1) + '…' : s
}

export function ChatDrawerTabStrip({
  tabs,
  dock = 'top',
  onSelect,
  onClose,
  onTogglePin,
}: Props) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const [overflow, setOverflow] = useState<{ left: boolean; right: boolean }>({
    left: false,
    right: false,
  })

  const measure = () => {
    const el = scrollRef.current
    if (!el) return
    const left = el.scrollLeft > 0
    const right = el.scrollLeft + el.clientWidth < el.scrollWidth - 1
    setOverflow({ left, right })
  }

  useLayoutEffect(measure, [tabs.length])

  useEffect(() => {
    const el = scrollRef.current
    if (!el) return
    const handler = () => measure()
    el.addEventListener('scroll', handler, { passive: true })
    window.addEventListener('resize', handler)
    return () => {
      el.removeEventListener('scroll', handler)
      window.removeEventListener('resize', handler)
    }
  }, [])

  const paginate = (dir: -1 | 1) => {
    const el = scrollRef.current
    if (!el) return
    el.scrollBy({ left: dir * (el.clientWidth * 0.8), behavior: 'smooth' })
  }

  return (
    <div className={`flex items-center gap-1 px-1 ${dock === 'bottom' ? 'border-t' : 'border-b'} border-border-subtle`}>
      {overflow.left && (
        <button
          type="button"
          onClick={() => paginate(-1)}
          className="flex h-7 w-6 items-center justify-center rounded-[4px] text-fg-muted hover:bg-surface hover:text-fg"
          aria-label="Scroll tabs left"
        >
          <ChevronLeft size={14} />
        </button>
      )}
      <div
        ref={scrollRef}
        className="flex flex-1 items-center gap-1 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
      >
        {tabs.map((t) => (
          <div key={t.id} className="group relative shrink-0">
            <button
              type="button"
              onClick={() => onSelect(t.id)}
              title={t.label}
              className={`flex items-center gap-1.5 rounded-[6px] px-2.5 py-1 font-mono text-[11px] tracking-wide transition-colors ${
                t.active
                  ? 'bg-surface text-fg'
                  : 'text-fg-muted hover:bg-surface hover:text-fg-secondary'
              }`}
            >
              <span>{truncate(t.label)}</span>
              {t.runningPip && (
                <span className="inline-block h-1.5 w-1.5 rounded-full bg-warning animate-pulse" />
              )}
              {t.pinnable && onTogglePin && (
                <button
                  type="button"
                  onClick={(e) => {
                    e.stopPropagation()
                    onTogglePin(t.id)
                  }}
                  className="opacity-0 group-hover:opacity-100 transition-opacity"
                  aria-label={t.pinned ? 'Unpin tab' : 'Pin tab'}
                >
                  {t.pinned ? <PinOff size={10} /> : <Pin size={10} />}
                </button>
              )}
              {t.closeable && onClose && (
                <button
                  type="button"
                  onClick={(e) => {
                    e.stopPropagation()
                    onClose(t.id)
                  }}
                  className="opacity-0 group-hover:opacity-100 transition-opacity"
                  aria-label="Close tab"
                >
                  <X size={10} />
                </button>
              )}
            </button>
          </div>
        ))}
      </div>
      {overflow.right && (
        <button
          type="button"
          onClick={() => paginate(1)}
          className="flex h-7 w-6 items-center justify-center rounded-[4px] text-fg-muted hover:bg-surface hover:text-fg"
          aria-label="Scroll tabs right"
        >
          <ChevronRight size={14} />
        </button>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Build to confirm no type errors**

```bash
cd ui && npm run build
```

- [ ] **Step 3: Commit**

```bash
git add ui/src/components/chat/ChatDrawerTabStrip.tsx
git commit -m "feat(chat): add shared ChatDrawerTabStrip with pagination"
```

---

### Task 4: Extract `ToolCallBanner` from `ToolCallDrawer`

**Files:**

- Read: `ui/src/components/chat/ToolCallDrawer.tsx` (full file)
- Create: `ui/src/components/chat/ToolCallBanner.tsx`
- Modify: `ui/src/components/chat/ToolCallDrawer.tsx` (replace inline banner JSX with `<ToolCallBanner />`)

- [ ] **Step 1: Read the full ToolCallDrawer.tsx to identify the condensed-banner JSX**

```bash
wc -l ui/src/components/chat/ToolCallDrawer.tsx
```

Read the file and identify the **header bar** JSX (the part shown when the drawer is collapsed — running/done counts, current tool name, controls). It's typically ~30–60 lines around the `drawerState === 'closed'` or compact-state render path.

- [ ] **Step 2: Create `ToolCallBanner.tsx` with the extracted JSX**

The component is parameterized by props rather than reading the store directly, so it's reusable in contexts beyond `ToolCallDrawer`:

```tsx
import { Loader2, Cpu, CheckCircle2 } from 'lucide-react'
import type { ToolCall } from '@/lib/types'

interface ToolCallBannerProps {
  toolCalls: ToolCall[]
  isStreaming: boolean
  /** Optional click handler — when provided, the banner is interactive. */
  onClick?: () => void
}

/**
 * Condensed running/done summary bar for tool calls.
 *
 * Extracted from the original ToolCallDrawer header so the banner UI can
 * be reused elsewhere (e.g. as a fallback indicator when no drawer is
 * open). The Tools tab in ChatPrimaryDrawer renders the full ToolCallItem
 * list directly; this banner is the compact form for tight slots.
 */
export function ToolCallBanner({ toolCalls, isStreaming, onClick }: ToolCallBannerProps) {
  const running = toolCalls.filter((tc) => tc.status === 'running')
  const done = toolCalls.filter((tc) => tc.status === 'done')
  const current = running[0] ?? toolCalls[toolCalls.length - 1] ?? null

  const Wrapper = onClick ? 'button' : 'div'

  return (
    <Wrapper
      type={onClick ? 'button' : undefined}
      onClick={onClick}
      className="flex w-full items-center gap-2 px-3 py-1.5 text-xs text-fg-muted"
    >
      {running.length > 0 ? (
        <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-primary" />
      ) : done.length > 0 ? (
        <CheckCircle2 className="h-3.5 w-3.5 shrink-0 text-success" />
      ) : (
        <Cpu className="h-3.5 w-3.5 shrink-0" />
      )}
      <span className="truncate">
        {current ? current.tool : isStreaming ? 'Thinking…' : 'No active tools'}
      </span>
      <span className="ml-auto font-mono text-[10px]">
        {running.length > 0 ? `${running.length} running · ` : ''}
        {done.length} done
      </span>
    </Wrapper>
  )
}
```

- [ ] **Step 3: Replace the inline banner in `ToolCallDrawer.tsx` with `<ToolCallBanner />`**

In `ToolCallDrawer.tsx`, locate the closed/compact header JSX and replace it with:

```tsx
import { ToolCallBanner } from './ToolCallBanner'

// ... inside the component, where the closed-state banner was rendered:
<ToolCallBanner
  toolCalls={toolCalls}
  isStreaming={isStreaming}
  onClick={() => setDrawerState('expanded')}
/>
```

Delete the old inline banner JSX and any imports that become unused (`Loader2`, `Cpu`, `CheckCircle2` — keep them only if referenced elsewhere in the file).

- [ ] **Step 4: Build + manual smoke test**

```bash
cd ui && npm run build
npm run dev
# Manual: trigger a tool call in the running app; confirm the closed-state banner
# still renders the same content via ToolCallBanner.
```

- [ ] **Step 5: Commit**

```bash
git add ui/src/components/chat/ToolCallBanner.tsx ui/src/components/chat/ToolCallDrawer.tsx
git commit -m "refactor(chat): extract ToolCallBanner from ToolCallDrawer"
```

---

### Task 5: `ChatPrimaryDrawer` (relocated chrome from `BottomChatDrawer`)

**Files:**

- Read: `ui/src/components/drawers/BottomChatDrawer.tsx` (full)
- Create: `ui/src/components/drawers/ChatPrimaryDrawer.tsx`

- [ ] **Step 1: Read the full BottomChatDrawer.tsx to identify reusable patterns**

Take note of: the resize/drag handlers, how built-in tabs render, how dynamic pinned-card tabs are queried (`api.listDrawerCards`) and how pin/unpin mutations work. We're keeping the same backend API.

- [ ] **Step 2: Create `ChatPrimaryDrawer.tsx` skeleton with state wiring**

```tsx
import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ChevronUp, X } from 'lucide-react'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useAppStore } from '@/stores/useAppStore'
import { useChatStore } from '@/stores/useChatStore'
import { api } from '@/lib/api'
import { ChatDrawerTabStrip, type ChatDrawerTab } from '@/components/chat/ChatDrawerTabStrip'
import type { DrawerPinnedCard } from '@/lib/types'

const FIXED_TABS: { id: string; label: string }[] = [
  { id: 'documents', label: 'Documents' },
  { id: 'reports', label: 'Reports' },
  { id: 'diffs', label: 'Diffs' },
  { id: 'tools', label: 'Tools' },
  { id: 'pins', label: 'Pins' },
]

export function ChatPrimaryDrawer() {
  const drawer = useLayoutStore((s) => s.chatPrimaryDrawer)
  const setDrawer = useLayoutStore((s) => s.setChatPrimaryDrawer)
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const toolCalls = useChatStore((s) => s.toolCalls)
  const isStreaming = useChatStore((s) => s.isStreaming)

  const hasRunningTool = toolCalls.some((tc) => tc.status === 'running')

  // DB-backed pinned cards (existing API).
  const { data: pinnedCards = [] } = useQuery<DrawerPinnedCard[]>({
    queryKey: ['drawer-cards', activeSessionId],
    queryFn: () => api.listDrawerCards(activeSessionId!),
    enabled: !!activeSessionId,
  })

  const tabs: ChatDrawerTab[] = useMemo(() => {
    const fixed: ChatDrawerTab[] = FIXED_TABS.map((t) => ({
      id: t.id,
      label: t.label,
      active: drawer.activeTab === t.id,
      runningPip: t.id === 'tools' && hasRunningTool && isStreaming,
    }))
    const dynamic: ChatDrawerTab[] = pinnedCards.map((c) => ({
      id: `pin:${c.id}`,
      label: c.label || 'Pinned',
      active: drawer.activeTab === `pin:${c.id}`,
      pinnable: true,
      pinned: true,
    }))
    return [...fixed, ...dynamic]
  }, [drawer.activeTab, pinnedCards, hasRunningTool, isStreaming])

  // Drag-resize handle. Same pattern as BottomChatDrawer's drag.
  const dragRef = useRef<{ y: number; height: number } | null>(null)
  const onPointerDown = (e: React.PointerEvent) => {
    dragRef.current = { y: e.clientY, height: drawer.height }
    ;(e.target as HTMLElement).setPointerCapture(e.pointerId)
  }
  const onPointerMove = (e: React.PointerEvent) => {
    const d = dragRef.current
    if (!d) return
    // Top drawer drags DOWN to grow (handle is below the body).
    const next = Math.max(0, d.height + (e.clientY - d.y))
    setDrawer({ height: next })
  }
  const onPointerUp = (e: React.PointerEvent) => {
    dragRef.current = null
    ;(e.target as HTMLElement).releasePointerCapture(e.pointerId)
  }

  if (!activeSessionId) return null

  return (
    <div className="w-[85%] max-w-7xl mx-auto px-4">
      <div
        className={`relative overflow-hidden border border-border-subtle bg-bg-elevated transition-[height] ${
          drawer.open ? '' : 'h-0'
        }`}
        style={{
          height: drawer.open ? drawer.height : 0,
          borderRadius: '0 0 10px 10px',
        }}
      >
        {/* Top accent ribbon — primary color */}
        <div className="pointer-events-none absolute inset-x-0 top-0 h-[2px] bg-primary opacity-65" />
        {drawer.open && (
          <DrawerBody activeTab={drawer.activeTab} pinnedCards={pinnedCards} />
        )}
      </div>
      <ChatDrawerTabStrip
        tabs={tabs}
        dock="bottom"
        onSelect={(id) => {
          if (drawer.activeTab === id && drawer.open) {
            // Click on the active tab while open = close.
            setDrawer({ open: false })
            return
          }
          setDrawer({ open: true, activeTab: id })
        }}
        onTogglePin={async (id) => {
          if (id.startsWith('pin:')) {
            const cardId = id.slice(4)
            await api.deleteDrawerCard(cardId)
            // queryClient invalidate handled in caller wiring (ChatMain)
          }
        }}
      />
    </div>
  )
}

function DrawerBody({
  activeTab,
  pinnedCards,
}: {
  activeTab: string
  pinnedCards: DrawerPinnedCard[]
}) {
  // Tab content panes — implement per spec section "Tab Assignments".
  // For dynamic pin tabs, render the typed card payload via EnvelopeRenderer.
  switch (activeTab) {
    case 'documents': return <DocumentsTab />
    case 'reports': return <ReportsTab />
    case 'diffs': return <DiffsTab />
    case 'tools': return <ToolsTab />
    case 'pins': return <PinsTab />
    default:
      if (activeTab.startsWith('pin:')) {
        const card = pinnedCards.find((c) => `pin:${c.id}` === activeTab)
        return card ? <PinnedCardTab card={card} /> : null
      }
      return null
  }
}

// Implement the tab content panes. The Documents/Pins/PinnedCardTab content
// can largely be lifted from BottomChatDrawer.tsx's existing render branches.
// Reports/Diffs render existing report-card/diff-card envelopes (already
// available via EnvelopeRenderer). ToolsTab renders the toolCalls list via
// ToolCallItem (existing component).
/**
 * DocumentsTab — copy the body of `BottomChatDrawer.tsx`'s
 * `case 'documents':` render branch verbatim (it queries
 * `api.listDocuments(sessionId)` and renders a list with click-to-open).
 * Do not refactor; just relocate.
 */
function DocumentsTab() {
  // Lift the implementation from BottomChatDrawer.tsx — see the existing
  // 'documents' branch around the BUILTIN_TABS render switch.
  return null
}

/**
 * ReportsTab — empty-state in this iteration. Content population is a
 * follow-up: filter session-history envelopes whose `kind === 'report-card'`
 * and render via EnvelopeRenderer. Source query in the follow-up will come
 * from the same data hook ChatTranscript uses for messages.
 */
function ReportsTab() {
  return (
    <div className="flex h-full items-center justify-center p-6 text-xs text-fg-muted">
      No reports yet — they'll appear here as agents produce them.
    </div>
  )
}

/**
 * DiffsTab — empty-state in this iteration. Same follow-up pattern as
 * ReportsTab (filter envelopes for `kind === 'diff-card'`).
 */
function DiffsTab() {
  return (
    <div className="flex h-full items-center justify-center p-6 text-xs text-fg-muted">
      No diffs in this session yet.
    </div>
  )
}

function ToolsTab() {
  const toolCalls = useChatStore((s) => s.toolCalls)
  // Render the existing ToolCallItem list — same UI as today's ToolCallDrawer body.
  return (
    <div className="overflow-y-auto h-full p-3">
      {/* eslint-disable-next-line @typescript-eslint/no-explicit-any */}
      {toolCalls.map((tc: any) => (
        <div key={tc.id} className="mb-2 text-xs">{tc.tool} — {tc.status}</div>
      ))}
    </div>
  )
}

/**
 * PinsTab — copy the body of `BottomChatDrawer.tsx`'s `case 'pins':` render
 * branch verbatim. It lists DB-backed pinned cards with promote/unpin
 * controls; no logic change.
 */
function PinsTab() { return null }
function PinnedCardTab({ card }: { card: DrawerPinnedCard }) {
  // Render typed card payload — same dispatcher used by BottomChatDrawer today.
  return <div className="p-3">{card.type}</div>
}
```

> **Spec reference:** Visual details (ribbon color, exact tab content layouts, scroll behavior in panes) are in the spec. The skeleton above gives the integration shape; tab content panes that say "port from BottomChatDrawer" copy the existing render branches verbatim. `ReportsTab` / `DiffsTab` are net-new — they query `useChat`'s messages list and filter to envelopes whose `kind` is `report-card` / `diff-card`.

- [ ] **Step 3: Replace the placeholder `ToolsTab` with the real `ToolCallItem` list**

Import `ToolCallItem` from `./ToolCallItem.tsx` (existing) and render the real list. Match the layout used by `ToolCallDrawer` today.

- [ ] **Step 4: Build + commit**

```bash
cd ui && npm run build
git add ui/src/components/drawers/ChatPrimaryDrawer.tsx
git commit -m "feat(drawers): add ChatPrimaryDrawer (skeleton)"
```

> **Note:** ChatPrimaryDrawer is not yet mounted anywhere — Task 8 wires it into ChatMain.

---

### Task 6: `ChatWorkingDrawer` (new alert-banner-chrome drawer)

**Files:**

- Create: `ui/src/components/drawers/ChatWorkingDrawer.tsx`

- [ ] **Step 1: Create the component skeleton**

```tsx
import { useEffect, useMemo, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useAppStore } from '@/stores/useAppStore'
import { useSettings } from '@/hooks/useSettings'
import { ChatDrawerTabStrip, type ChatDrawerTab } from '@/components/chat/ChatDrawerTabStrip'
import { EnvelopeRenderer } from '@/components/chat/envelopes/EnvelopeRenderer'
import { api } from '@/lib/api'
import type { DynamicCardTab } from '@/lib/types'

const FIXED_TABS: { id: string; label: string; devOnly?: boolean }[] = [
  { id: 'scratchpad', label: 'Scratchpad' },
  { id: 'terminal-1', label: 'Terminal 1' },
  { id: 'terminal-2', label: 'Terminal 2', devOnly: true },
  { id: 'artifacts', label: 'Artifacts' },
  { id: 'session-context', label: 'Session Context' },
]

export function ChatWorkingDrawer() {
  const drawer = useLayoutStore((s) => s.chatWorkingDrawer)
  const setDrawer = useLayoutStore((s) => s.setChatWorkingDrawer)
  const cardTabs = useLayoutStore((s) => s.chatWorkingDrawerCardTabs)
  const appendCardTab = useLayoutStore((s) => s.appendChatWorkingDrawerCardTab)
  const removeCardTab = useLayoutStore((s) => s.removeChatWorkingDrawerCardTab)
  const panelEnvelopes = useLayoutStore((s) => s.panelEnvelopes)
  const clearPanelEnvelopes = useLayoutStore((s) => s.clearPanelEnvelopes)
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const queryClient = useQueryClient()
  const { data: settings } = useSettings()
  const developerMode = settings?.developer_mode ?? false

  // Reactive: when a new envelope arrives in panelEnvelopes['bottom_chat_drawer'],
  // append it as a DynamicCardTab and clear the inbox slot.
  useEffect(() => {
    const incoming = panelEnvelopes['bottom_chat_drawer'] ?? []
    if (incoming.length === 0) return
    for (const env of incoming) {
      const tab: DynamicCardTab = {
        id: `card:${env.id ?? crypto.randomUUID()}`,
        label: env.title || envelopeFallbackLabel(env),
        payload: env,
        focused: env.focused !== false, // default true unless explicitly false
        pinned: false,
        createdAt: Date.now(),
      }
      appendCardTab(tab)
    }
    clearPanelEnvelopes('bottom_chat_drawer')
  }, [panelEnvelopes, appendCardTab, clearPanelEnvelopes])

  const visibleFixedTabs = developerMode
    ? FIXED_TABS
    : FIXED_TABS.filter((t) => !t.devOnly)

  const tabs: ChatDrawerTab[] = useMemo(() => {
    const fixed: ChatDrawerTab[] = visibleFixedTabs.map((t) => ({
      id: t.id,
      label: t.label,
      active: drawer.activeTab === t.id,
    }))
    const dynamic: ChatDrawerTab[] = [...cardTabs]
      .sort((a, b) => a.createdAt - b.createdAt)
      .map((c) => ({
        id: c.id,
        label: c.label,
        active: drawer.activeTab === c.id,
        closeable: !c.pinned,
        pinnable: true,
        pinned: c.pinned,
      }))
    return [...fixed, ...dynamic]
  }, [visibleFixedTabs, cardTabs, drawer.activeTab])

  if (!activeSessionId) return null

  return (
    <div className="w-[85%] max-w-7xl mx-auto px-4 relative">
      <ChatDrawerTabStrip
        tabs={tabs}
        dock="top"
        onSelect={(id) => {
          if (drawer.activeTab === id && drawer.open) {
            setDrawer({ open: false })
            return
          }
          setDrawer({ open: true, activeTab: id })
        }}
        onClose={(id) => removeCardTab(id)}
        onTogglePin={async (id) => {
          const tab = cardTabs.find((t) => t.id === id)
          if (!tab) return
          if (tab.pinned) {
            // Promoted card — call DELETE; refresh pinned cards in ChatPrimaryDrawer.
            const dbId = id.slice(5) // strip 'card:' prefix
            await api.deleteDrawerCard(dbId)
            removeCardTab(id)
          } else {
            // Promote: POST /drawer-cards via existing API.
            await api.createDrawerCard(activeSessionId, {
              type: tab.payload.kind ?? 'envelope',
              label: tab.label,
              payload: tab.payload as unknown as Record<string, unknown>,
            })
            queryClient.invalidateQueries({ queryKey: ['drawer-cards', activeSessionId] })
            removeCardTab(id) // tab now lives in ChatPrimaryDrawer's pinned-tabs list
          }
        }}
      />
      <div
        className="overflow-hidden border-x border-b border-border-subtle bg-bg-elevated"
        style={{
          height: drawer.open ? drawer.height : 0,
          // Square bottom corners — flush against composer.
          borderRadius: '10px 10px 0 0',
        }}
      >
        {/* Top accent ribbon — success color (differentiates from ChatPrimaryDrawer) */}
        <div className="pointer-events-none absolute inset-x-0 top-0 h-[2px] bg-success opacity-65" />
        {drawer.open && (
          <DrawerBody activeTab={drawer.activeTab} cardTabs={cardTabs} />
        )}
      </div>
    </div>
  )
}

function DrawerBody({ activeTab, cardTabs }: { activeTab: string; cardTabs: DynamicCardTab[] }) {
  switch (activeTab) {
    case 'scratchpad': return <ScratchpadTab />
    case 'terminal-1': return <Terminal1Tab />
    case 'terminal-2': return <Terminal2Tab />
    case 'artifacts': return <ArtifactsTab />
    case 'session-context': return <SessionContextTab />
    default:
      if (activeTab.startsWith('card:')) {
        const tab = cardTabs.find((t) => t.id === activeTab)
        return tab ? <EnvelopeRenderer envelope={tab.payload} /> : null
      }
      return null
  }
}

function envelopeFallbackLabel(env: import('@/lib/types').Envelope): string {
  const ts = new Date().toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })
  const typeLabel = env.kind ? env.kind.replace(/-/g, ' ') : 'Card'
  return `${typeLabel} ${ts}`
}

// Tab body components — Scratchpad / Artifacts / SessionContext are ported
// from `BottomChatDrawer.tsx`'s existing render branches verbatim. Look for
// the corresponding `case '<tab-id>':` arms in the BUILTIN_TABS render
// switch and copy them here without refactoring. Terminal 1 / Terminal 2
// are new.
function ScratchpadTab() {
  // Copy `BottomChatDrawer.tsx`'s `case 'scratchpad':` branch — it renders
  // a textarea with debounced auto-save via the existing scratchpad mutation.
  return null
}
function Terminal1Tab() {
  // Streamed output from `!command` shell-exec. Source: existing shellExec
  // result store (or, if not yet stored centrally, a new Zustand slice
  // 'shellOutput' added in this task as a sibling). Display is read-only,
  // monospace, auto-scroll-to-bottom.
  return <div className="p-3 font-mono text-xs">Terminal 1 placeholder</div>
}
function Terminal2Tab() {
  // Full interactive shell — gated by developer_mode. xterm.js or similar.
  // Implementation deferred to a follow-up task; ship with placeholder.
  return <div className="p-3 font-mono text-xs">Terminal 2 (dev) — interactive shell, follow-up</div>
}
function ArtifactsTab() {
  // Copy `BottomChatDrawer.tsx`'s `case 'artifacts':` branch — it queries
  // session artifacts and renders previews with download buttons.
  return null
}
function SessionContextTab() {
  // Copy `BottomChatDrawer.tsx`'s `case 'context':` branch — user-editable
  // textarea backed by the session-context PATCH mutation, persists across
  // compaction.
  return null
}
```

- [ ] **Step 2: Port Scratchpad / Artifacts / SessionContext bodies**

These three already exist in `BottomChatDrawer.tsx`. Copy the relevant render branches — they each consume their own data hooks (artifacts query, scratchpad mutations, session-context PATCH). Do not refactor them; just relocate.

- [ ] **Step 3: Add `Terminal1Tab` shell-exec source**

The existing composer's shell-running banner reads from local state inside `ChatComposer.tsx` (`shellRunning`, `pendingShellCommand`). Move that state OUT of the composer and into a new lightweight Zustand slice (or expose via `useChatStore`). Subscribe `Terminal1Tab` to it. The composer will set the state; the tab renders the streamed output. Specifics:

- Add `shellOutput: string`, `shellRunning: boolean`, `pendingShellCommand: string | null`, `appendShellOutput(line)`, `clearShellOutput()` to a new `useShellStore` (or extend `useChatStore`).
- `Terminal1Tab` body:

```tsx
function Terminal1Tab() {
  const output = useShellStore((s) => s.shellOutput)
  const running = useShellStore((s) => s.shellRunning)
  const ref = useRef<HTMLPreElement>(null)
  useEffect(() => {
    ref.current?.scrollTo({ top: ref.current.scrollHeight })
  }, [output])
  return (
    <div className="h-full overflow-hidden">
      <pre
        ref={ref}
        className="h-full overflow-y-auto p-3 font-mono text-[11px] leading-relaxed text-fg-secondary whitespace-pre-wrap"
      >{output || (running ? 'Running…' : 'No shell output yet. Type ! followed by a command in the composer.')}</pre>
    </div>
  )
}
```

The composer changes that drive this state move into Task 11.

- [ ] **Step 4: Build + commit**

```bash
cd ui && npm run build
git add ui/src/components/drawers/ChatWorkingDrawer.tsx
git commit -m "feat(drawers): add ChatWorkingDrawer with dynamic card-tabs"
```

---

### Task 7: `ChatAlertOverlay` (absolute-positioned alert overlay)

**Files:**

- Create: `ui/src/components/drawers/ChatAlertOverlay.tsx`

- [ ] **Step 1: Create the overlay component**

```tsx
import { AlertTriangle, RefreshCw, X } from 'lucide-react'

type Severity = 'info' | 'warning' | 'danger'

interface AlertSpec {
  severity: Severity
  title: string
  body: string
  actions: { label: string; onClick: () => void; primary?: boolean }[]
}

interface ChatAlertOverlayProps {
  sessionTakeover: boolean
  streamStalled: boolean
  circuitOpen: boolean
  statusMessage: string | null
  onReconnect: () => void
  onRetry: () => void
  onDismissCircuit: () => void
}

export function ChatAlertOverlay({
  sessionTakeover,
  streamStalled,
  circuitOpen,
  statusMessage,
  onReconnect,
  onRetry,
  onDismissCircuit,
}: ChatAlertOverlayProps) {
  // Resolve the topmost alert (priority: takeover > circuit > stalled > status).
  let alert: AlertSpec | null = null
  if (sessionTakeover) {
    alert = {
      severity: 'info',
      title: 'This session is now active in another tab',
      body: 'The streaming connection moved to a newer tab. Reload to reconnect here.',
      actions: [{ label: 'Reload', onClick: () => window.location.reload(), primary: true }],
    }
  } else if (circuitOpen) {
    alert = {
      severity: 'warning',
      title: 'Provider rate limited after multiple retries',
      body: 'The API provider returned rate limit errors. Retry, or dismiss to keep the partial response.',
      actions: [
        { label: 'Retry', onClick: onRetry, primary: true },
        { label: 'Dismiss', onClick: onDismissCircuit },
      ],
    }
  } else if (streamStalled) {
    alert = {
      severity: 'warning',
      title: 'Connection appears stalled',
      body: 'No activity from the server in the last minute. Reconnect to retry this turn.',
      actions: [{ label: 'Reconnect', onClick: onReconnect, primary: true }],
    }
  } else if (statusMessage) {
    alert = { severity: 'info', title: statusMessage, body: '', actions: [] }
  }

  if (!alert) return null

  const colorBySeverity: Record<Severity, { ring: string; ribbon: string; icon: string }> = {
    info: { ring: 'border-info-muted bg-info-muted', ribbon: 'bg-info', icon: 'text-info' },
    warning: { ring: 'border-warning-muted bg-warning-muted', ribbon: 'bg-warning', icon: 'text-warning' },
    danger: { ring: 'border-danger-muted bg-danger-muted', ribbon: 'bg-danger', icon: 'text-danger' },
  }
  const c = colorBySeverity[alert.severity]

  return (
    <div
      className={`pointer-events-auto absolute left-[7.5%] right-[7.5%] bottom-0 z-30 border ${c.ring} shadow-lg`}
      style={{ borderRadius: '10px 10px 0 0' }}
    >
      <div className={`pointer-events-none absolute inset-x-0 top-0 h-[2px] ${c.ribbon} opacity-75`} />
      <div className="flex items-start gap-3 p-4">
        <AlertTriangle className={`h-5 w-5 shrink-0 mt-0.5 ${c.icon}`} />
        <div className="flex-1 min-w-0">
          <p className={`text-sm font-medium ${c.icon}`}>{alert.title}</p>
          {alert.body && <p className="text-xs text-fg-muted mt-1">{alert.body}</p>}
          {alert.actions.length > 0 && (
            <div className="flex gap-2 mt-3">
              {alert.actions.map((a) => (
                <button
                  key={a.label}
                  type="button"
                  onClick={a.onClick}
                  className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-[6px] text-xs font-medium transition-colors ${
                    a.primary
                      ? 'bg-surface text-fg hover:bg-surface-hover'
                      : 'text-fg-secondary hover:bg-surface-hover'
                  }`}
                >
                  {a.label === 'Reconnect' && <RefreshCw className="w-3.5 h-3.5" />}
                  {a.label === 'Retry' && <RefreshCw className="w-3.5 h-3.5" />}
                  {a.label === 'Dismiss' && <X className="w-3.5 h-3.5" />}
                  {a.label}
                </button>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
```

> **Z-index note:** `z-30` per the spec's risk-mitigation z-stack. Composer popovers will be `z-40`, modals `z-50`+, portaled dropdowns stay at the existing `z-[9999]`.

- [ ] **Step 2: Build + commit**

```bash
cd ui && npm run build
git add ui/src/components/drawers/ChatAlertOverlay.tsx
git commit -m "feat(drawers): add ChatAlertOverlay (z-stacked, no-reflow)"
```

---

### Task 8: Wire `ChatMain` — mount new drawers, remove inline alerts, update wrappers

**Files:**

- Modify: `ui/src/components/chat/ChatMain.tsx`

This is the largest single edit. The existing `ChatMain.tsx` has:

- 4 inline alert blocks (`sessionTakeover`, `streamStalled`, `circuitOpen`, `statusMessage`)
- A `<BottomChatDrawer />` mount at the bottom
- A `<ToolCallDrawer />` mount above the transcript
- The composer wrapped in `<div className="max-w-3xl w-full mx-auto px-4 pb-4 shrink-0">`

We replace those.

- [ ] **Step 1: Update imports**

```tsx
import { ChatPrimaryDrawer } from '@/components/drawers/ChatPrimaryDrawer'
import { ChatWorkingDrawer } from '@/components/drawers/ChatWorkingDrawer'
import { ChatAlertOverlay } from '@/components/drawers/ChatAlertOverlay'
// REMOVE: import { ToolCallDrawer } from './ToolCallDrawer'
// REMOVE: import { BottomChatDrawer, type ScratchpadControls } from '@/components/drawers/BottomChatDrawer'
```

- [ ] **Step 2: Replace the chat layout column with the new structure**

Replace the existing `return` block of `ChatMain` with:

```tsx
return (
  <div className="flex-1 flex min-w-0">
    <main className="flex-1 flex flex-col min-w-0 bg-bg relative">
      <ChatHeader />
      <ChatPrimaryDrawer />
      <ChatTranscript
        messages={messages}
        isStreaming={isStreaming}
        streamingContent={streamingContent}
        onSendMessage={sendMessage}
        onLoadOlder={loadOlderMessages}
        hasOlderMessages={hasOlderMessages}
        loadingOlder={loadingOlder}
      />
      {statusMessage && !sessionTakeover && !streamStalled && !circuitOpen && (
        <div className="w-[85%] max-w-7xl mx-auto px-4 py-1.5 text-xs text-warning animate-pulse">
          {statusMessage}
        </div>
      )}
      <ChatWorkingDrawer />
      <div className="w-[85%] max-w-7xl mx-auto px-4 pb-2 shrink-0 relative">
        <ChatAlertOverlay
          sessionTakeover={sessionTakeover}
          streamStalled={streamStalled && !circuitOpen && !sessionTakeover}
          circuitOpen={circuitOpen}
          statusMessage={null /* handled by inline status row above; overlay only for hard alerts */}
          onReconnect={() => void reconnectStalledStream()}
          onRetry={() => void retryStream()}
          onDismissCircuit={dismissCircuit}
        />
        <ChatComposer
          onSend={sendMessage}
          isStreaming={isStreaming}
          onStop={stopStreaming}
          onEditorReady={onEditorReady}
          reloadMessages={loadMessages}
        />
      </div>
    </main>
    {isTaskSession && taskId && (
      <TaskThreadPanel taskId={taskId} open={taskThreadOpen} onToggle={toggleTaskThread} />
    )}
  </div>
)
```

Remove the 4 standalone alert `<div>` blocks (sessionTakeover / streamStalled / circuitOpen / statusMessage) — `ChatAlertOverlay` replaces them. Remove the `scratchpadControlsRef` ref and the `<BottomChatDrawer onScratchpadRef={…} />` mount; the new `ChatWorkingDrawer` reads its scratchpad controls directly from its own state hooks.

- [ ] **Step 3: Update the `/scratch` slash-command handler in `ChatComposer.tsx`**

The `/scratch` handler in `ChatComposer.tsx` currently calls `scratchpadControlsRef?.current?.append(...)`. Replace the ref-based call with a direct mutation against the new working-drawer scratchpad source. If scratchpad content lives in a hook (`useScratchpad` or similar), call its mutation directly. If not yet, add a small `useScratchpadStore` slice that both ChatWorkingDrawer's ScratchpadTab and the slash command share.

- [ ] **Step 4: Build + manual smoke test**

```bash
cd ui && npm run build
npm run dev
# Manual:
# - Confirm chat surface renders with the new drawers
# - Open ChatPrimaryDrawer via tab click; close via tab click on active
# - Open ChatWorkingDrawer; confirm fixed tabs visible
# - Send a message; if alerts fire (e.g. force a 1m stall), confirm overlay
#   does NOT push the composer up (no reflow)
# - Existing keyboard shortcuts still work
```

- [ ] **Step 5: Commit**

```bash
git add ui/src/components/chat/ChatMain.tsx ui/src/components/chat/ChatComposer.tsx
git commit -m "feat(chat): wire new drawers + overlay; remove inline alerts"
```

---

### Task 9: Width unification + bottom gap reduction

**Files:**

- Modify: `ui/src/components/chat/ChatHeader.tsx:168`
- Modify: `ui/src/components/chat/ChatTranscript.tsx:349`
- Modify: `ui/src/components/chat/ChatMain.tsx` (already partially done in Task 8 — verify)

- [ ] **Step 1: ChatHeader wrapper**

In `ChatHeader.tsx` line 168:

```tsx
// before:
<div className="max-w-3xl w-full mx-auto flex items-center justify-between px-[18px]">

// after:
<div className="w-[85%] max-w-7xl mx-auto flex items-center justify-between px-[18px]">
```

- [ ] **Step 2: ChatTranscript inner wrapper**

In `ChatTranscript.tsx` line 349:

```tsx
// before:
<div className="mx-auto max-w-3xl space-y-5">

// after:
<div className="mx-auto w-[85%] max-w-7xl space-y-5">
```

- [ ] **Step 3: Verify `ChatMain.tsx` composer wrapper**

Confirm Task 8's changes left the composer wrapper at `w-[85%] max-w-7xl mx-auto px-4 pb-2`. The `pb-2` (vs the old `pb-4`) is the bottom-gap halving.

- [ ] **Step 4: Build + visual verification**

```bash
cd ui && npm run build
npm run dev
# Manual: at 1024 / 1440 / 1920 viewports, confirm header / transcript /
# composer all snap to the same column width with consistent L/R margins.
```

- [ ] **Step 5: Commit**

```bash
git add ui/src/components/chat/ChatHeader.tsx ui/src/components/chat/ChatTranscript.tsx ui/src/components/chat/ChatMain.tsx
git commit -m "feat(chat): widen surface to 85% w/ max-w-7xl cap; halve bottom gap"
```

---

### Task 10: Composer DEV indicator polish

**Files:**

- Modify: `ui/src/components/chat/ChatComposer.tsx`

- [ ] **Step 1: DEV pill border-radius**

Locate the DEV indicator JSX (around line 552–558 in the existing file). The `StatusPill` component has its own `rounded-*` class. Either pass a `className` override on `StatusPill` or wrap with custom styling:

```tsx
{developerMode && (
  <div className="pointer-events-none select-none absolute top-2 right-3 z-[2]">
    <StatusPill tone="danger" className="rounded-[4px]">
      <span className="inline-block w-1.5 h-1.5 rounded-full bg-danger animate-pulse shrink-0" />
      DEV
    </StatusPill>
  </div>
)}
```

If `StatusPill` does not yet accept a `className` prop, add it:

```tsx
// in components/chat/envelopes/primitives/StatusPill.tsx
interface StatusPillProps {
  tone: 'primary' | 'success' | 'warning' | 'danger' | 'info'
  className?: string
  children: React.ReactNode
}
// merge incoming className with the default classes via cn() utility
```

- [ ] **Step 2: Editor right-padding when developer_mode is on**

In the `EditorContent` block (around line 670–682):

```tsx
<div className={`bg-bg-elevated px-[14px] pt-[10px] pb-2 ${developerMode ? 'pr-[88px]' : ''}`}>
  <input
    ref={fileInputRef}
    type="file"
    multiple
    className="hidden"
    onChange={(e) => void handleFileUpload(e.target.files)}
  />
  <EditorContent
    editor={editor}
    className="..."
  />
</div>
```

- [ ] **Step 3: Build + visual verify**

```bash
cd ui && npm run build
npm run dev
# Manual: enable developer_mode in Preferences. Type a long message in the
# composer. Confirm:
# - DEV pill is squarer (4px corners)
# - Editor text wraps cleanly to the LEFT of the DEV pill, never under it
```

- [ ] **Step 4: Commit**

```bash
git add ui/src/components/chat/ChatComposer.tsx ui/src/components/chat/envelopes/primitives/StatusPill.tsx
git commit -m "feat(composer): tighten DEV pill radius; wrap editor text around it"
```

---

### Task 11: Composer chrome cleanup — `+` popover, effort cycle pill, send icon, hint removal

**Files:**

- Modify: `ui/src/components/chat/ComposerToolbar.tsx`
- Modify: `ui/src/components/chat/ChatComposer.tsx` (move shell-running state into shared store)

This is a substantial refactor of the toolbar. Split into two files: keep `ComposerToolbar.tsx` as the host, extract the popover into a sibling.

- [ ] **Step 1: Create `ComposerPlusMenu.tsx` (the popover)**

New file `ui/src/components/chat/ComposerPlusMenu.tsx`:

```tsx
import { Plus, Paperclip, Slash, AtSign, Sparkles, Terminal, Zap, Unlock } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { LayoutMenuTrigger, LayoutMenu } from './LayoutMenu'
import { Tooltip } from '@/components/ui/tooltip'

interface Props {
  onAttach: () => void
  onSlash: () => void
  onMention: () => void
  shellMode: 'ask' | 'session' | 'yolo'
  onCycleShell: () => void
  autoSwitchOverride: 'inherit' | 'off' | 'on' | undefined
  onCycleAutoSwitch: () => void
  uploading: boolean
  pluginButtons: React.ReactNode
}

export function ComposerPlusMenu(props: Props) {
  const [open, setOpen] = useState(false)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const popoverRef = useRef<HTMLDivElement>(null)

  // Click-outside dismiss.
  useEffect(() => {
    if (!open) return
    function onDown(e: MouseEvent) {
      if (
        triggerRef.current?.contains(e.target as Node) ||
        popoverRef.current?.contains(e.target as Node)
      ) return
      setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  return (
    <div className="relative">
      <Tooltip content="More" side="top">
        <button
          ref={triggerRef}
          type="button"
          onClick={() => setOpen((o) => !o)}
          className="flex h-7 w-7 items-center justify-center rounded-[6px] bg-surface text-primary font-bold transition-colors hover:bg-surface-hover"
        >
          <Plus size={14} strokeWidth={2.5} />
        </button>
      </Tooltip>
      {open && (
        <div
          ref={popoverRef}
          className="absolute bottom-full left-0 mb-2 z-40 flex flex-wrap items-center gap-1 rounded-[8px] border border-border-subtle bg-bg-elevated p-1.5 shadow-2xl min-w-[300px]"
        >
          <LayoutMenuTrigger ref={null as unknown as React.RefObject<HTMLButtonElement>} open={false} onClick={() => { /* delegate */ }} />
          <button onClick={props.onAttach} className="flex items-center gap-1.5 px-2 py-1 rounded-[4px] text-xs text-fg-muted hover:bg-surface hover:text-fg" disabled={props.uploading}>
            <Paperclip size={14} /> Attach
          </button>
          <button onClick={props.onSlash} className="flex items-center gap-1.5 px-2 py-1 rounded-[4px] text-xs text-fg-muted hover:bg-surface hover:text-fg">
            <Slash size={14} /> Slash
          </button>
          <button onClick={props.onMention} className="flex items-center gap-1.5 px-2 py-1 rounded-[4px] text-xs text-fg-muted hover:bg-surface hover:text-fg">
            <AtSign size={14} /> Mention
          </button>
          <button onClick={props.onCycleShell} className="flex items-center gap-1.5 px-2 py-1 rounded-[4px] text-xs text-fg-muted hover:bg-surface hover:text-fg">
            {props.shellMode === 'yolo' ? <Zap size={14} className="fill-current text-warning" /> : props.shellMode === 'session' ? <Unlock size={14} className="text-primary" /> : <Terminal size={14} />}
            Shell: {props.shellMode}
          </button>
          <button onClick={props.onCycleAutoSwitch} className="flex items-center gap-1.5 px-2 py-1 rounded-[4px] text-xs text-fg-muted hover:bg-surface hover:text-fg">
            <Sparkles size={14} /> Auto-switch: {props.autoSwitchOverride ?? 'inherit'}
          </button>
          {props.pluginButtons}
        </div>
      )}
    </div>
  )
}
```

> **LayoutMenu integration note:** the `LayoutMenu` and its trigger are slightly idiosyncratic (anchorRef, separate open state). Either wire it as its own popover-inside-popover (simplest), or absorb its trigger into the `+` menu's `<button>` row above. Pick the simpler one at implementation; the existing `LayoutMenu.tsx` carries enough internal state to handle either.

- [ ] **Step 2: Replace ComposerToolbar's left-side icon row with the `+` menu and the effort cycle pill**

In `ComposerToolbar.tsx`, replace the entire left-side `<div className="relative flex items-center gap-0.5">…</div>` block with:

```tsx
<div className="relative flex items-center gap-2">
  <ComposerPlusMenu
    onAttach={onAttach}
    onSlash={onSlash}
    onMention={onMention}
    shellMode={shellMode}
    onCycleShell={onCycleShell}
    autoSwitchOverride={autoSwitchOverride}
    onCycleAutoSwitch={cycleAutoSwitch}
    uploading={uploading}
    pluginButtons={pluginButtons.length > 0 ? (
      <>
        <span className="mx-1 h-4 w-px bg-divider" />
        {pluginButtons.map((entry) => {
          const PluginIcon = resolveIcon(entry.icon)
          return (
            <button
              key={entry.id}
              type="button"
              onClick={() => handlePluginAction(entry)}
              className="flex items-center gap-1.5 px-2 py-1 rounded-[4px] text-xs text-fg-muted hover:bg-surface hover:text-fg"
            >
              <PluginIcon size={14} />
              <span>{entry.label}</span>
            </button>
          )
        })}
      </>
    ) : null}
  />

  <span className="h-4 w-px bg-divider" />

  {/* Effort cycle pill */}
  <Tooltip
    content={EFFORT_LEVELS.find((l) => l.value === activeEffort)?.title ?? 'Effort'}
    side="top"
  >
    <button
      type="button"
      onClick={() => {
        const idx = EFFORT_LEVELS.findIndex((l) => l.value === activeEffort)
        const next = EFFORT_LEVELS[(idx + 1) % EFFORT_LEVELS.length]
        setActiveEffort(next.value)
      }}
      className="rounded-[6px] border border-divider px-2.5 py-0.5 font-mono text-[10.5px] uppercase tracking-wider text-fg-secondary transition-colors hover:bg-surface"
    >
      {EFFORT_LEVELS.find((l) => l.value === activeEffort)?.label.toUpperCase() ?? 'NORM'}
    </button>
  </Tooltip>

  <span className="h-4 w-px bg-divider" />

  {/* Model picker — unchanged from existing implementation */}
  <div ref={modelRef}>
    {/* ... existing model picker JSX, unchanged ... */}
  </div>
</div>
```

- [ ] **Step 3: Replace the right-side send hint + button**

In `ComposerToolbar.tsx`, replace the right-side block with:

```tsx
import { ArrowBigUp, Square } from 'lucide-react'

<div className="flex items-center gap-2">
  {isStreaming ? (
    <Tooltip content="Stop generating" side="top">
      <button
        type="button"
        onClick={onStop}
        className="flex h-7 w-7 items-center justify-center rounded-[6px] text-danger transition-colors hover:bg-surface"
      >
        <Square size={14} />
      </button>
    </Tooltip>
  ) : (
    <Tooltip content="Send (Enter)" side="top">
      <button
        type="button"
        onClick={onSend}
        disabled={!hasContent}
        className={`flex h-7 w-7 items-center justify-center rounded-[6px] transition-colors ${
          hasContent
            ? 'bg-primary text-primary-foreground hover:bg-primary-hover'
            : 'cursor-default text-fg-faint'
        }`}
      >
        <ArrowBigUp size={16} strokeWidth={2.5} />
      </button>
    </Tooltip>
  )}
</div>
```

The `⌘↵ to send` `<span>` is removed.

- [ ] **Step 4: Move shell-running state out of ChatComposer into a shared store**

Create `ui/src/stores/useShellStore.ts`:

```ts
import { create } from 'zustand'

interface ShellState {
  output: string
  running: boolean
  pendingCommand: string | null
  appendOutput: (chunk: string) => void
  setRunning: (b: boolean) => void
  setPendingCommand: (cmd: string | null) => void
  clearOutput: () => void
}

export const useShellStore = create<ShellState>((set) => ({
  output: '',
  running: false,
  pendingCommand: null,
  appendOutput: (chunk) => set((s) => ({ output: s.output + chunk })),
  setRunning: (b) => set({ running: b }),
  setPendingCommand: (cmd) => set({ pendingCommand: cmd }),
  clearOutput: () => set({ output: '' }),
}))
```

In `ChatComposer.tsx`:

- Remove the local `shellRunning` and `pendingShellCommand` `useState` declarations.
- Replace with `useShellStore` selectors.
- Update `executeShellCommand`/`handleShellExec` to call `setRunning`, `appendOutput` (when streamed output arrives — wire to the existing shell-exec response stream), and `setPendingCommand`.
- **Remove the `shell-running` banner JSX entirely** (the `{shellRunning && …}` block). Its content now lives in `ChatWorkingDrawer`'s `Terminal1Tab`.

- [ ] **Step 5: Build + manual verification**

```bash
cd ui && npm run build
npm run dev
# Manual:
# - + popover opens; click each item; confirm wired correctly
# - Effort pill cycles LOW → NORM → HIGH → MAX → LOW
# - Send button sends (no hint visible)
# - Type !echo hi; confirm Terminal 1 tab populates with output;
#   composer's old shell-running banner is gone
# - Plugin slot still works if any plugin registers composer-toolbar
```

- [ ] **Step 6: Commit**

```bash
git add ui/src/components/chat/ComposerToolbar.tsx ui/src/components/chat/ComposerPlusMenu.tsx ui/src/components/chat/ChatComposer.tsx ui/src/stores/useShellStore.ts
git commit -m "feat(composer): consolidate toolbar to + popover + cycle pill; move shell to working drawer"
```

---

### Task 12: Retire old files

**Files:**

- Delete: `ui/src/components/drawers/BottomChatDrawer.tsx`
- Delete: `ui/src/components/chat/ToolCallDrawer.tsx`

- [ ] **Step 1: Verify no remaining imports**

```bash
cd ui
grep -rn "BottomChatDrawer" src/ || echo "no consumers — safe to delete"
grep -rn "ToolCallDrawer" src/ || echo "no consumers — safe to delete"
```

If any imports remain, fix them. `ChatMain.tsx` from Task 8 should be the only consumer of `BottomChatDrawer` and `ToolCallDrawer`, both replaced.

- [ ] **Step 2: Delete the files**

```bash
rm ui/src/components/drawers/BottomChatDrawer.tsx
rm ui/src/components/chat/ToolCallDrawer.tsx
```

- [ ] **Step 3: Drop now-unused layout-store fields**

In `useLayoutStore.ts`, the existing `bottomChatDrawerOpen` and `setBottomDrawerOpen` are no longer referenced (replaced by `chatWorkingDrawer`). Remove them.

Also remove the `defaultDrawerTab` field if no consumer uses it after Task 11 — check `grep -rn "defaultDrawerTab" src/` first; if used by `PreferencesPanel.tsx` for the working-drawer default tab pref, keep it but rename to `chatWorkingDrawerDefaultTab`.

- [ ] **Step 4: Drop the obsolete `ToolDrawerState` type and related fields**

If `toolDrawerEnabled`, `toolDrawerState`, `toolDrawerHeight`, `setToolDrawerState`, `setToolDrawerHeight`, `toggleToolDrawer` are no longer referenced (Tools is now a tab, not a separate drawer), remove them. Verify with grep first.

- [ ] **Step 5: Build + final smoke test**

```bash
cd ui && npm run build
npm run dev
# Full smoke test of the chat surface — open/close drawers, send messages,
# fire alerts, check tab pagination with multiple dynamic cards.
```

- [ ] **Step 6: Commit**

```bash
git add -A ui/src/
git commit -m "chore(chat): retire BottomChatDrawer + ToolCallDrawer (replaced by new drawers)"
```

---

### Task 13: Submit-arrow bug — repro and fix

**Files:**

- Investigate: `ui/src/components/chat/ChatComposer.tsx` (`handleSend`, `editor.getText()`, focus)
- Investigate: `ui/src/components/chat/ComposerToolbar.tsx` (the send button's `onClick={onSend}` wiring)

- [ ] **Step 1: Reproduce**

```bash
cd ui && npm run dev
# Open the running app. Type text into the composer. Click the send arrow.
# Observe: does it fire? Does it fire only once and then stop? Does it require
# a second click? Does pressing Enter still work?
```

Note the exact failure mode. Three plausible root causes:

1. **Focus race:** clicking the button defocuses the editor, `editor.getText()` may return a stale value or empty string for a tick.
2. **Stale handler reference:** `onSend={handleSend}` captures `handleSend` at render time; if `editor.getText()` is closed over an out-of-date editor instance from a previous render, it returns wrong text.
3. **Pointer-events / overlay:** something in the new chrome (popover layer) intercepts the click — but this regression would be new from Task 11, so verify against pre-Task-11 git history if needed.

- [ ] **Step 2: Add a temporary console log to confirm the failure mode**

In `handleSend` (around line 460 of `ChatComposer.tsx`):

```tsx
const handleSend = useCallback(() => {
  if (!editor) {
    console.warn('[handleSend] no editor instance')
    return
  }
  const text = editor.getText().trim()
  console.log('[handleSend] text=', JSON.stringify(text))
  if (!text) return
  // ... rest unchanged
}, [editor, onSend, handleShellExec, flushIfDirty])
```

Run, click send. Confirm what's logged.

- [ ] **Step 3: Apply the fix based on the observed mode**

Most likely (focus race): defer `editor.getText()` by one frame, or call `editor.commands.focus()` first to flush pending changes:

```tsx
const handleSend = useCallback(() => {
  if (!editor) return
  // Flush any in-flight selection/composition before reading text.
  editor.commands.focus()
  const text = editor.getText().trim()
  if (!text) return
  // ... rest unchanged
}, [editor, onSend, handleShellExec, flushIfDirty])
```

If stale handler reference (`handleSend` captures an old `editor`):

```tsx
// handleSendRef pattern is already in place; ensure ComposerToolbar's
// onSend prop calls handleSendRef.current() rather than the captured handleSend.
```

In `ChatComposer.tsx`'s render block, when passing `onSend` to `ComposerToolbar`:

```tsx
<ComposerToolbar
  onSend={() => handleSendRef.current()}
  // ...
/>
```

- [ ] **Step 4: Remove the temporary console log; verify fix**

```bash
cd ui && npm run dev
# Click send 5+ times in succession with different content; confirm every
# click fires. Confirm Enter still works. Confirm Cmd+Enter (which currently
# is NOT bound — should be a no-op given the hint is removed) still no-ops.
```

- [ ] **Step 5: Commit**

```bash
git add ui/src/components/chat/ChatComposer.tsx ui/src/components/chat/ComposerToolbar.tsx
git commit -m "fix(composer): submit arrow click reliably fires send"
```

---

### Task 14: Manual verification pass

This is the spec's "must-pass before merge" checklist. No code; just verification. Treat each line as a checkbox.

- [ ] **Drawer open/close:** click tab, double-click handle, drag resize (up to 100% viewport height per spec), close-arrow icon — both `ChatPrimaryDrawer` and `ChatWorkingDrawer`.

- [ ] **Tab pagination:** load 6+ dynamic card-tabs by emitting envelopes with `render_target=bottom_chat_drawer` (use a test plugin or manual API hit). Confirm:
  - Native scrollbar is hidden.
  - Left/right arrows appear on overflow.
  - Click arrow → tab strip scrolls smoothly.

- [ ] **Alert overlay z-stack:**
  - With `ChatWorkingDrawer` **closed**, force a stream-stalled alert. Confirm overlay appears flush against composer top, no layout reflow.
  - With `ChatWorkingDrawer` **open**, force the same alert. Confirm overlay sits on top of the drawer body, not pushing it.
  - Open the model picker dropdown while alert is showing. Confirm dropdown layers above the alert (existing `z-[9999]`).

- [ ] **Width responsiveness:** at 1024 / 1440 / 1920 / 2560 viewports, confirm header / transcript / composer all match column width. At 2560, confirm `max-w-7xl` cap kicks in (1280px content).

- [ ] **DEV indicator text-wrap:** with `developer_mode=true`, type a long message in the composer. Confirm text wraps cleanly to the left of the DEV pill.

- [ ] **Composer popovers:**
  - Click `+` → popover opens; click outside → closes.
  - Effort cycle pill cycles all 4 levels.
  - Both popovers' z-stack above the alert overlay (`z-40` vs `z-30`).
  - Keyboard: tab into popover items, Enter to invoke.

- [ ] **Send button:** the existing bug is fixed. Click 5+ times with different content; every click sends.

- [ ] **Regression watch:**
  - Pinned-card lifecycle: pin a transient card; confirm it appears as a tab in `ChatPrimaryDrawer`. Unpin; confirm it disappears.
  - Envelopes with `render_target=bottom_chat_drawer` create new tabs; previous tabs not replaced.
  - Multi-session streaming presence pips unaffected.
  - Keyboard shortcuts: `Cmd+B`, `Cmd+L`, `Cmd+/`, `Cmd+N`, `Cmd+K`, `Cmd+]`, `Cmd+[`, `Cmd+D`, `Cmd+.` all still wired.

If anything fails, file a fix-task back to the relevant earlier task and re-run.

---

## Self-review

**1. Spec coverage** — every requirement in `2026-05-01-chat-surface-redesign-design.md` maps to a task above:

- Architecture (3 regions): Tasks 5, 6, 7
- Tab assignments: Tasks 5, 6 (tab definitions), 8 (wire-up)
- Composer cleanup: Tasks 10, 11
- Width unification: Task 9
- Bottom gap: Task 9
- DEV indicator: Task 10
- Banners moved out of composer: Task 11 (shell-running → Terminal 1)
- Migration map: Tasks 4, 5, 6, 7, 8, 12
- Submit-arrow bug: Task 13
- Verification: Task 14

No orphaned spec requirements.

**2. Placeholder scan** — no "TBD" / "TODO" / "implement later" without specifics. The Reports/Diffs tabs in Task 5 carry a brief deferral ("filtered from session messages") because the exact filter source is straightforward but won't be wired until consumed; if the implementer wants concrete code, query `useChat`'s `messages` for envelopes with `kind === 'report-card'` / `kind === 'diff-card'` and render via `EnvelopeRenderer`.

**3. Type consistency** — `ChatDrawerState`, `DynamicCardTab`, `ChatDrawerTab` props names are stable across tasks. Tab IDs follow conventions: fixed tabs use kebab-case strings (`scratchpad`, `terminal-1`), dynamic tabs use prefixed UUIDs (`pin:<id>`, `card:<id>`).

**4. Dependency order** — Task 1 (state) → Task 2 (types) → Task 3 (TabStrip) → Task 4 (ToolCallBanner extract) → Tasks 5, 6, 7 (drawers, parallel) → Task 8 (wire ChatMain) → Tasks 9, 10, 11 (polish, parallel) → Task 12 (retire old files) → Task 13 (bug fix) → Task 14 (verify). No backward dependencies.

---

## Execution handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-01-chat-surface-redesign.md`.

Tasks in this plan are mostly sequenced; the new-drawer tasks (5, 6, 7) are independent of each other and could parallelize via sub-agent dispatch. Tasks 9, 10, 11 are also parallelizable (different files). For the rest, inline execution in the main agent is lower overhead.

Next steps are up to you:

- Run `sp-executing-plans` to execute the plan inline in this session.
- Run `sp-subagent-driven-development` to dispatch sub-agents per task — your `feedback_use_subagents` memory says to prefer this for execution.
- Pick up the plan yourself in a future session.

This skill is done. Your call on when and how to execute.
