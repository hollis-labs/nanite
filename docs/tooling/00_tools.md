# Tooling map

This repo assumes these core tools.

## Volon

- `volon` CLI
- Volon GUI server (port 8085)
- MCP: 14 tools (tasks, sprints, backlog, projects, health)

Mentat uses Volon for:
- task/sprint lifecycle
- workflow invocation
- deterministic state externalization (`.agentrc/`)

## Hadron

- `hadrond` daemon (port 8095)
- `hadron` CLI
- MCP: 19 tools (blueprints, runs, schedules, pipelines)

Mentat uses Hadron for:
- repeatable bootstraps (start servers, validate connectivity)
- reproducible multi-step shell automation

## Cortex

- `contextd` server (port 8080)
- MCP: 24 tools (context, namespaces, views, search, broker)

Mentat uses Cortex for:
- cross-project context and memory
- PCC storage and retrieval
- semantic search (context_search)

## Cerberus

- Service lifecycle manager

Mentat uses Cerberus for:
- starting/stopping/restarting Fragments Engine services
- health monitoring
