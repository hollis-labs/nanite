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
in the provider request, including Nanite's built-ins. The permissions token
count varies with the lengths of the workspace paths.

```text
Turn 1 slots:
  universal: 20 tokens, changed yes
    engine/rules: ship
  agent: 19 tokens, changed yes
    file/instructions: ship
  permissions: 79 tokens, changed yes
    engine/mode: ship
  workspace: 1 tokens, changed yes
    demo/view: ship
  context: 4 tokens, changed yes
    demo/selection: ship
Tools offered: ask_user, background_cancel, background_start, background_status, bash, edit_file, fetch_tool_result, list_dir, notes_search, read_file, scratchpad_read, scratchpad_write, search_tool_result, skill_get, skill_list, subagent_cancel, subagent_spawn, subagent_status, todo_list, todo_write, write_file
Tool called: notes_search
The notes mention a planning meeting.
Turn 2 slots:
  universal: 20 tokens, changed no
    engine/rules: ship
  agent: 19 tokens, changed no
    file/instructions: ship
  permissions: 79 tokens, changed no
    engine/mode: ship
  workspace: 1 tokens, changed yes
    demo/view: ship
  context: 4 tokens, changed yes
    demo/selection: ship
Tools offered: ask_user, background_cancel, background_start, background_status, bash, edit_file, fetch_tool_result, files_list, list_dir, read_file, scratchpad_read, scratchpad_write, search_tool_result, skill_get, skill_list, subagent_cancel, subagent_spawn, subagent_status, todo_list, todo_write, write_file
Tool called: files_list
The workspace contains agenda.md.
Turn 3 slots:
  universal: 20 tokens, changed no
    engine/rules: ship
  agent: 19 tokens, changed no
    file/instructions: ship
  permissions: 79 tokens, changed no
    engine/mode: ship
  workspace: 0 tokens, changed yes
  context: 0 tokens, changed yes
Tools offered: ask_user, background_cancel, background_start, background_status, bash, edit_file, fetch_tool_result, list_dir, read_file, scratchpad_read, scratchpad_write, search_tool_result, skill_get, skill_list, subagent_cancel, subagent_spawn, subagent_status, todo_list, todo_write, write_file
The workspace context is clear.
```
