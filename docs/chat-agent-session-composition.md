# Chat / Agent Session Composition

**Status:** Curated composition notes for the current Nanite product shape  
**Scope:** Nanite chat UI, durable agents, boot-profile CLI harnesses, and the
boundaries between session, profile, runtime, and durable identity.

## Executive Summary

Nanite exposes several related but distinct concepts through one chat product:

- a persisted `sessions` row
- a primary `agent_profiles` binding
- a runtime process for CLI-backed sessions
- a durable-agent identity and lifecycle layer above disposable sessions

The main product rule is that provider/model, mode, boot profile, runtime
kind, and lifecycle class are different axes. The Start surface and details
panel exist to make those axes explicit.

## Current Session Types

| Type | Backing rows | Runtime process? | Primary use |
|---|---|---:|---|
| API Chat | `sessions`, `messages`, `session_agents` | No | Normal model-backed chat |
| Boot-profile or CLI Chat | Same plus `agent_runtime` | Yes | Chat UI driving local CLI automation |
| Durable Advisor | durable instance plus attached session | Usually API today | Reusable scoped advisor continuity |
| Durable Process Agent | durable instance plus wake-created sessions | Usually API today | Scheduled or explicit wake-driven monitoring/orchestration |
| Durable Template Agent | durable instance plus one-shot run session | Often API in current product shape | Boot, run, stop task execution |

Raw PTY or TUI attach is not a product surface in the current implementation.

## Axes That Should Not Be Conflated

| Axis | Examples | Notes |
|---|---|---|
| Session | `sessions.id`, messages, status | UI thread and transcript anchor |
| Agent identity | primary profile or durable profile | Persona, tools, and policy |
| Mode | `chat`, `plan`, `work` | Behavior pointer, not runtime identity |
| Provider/model | `anthropic` / `claude-sonnet-4` | Locked once the session is underway |
| Runtime kind | `api`, `streaming-stdio`, `subprocess` | Backend substrate |
| Lifecycle class | `advisor`, `process`, `template`, `harness` | Durable launch/session policy |

## Durable-Agent Rows

| Row | Meaning |
|---|---|
| `agent_profiles` | Reusable persona/template/capability source |
| `durable_agent_instances` | Configured durable instance with immutable launch identity fields |
| `durable_agent_instance_sessions` | Attachment rows linking durable instances to sessions |
| `sessions` | Conversation thread and message persistence |
| `agent_runtime` | Spawned CLI runtime process row when a CLI session is live |

The instance layer is additive. Durable start, resume, and wake resolve through
existing session/runtime services rather than introducing a second boot stack.

## Canonical Vocabulary

Use these terms consistently:

- **Agent profile**: reusable role or persona template
- **Capability bundle**: tools, skills, prompts, procedures, reflexes, knowledge seed, boot-plan config
- **Durable agent instance**: configured scoped use of a profile
- **Lifecycle class**: advisor, process, template, or harness
- **Runtime session**: actual chat/runtime execution surface

## Durable Launch Policy

| Lifecycle class | Default policy | Attachment relation |
|---|---|---|
| `advisor` | Reuse latest compatible attached session or create one | `primary` |
| `process` | Create a fresh wake session per tick/manual wake | `wake` |
| `template` | Create a fresh one-shot run session | `run` |
| `harness` | Reuse a managed harness session when available | `harness` |

Wake loops are intentionally conservative:

- manual wake is supported
- due work can be listed
- an explicit run-due pass can be triggered
- no background poller is present
- launch still flows through the existing durable-agent start path

## Product Recipes

Recipes are declarative setup inputs that compile into the durable-agent layer.

Important boundaries:

- no raw PTY or TUI product surface
- no second runtime launch stack
- no external widget or adapter provisioning
- web/company recipes prepare durable agents and point at `/api/harness/v1`
- boot callbacks and ready notices remain preview-only
