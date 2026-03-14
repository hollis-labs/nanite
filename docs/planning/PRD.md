# Mentat Chat — Product Requirements Document

**Version:** 1.0
**Date:** 2026-03-07
**Status:** Draft
**Author:** Chrispian + Mentat

---

## 1. Vision

Mentat Chat is a keyboard-first, multi-agent chat client purpose-built for working with AI agents across multiple contexts. It is the primary interface for Fragments Engine — replacing the CLI-based Mentat with a proper GUI while preserving the cognitive agent model, deterministic workflows, and tool-first architecture that make Fragments Engine effective.

Mentat Chat is NOT an IDE, NOT a project manager, NOT a dashboard. It is a **conversational command center** where a human and one or more AI agents plan, discuss, delegate, and execute work across any domain.

## 2. Core Principles

1. **Chat-first** — Everything happens through conversation. The UI exists to support chat, not replace it.
2. **Context is king** — Smart context scoping per session. The right information at the right time, never polluting the conversation.
3. **Mentat doesn't do work** — The primary agent (Mentat) is a cognitive partner. It plans, delegates, manages context, and coordinates. Worker agents do the actual tool calls and execution.
4. **Agents talk to agents** — Multi-agent communication is first-class. Mentat spawns sessions with workers, delegates tasks, reports back. The user can observe or participate.
5. **Keyboard-first** — Minimal buttons/UI chrome. Modals, drawers, overlays used cleverly for rich output. Every panel has a keyboard shortcut.
6. **Local-first, cloud-ready** — Runs locally as a single binary. Deployable to a VPS for remote access to Fragments Engine.

## 3. Users

**Primary user:** Chrispian (solo operator managing Fragments Engine, small web businesses, writing, personal planning)

**Agent users:** Mentat (primary cognitive agent), worker agents (code, research, review), specialist agents (per-project)

## 4. Use Cases

### 4.1 Fragments Engine Management
- Plan sprints, create tasks, review progress via conversation with Mentat
- Mentat delegates coding tasks to worker agents in separate sessions
- User reviews agent work, approves proposals, provides direction
- Context automatically scoped to Fragments Engine workspace

### 4.2 Small Web Businesses
- Discuss business strategy, marketing, product decisions
- Mentat helps with planning, research, document generation
- Different workspace, different context, same agent

### 4.3 Writing
- Collaborative writing sessions with Mentat as editor/advisor
- Different mode (e.g., `/writer`) adjusts Mentat's behavior
- Artifacts drawer for drafts, outlines, generated content

### 4.4 Personal Planning
- Day-to-day task management, goal tracking
- Mentat as a cognitive partner for life planning
- Structured input forms for goal-setting workflows

### 4.5 Multi-Agent Collaboration
- User + Mentat + Architect agent discuss system design
- Mentat moderates, Architect provides technical depth
- User participates when needed, or Mentat proxies

### 4.6 Autonomous Agent Work
- Mentat spawns a session with a worker agent
- Worker executes tasks (code changes, research, analysis)
- Mentat monitors, reports to user when complete
- User reviews results in the worker session or via Mentat's summary

## 5. Information Architecture

```
Workspace (e.g., "Fragments Engine", "Writing", "Personal")
  Project (optional — e.g., "Volon", "Hadron", "Novel Draft")
    Chat Session (conversation with context)
      Messages (user, agent, system, tool results)
      Bookmarks (pinned messages for reference)
      Artifacts (generated files, images, documents)
```

## 6. Agent Model

### 6.1 Agent Identity
An agent has: name, avatar, system prompt template, available modes, default model, MCP server bindings, tool permissions.

### 6.2 Modes
A mode modifies an agent's behavior without changing identity. Modes adjust: system prompt addendum, tool availability, response style, context assembly strategy.

Examples:
- Mentat `/default` — cognitive partner, plans and delegates
- Mentat `/architect` — focuses on system design, asks probing questions
- Mentat `/planner` — structured planning, uses input forms for goal-setting
- Mentat `/writer` — editorial mode, focuses on prose quality

### 6.3 Multi-Agent Sessions
- A session can have 1+ agents
- Each agent has its own system prompt and tool set
- Messages are attributed to specific agents
- Mentat can create sessions and invite agents programmatically
- Turn-taking: round-robin, directed (@agent), or free-form

### 6.4 Agent-to-Agent Communication
- Mentat can spawn a session with a worker agent (no user present)
- Worker agents can make tool calls (MCP, shell, file operations)
- Mentat monitors worker sessions and reports status
- User can join any session at any time

## 7. Context Management

### 7.1 Context Scoping
Each session has context assembled from:
- Workspace context (always included)
- Project context (if session is project-scoped)
- Session history (with compaction)
- Pinned/bookmarked items
- Active task context (if session is task-scoped)
- Mode-specific context addendum

### 7.2 Context Budget
- Configurable budget ceiling (default: 75% of model context window)
- Per-turn pruning: old tool results compacted after each turn
- Session compaction: LLM-based summarization when approaching limit
- Head+tail truncation for individual tool outputs

### 7.3 Context Broker (Progressive)
MVP: Manual context assembly from workspace + project + session history.
V2: Smart context broker that auto-selects relevant context based on conversation topic, recent activity, and user preferences.

## 8. UI Requirements

### 8.1 Layout
4-column layout: Nav Rail | Left Sidebar | Chat Main | Right Rail

- **Nav Rail** (64px): Workspace switcher, search, new chat, settings. Always visible.
- **Left Sidebar** (272px): Session list (pinned + recent), project filter, agent list. Toggle: `Cmd+B`.
- **Chat Main**: Header (widget area) + transcript + composer. Always visible.
- **Right Rail** (384px): Widget dashboard. Toggle: `Cmd+/`.

### 8.2 Chat Composer (TipTap)
- Rich text with markdown support
- Slash commands (`/mode`, `/workflow`, custom)
- Wiki links (`[[reference]]`)
- Hashtags (`#tag`)
- File upload (drag-drop + paste + button)
- Enter to send, Shift+Enter for newline
- Model picker in toolbar
- Agent/mode indicator

### 8.3 Message Display
- Markdown rendering (react-markdown + remark-gfm)
- Code blocks with syntax highlighting and copy button
- Envelope parsing → proposal cards, question forms, status indicators
- Tool call progress indicators
- Agent attribution (avatar + name) for multi-agent sessions
- Bookmark toggle per message
- Copy/download actions

### 8.4 Interactive Components
- **Proposal Cards**: Editable inline forms from agent suggestions. Apply/dismiss.
- **Question Forms**: Agent-driven structured input (radio, select, textarea, checkbox). Answers cached for reuse/editing.
- **Modals**: Agent-generated rich content (dashboards, summaries, HTML views).
- **Artifacts Drawer**: Slide-out panel listing generated files, images, documents for download.
- **Approval Cards**: Inline approve/reject for high-risk operations with risk scoring.
- **Command Result Modal**: Slash command output with navigation stack.

### 8.5 Keyboard Shortcuts
Every panel and major action has a keyboard shortcut. No mouse required for normal operation.

| Action | Shortcut |
|--------|----------|
| Toggle left sidebar | `Cmd+B` |
| Toggle right rail | `Cmd+/` |
| New chat | `Cmd+N` |
| Search | `Cmd+K` |
| Focus composer | `Cmd+L` |
| Next session | `Cmd+]` |
| Previous session | `Cmd+[` |
| Bookmark message | `Cmd+D` |
| Toggle artifacts | `Cmd+.` |

### 8.6 Widget System
Widgets appear in right rail, left sidebar footer, and chat header. Configurable per workspace.

Widget types: Session Info, Bookmarks, Recent Activity, Tool Calls, Agent Status, Task Summary, Context Budget.

## 9. Tool Integration

### 9.1 MCP as Primary Interface
All tool calls route through MCP servers:
- **Volon**: Tasks, sprints, projects, backlog
- **Cortex**: Context, namespaces, typed records
- **Hadron**: Blueprints, pipelines, automation
- Custom MCP servers per workspace/project

### 9.2 Deterministic Workflows
YAML-defined workflows with inputs, steps, and routing:
```yaml
name: Architecture Review
trigger: /arch-review
inputs:
  - name: project
    type: select
    source: volon_projects
  - name: focus
    type: text
steps:
  - tool: cortex_context_view
    args: { namespace: "{{project}}" }
  - prompt: "Review architecture of {{project}} focusing on {{focus}}..."
```

### 9.3 Tool Permissions
Worker agents: full tool access (scoped by profile)
Mentat: no direct tool execution — delegates to workers or uses read-only tools

## 10. Provider Support

### 10.1 MVP
- Anthropic (Claude) — primary, streaming via Go adapter

### 10.2 Post-MVP
- OpenAI (GPT-4o, o1)
- Ollama (local models)
- OpenRouter (multi-provider gateway)

### 10.3 Per-Session Model Selection
Each session can override the default model. Model picker in composer toolbar.

## 11. Data Persistence

### 11.1 SQLite Database
Single file, portable, embeddable. Tables: workspaces, projects, sessions, messages, bookmarks, artifacts, agent_profiles, providers, models, workflows.

### 11.2 Messages in Separate Table
NOT JSON column. Each message is a row with: session_id, role, agent_id, content, envelope (JSON), metadata (JSON), created_at.

### 11.3 Bookmarks
Message-level bookmarks with optional notes. Scoped to session, queryable across workspace.

## 12. Deployment

### 12.1 Local
Single Go binary embeds React SPA. `mentat serve` starts on localhost.

### 12.2 VPS
Docker container. Same binary, exposed on HTTPS. Connects to remote Fragments Engine services (Volon, Cortex, Hadron running on VPS).

## 13. Non-Goals (MVP)

- Code editing / IDE features
- Task/sprint management UI (Volon GUI handles this)
- Vector embeddings / semantic search
- Voice input/output
- Mobile-optimized layout
- Plugin marketplace
- User authentication (single-user local app for MVP)
