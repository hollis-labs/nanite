# Running Nanite headless

A program can drive Nanite the way it drives `claude -p`: the same flags, the same stream-json
messages in and out. exo runs Nanite like this, with the adapter it uses for Claude Code.

## One prompt

```sh
nanite -p "Summarize this workspace"                        # the answer as text
nanite -p "Summarize this workspace" --output-format json   # one result object
nanite -p --output-format stream-json "Summarize this workspace"
```

`--output` (text, json, or Nanite's own event envelope as stream-json) works as before. Use
`--output-format` for Claude Code's shapes.

## A long-running session

```sh
nanite -p --input-format stream-json --output-format stream-json --session-id 6f0c…
```

Nanite reads one JSON object per line from stdin and runs until stdin closes:

```json
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"Fix the failing test"}]}}
{"type":"control_request","request_id":"r1","request":{"subtype":"interrupt"}}
```

- A user message's `content` is a string or a list of blocks (their `text` blocks are joined).
- A message that arrives while a turn runs waits; each message gets its own `result`.
- `interrupt` cancels the running turn and is answered with
  `{"type":"control_response","response":{"subtype":"success","request_id":"r1"}}`.
  Other control requests are answered with an error, except `initialize`.
- SIGINT interrupts the running turn; between turns it ends the session.

## Output

One JSON object per line:

| `type` | When | Fields |
| --- | --- | --- |
| `system` (`subtype: init`) | first | `session_id`, `model`, `cwd`, `tools`, `mcp_servers`, `permissionMode` |
| `assistant` | each text, thinking or tool call | `message.content`: `text`, `thinking` or `tool_use` (`id`, `name`, `input`) blocks |
| `user` | each tool result | `message.content`: a `tool_result` block (`tool_use_id`, `content`, `is_error`) |
| `result` | each turn's end | `subtype` (`success`, `error_during_execution`, `error_max_turns`), `is_error`, `result`, `stop_reason`, `usage`, `num_turns`, `duration_ms`, `permission_denials`, `queued_turn_count` |

An interrupted turn ends with `subtype: error_during_execution`, `stop_reason: interrupted` and
`is_error: false`. A failed one has `is_error: true` and the error in `result`. With `json`
output only the `result` object is written; with `text`, the answer.

## Claude Code's flags

These take the values `claude -p` takes, so the same argv drives either:

| Flag | What Nanite does |
| --- | --- |
| `--model <name>` | the chat model for this run, over `host.yaml` and `providers.yaml` |
| `--permission-mode <mode>` | `default`, `acceptEdits`, `plan` or `bypassPermissions` (Nanite's `default`, `accept-edits`, `plan`, `yolo` work too) |
| `--dangerously-skip-permissions` | `bypassPermissions` |
| `--mcp-config <file or JSON>` | MCP servers in `.mcp.json`'s shape, added over the agent directory's; repeatable |
| `--strict-mcp-config` | only the `--mcp-config` servers |
| `--append-system-prompt <text>`, `--append-system-prompt-file <path>` | instructions after the agent's own (the Context slot) |
| `--settings <file or JSON>` | `permissions.allow` / `ask` / `deny` in claude's rule syntax, `permissions.defaultMode`, `permissions.additionalDirectories`, `env`, `model`. Other keys (hooks, sandbox, ...) are named on stderr and ignored. |
| `--add-dir <dir>` | another directory the file tools may use; repeatable |
| `--plugin-dir <dir>` | the plugin's `skills/` are offered (the agent's own skill of a name wins) |
| `--permission-prompt-tool mcp__<server>__<tool>` | approvals go to that MCP tool (below) |
| `--setting-sources`, `--verbose` | accepted |

Permission rules map claude's tool names to Nanite's: `Bash` → `bash`, `Read` → `read_file`,
`Write` → `write_file`, `Edit`/`MultiEdit` → `edit_file`, `Glob` → `find_files`, `Grep` →
`search_files`, `LS` → `list_dir`. `Bash(git log:*)` matches commands starting `git log`;
`mcp__srv` matches every tool of that server. Deny wins over ask, and ask over allow.

### Approvals through an MCP tool

With `-p`, a tool call that needs approval is otherwise denied. With
`--permission-prompt-tool mcp__exo_runner__approve`, Nanite calls that tool (its server is in
`--mcp-config` or the agent directory's `.mcp.json`) with

```json
{"tool_name": "write_file", "input": {"path": "notes.md", "content": "..."}, "tool_use_id": "..."}
```

and reads claude's reply from the text it returns: `{"behavior":"allow"}` runs the call,
`{"behavior":"deny","message":"..."}` refuses it with that message. Nanite runs the call with its
original input (`updatedInput` is not applied). A refused call is listed in the turn's
`result.permission_denials`.

## Sessions

`--session-id <id>` opens that conversation, creating it the first time, so the caller picks
the id (a UUID works). `--resume <id>` and `--continue` work as before; neither combines with
`--session-id`.
