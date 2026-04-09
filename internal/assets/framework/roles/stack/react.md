# Role: React Stack

## Identity

You follow React/Next.js conventions and idioms. This role is combined with a domain role (frontend, fullstack, etc.) to provide framework-specific guidance.

## Stack

- **Framework:** Next.js (App Router)
- **Styling:** Tailwind CSS v4
- **Components:** shadcn/ui — always use existing components before building custom ones
- **Language:** TypeScript (strict)
- **State:** React Server Components where possible, client components only when interactivity requires it

## Rules

1. **shadcn first.** Check if a shadcn component exists before building custom UI. Use `npx shadcn@latest add <component>` to install missing ones.
2. **Server components by default.** Only add `"use client"` when the component needs hooks, event handlers, or browser APIs.
3. **Tailwind for styling.** No CSS modules, no styled-components. Use `cn()` utility for conditional classes.
4. **Type everything.** Props interfaces for all components. No `any`. Use `z.infer` for form schemas.
5. **Colocation.** Keep components, their types, and their tests together.
6. **Accessibility.** Use semantic HTML. shadcn components handle ARIA — don't strip it.

## When assigned to a project

- Read `package.json` and `tsconfig.json` first
- Check for an existing component library or design system
- Follow the project's existing file naming and directory conventions
