# External Harness / Runtime API

**Status:** Shipped in Nanite  
**Namespace:** `/api/harness/v1`

This API makes Nanite usable by another local GUI, service, or integration
without introducing a second runtime or session stack. It is thin over the
same session, message, SSE stream, cancel, approval, and durable-agent
services used by the built-in React UI.

## Routes

| Route | Purpose |
|---|---|
| `GET /api/harness/v1/initialize` | Compact protocol/app/operation summary |
| `GET /api/harness/v1/capabilities` | Cacheable enum and support contract |
| `POST /api/harness/v1/sessions` | Create a session from supported start fields |
| `GET /api/harness/v1/sessions/{id}` | Load one session plus observability details |
| `POST /api/harness/v1/sessions/{id}/turns` | Send a turn through the normal product turn path |
| `POST /api/harness/v1/sessions/{id}/cancel` | Cancel the active turn for the session |
| `GET /api/harness/v1/sessions/{id}/events?message_id=...` | SSE stream for one turn/message |
| `POST /api/harness/v1/sessions/{id}/approvals/{requestId}` | Respond to a tool approval request |
| `GET /api/harness/v1/durable-agents` | List durable agents |
| `GET /api/harness/v1/durable-agents/{id}` | Get one durable agent |
| `POST /api/harness/v1/durable-agents/{id}/start` | Start via existing durable-agent launch policy |
| `POST /api/harness/v1/durable-agents/{id}/resume` | Resume via existing durable-agent resume policy |
| `POST /api/harness/v1/durable-agents/{id}/wake` | Wake via explicit durable wake semantics |

## Transport

The event stream is SSE-only in v1. There is no WebSocket transport and no
separate runtime-state SSE family in this slice.

Use the `message_id` from the turn response:

```text
GET /api/harness/v1/sessions/{sessionId}/events?message_id={messageId}
```

## Session Create

Supported fields:

- `workspace_id`
- `project_id`
- `provider`
- `model`
- `agent_id`
- `title`
- `metadata`
- `mode_id`
- `boot_profile_id`

Explicitly unsupported in plain session create:

- `runtime_kind`
- `work_root`
- `durable_agent_id`

`boot_profile_id` is translated into the existing provider convention
`bootprofile:<id>`.

## Turn Send And Events

Turn send body:

```json
{
  "content": "Summarize the latest errors",
  "cycle_kind": "wake",
  "effort": "high"
}
```

Turn send returns:

- `session_id`
- `message_id`
- `stream_url`
- `raw_stream_url`
- `event_transport`
- `initial_activity_state`

Supported stream event types mirror the existing message stream:

- `stream_start`
- `delta`
- `replace_content`
- `tool_call`
- `tool_result`
- `approval_request`
- `tool_warning`
- `notify_pause`
- `plugin_envelope`
- `message_received`
- `subagent_run_status_changed`
- `mode_suggestion`
- `status`
- `error`
- `stream_end`
- `session_takeover`

## Cancel

`POST /api/harness/v1/sessions/{id}/cancel`

Response statuses:

- `cancelled` — an active generation was cancelled
- `idle` — there was no active generation to cancel

## Permission Support

This namespace does not add a new permission engine. It exposes the current
support level honestly:

- tool approval prompts appear as stream events of type `approval_request`
- clients respond through `POST /api/harness/v1/sessions/{id}/approvals/{requestId}`
- interactive envelope flows beyond permission approval remain proxied as existing `plugin_envelope` events

## Durable-Agent Mapping

The harness namespace does not reinterpret durable lifecycle behavior.

- `start` maps to the existing durable-agent start service
- `resume` maps to the existing durable-agent resume service
- `wake` maps to the explicit wake service

Wake keeps the current conservative semantics:

- only `process` and `template` agents are wake-managed
- launch still flows through the existing durable `Start(...)` boundary
- no raw second runtime stack is introduced

## Out Of Scope

- raw PTY or TUI attach
- WebSocket transport
- runtime-kind override on plain session create
- work-root policy on plain session create
- arbitrary callback execution
- external widget or adapter provisioning
