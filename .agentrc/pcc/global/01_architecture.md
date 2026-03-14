---
intent: pcc_global
project: mentat
updated_at: "2026-03-13"
---

# Architecture

## Stack

- **Backend**: Go 1.25, HTTP API, SQLite (modernc.org/sqlite, pure Go)
- **Frontend**: React + TypeScript SPA (`ui/src/`)
- **Observability**: OpenTelemetry via tiamat-otel shared library

## Directory Layout

| Path | Purpose |
|------|---------|
| `cmd/mentat/` | Entry point, CLI commands (serve, etc.) |
| `internal/api/` | HTTP API handlers |
| `internal/chat/` | Chat engine — turn orchestration, context assembly |
| `internal/mcp/` | MCP client integration |
| `internal/provider/` | LLM provider abstractions (Anthropic, OpenAI, Ollama) |
| `internal/store/` | SQLite persistence layer |
| `internal/builders/` | Agent and skill builders |
| `internal/toolbroker/` | Intent-based tool selection and discovery |
| `internal/workflow/` | Workflow orchestration |
| `ui/src/` | React frontend |
| `config/` | Service URLs, repo configs |
| `docs/` | Architecture, process, planning |
| `adr/` | Architecture decision records (10 accepted) |

## Key Components

- **Engine**: Orchestrates chat turns — receives user input, assembles context, calls LLM, processes tool use, returns response
- **ContextBroker**: Assembles context window with token budgeting (ADR-008). Manages PCC, conversation history, tool results
- **ToolBroker**: Intent-based tool selection from large tool sets. Progressive discovery for MCP tools
- **Provider Registry**: Multi-LLM abstraction with circuit breaker, rate limit handling, prompt caching
- **Store**: SQLite persistence for conversations, messages, settings
- **MCP Client**: Connects to Volon (tasks), Cortex (context), Hadron (automation), Cerberus (services)

## Integration Points

- Volon MCP: task/sprint CRUD, project queries (always scoped by `project_id`)
- Cortex MCP: context read/write, PCC refresh, namespace queries
- Hadron MCP: blueprint execution, pipeline runs, scheduling
- Cerberus MCP: service start/stop/status for local development

## Evidence
- Last refreshed: 2026-03-13
- Sources: CLAUDE.md, internal/ package layout, ADRs 004-010
