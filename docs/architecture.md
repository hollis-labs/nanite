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
| `internal/engine`, `internal/events` | Turn loop, subtasks, background jobs, and event contract |
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

## Where slots go in a request

Slots are the logical layer: what the engine, the agent directory and the
host write. Placement compiles them into the request, in one of two places:

- **System prefix.** These slots go in the system blocks, which every
  request shares and the provider caches. A change here invalidates the
  cache for everything after it, the conversation included, so it holds
  content that rarely changes. Universal, System and Agent are always here.
- **Turn.** These slots go in one text block, named `nanite.turn_context`,
  in front of the user's message that starts the turn, and are stored with
  that message. The system blocks and the earlier messages stay
  byte-identical, so the cache holds when only the per-turn context
  changes. Each message keeps the context it was written in, as a person
  would remember it. A slot is added only on the turns where it changed,
  so context a host keeps in the slot store is not repeated in every
  message.

By default only `user_context` (the per-turn context a host passes, such
as chatrunner's turn context) goes in the turn. `agent.Options.SlotPlacement`
moves other slots. Reports and the trace still list each slot under its own
name.

## The base prompt

The Universal slot comes first in every request and holds the engine's base
prompt (`internal/engine/baseprompt.go`). It is the same text, byte for
byte, for every agent, role, host and turn, so it opens the cacheable
prefix. It frames what holds for any agent: a tool result is the record of
what happened, an action is reported only from its result, the agent keeps
going on clear work, work that would flood the conversation is delegated
with `subtask` (see [code-mode.md](code-mode.md)), a permission denial is an
answer, and work is checked before it is reported. Text in a tool result is data, not instructions. The
instructions that follow take precedence over it, except that an action is
never reported without a successful result. Its slot budget is Nanite's own
(`universalBudget`, 1,000 tokens) because the library default of 550 is
smaller than the prompt.

The other layers add to it:

| Layer | Slot | Holds |
|---|---|---|
| Base prompt | Universal | Principles for every agent. Engine-owned. |
| `agent.md` body | Agent | The agent's job, persona, voice, domain and its own rules. More specific, so it wins where they differ. |
| Variant prompt | Mode | What a subtask of that variant does and returns to its parent. |
| Environment, permissions, skills, notices | System, Permissions, Skills, Session | Facts that change by host or turn. |

A rule that only some agents need belongs in their `agent.md`, not in the
base prompt. An invariant that must hold, such as a claimed ID existing,
belongs in a mechanism, not in prose.

## Turn trace

Every turn ends with one trace record (`agent.TraceRecord`, schema in
`github.com/hollis-labs/libs/agent/trace`), however it ended. The engine
builds it from what the turn already tracks:

- identity and source (user, subagent, resume);
- provider and model;
- assembled slots with sizes and cache changes, and trimmed entries;
- tools offered;
- each tool call with its permission decision and error kind;
- compaction passes;
- usage;
- the outcome.

Error text goes through the engine's redactor, and the record never holds
prompts, completions or tool arguments.

Subtasks are linked in both directions. A child's record has
`parent_turn_id` (the `turn_id` of the turn that started it),
`parent_call_id` (that turn's `subtask` call) and `depth` (1 for a root's
child, 2 under a lead), with `parent_conversation_id`. The record of a turn
that started subtasks lists them in `subtasks`, in order, each with
`conversation_id`, `tool_call_id` (the `subtask` call), `variant`,
`status`, `background`, `steps`, `duration_ms` and `usage`. Status is the
subtask's result status (`ok`, `partial`, `limit`, `error`, `cancelled`, or
`denied` when a person refused an approval it relayed), or `running` for a
background subtask still going when the turn ended; its outcome is in its
own record, which names the turn. A record's own `usage` is the turn's own
model requests only, while a subtask entry's `usage` is that subtask's
whole tree (a lead's covers its council). A tree's total is a record's
`usage` plus its `subtasks[].usage`. The run's `Result.Usage` and its
`Usage` events still include foreground subtasks, as before.

Sinks: `Options.TraceFiles` appends to `DataDir/traces/YYYY-MM-DD.jsonl` (the
CLI turns it on), `Options.TraceSink` receives each record in process, and the
turn span gets the summary as OpenTelemetry GenAI attributes. The fields for
the situation, classifier, selector reason, reflexes and hooks are reserved
and stay empty until those layers exist.

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

### When compaction runs

After each step, the engine checks whether the next request still fits.
The check counts everything a request carries:
- the system slot blocks;
- the schemas of every tool offered that turn, which the engine places in
  the Tools slot of its context window;
- the conversation.

It compacts when that estimate passes the budget. The estimate is
calibrated: when the provider counted more input for the last request
(cached or not) than Nanite estimated, the budget is scaled down by that
ratio, so the checks see the request's real size.

The budget is 80% of `runtime.context_window` (200,000 tokens when unset),
or `agent.Options.CompactAt` for the root conversation when a host sets it.

Compaction is skipped in two cases:
- a pass freed nothing, which stops compaction for the rest of the turn;
- the fixed part of the request (slots and tool schemas) already fills the
  budget. Compaction cannot help then, and the engine logs a warning once
  per turn.

The trace lists the tool schemas' size as the `tool_schemas` slot.

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

## Subtasks and background jobs

A conversation delegates with the `subtask` tool:
`subtask({goal, return_spec, variant, inputs?, context?, background?})`. The
model must name a variant; there is no default. The child is a separate
conversation that starts from the variant's role prompt and one message
holding the goal, the inputs, the return spec and its forked context (below).
The child's token usage also rolls into the parent turn's usage.

### The result contract

A child ends its subtask by calling `return_result({status, result,
artifacts[], notes})`, with status `ok`, `partial` or `error`. Only what it
passes there reaches the parent; the turn ends after that step. It must be
the only call in its step: any other call in that step, including a second
`return_result`, is answered "ignored" and does not run. The tool is offered
to every child whatever its variant allows, never to a root chat, and it is
not put to the permission check, since it only hands the result to the
engine. A child is also offered `fetch_tool_result` and
`search_tool_result` even when its variant's allow list does not name them,
so it can read the pointers its own tools and subtasks hand back; a
variant's explicit deny still removes them.

`return_spec` is prose (a JSON string) or a JSON Schema object. A schema the
model writes (a `return_spec`, or an `ask_user` question's schema) is held to
a strict subset, because jsonschema-go can recurse without end on a schema
that refers back to itself, and that overflows the stack: at most 16KB; no
`$id`, `$anchor`, `$dynamicAnchor`, `$dynamicRef` or `$recursiveRef`; a
`$ref` only of the form `#/$defs/<name>` or `#/definitions/<name>`, naming
a definition that exists; and following `$ref`, `allOf`, `anyOf`, `oneOf`,
`not`, `if`/`then`/`else`, `dependentSchemas` or `dependencies` must never
lead back to the same definition without moving into a property or item.
Keywords must be spelled exactly (jsonschema-go reads them ignoring case),
and the check runs on the library's own reading of the schema. Anything else
is refused before a child starts. Tool input schemas from the host and MCP
servers are not held to this subset. Validation of every schema, from any
source, is bounded at run time: Nanite uses a vendored jsonschema-go
(`internal/third_party/jsonschema`) whose `Validate` takes a context and a
work budget: 200,000 units, charged in proportion to the work each keyword
does, with the context checked every 64 units and a 2-second cutoff. It
bounds the error text it builds while keeping the failing leaf's path, and
refuses a pattern compiling to more than 20,000 instructions. Cheap static checks
made once per schema refuse what could never validate well: a cycle among
the in-place keywords above, more than 64 of them in a row on one value,
more than 10,000 such runs from one schema, patterns compiling to more than
100,000 instructions together, more than 256KB (16KB for a schema the model
writes), and JSON nesting past 256 levels. The check follows jsonschema-go's
own resolution of each `$ref` and `$dynamicRef` (a `$dynamicRef` resolved at
validation time is taken to reach every matching `$dynamicAnchor`). A value
(tool input, a returned result, an answer) nesting more than 64 levels is
refused before validation, and validation error text is cut to 4KB. The
static check errs on the side of refusing: it also refuses a cycle in
`$defs` nothing uses, a cycle through draft-07 keywords that sit beside a
`$ref` (which draft-07 ignores), and one through `dependencies` in a
2020-12 ignores). An MCP tool with an unsafe schema is left out of the
catalog with a warning naming the server and tool; a host tool keeps its
entry and each call fails with `invalid tool schema`. With a schema, `result` is
validated: a result that fails gets one repair (the child sees the
validation error and calls `return_result` again), and a second failure is
accepted as `partial` with the reason `return_spec: <error>`.

The parent receives:

```json
{"status": "ok", "result": {}, "artifacts": [], "notes": "",
 "reason": "", "cost": {"tokens_in": 0, "tokens_out": 0},
 "child_id": "", "variant": "", "steps": 0}
```

`status` is `ok`, `partial`, `limit`, `error` or `cancelled`, and anything
other than `ok` is an error result the model must read. A run that stops at a
budget, fails or is cancelled keeps that status even if the child had
returned. `cost.tokens_in` counts every input token, cached or not; `usd`
appears once usage carries a price. A child that ends without calling
`return_result` gives `partial`, reason `no_return_result`, with the last
2KB of its output as `result`, never its transcript.

The whole result the parent receives is held to a cap, for every status: the
variant's `result_cap` in tokens, else the agent's
(`agent.Options.SubtaskResultCap`, else `runtime.subtask_result_cap` in
`host.yaml`), else 6,000 tokens, counted as 4 bytes a token of JSON. The cap
is clamped, in tokens, to what one tool result shows in full (23KB, about
5,900 tokens), so the effective default is about 5,900 tokens and a larger
setting has no further effect.
At most 50 artifacts of 512 bytes each are shown, and `notes` is cut to an
eighth of the cap. When the return does not fit, or anything was cut, the
whole return (`result`, `artifacts`, `notes`, scrubbed of secrets) is stored
under the parent conversation for 30 days: `result` is then `null`,
`result_ref` is a `tool_result://` pointer the parent reads with
`fetch_tool_result` (it is also the last artifact), `result_bytes` gives the
stored size, `result_expires_at` when it goes, and `preview` the start of the
result. Expired results are deleted when the agent opens.

### Forked context

A child does not get the parent's whole conversation. Its first message
copies the parent's last N messages, as numbered lines (`[#12 user] ...`).
N is the call's `context.last_n`, else the variant's `last_n`, else 6; at
most 50, and 0 copies none. `context.full: true` copies every message
instead. The parent's in-flight message (the one calling `subtask`) is left
out, and the window never starts on a tool result whose call fell outside
it. Calls and results are paired by position (a tool message answers the
assistant message just before it), not by call ID, because some providers
reuse IDs such as `call_0` in every turn.

`context.include` copies parent state too, each item in its own tagged
block: `todos` (the checklist), `plan` (the scratchpad entry `plan`),
`handoff` (the `handoff` slot) and `scratchpad:<key>`, at most 8 items. A
missing item reads `(none)`.

Everything copied is redacted first and only then shortened, so a cut can
never leave part of a secret the redactor would have caught whole. Each text
block or tool result is cut to about 2,000 characters, each tool input to
500, and each include item to 4,000. The whole forked context fits a budget
of 32,000 characters. Include items come first and take at most half of it;
any that do not fit are left out and counted. Then come as many of the newest
messages as fit, with the number left out stated. `context.full` is bounded
the same way.

Every child is also offered two read-only tools over its parent conversation,
which a root conversation never gets: `parent_history_search(query, k)`, a
full-text search over the parent's messages (the store indexes message text,
not thinking, in `messages_fts`), and `parent_history_get(from, to)`, which
returns up to 20 messages by number, each block up to 8,000 characters.
Search results are whole messages read back from the store, redacted, and
only then cut to a snippet with the matches marked; the index's own text is
never shown. Both tools' results go through the tool-result cache like any
other tool's, so a long one comes back as a page with a pointer. A lead's
children read the lead's conversation. Both belong to the `subtasks`
built-in group, and an authored variant with an allow list must list them
to keep them; the forked context mentions them only when the child has them.

A variant's tool rules govern what a child may call, not what it may read:
a child with parent history tools can read anything in its parent
conversation, including the output of tools its own variant denies. Withhold
`parent_history_*` (or keep the parent from holding it) when that matters.

A pointer to a cached tool result in the copied context belongs to the
parent's cache. A child's `fetch_tool_result` and `search_tool_result` look
in its own cache first, then its parent's, then the root's (for a depth-2
child, the lead's and then the root's); never a sibling's or another
conversation's. The child's own transcript is a normal
child conversation.

### Variants

Variants are `code`, `research`, `worker` and `planner`, built in, plus any
the agent authors as `variants/<name>.md`, re-read every turn. The
frontmatter sets `description`, `tools.allow` / `tools.deny` (names or globs),
`read_only`, `provider`, `model`, `budgets` (`steps`, `tokens`, `wall`,
`cost`), `last_n`, `result_cap` and `can_spawn`; the body is the role prompt.
A variant only narrows the tools the agent already has, and every call still
passes the agent's permission check. `nanite validate` checks authored
variants, including that a `provider` is `anthropic`, `openai` or one defined
in `providers.yaml`.

A variant with `provider` and `model` runs its children on that selection,
resolved like a use in `providers.yaml` (a host-defined provider name works
too). `model` without `provider` runs on the provider of the `subtask` use
(else `chat`), which must serve that model. Compaction
summaries in a child use the `summarizer` use when `providers.yaml` sets one,
and otherwise the provider and model the child runs on.

The built-in `planner` is read-only and also denies every `mcp__*` tool: an
MCP server's `readOnlyHint` is the server's own claim, which Nanite does not
take as proof that a tool is safe for a read-only planner. An authored
variant may allow MCP tools explicitly, for example `tools.allow:
["read_file", "mcp__notes__search"]`.

Budgets only narrow. `budgets.steps` lowers the agent's `max_steps` and never
raises it. A child that spends a budget ends with status `limit` and the
reason: `max_steps`, `token_budget` or `wall_time`. The token budget counts
input, output, cache-read and cache-write tokens, checked between steps; a
lead's spend includes the usage of the children it has finished. The wall
budget is a deadline on the child, so it also stops a step in progress.
`cost` is validated but not yet enforced.

Subtasks run one level deep. A variant with `can_spawn: true` (a "lead") may
start subtasks one more level down; nothing deeper. A lead's children get a
tool only when both the lead and their own variant allow it, so a lead cannot
hand its children a tool it lacks, and `read_only` cannot be combined with
`can_spawn`. A lead with an allow list must list `subtask` itself.

A step's tool calls run in the order the model made them. Consecutive calls
that may run side by side (read-only tools and `subtask`) run together,
bounded by `runtime.tool_concurrency` (default 4); any other call runs alone,
in its place. So a write the model makes before a `subtask` call finishes
before the subtask starts, and results come back in call order. `subtask` is
not marked read-only: its child may write, but each child runs in its own
conversation and its approvals reach the owner one at a time.

Subtasks share two agent-wide limits, each `Options.SubtaskLimit` slots
(else `runtime.subtask_limit` in `host.yaml`; default 4, at most 8, and a
`host.yaml` value above 8 is refused when the agent opens), across every
conversation: one for leads (a
`can_spawn` variant started at depth 1) and one for every other subtask. A
lead's children therefore never compete with the lead for a slot, and a
council of leads cannot starve itself. A foreground subtask over its limit
waits for a slot while its turn lasts; cancelling the turn ends the wait.
Waiting cannot deadlock: a subtask waiting for a slot holds none of its own
kind. A background subtask must not hold up the turn that starts it, so over
the limit it is refused at once.

`Options.TurnTokenBudget` (else `runtime.turn_token_budget` in `host.yaml`),
when set, is a token ceiling one root turn shares
with every foreground subtask it starts, at any depth. Each turn adds its
own model usage (cached tokens included) as it arrives, so a child's tokens
count once; compaction summaries are charged too, as an estimate, since the
summary call reports no usage (a failed summary call is not charged). When
the total reaches the budget, every subtask still running is stopped at
once, mid-step, and ends `limit` with reason `token_budget`, except one that
has already handed back its result, which keeps it; the root stops at its next step boundary. A
foreground subtask does not start on a spent budget, including one that was
waiting for a slot. Background subtasks outlive the turn and draw only on
their variant's own budget. A resumed turn starts a fresh budget. A cost
(USD) budget is not enforced yet: usage carries no price.

Cancelling a parent turn cancels every foreground child it started, and
their slots are released.

Only a root chat starts background subtasks, and only when it is also offered
`background_status`; otherwise the `background` field is not in the tool's
schema. A background subtask runs until it finishes or is cancelled with
`background_cancel`. Its completion notice and `background_status` report the
subtask status (`ok`, `partial`, `limit`, `error` or `cancelled`), and the
notice carries its token usage. Its job output is the same capped result
the parent of a foreground subtask gets; `background_status` embeds it as
JSON under `result`. If the parent conversation has a turn running
when the subtask finishes, that turn also gets a `Usage` event with `Subtask`
set and `Background` true. Background usage is never part of a turn's
totals: summing a turn's `Usage` events without `Background` gives the turn's
usage, foreground subtasks included. chatrunner forwards background usage as
`nanite.subagent` activity with state `usage`, not as a usage delta.

While a subtask runs, its parent's run gets `SubtaskProgress` events: the
child's ID, its parent's ID and depth, its variant, its step (the turn's own
step count, whatever the provider reports), the tool it is calling and a
short status line. They are rate-limited per subtask, at most one each
500ms; a state that arrives sooner is held and sent when the interval
passes, and anything held is sent before the subtask's `SubagentFinished`.
A foreground subtask that has to wait for a slot first sends one with the
status line "waiting for a subtask slot", under the ID it then starts with.
If it never starts, because the wait was cancelled, the turn's budget ran
out, or it failed to start, a last `SubtaskProgress` closes it with `Status`
`cancelled`, `refused` or `error` (there is no `SubagentFinished`, since it
never started).
They are for the host only: the parent model never sees them. A depth-2
subtask's `SubagentStarted`, progress and `SubagentFinished` all reach the
root's run through its lead, each with its parent (the lead) and depth 2. A
background subtask's progress goes to the parent conversation's running
turn, if it has one, and is dropped otherwise. It is marked `Background`,
sent at most every 5s, and its last progress event carries `Status`, how it
ended. chatrunner forwards progress as `nanite.subagent` activity with state
`progress`.

Background Bash uses the same permission check, environment, and denylist as
foreground Bash. A job can outlive its turn and streams bounded, redacted output
to `.nanite/jobs/`. The status tool includes the current 8 KiB log tail. Completion writes a notice for the parent conversation.


### Subtask approvals and questions

A subtask inherits its root conversation's permissions. Its calls are
evaluated as the root's: the same `permissions.yaml` and mode, and the host
PDP with the root's conversation as scope and the root's role. The PDP
request ID is `<child>:<call>`, and the request context adds
`subtask_variant` (the variant, for rules that treat subtasks differently),
`subtask_conversation` and `subtask_depth`. A variant only narrows which
tools a subtask is offered; it never loosens a decision.

Grants flow down, never up or across. An allow for the conversation covers
the conversation it was given on and the subtasks under it: one given in the
root covers every subtask, and one given on a subtask's relayed ask covers
that subtask and its own subtasks only, not the root or a sibling.

A call those rules and grants do not cover is relayed to the root: the
`agent.Gate` gets a `PermissionRequest` with the root's `ConversationID`, ID
`<child>:<call>` and `Subtask` (child conversation, variant, scrubbed goal,
depth, and the root's subtask call ID), and the root's turn reports the
matching `PermissionAsked` and `PermissionResolved`. Only the asking subtask
waits. An MCP elicitation inside a subtask is relayed the same way, as a
`Question` on the root's turn with `Subtask` set.

One ask is open at a time per root conversation: the root's own approvals and
questions and every relayed one take turns, each holding the turn from its
`PermissionAsked` (or `Question`) to its answer, so prompts never overlap and
the stream shows asks in the order the gate gets them.

A gate decides how far an allow reaches. chatrunner's chat cards for a
subtask's ask take only a once answer; the REPL's "always" for a subtask's ask
covers that subtask and its own subtasks.

A refusal reaches the subtask as a tool result with the gate's reason. A
subtask that then ends normally reports status `denied` (reason `approval
denied for <tool>: <reason>`) only when a person refused: a gate sets
`PermissionDecision.Human` for an answer a person gave. A default gate, a
gate failure, an expiry or a policy is not a person's refusal. Only the
engine sets `denied`, after the subtask ends, so it overrides an `ok` or
`partial` the child returned; `return_result` refuses `denied`.

A background subtask may ask after the turn that started it has ended. A
notice tells the root conversation that an ask is waiting (it is withdrawn if
the ask ends before a turn shows it), and the ask is put to the root's next
turn. An ask still open when a turn ends, that nobody
answered, is asked again on the next turn. After `Options.SubtaskAskWait`
(default 30 minutes) in total without a root turn to ask on, the ask ends
denied with reason `unanswered`. It also ends when the subtask is cancelled or
reaches its wall budget.

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

## MCP server adoption boundary

Nanite is an MCP client, not an MCP server. The current CLI and embedding API
have no admin MCP listener or `admin_mcp.go`; the Substrate manifest enables
CLI, secrets, database and policy modules, without the MCP server module.
`internal/mcpclient` owns outbound MCP connections and `internal/engine` uses
those connections to execute discovered tools. There is no application server
to migrate to `go-mcp/server` or wrap in `go-mcp/auth`.

Direct official-SDK server and Streamable HTTP handler construction remains
only in client/engine test fixtures. Those fixtures intentionally exercise
remote-peer protocol behavior, including stateful sessions and elicitation,
without coupling the test peer to Nanite's client library. They are not
production listeners. Gateway credentials and host policy stay in the client
and embedding configuration; adding inbound bearer middleware would not
replace that outbound authentication boundary.

The `plugin-mcp` version follows the Substrate dependency set. Server library
adoption does not require overriding that BOM or introducing an admin server.
