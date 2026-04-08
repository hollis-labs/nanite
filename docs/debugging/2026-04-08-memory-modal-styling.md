# Memory Modal Styling Bug — Investigation Notes

**Date:** 2026-04-08
**Status:** UNRESOLVED
**Component:** `ui/src/components/memory/MemoryModal.tsx` (and children)

## Symptom

The `/memory` slash command opens a modal that renders with **zero Tailwind/theme styles** — white background, default system fonts, no dark theme, no spacing. It looks like completely raw/unstyled HTML. The layout/structure is correct (header, search, filters, empty state all render), but no CSS from the app is applied.

All other dialogs in the app (ToolDashboard detail, server form, delete confirmation, import dialog, CommandPalette, SearchModal) render correctly with full dark theme styling using the exact same `Dialog`/`DialogContent` component from `ui/src/components/ui/dialog.tsx`.

Screenshot shows: white background, black text, no border-radius, no dark theme — as if Tailwind classes are present in the HTML but the corresponding CSS rules don't exist.

## Architecture Context

### Two servers running

| Port | Process | Purpose | Serves frontend from |
|------|---------|---------|---------------------|
| 8090 | Go binary (`nanite`) | API + embedded SPA | `internal/server/ui_dist/` via `//go:embed` |
| 5176 | Node (Vite dev server) | HMR frontend dev | `ui/src/` live |

- **Cerberus** manages the Go binary on 8090. `cerberus_rebuild` runs `go install` which embeds `internal/server/ui_dist/`.
- The Go binary has a `-dev` flag that skips embedded UI, but the production path embeds from `internal/server/ui_dist/`.
- The user accesses the app at **localhost:5176** (Vite dev server).

### Frontend build pipeline

```
ui/src/ → (npm run build) → ui/dist/ → (manual copy) → internal/server/ui_dist/ → (go:embed) → Go binary
```

**Critical gap:** There is no local script that copies `ui/dist/` → `internal/server/ui_dist/`. The Dockerfile does this (`COPY --from=ui-build /app/ui/dist ./internal/server/ui_dist/`) but nothing in the local dev flow does it. The embed directory had a **stale placeholder HTML** (`<title>Mentat Chat</title>`, `<div id="app-root">`) that was never the real Vite build output.

### CSS/Tailwind setup

- **Tailwind CSS v4.2.1** with `@tailwindcss/vite` plugin
- **Entry:** `ui/src/index.css` — `@import "tailwindcss"` with `@theme` block defining semantic color tokens
- **Dark theme:** CSS variables on `:root` (dark by default), `.light` class for light theme
- **Custom variant:** `@custom-variant dark (&:where(.dark, .dark *))` — `dark:` prefix requires `.dark` class on ancestor
- **Theme application:** `useLayoutStore` adds `.dark`/`.light` class to `document.documentElement`
- **No `@source` directive** — Tailwind v4 relies on auto-discovery of source files

### Dialog component

- `ui/src/components/ui/dialog.tsx` — wraps Radix `DialogPrimitive`
- Uses `DialogPrimitive.Portal` which renders into a portal (default: `document.body`, currently modified to target `document.getElementById('root')`)
- Base DialogContent class: `bg-bg-elevated` (was `bg-white dark:bg-bg-elevated`, changed during debugging)

### MemoryModal rendering

- Rendered unconditionally in `AppShell.tsx` line 171: `<MemoryModal />`
- State managed via Zustand store: `useLayoutStore.memoryModalOpen`
- Opens via `/memory` slash command → `useLayoutStore.getState().setMemoryModalOpen(true)`
- Uses same `Dialog`/`DialogContent` as all other working modals

## What was tried (all failed)

| # | Change | File | Result |
|---|--------|------|--------|
| 1 | Changed `bg-white dark:bg-bg-elevated` → `bg-bg-elevated` | `dialog.tsx` | No change |
| 2 | Added `!important` overrides (`!bg-bg`, `!gap-0`, `!p-0`) | `MemoryModal.tsx` | No change |
| 3 | Added inline `style={{ backgroundColor: 'var(--color-bg)' }}` | `MemoryModal.tsx` | No change |
| 4 | Hardcoded `!bg-[#09090b]` | `MemoryModal.tsx` | No change |
| 5 | Added `DialogTitle` + `DialogDescription` (sr-only) for Radix accessibility | `MemoryModal.tsx` | No change |
| 6 | Changed `DialogPortal` to render into `#root` instead of `document.body` | `dialog.tsx` | No change |
| 7 | Removed `showCloseButton={false}` and matched ToolDashboard dialog pattern exactly | `MemoryModal.tsx` | No change |
| 8 | Changed select elements to `appearance-none bg-bg-elevated` | `MemoryDetail.tsx` | No change |
| 9 | Tested in Safari (never visited site before) | — | Same broken styling |
| 10 | Cmd+Shift+R hard refresh | — | No change |
| 11 | Killed stale Vite dev server (PID 74976) | — | Unknown (server may have restarted) |
| 12 | Copied `ui/dist/` → `internal/server/ui_dist/` + Cerberus rebuild | — | No change on 5176 |

## Hypotheses explored

### 1. Radix Portal renders outside styled DOM ❌
Radix Portal renders to `document.body` by default. Changed to render into `#root`. Didn't help. Also, other dialogs use the same Portal and work fine.

### 2. `dark:` variant not activating ❌
Changed to use non-dark-prefixed classes (`bg-bg-elevated` instead of `dark:bg-bg-elevated`). Didn't help. The issue is ALL styles missing, not just dark-mode-specific ones.

### 3. Tailwind v4 file discovery (subagent hypothesis) ⚠️ PLAUSIBLE
Tailwind v4 with `@tailwindcss/vite` auto-discovers source files. If the `memory/` directory was created after the Vite dev server started, the new files might not be scanned for class names. This would explain why the classes are in the HTML but no corresponding CSS rules exist.

**Evidence for:** No `@source` directive in `index.css`. The `memory/` directory is brand new (created this session). Tested in fresh Safari — same result (not a browser cache issue).

**Evidence against:** The Vite dev server was killed and presumably a new one started. Build output (`ui/dist/`) contains all the correct CSS classes.

### 4. Stale Vite dev server ⚠️ PLAUSIBLE
A Vite dev server (PID 74976) was running on port 5176 from before the memory components were created. It was killed, but a new process may have appeared on the same port (from Cerberus or another tool). If the new server also has stale state, the issue persists.

### 5. Two servers serving different content ⚠️ CONFIRMED ISSUE
Port 5176 (Vite) and port 8090 (Go) serve different frontend builds. The Go binary on 8090 was serving a stale placeholder HTML until the embed directory was updated. The user primarily uses 5176. The relationship between these two servers and which one should be canonical for development is unclear.

## Current file state

### `ui/src/components/ui/dialog.tsx` (DialogContent base class)
```
bg-bg-elevated (was bg-white dark:bg-bg-elevated)
```
Portal targets `document.getElementById('root')` (was default `document.body`).

### `ui/src/components/memory/MemoryModal.tsx`
```tsx
<DialogContent className="sm:max-w-2xl max-h-[80vh] flex flex-col p-0 overflow-hidden">
  <DialogTitle className="sr-only">Memories</DialogTitle>
  <DialogDescription className="sr-only">Browse and manage agent memories</DialogDescription>
  {/* MemoryBrowse or MemoryDetail */}
</DialogContent>
```

### `ui/src/components/memory/MemoryBrowse.tsx`
Uses theme tokens: `bg-bg-elevated`, `border-border-subtle`, `text-fg`, `text-fg-faint`, `bg-surface/40`, `bg-indigo-500/20`, `text-indigo-300`.

### `ui/src/components/memory/MemoryDetail.tsx`
Uses `selectClass` with `appearance-none bg-bg-elevated`, `inputClass` with `bg-bg-elevated border-border-subtle`.

## Next steps to investigate

1. **Check if a Vite dev server is still running on 5176** — is it a fresh process or the original stale one?
2. **Check Tailwind CSS output in the dev server** — curl `http://localhost:5176/src/index.css` and search for memory-component-specific classes (e.g., `line-clamp-2`, `bg-indigo-500/20`)
3. **Inspect browser DevTools** — look at the actual computed styles on the dialog element. Are Tailwind classes in the HTML but no matching CSS rules? Or are the classes missing too?
4. **Add `@source` directive** to `index.css`: `@source "../src/**/*.{ts,tsx}";` to force Tailwind to scan all files
5. **Check if the Go binary `-dev` flag** is set — if so, it skips embedded UI and proxies to... what port?
6. **Clarify the intended dev workflow** — should developers use 8090 (Go) or 5176 (Vite)? Which one does Cerberus manage?
