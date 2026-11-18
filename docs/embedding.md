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
  universal: 20 tokens, changed yes
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
Tools offered: ask_user, fetch_tool_result, find_files, list_dir, notes_search, read_file, scratchpad_read, scratchpad_write, search_files, search_tool_result, skill_get, skill_list, subagent_cancel, subagent_spawn, subagent_status, todo_list, todo_write
Tool called: notes_search
The notes mention a planning meeting.
Turn 2 slots:
  universal: 20 tokens, changed no
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
Tools offered: ask_user, fetch_tool_result, files_list, find_files, list_dir, read_file, scratchpad_read, scratchpad_write, search_files, search_tool_result, skill_get, skill_list, subagent_cancel, subagent_spawn, subagent_status, todo_list, todo_write
Tool called: files_list
The workspace contains agenda.md.
Turn 3 slots:
  universal: 20 tokens, changed no
    engine/rules: ship
  system: 72 tokens, changed no
    engine/environment: ship
  agent: 19 tokens, changed no
    file/instructions: ship
  permissions: 79 tokens, changed no
    engine/mode: ship
  workspace: 0 tokens, changed yes
  context: 0 tokens, changed yes
Tools offered: ask_user, fetch_tool_result, find_files, list_dir, read_file, scratchpad_read, scratchpad_write, search_files, search_tool_result, skill_get, skill_list, subagent_cancel, subagent_spawn, subagent_status, todo_list, todo_write
The workspace context is clear.
```

## Choosing the built-in tools

An agent opened by the terminal gets every built-in tool. A host usually
wants fewer: a seat inside an application has no business with a shell.
Set `Options.Builtins` to the groups the agent should have.

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
| `BuiltinSubagents` | `subagent_spawn`, `subagent_status`, `subagent_cancel` |
| `BuiltinSkills` | `skill_list`, `skill_get`, and the skill listing in the prompt |
| `BuiltinNotes` | `todo_list`, `todo_write`, `scratchpad_read`, `scratchpad_write` |
| `BuiltinQuestions` | `ask_user` |

Leaving `Builtins` nil offers all groups. An empty slice offers none. A
withheld tool is not sent to the model at all, which is different from a
permission rule: a denied tool is still described to the model and refused
when called. The two tools that page through a long tool result are always
offered. Tools the host registers with `a.Tools().SetGroup` and MCP tools are
not affected.

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
