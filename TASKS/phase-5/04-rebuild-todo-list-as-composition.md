# Rebuild `todo-list` as a `list-card` composition

**Phase:** 5
**Status:** not-started
**Depends on:** none
**Touches:** `libs/go-envelopes/manifest/envelopes.yaml` (external module, local `replace` directive — remove the standalone `todo-list` entry), `ui/src/components/chat/envelopes/TodoListCard.tsx` (retire or fold into `list-card`'s component), `ui/src/components/chat/envelopes/primitives/` (the `list-card` primitive component), `internal/mcp/self_tools_transport.go:1074` (the `"type": "todo-list"` emitter — update to emit `list-card` with a status field/discriminator instead)

## Context

Architecture doc `08-cards.md`: *"`todo-list`, `plan-review`, and `subagent-spawn-approval` are being rebuilt this way [composed from the primitive set] rather than staying separate types."* Decision log §36: *"`todo-list` → `list-card` + status field."*

### Current implementation, verified

`TodoListCard.tsx` — `data: {scope, scope_id, title?}`. **The envelope itself is just a pointer/trigger, not a data payload** — the component fetches live via `useTodos({scope, scope_id})` (a `useQuery` hook against a real REST endpoint) and mutates via `useToggleTodo()`. This live-refetch behavior must be preserved by the composition — `list-card` is not currently built to re-fetch its own data, so this rebuild needs either a `list-card` variant that supports a live-data-source prop, or a thin wrapper component that fetches and then renders through `list-card`'s presentation layer. Emitted from `internal/mcp/self_tools_transport.go:1074`.

## What to do

1. Confirm `list-card`'s current schema/component (`ui/src/components/chat/envelopes/primitives/`) and decide how it accepts a status field per item (checkbox/done-state) — extend the schema if it doesn't already support this, following the existing `props` discriminator pattern.
2. Decide the live-refetch question above and implement: either extend `list-card` to accept a live-data-source configuration, or keep a thin `TodoListCard`-shaped wrapper that fetches via `useTodos` and renders through `list-card`'s presentation internals (not a full parallel component) — document the choice in this file's Work Log.
3. Update the manifest (`libs/go-envelopes/manifest/envelopes.yaml`) to remove the standalone `todo-list` type once the composition is live — run `node scripts/generate-plugin-imports.mjs` (or confirm it runs automatically) to regenerate `ui/src/generated/plugin-envelopes.ts`.
4. Update `self_tools_transport.go:1074`'s emitter to produce the new composed shape.
5. Verify toggle/mutation behavior (`useToggleTodo`) still works identically through the composed card.

## Done means

- `todo-list` no longer exists as a standalone manifest entry; its behavior is fully reproduced via `list-card` + composition.
- A real todo list still renders and toggles correctly in a live chat session, streaming and post-reload.
- `cd ui && npm run build` passes; `node scripts/generate-plugin-imports.mjs --check` passes.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
