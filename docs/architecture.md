# Nanite architecture

Nanite runs one authored agent directory per process. The embedding API lives
in `agent`. Substrate handles CLI startup in `cmd/nanite`. Each turn reads the
agent definition and takes a snapshot of the host's tool groups and context
contributions.

## Conversation storage

The agent's data directory defaults to `<agent dir>/.nanite/`. Each
conversation has a transcript under `transcripts/<conversation ID>/`. The
directory name is the conversation's ULID. Numbered JSONL segments record
messages and turn state transitions.

Nanite syncs each transcript record before updating SQLite. It commits the
projection and transcript cursor in the same transaction, then replays any
remaining records on open. Compaction records the summary and the last message
it covers. Provider requests use that summary and the messages that follow it.
Host history retains the full transcript. New schema changes use numbered
migrations.

## MCP and skills

Nanite reads `.mcp.json` at the start of each turn. It supports stdio,
streamable HTTP, and SSE. An unchanged server connection is reused. A replaced
connection closes when turns already using it finish. Tool discovery runs
concurrently and caches successful lists until the server announces an update
or reports an unknown tool. Discovery failures appear
as warnings in the session slot and logs.

The skills slot contains names and descriptions. The read-only `skill_get`
tool returns the instructions and, for authored skills, their directory. Changes
to `SKILL.md` take effect on the next turn.

## Questions and notices

The `ask_user` tool and MCP form elicitation pause an active tool call until a
host answers or the turn is cancelled. The REPL prompts for an answer. One-shot
CLI commands decline questions. A completion notice goes to the host's notice
channel and stays in SQLite until it appears in a later turn's session slot.

Before running a tool, Nanite validates the arguments against its schema. For
invalid arguments, it can ask the provider for one structural repair using
existing values or schema defaults. Repaired arguments pass schema validation
and authorization before execution. Set `NANITE_AUTO_REPAIR=false` to turn off
repair calls.

## Subagents and background jobs

A chat conversation can start a worker or planner child as a foreground tool
call or background job. Worker tools use the same permission check as chat
tools. Planners see read-only tools. Children cannot start another child, and
up to four children can run per agent. Cancelling a parent turn cancels its
foreground child. Background children run until completion or job cancellation.

Background Bash uses the same permission check, environment, and denylist as
foreground Bash. A job can outlive its turn and records bounded, redacted output
in `.nanite/jobs/`. Completion writes a notice for the parent conversation.

## Known limitations

File tools are confined to the workspace roots. Approved Bash commands run with
the user's privileges and can access paths outside those roots. The shell
denylist is a backstop against obvious accidents and applies in every permission
mode. Bash starts in the workspace root, limits execution time and output, and
receives a small set of environment variables plus literal values declared in
`host.yaml` under `runtime.env`.

The provider adapters carry failure classification in the stream event's
`Content` field because `llm/core`'s `StreamEvent` has no typed error field.
The engine uses the classification to distinguish retryable failures, context
overflow, and terminal errors.

Each turn sends every discovered MCP tool definition to the model. A large
catalog consumes context, and Nanite logs a warning above 100 MCP tools.
Default-mode approvals use the server's `readOnlyHint`. An explicit deny or
ask rule in `permissions.yaml`, such as `tool: mcp__myserver__*`, overrides
that hint.

MCP elicitation supports form prompts. Calls sharing one registration run
serially so a server question can be routed to the correct turn. This also
serializes read-only calls to that server.
