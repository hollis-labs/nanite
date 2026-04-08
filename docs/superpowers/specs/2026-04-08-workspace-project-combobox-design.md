# Workspace / Project Unified Combobox

**Status:** Approved  
**Scope:** Frontend only  
**Date:** 2026-04-08

## Summary

Replace the NavRail workspace selector and sidebar ProjectDropdown with a single unified combobox in the sidebar header. Replace the NavRail workspace icon with a branded "N" that navigates home.

## Changes

### NavRail
- Replace workspace selector (icon + dropdown) with a static **N** logo button
- N uses the theme's primary/brand color (`bg-primary text-white`), italic, bold
- Click navigates to home/main screen (sets `currentPage: 'chat'`, clears active session)
- Remove: workspace dropdown, chevron overlay, workspace create flow from NavRail

### Sidebar Header
- Replace `ProjectDropdown` with a unified selector trigger showing:
  - Folder icon (primary muted)
  - Project name (bold) — or "All Chats" if no project selected
  - Workspace name (muted subtitle)
  - Chevron
- Click opens a combobox popover (shadcn `Command` component)

### Combobox Popover
Built on shadcn's `Command` (cmdk) component.

**Search:** `CommandInput` at top — filters both workspaces and projects by name.

**Filter bar:** Two equal-width 50/50 divs below search, separated by the standard combobox border. Each div is fully clickable, text centered. Active state: `bg-primary/15 text-primary`. Inactive: `bg-surface text-fg-muted`.

**Guard logic:** At least one filter must be active. If only one is active and user clicks it to deactivate, the other activates instead. Both can be active simultaneously.

**Items:** Grouped by type when both filters active. Each item shows:
- Workspace items: letter initial in `bg-surface-hover` badge
- Project items: folder icon in `bg-primary/15` badge, or chat icon for "All Chats"
- Checkmark for currently selected workspace/project

**Selection behavior:**
- Clicking a workspace: switches active workspace, resets project to "All Chats", closes popover
- Clicking a project: switches active project filter, closes popover
- "All Chats" clears project filter

**Footer:** Keyboard hint bar (`↑↓ navigate`, `Enter select`, `Esc close`).

**Scrollbar:** Uses site's custom scrollbar CSS (already in `index.css`).

## Files to Modify

| File | Change |
|------|--------|
| `components/NavRail.tsx` | Replace workspace selector with N logo button, remove workspace dropdown/state |
| `components/sidebar/LeftSidebar.tsx` | Replace `<ProjectDropdown>` with `<ScopeSelector>` |
| `components/sidebar/ScopeSelector.tsx` | **New** — unified combobox component |
| `components/sidebar/ProjectDropdown.tsx` | Delete (replaced by ScopeSelector) |

## Not In Scope

- Workspace CRUD (create/rename/delete) — moves to Settings or future spec
- Keyboard shortcut to open combobox — future enhancement
- Project creation from combobox — users go to Settings > Projects
