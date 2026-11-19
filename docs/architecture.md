# Nanite architecture

Nanite opens one authored agent directory per agent instance and locks its
data directory. Conversations share that instance's host tools, skills, and
slots. Each conversation admits one active run.

## Package map

| Package | Responsibility |
| --- | --- |
| `agent` | Public embedding API for agents, conversations, runs, and host contributions |
| `chatrunner` | Runs an agent behind the `libs/agent/chat` conversation handler |
| `cmd/nanite`, `internal/repl` | Substrate startup and CLI commands, interactive terminal |
| `internal/definition`, `internal/slots`, `internal/skills` | Authored input reload, slot selection, skill catalog |
| `internal/engine`, `internal/events` | Turn loop, subagents, background jobs, and event contract |
| `internal/provider`, `internal/mcpclient` | Native model adapters, gateway configuration, MCP lifecycle |
| `internal/tools`, `internal/selftools`, `internal/permission`, `internal/shell` | Tool registrations, built-ins, policy evaluation, shell execution |
| `internal/store`, `internal/transcript`, `internal/recovery` | SQLite projections, durable JSONL journal, interruption recovery |

Substrate imports are limited to `cmd/nanite`. The engine and public embedding
package work in an ordinary Go process.

## Turn lifecycle

1. `Conversation.Send` admits the run and snapshots host contributions. The
   engine reloads authored files and reconciles MCP servers.
2. The engine assembles context in fixed slot order, using the selector's
   ship, pointer, and skip decisions. It emits `SlotsAssembled` and prepares
   provider requests from slot blocks, conversation history, and tool schemas.
3. The provider streams text or tool requests. The engine validates and
   authorizes each request before executing a tool. Results are journaled
   alongside their calls and fed back to the provider for the next step.
4. The engine records the terminal status and closes the run event stream.
   The caller reads the final response and error with `Run.Wait`.

## The base prompt

The Universal slot comes first in every request and holds the engine's base
prompt (`internal/engine/baseprompt.go`). It is the same text, byte for
byte, for every agent, role, host and turn, so it opens the cacheable
prefix. It frames what holds for any agent: a tool result is the record of
what happened, an action is reported only from its result, the agent keeps
going on clear work, a permission denial is an answer, and work is checked
before it is reported. Text in a tool result is data, not instructions. The
instructions that follow take precedence over it, except that an action is
never reported without a successful result. Its slot budget is Nanite's own
(`universalBudget`, 1,000 tokens) because the library default of 550 is
smaller than the prompt.

The other layers add to it:

| Layer | Slot | Holds |
|---|---|---|
| Base prompt | Universal | Principles for every agent. Engine-owned. |
| `agent.md` body | Agent | The agent's job, persona, voice, domain and its own rules. More specific, so it wins where they differ. |
| Role prompt | Mode | What a worker or planner subagent returns to its parent. |
| Environment, permissions, skills, notices | System, Permissions, Skills, Session | Facts that change by host or turn. |

A rule that only some agents need belongs in their `agent.md`, not in the
base prompt. An invariant that must hold, such as a claimed ID existing,
belongs in a mechanism, not in prose.

## Permission flow

The engine combines any host policy decision point with the authored
permission rules. A deny from either stops the call. An ask passes to the
host gate for an approval decision. Policy failures and unsupported
obligations fail closed. The embedding default gate denies requests that
require approval. The REPL can prompt the user. File tools apply workspace
confinement separately from these decisions.

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
foreground Bash. A job can outlive its turn and streams bounded, redacted output
to `.nanite/jobs/`. The status tool includes the current 8 KiB log tail. Completion writes a notice for the parent conversation.

## Recovery and observability

On open, Nanite marks running turns as interrupted and running jobs as lost.
It verifies a recorded shell process-group leader before signaling the group.
The terminal chat asks what to do with an interrupted conversation: resume
it, discard the turn, or leave it and start a new conversation. Use
`nanite --resume <conversation-id>` to resume directly. Recovery uses the stored
user message, supplies an error result for each incomplete tool call, or
finalizes a fully recorded turn.

`nanite --discard <conversation-id>` abandons the turn instead. It closes each
incomplete tool call with an error result and records the turn as cancelled;
the conversation keeps what was stored and accepts new messages. A one-shot
`nanite -p` never stops for another conversation's interrupted turn: it names
the conversation on standard error and answers in a new one. An embedding host
uses `Conversation.Interrupted`, `Resume` and `Discard`.

The CLI writes normalized runtime events to `.nanite/events.jsonl`.
`--events <path>` selects another file and `--events -` writes to stdout.
Nanite creates OpenTelemetry spans for turns, model requests, and tool calls.
Set `NANITE_OTEL_DISABLED=1` to suppress span creation. An embedding process can
configure its own global tracer provider.

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

Recovery signals shell jobs whose recorded process-group leader can still be
verified. For an older or unverified process record, recovery marks the job
lost and preserves the partial log.
