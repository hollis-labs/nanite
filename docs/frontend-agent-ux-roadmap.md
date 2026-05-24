# Frontend Agent UX Roadmap

**Status:** Core durable-agent/admin/start surfaces shipped in Nanite  
**Scope:** Start UX, sidebar session differentiation, durable-agent
administration, lifecycle controls, and session inspection.

## Product Direction

Nanite's frontend makes the runtime shape explicit without forcing the operator
to reason about backend table names.

The primary distinction is:

- **Start surface**: how a new chat, harness, or durable-agent session begins
- **Chat surface**: where the operator reads and sends turns for one session
- **Admin surface**: where durable agents are created, configured, started, paused, resumed, archived, and inspected

Provider, model, runtime kind, mode, and lifecycle class are different axes.
The UI should not present them as if they were the same control.

## Start Surface

Nanite's left-rail action is a deliberate **Start** surface rather than a raw
new-chat shortcut.

Supported paths:

| Choice | Backend path | Result |
|---|---|---|
| Chat with a model | `POST /api/sessions` | Normal chat session |
| Start local harness | boot profile or recipe-backed harness | Managed headless CLI session |
| Wake durable agent | durable start or resume APIs | Attached session reused or created by launch policy |
| Create from recipe | recipe dry-run then apply | Durable-agent instance, optionally attached session |

## Sidebar

The session rail distinguishes:

- API chat
- boot-profile or CLI harness session
- durable-agent-backed session
- subagent child rows when session ancestry is present

Presence and activity signals come from the existing session and runtime state
plus Nanite's active-stream and pending-tool maps.

## Session Details

Nanite exposes a read-only details panel backed by
`GET /api/sessions/{id}/details`.

The panel answers:

- what session this is
- how it started
- which provider/model/runtime it is using
- which durable agent or primary profile is attached
- whether runtime, halt, usage, and recent durable-event state exists

Immutable start fields remain locked after a session has messages. Changing
them belongs to fork/restart/new-session flows, not in-place mutation.

## Durable-Agent Admin

Durable-agent CRUD and lifecycle belong in settings/admin, not in the chat
composer.

Current MVP covers:

- list and detail
- recipe-based creation
- lifecycle actions
- launch-policy preview
- attached sessions
- durable events
- due-wake inspection and explicit run-due trigger

Current limits:

- no background wake poller
- no second runtime launch path
- no callback execution from the UI

## Agent Capability Admin

Agent detail surfaces now cover:

- skills and prompt-template assignment
- tool permissions
- known tools and known skills
- procedures and knowledge seeds
- boot plans with dry-run preview
- reflexes when supported by the backend

Internal or file source-of-truth agents remain read-only where the backend
rejects mutation.

## Agent Builder

The Agent Builder wizard is deterministic at write time:

- draft and review are advisory
- dry-run validates without writing
- final submit performs the actual mutations
- launch can be toggled on submit
- ready notices are preview-only

## Preserved Non-Goals

- no raw PTY or TUI product surface
- no external widget or adapter provisioning
- no hidden mutation of provider or model on active sessions
- no broad UI refactor beyond the shipped start/admin surfaces
