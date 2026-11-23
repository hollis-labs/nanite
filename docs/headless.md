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

## Sessions

`--session-id <id>` opens that conversation, creating it the first time, so the caller picks
the id (a UUID works). `--resume <id>` and `--continue` work as before; neither combines with
`--session-id`.
