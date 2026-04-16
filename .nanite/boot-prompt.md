# Session Boot Prompt — Nanite

> **Session state for remote-booted agents lives in `agent-workspaces/boot/nanite/boot-prompt.md`**, not here. This file is a quick-reference fallback for agents booted directly in this repo.

## Current state

- **Phase 3 — Core Features** in progress. Phase 2 (plugin system) closed 2026-04-14.
- **`main` tip:** `1f3d7eb` — S4a merged (PR #50).
- **Completed sessions:** S5 (envelope), S2a (memory), S3a (context pipeline), S4a (tool broker).
- **Next candidates:** S3b (tool cache hotswap), S4b (trust boundary hardening).

## For full session context

Read `agent-workspaces/boot/nanite/boot-prompt.md` — it has the complete session status table, worktree state, guardrails, and backlog discipline.

## Quick reference

- Build: `go build ./cmd/nanite/` · `go test ./...`
- Deploy: always use Cerberus (`cerberus_rebuild nanite-api`)
- Migrations: DDL only, `internal/store/migrations/`. Latest: 012.
- Phase 3 plans: `agent-workspaces/planning/nanite-release-prep/plans/phase-3-*.md`
- Audits: `docs/audits/`
