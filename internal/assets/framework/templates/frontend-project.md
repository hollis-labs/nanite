# Frontend Context — {PROJECT_NAME}

> Project-specific frontend conventions. Loaded as agent context when working in this project.
> Lives at `<project>/.nanite/agents/frontend.md`.

## Stack

- **Framework:** {Next.js App Router / Vite / etc.}
- **Styling:** {Tailwind v4 / etc.}
- **Components:** {shadcn/ui / custom library / etc.}
- **State:** {Server Components / Zustand / React Query / etc.}
- **Forms:** {React Hook Form + Zod / etc.}

## Project Structure

```
{Describe the relevant directory layout}
src/
├── app/              # Next.js app router pages
├── components/       # Shared components
│   ├── ui/           # shadcn primitives
│   └── {domain}/     # Domain-specific components
├── lib/              # Utilities, API clients
└── types/            # Shared TypeScript types
```

## Component Inventory

> List the key reusable components and when to use them.

| Component | Location | Use for |
|-----------|----------|---------|
| {Shell} | `components/shell.tsx` | Page-level layout wrapper |
| {Panel} | `components/ui/panel.tsx` | Card/section containers |
| {DataTable} | `components/data-table.tsx` | Tabular data display |

## Patterns to Follow

### Composition
- Favor composition over configuration. Build complex UI from small, focused components.
- Push props down. Parent components own data; children render it.
- Use `children` and render props over complex prop APIs.

### Component Design
- One component per file. Export the component as the default.
- Props interface defined at the top of the file, named `{Component}Props`.
- Colocate component, types, and tests.

### Data Flow
- {Describe how data flows — server components fetch, client components receive via props, etc.}

### Styling
- Use Tailwind utility classes directly. No CSS modules.
- Use `cn()` for conditional classes.
- {Any project-specific design tokens, spacing conventions, color usage}

## Anti-Patterns to Avoid

- **Prop tunneling** — Don't pass props through 3+ layers unchanged. Extract a shared component or use composition.
- **God components** — If a component file exceeds ~200 lines, it likely needs decomposition.
- **Inline business logic** — Keep business logic in `lib/` or hooks. Components should render, not compute.
- **Duplicate components** — Check the inventory above before creating anything new.
- {Project-specific anti-patterns discovered during audit}

## Reference Implementations

> Point the agent at canonical examples of well-structured components.

| Pattern | Reference File | Why it's good |
|---------|---------------|---------------|
| {Data display} | `components/{example}.tsx` | {Clean composition, proper prop types} |
| {Form pattern} | `components/{example}.tsx` | {Zod schema, proper error handling} |

## Design Tokens / Visual Rules

- {Color palette, spacing scale, typography if not covered by Tailwind config}
- {Any brand-specific rules}

## Notes

- {Anything else the frontend agent should know about this project}
