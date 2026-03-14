---
intent: pcc_global
project: mentat
updated_at: "2026-03-13"
---

# Conventions

## Go Backend

- Module: `github.com/hollis-labs/mentat`
- Go version: 1.25
- Standard Go project layout: `cmd/` for binaries, `internal/` for private packages
- Local `replace` directives for shared modules: `otel`, `tool-broker`
- SQLite via `modernc.org/sqlite` (pure Go, no CGO required)
- OTel instrumentation via `otel` shared library

## Frontend

- React + TypeScript in `ui/src/`
- Build: `cd ui && npm install && npm run build`
- Served by Go backend in production mode

## Naming

- Project: "Mentat" (formerly mentat-chat)
- Portfolio: "Fragments Engine" (formerly Project Tiamat)
- Config dir: `.agentrc/` (formerly `.volon/`, per ADR-005)

## ADRs

- Location: `adr/ADR-NNN-slug.md`
- Current range: ADR-001 through ADR-010
- All decisions recorded as ADRs before proceeding (behavior rule)

## Task Discipline

- Volon is the task system of record
- Tasks updated immediately on state change
- Decompose before executing: parent task -> child tasks -> closeout
- Hadron attempted first for automatable work

## Testing

- Backend: `go test ./...`
- No separate integration test suite yet

## Licensing

- MIT (Hollis Labs)

## Evidence
- Last refreshed: 2026-03-13
- Sources: go.mod, CLAUDE.md, docs/process/00_behavior-rules.md
