---
intent: pcc_global
project: mentat
updated_at: "2026-03-13"
---

# Project Identity

**Mentat** is the unified meta-agent and chat application for Fragments Engine (formerly Project Tiamat). It combines a Go backend, React frontend, and CLI integration to provide AI-powered chat with MCP integration, portfolio orchestration, and cognitive aid capabilities.

Repository: `hollis-labs/mentat` (renamed from mentat-chat)
Path: `~/Projects-apps/mentat/`
Module: `github.com/hollis-labs/mentat` (Go 1.25)

## Goals

- Provide a unified AI chat interface with multi-provider LLM support (Anthropic, OpenAI, Ollama)
- Integrate with Fragments Engine services via MCP (Volon, Cortex, Hadron)
- Orchestrate portfolio-wide operations (health checks, context sync, task management)
- Serve as cognitive aid for the user — proactive context capture, decision recording, executive function support
- Support agent workflows: skills, slash commands, hooks, boot profiles

## Non-goals

- Not a general-purpose chat application for end users
- Not a standalone LLM or model host
- Not a replacement for individual project CLIs — Mentat coordinates, projects own their domains

## Active configuration

- Backend: Go HTTP API on port 8090, SQLite persistence
- Frontend: React + TypeScript SPA
- Dependencies: tiamat-otel (observability), tiamat-tool-broker (intent-based tool selection)
- MCP servers: Volon, Hadron, Cortex, Cerberus (configured in `~/.claude.json`)
- 11 skills, 14 slash commands, boot profiles (orchestrator, worker)

## Evidence
- Last refreshed: 2026-03-13
- Sources: CLAUDE.md, go.mod, docs/roadmap.md, git log
