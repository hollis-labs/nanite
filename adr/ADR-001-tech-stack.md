# ADR-001: Technology Stack Selection

**Date:** 2026-03-07
**Status:** Accepted
**Decision Makers:** Chrispian, Mentat

## Context

Mentat Chat is a new chat client for interacting with AI agents across multiple contexts. It needs to run locally as a single binary and eventually on a VPS. It replaces the CLI-based Mentat meta-agent with a GUI while preserving the cognitive agent model.

Previous analysis of Seer (Laravel/React), Volon GUI (Go/React), and four coding agent projects (OpenClaw, Pi, OpenCode, Volon scheduler) informed this decision.

## Decision

| Layer | Choice |
|-------|--------|
| Backend | Go 1.25 |
| Frontend | React 19 + TypeScript |
| UI Components | Tailwind CSS 4 + shadcn/ui |
| Rich Editor | TipTap |
| State Management | Zustand + React Query |
| Database | SQLite via modernc.org/sqlite (pure Go) |
| API Protocol | REST + SSE (two-phase send) |
| AI Providers | Go adapters (Anthropic first) |
| Tool Integration | MCP (stdio + HTTP) |
| Build | Single binary with embedded SPA |

## Rationale

- **Go backend**: Consistent with Volon, Hadron, Cortex. Single binary deployment. SSE streaming is trivial in Go. Pure Go SQLite avoids CGO. Volon's chat backend already proves the streaming pattern.
- **React + TypeScript**: Proven in Seer. Huge ecosystem for TipTap, shadcn, Zustand.
- **SQLite**: Single file, portable. Consistent with all Tiamat projects. No external DB service needed.
- **Two-phase SSE**: POST message returns immediately, GET SSE streams response. Proven in Seer, decouples submission from generation.
- **MCP for tools**: Aligns with tool-first architecture. All Tiamat services already expose MCP interfaces.
- **Monorepo**: Single repo with `cmd/` for Go and `ui/` for React. Simplifies builds, deployments, and development.

## Alternatives Considered

- **Node/Bun backend**: Faster prototyping, better AI SDK ecosystem, shared types with frontend. Rejected for inconsistency with Tiamat stack and different deployment story.
- **Separate repos**: Frontend and backend in separate repos. Rejected for unnecessary complexity in a focused app.
- **Tauri/Electron**: Desktop app wrapper. Rejected — web app serves both local and VPS use cases.

## Consequences

- Must write Go streaming adapters for AI providers (Anthropic, OpenAI)
- Must implement MCP client in Go (or use existing from Volon/Hadron)
- Frontend/backend type sharing requires OpenAPI schema or code generation
- Hot reload needs two processes in dev (air for Go, vite for React)
