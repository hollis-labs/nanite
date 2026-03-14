# Conduit Plugin System Architecture

> Created: 2026-03-13 | Status: Draft | Related: ADR-013 (Conduit Separation)

## Overview

Conduit is an agent-agnostic chat harness. Plugins extend it with agent-specific UI, actions, workflows, and hooks without polluting the core app. Each Fragments Engine service (Volon, Mentat, Hadron, etc.) can ship a plugin that adds its capabilities to Conduit.

## Plugin Spec

A plugin is a self-describing package defined by `plugin.yaml`:

```yaml
name: volon-planning
version: 1.0.0
description: Sprint planning, review, and task management for Volon
author: hollis-labs
requires:
  mcp_servers: [volon]

registers:
  envelopes:
    - type: sprint-review
      renderer: SprintReviewCard
    - type: task-board
      renderer: TaskBoardView

  quick_actions:
    - id: approve-task
      label: Approve
      icon: check
      handler: volon_task_approve
      context: [task]
    - id: backlog-item
      label: "→ Backlog"
      handler: volon_backlog_capture
      context: [task, idea]

  widgets:
    - id: sprint-progress
      component: SprintProgressBar
      slot: right-rail

  workflows:
    - file: workflows/sprint-review.yaml
    - file: workflows/status-report.yaml

  hooks:
    - event: session.start
      handler: loadSprintContext
    - event: mode.changed
      handler: adjustToolbar
```

## Five Layers

### Layer 1: Plugin Registry & Loader

- Plugins installed to a known directory (e.g., `~/.conduit/plugins/` or DB-backed)
- Conduit scans for `plugin.yaml` on startup
- Validates: required MCP servers available, no ID conflicts
- Registers all declared extensions into the runtime

### Layer 2: Display Hooks (Envelope Extensions)

- Extends the existing envelope renderer (`EnvelopeRenderer.tsx`)
- Plugins register new envelope `type` values with React components
- Agent responses with a registered type auto-render using the plugin's component
- Built-in generic renderers for common patterns:
  - `dynamic-table` — structured JSON → sortable table
  - `option-picker` — list of choices → buttons, user picks, selection sent as message
  - `report` — formatted markdown with sections
  - `diff-view` — before/after comparison
  - `progress` — status bars and completion tracking

### Layer 3: Quick Actions

- Generalize the existing VolonBacklogButton pattern
- Actions are context-aware: shown based on current context (task, sprint, idea, etc.)
- Actions can: call MCP tools, call API endpoints, emit frontend events, send chat messages
- Agent-suggested actions: agent emits an `option-picker` envelope → rendered as action buttons
- User selection flows back as a structured message

### Layer 4: Agent-Generated Temporary UI

- Agent includes `render_as` hint in structured output: `"table"`, `"options"`, `"report"`, `"form"`
- Conduit renders using generic renderers (no plugin needed for standard types)
- User can "pin" a useful rendering → saves as a template or widget
- Pinned patterns can be promoted to bundled workflows or plugin components over time

### Layer 5: Composable Skills & Commands

- Skills support merge tags: `{{project_id}}`, `{{active_sprint}}`, `{{current_mode}}`, `{{scope}}`
- Skills declare output type (text, JSON, envelope) for downstream consumers
- Skill chaining planned for future: output of one feeds input of next
- Structured input beyond string args (JSON schemas for complex inputs)

## Event System

Frontend events that plugins can subscribe to:

| Event | Payload | Use Case |
|-------|---------|----------|
| `session.start` | session_id, agent_id, mode | Load context, adjust UI |
| `session.end` | session_id | Capture, cleanup |
| `mode.changed` | old_mode, new_mode | Adjust toolbar, reload context |
| `scope.changed` | old_scope, new_scope | Filter widgets, update displays |
| `message.received` | message, role | React to specific content |
| `tool.called` | tool_name, result | Update widgets, track state |
| `envelope.rendered` | envelope_type, data | Coordinate between plugins |

## Cross-App Standard

This plugin spec is designed to be implementable by any FE app in the portfolio:
- `plugin.yaml` schema is the contract
- React component interface for renderers is documented
- Event names and payloads are standardized
- Any app that implements the loader can use the same plugins

## Relationship to Existing Systems

| Existing System | How Plugins Extend It |
|----------------|----------------------|
| Envelope renderer | New envelope types via `registers.envelopes` |
| Workflow engine | Bundled YAML workflows via `registers.workflows` |
| Widget system | New widgets via `registers.widgets` with slot targeting |
| Slash commands | Plugins can register commands (future) |
| Prompt templates | Plugins can bundle templates (future) |
| MCP integration | Plugins declare MCP dependencies, actions call MCP tools |

## Implementation Phases

**Phase 1 (Near-term):** Plugin spec document, quick action framework, sprint review UI, bundled workflow YAMLs, dynamic renderers
**Phase 2 (Mid-term):** Plugin loader, display hook registration, event system, agent-generated temp UI, composable skills
**Phase 3 (Long-term):** Plugin registry/marketplace, cross-app standard in fe-core, community plugins, widget customizer
