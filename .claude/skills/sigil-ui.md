# Sigil UI Generation (/sigil-ui)

Generate UI pages/components using Sigil's declarative YAML system. Creates framework-specific code (React/shadcn or Go/Templ) from a description.

## When to use

- When the user asks to create a new UI page, dashboard, or view
- When you need to generate a quick data table, form, or management page
- When the user says "build me a UI for X", "create a page for Y", "/sigil-ui"
- When prototyping UI for a new feature before committing to hand-coded components

## Arguments

- `$ARGUMENTS` — Description of the UI to generate (e.g., "sprint review dashboard with task table and metrics")

## Procedure

### Step 1: Design the config

Based on the user's description, design a Sigil page config. Consider:
- What data does this page display? → Define datasources with fields and endpoints
- What layout? → rows, columns, grid, split, tabs
- What components? → data-table, stat-card, chart, form, etc.
- What actions? → buttons that navigate, open modals, make HTTP calls
- What keyboard shortcuts? → Ctrl+N for new, Ctrl+F for search, etc.

### Step 2: Create via MCP

Use Sigil MCP tools (if available):
- `sigil_create_datasource` — create datasource manifest
- `sigil_create_page` — create page config
- `sigil_validate` — validate the config
- `sigil_preview` — generate preview

If Sigil MCP is not available, write the YAML config directly to the project's `.sigil/pages/` directory.

### Step 3: Generate code

Ask user which target:
- `react-shadcn` — for Conduit/React projects
- `go-templ` — for Volon/Go projects

Run: `sigil generate --target <target> --output <dir>`

Or via MCP: `sigil_generate` with target and output path.

### Step 4: Review with user

Show the generated code. Ask if it meets their needs. If they like it, integrate into the project. If they want changes, modify the YAML config and regenerate.

## Config Quick Reference

```yaml
sigil: "1.0"
kind: page
id: my-page
title: "My Page"
layout:
  type: rows
  props: { gap: 6, padding: 6 }
  children:
    - type: heading
      props: { text: "Title", level: 1 }
    - type: data-table
      props:
        datasource: MyData
        columns:
          - field: name
            label: Name
          - field: status
            label: Status
            render: status-chip
```

## Component Types (49 available)

**Primitives:** heading, text, button, input, textarea, select, checkbox, switch, badge, avatar, separator, progress, alert, label, icon, icon-button
**Layouts:** rows, columns, grid, card, tabs, tab, split, sidebar, accordion, accordion-item, scroll-area, spacer
**Navigation:** breadcrumb, pagination, nav-menu, command-palette
**Data:** data-table, list, detail-view, stat-card, chart, timeline, search-bar
**Composites:** modal, sheet, dropdown-menu, context-menu, tooltip, popover, confirm-dialog, toast
**Forms:** form, field-group

## Invariants

- Always validate config before generating
- Always show generated code to user before integrating
- Use datasource manifests for any data-backed components
- Prefer React/shadcn target for Conduit, Go/Templ for Volon
