# Planner Context — Nanite

> Project-specific planning conventions. Loaded by the planner agent role.

## Project

Nanite — AI chat harness with plugin system, tool brokering, and agent coordination. Go backend with React frontend.

## Architecture

- **Go backend** — `cmd/nanite/` (serve, plugin, mcp subcommands)
- **Frontend** — React/TypeScript chat UI
- **Plugin system** — Plugin SDK, scaffold, catalog
- **Agent adapters** — CLIAgentAdapter interface, 5 built-in adapters (nanite-native, claude, codex, gemini, opencode)
- **Dependencies** — tool-broker, go-providers, go-plugin, OTel, vanta-conduit (all local `replace` directives). Nexus removed.
- **Storage** — SQLite only (WAL mode). All messaging SQLite-native.

## Planning Notes

- Use superpowers skills for brainstorming, plan writing, and execution
- ADRs for architectural decisions go in project docs
- Local `replace` directives mean changes to libs/ may affect Nanite — account for cross-repo impact in plans
- Agent adapter design spec: `docs/superpowers/specs/2026-04-08-agent-adapter-architecture-design.md`
