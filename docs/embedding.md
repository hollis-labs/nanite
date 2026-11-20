# Embed Nanite in a Go application

Run the [example host](../examples/embed-host/main.go) from the repository root.

```sh
go run ./examples/embed-host --offline
```

The command copies its bundled [authored agent](../examples/embed-host/agentdir/agent.md)
and [host profile](../examples/embed-host/agentdir/host.yaml) to a temporary
directory and opens it with `agent.Open`. The offline provider makes scripted
model requests and tool calls. Omit `--offline` to use the authored provider
with `NANITE_GATEWAY_URL`, `NANITE_GATEWAY_KEY_REF`, and optional
`NANITE_GATEWAY_AUTH` set for your gateway. Use `--dir` to supply an existing
authored directory and retain its conversations.

1. Open an agent and create a conversation. Keep that conversation across turns.
2. Give your application an owner with `a.Slots().Owner("demo")`. The example
   uses `Replace` to set the workspace and selection before each send.
3. Register tools with `a.Tools().SetGroup("demo", tool)`. Replacing a group
   removes the previous group's tools from the next turn. The first turn offers
   `notes_search` and the second offers `files_list`.
4. Use `conversation.Send` and consume `run.Events()`. Handle question events
   with `run.Answer`, then call `run.Wait` for the result and error. A host that
   wants to approve permission requests implements `agent.Gate`. The default
   gate denies requests that need approval.
5. Use `writer.Clear()` and `a.Tools().RemoveGroup("demo")` to remove the host
   contributions before the third turn. Call `a.Close()` at shutdown.

`SlotsAssembled` reports each slot's estimated token count, whether its content
changed since the previous turn, and each entry's disposition. A changed slot
with zero tokens was cleared. The example also prints the names it observes
in the provider request, including Nanite's built-ins. The system and permissions token
counts vary with the lengths of the workspace paths.

```text
Turn 1 slots:
  universal: 625 tokens, changed yes
    engine/rules: ship
  system: 72 tokens, changed yes
    engine/environment: ship
  agent: 19 tokens, changed yes
    file/instructions: ship
  permissions: 79 tokens, changed yes
    engine/mode: ship
  workspace: 1 tokens, changed yes
    demo/view: ship
  context: 4 tokens, changed yes
    demo/selection: ship
Tools offered: ask_user, fetch_tool_result, find_files, list_dir, notes_search, read_file, scratchpad_read, scratchpad_write, search_files, search_tool_result, skill_get, skill_list, subtask, todo_list, todo_write
Tool called: notes_search
The notes mention a planning meeting.
Turn 2 slots:
  universal: 625 tokens, changed no
    engine/rules: ship
  system: 72 tokens, changed no
    engine/environment: ship
  agent: 19 tokens, changed no
    file/instructions: ship
  permissions: 79 tokens, changed no
    engine/mode: ship
  workspace: 1 tokens, changed yes
    demo/view: ship
  context: 4 tokens, changed yes
    demo/selection: ship
Tools offered: ask_user, fetch_tool_result, files_list, find_files, list_dir, read_file, scratchpad_read, scratchpad_write, search_files, search_tool_result, skill_get, skill_list, subtask, todo_list, todo_write
Tool called: files_list
The workspace contains agenda.md.
Turn 3 slots:
  universal: 625 tokens, changed no
    engine/rules: ship
  system: 72 tokens, changed no
    engine/environment: ship
  agent: 19 tokens, changed no
    file/instructions: ship
  permissions: 79 tokens, changed no
    engine/mode: ship
  workspace: 0 tokens, changed yes
  context: 0 tokens, changed yes
Tools offered: ask_user, fetch_tool_result, find_files, list_dir, read_file, scratchpad_read, scratchpad_write, search_files, search_tool_result, skill_get, skill_list, subtask, todo_list, todo_write
The workspace context is clear.
```

## Choosing the built-in tools

The application decides what its embedded agent can do. An agent whose
`host.yaml` says `kind: embedded` gets **no built-in tools** unless the host
names them in `Options.Builtins`; it has the tools the host registers and its
MCP tools. A stable or task agent, and any agent run by the terminal `nanite`
command, gets every built-in tool by default.

```go
a, err := agent.Open(ctx, agent.Options{
    Dir:      dir,
    Builtins: []agent.BuiltinGroup{agent.BuiltinFilesRead, agent.BuiltinSkills, agent.BuiltinNotes},
})
```

| Group | Tools |
|---|---|
| `BuiltinFilesRead` | `read_file`, `list_dir`, `find_files`, `search_files` |
| `BuiltinFilesWrite` | `write_file`, `edit_file` |
| `BuiltinShell` | `bash` |
| `BuiltinBackground` | `background_start`, `background_status`, `background_cancel` |
| `BuiltinSubtasks` | `subtask`, plus `parent_history_search` and `parent_history_get`, which only a subtask's child conversation is offered. Every child also gets `return_result`, which ends its subtask; it belongs to no group and is never offered to a root conversation. Background subtasks are offered only together with `BuiltinBackground`, whose `background_status` and `background_cancel` poll and cancel them. The built-in `planner` variant denies `mcp__*` tools. `Options.SubtaskResultCap` sets the cap on a subtask's whole result in tokens (zero means the effective default of about 5,900; a variant's `result_cap` overrides it; either is clamped to the 23KB a tool result shows in full). These three subtask settings can also come from `host.yaml` (`runtime.subtask_result_cap`, `runtime.subtask_limit`, `runtime.turn_token_budget`), read when the agent opens; a non-zero Option wins over `host.yaml`, and a `host.yaml` `subtask_limit` above 8 is refused. `Options.SubtaskLimit` sets how many subtasks run at once across the agent, separately for leads and for other subtasks (zero means 4, at most 8; a foreground subtask over the limit waits for a slot, a background one is refused), and `Options.TurnTokenBudget` a token ceiling a root turn shares with its foreground subtasks, enforced mid-step (zero means none). Consecutive `subtask` and read-only calls in one step run side by side; calls otherwise run in model order. See "Subtasks and background jobs" in `architecture.md`. |
| `BuiltinSkills` | `skill_list`, `skill_get`, and the skill listing in the prompt |
| `BuiltinNotes` | `todo_list`, `todo_write`, `scratchpad_read`, `scratchpad_write` |
| `BuiltinQuestions` | `ask_user` |

Leaving `Builtins` nil gives the default for the agent's kind, as above.
Pass `agent.AllBuiltins()` to give an embedded agent everything. An empty
slice offers none for any kind. A
withheld tool is not sent to the model at all, which is different from a
permission rule: a denied tool is still described to the model and refused
when called. The two tools that page through a long tool result are always
offered. Tools the host registers with `a.Tools().SetGroup` and MCP tools are
not affected.

## Panics in host code

A panic in code the host gives Nanite does not take the host process down.
Nanite contains it at each boundary and turns it into an error carrying only
a reference, `internal error (ref pnc_...)`:

| Where it panics | What happens |
|---|---|
| A host tool's handler | The call gets an error tool result; the turn continues. |
| A slot source or the slot selector | The turn fails before any model request. |
| The approval gate or the PDP | The call is denied. |
| The event sink | The event is dropped; the turn continues. |
| A provider stream | The turn fails and is recorded as failed. |
| A background job | The job is marked failed. |

The next turn works as usual. The record of each panic, with its value and
stack passed through the agent's redactor, goes to `Options.Logger` and to
`Options.PanicReporter` when set. `agent.PanicRef(err)` returns the reference
from an error. Nanite never replaces the process-wide default guard of
`libs/core/panics`; that is the host's to set.

## Where an application keeps its agents

Nanite expects nothing beyond a directory path, so this is a convention for
hosts to share.

- One setting in the application's config, `agents.dir`, names a directory.
  The default is `agents/` beside the config file.
- Each agent is a subdirectory, and the directory name is the agent's name.
- The authored files (`agent.md`, `host.yaml`, `permissions.yaml`, `skills/`,
  `context/`) live there. They can be checked in or shipped read-only.
- The agent's `host.yaml` says `kind: embedded`, `lifetime: application`, and
  an `application_id` naming the application.
- Run-time state need not sit beside the authored files. Pass
  `Options.DataDir`, one directory per agent under the application's own
  state root. When it is empty, state goes in `.nanite/` inside the agent
  directory, as the terminal does.

Only one process can hold an agent's state directory open. If the
application keeps state in its own `DataDir`, `nanite -C agents/<name>` from a
terminal still works while the application runs: it reads the same authored
files and keeps separate conversations in `.nanite/` inside the agent
directory. If the application leaves `DataDir` empty, the two share one state
directory and the second to start is refused.

## Hosting an agent behind the chat handler

`github.com/hollis-labs/nanite/chatrunner` runs an agent behind the
conversation handler in `github.com/hollis-labs/libs/agent/chat`. It
implements `chat.Runner`, so the application gets the conversation routes,
replay, leases and approvals from the library and Nanite does the turns.

```go
r, err := chatrunner.Open(ctx, chatrunner.Options{
    Agent: agent.Options{Dir: "agents/desk", DataDir: stateDir},
    Tools: []chatrunner.Tool{{
        Name: "notes_save", Description: "Save a note.",
        InputSchema: schema, Level: chatrunner.LevelWrite,
        Run: func(ctx context.Context, call agent.ToolCall) (agent.ToolResult, error) { ... },
    }},
})
svc, err := chat.NewService(chat.Config{Runner: r, ...})
```

`Options.Agent` is the same `agent.Options` as above, so `Builtins` chooses
Nanite's own tools as usual. Leave `Gate` nil: approvals are answered on the
chat wire. `r.Agent()` gives the agent for slots, skills and notices, and
`r.Close()` releases the directory.

**Conversations.** Each chat conversation is one Nanite conversation, created
on its first turn. Its ID is `chatrunner.ConversationID(chatID)` and is the
`runner_ref` on the chat conversation record. A turn the previous process
left unfinished is discarded when the conversation is next used.

**The turn's context.** `Turn.Context` goes into one slot entry
(`SlotUserContext` unless `Options.ContextSlot` names another) for that turn
only. A JSON string is placed as its text; any other JSON is placed as
written. Nanite still assembles everything else.

**Host tools.** A tool has a level, and the level decides whether a call asks
first. Replace the whole set between turns with `r.SetTools(...)`. A tool's
`Run` function can read the turn with `chatrunner.TurnFromContext(ctx)`.

| Level | In `default` mode |
|---|---|
| `LevelRead` | Runs without asking. |
| `LevelAct` | Runs without asking. |
| `LevelWrite` | Asks. An answer for the conversation, or a standing grant, covers later calls. |
| `LevelConfirm` | Asks every time, including in `yolo` mode. No answer covers a later call. |

An empty or unknown level is `LevelWrite`. `Options.Ask` is called before
each host-tool call and can make that one call ask, whatever the level; it
cannot make a call looser. `Options.Granted` reports a standing grant the
application stored through `chat.Grants`; a covered call runs without asking.
The agent's `permissions.yaml` and mode still apply to every tool.

**Approvals.** A call that asks becomes `approval.request` and the turn waits
for the answer the Service delivers. Its `descriptor` carries `description`,
`standing` (false for a `LevelConfirm` tool), `tool` and `input`. The scopes
`conversation` and `always` both stand for the rest of the conversation in
Nanite; keeping `always` beyond it is the application's `Granted` function.

**Subtask approvals.** A subtask asks through its root conversation. Its
call is decided under the root's rules and grants, so a tool the conversation
already allowed runs in the subtask without asking. Anything still needing an
answer arrives on the root's turn as one `approval.request` with ID
`<child>:<call>`, `call_id` set to the root's `subtask` tool call (so the card
can be shown with it), a `descriptor.description` of `subtask "<goal>" wants
<tool>: … (this call only)`, `descriptor.standing` false, `grant_scope`
`once` and a `descriptor.subtask` object (`child_id`, `variant`, `goal`,
`depth`, `call_id`). It takes only a once answer: a standing grant would be
kept against the root chat and reach the root and the subtask's siblings,
and the chat wire has no scope for one subtask. A controller's denial ends
the subtask with status `denied`; an expiry does not count as one.

**Events.** Each model step is a `step.start` / `step.finish` pair around one
assistant message that holds the step's text, reasoning, tool calls and tool
results. Activity kinds are `nanite.slots`, `nanite.subagent` (a subtask's
child conversation, with `variant`, `parent_id` and `depth`, and `status`
when done; state `progress` carries `step`, `tool` and `status_line` while
it runs, and a background subtask's start and progress carry `background`) and
`nanite.compact` (`value.state` is `start`, `done` or `failed`). `run.finish`
has reason `turn_limit` at the step limit and `length` at the model's output
limit. A cancelled turn ends in `run.abort`, a failed one in `run.error` with
code `turn_failed`. The `ask_user` tool is declined.
