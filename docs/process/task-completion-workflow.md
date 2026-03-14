# Task Completion Workflow

When an agent is ready to transition a task to "done", it MUST follow this checklist before calling `volon_task_transition`. This ensures consistent quality gates across all agents and profiles.

## Backend Changes (Go)

1. **Scoped lint** — run only on changed files to keep feedback fast:
   ```bash
   golangci-lint run --new
   ```
2. **Scoped tests** — run tests for changed packages:
   ```bash
   go test ./changed/pkg/...
   ```
   Replace `./changed/pkg/...` with the actual packages you modified.
3. **Verify build** — confirm the whole project still compiles:
   ```bash
   go build ./...
   ```
4. If any step fails, fix before proceeding. Do not transition to "done" with failing checks.

## Frontend Changes (ui/)

1. **Lint and format** — from the `ui/` directory:
   ```bash
   npx biome check .
   ```
2. **Type check** — confirm no TypeScript errors:
   ```bash
   npx tsc --noEmit
   ```
3. If any step fails, fix before proceeding.

## All Changes

After quality checks pass:

1. **Transition task** — `volon_task_transition` to "done"
2. **Attach artifacts** — if the task produced noteworthy findings (performance numbers, API changes, migration notes), attach them via `volon_task_artifact_create`
3. **Notify lead** — if the change is cross-project or notable, send an inbox message:
   ```
   /send-message lead info <TASK-ID> done — <one-line summary>
   ```
4. **Sprint auto-close** — the system handles this automatically (see TASK-198). No manual action needed, but be aware that completing the last task in a sprint may trigger closure.

## When to Skip

- **Docs-only or process-only tasks** (like this one): skip the lint/test/build steps. Still transition in Volon and notify if notable.
- **Research tasks**: no code changes expected. Transition and attach findings as artifact.
