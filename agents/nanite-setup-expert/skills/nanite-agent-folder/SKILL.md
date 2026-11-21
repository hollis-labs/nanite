---
name: nanite-agent-folder
description: The Nanite agent folder format, what each file does, safe defaults, and how to validate a folder.
---
# The Nanite agent folder

```text
my-agent/
  agent.md                required: schema-2 frontmatter + charter (Markdown body)
  host.yaml               required standalone: kind, lifetime, identity key, runtime
  skills/<name>/SKILL.md  procedures fetched on demand (skill_list, skill_get)
  context/*.md            facts added to every turn (keep short)
  variants/<name>.md      subtask variants: internal helpers that narrow tools
  tools/<name>/tool.yaml  agent-built tool packages (each call is approved by digest)
  steering.json           modes, work slots and reflexes
  .mcp.json               MCP servers (standalone)
  identity.yaml           mesh principal + Warden tokens (standalone)
  permissions.yaml        tool allow/ask/deny rules
  .nanite/                generated state; never edit
```

Start from `examples/specialist` in the Nanite repo, or `nanite init <dir>`.
Check with `nanite validate <dir>`; it must print the agent's name.

## agent.md

Frontmatter fields: `schema_version: "2"`, `definition_id: def:<name>`,
`revision`, `name`, `description`, `behavior.purpose`, `requirements: {}`,
`harness_profile.permissions.profile`, `continuity.mode` (`durable` keeps
conversations). The body is the charter, read every turn. Use the five
sections: Role, You own, Not yours, How you work, Standards. Keep it under a
page.

## host.yaml

| Key | Example value | Meaning |
|---|---|---|
| `kind` | `embedded` | `embedded`, `stable` or `task` |
| `lifetime` | `application` | `application`, `on-demand` or `until-task-end` |
| `application_id` | the agent's name | the identity key for the kind (`seat_id`, `task_id` for the others) |
| `runtime.provider` | `anthropic` | or `openai` (then set a model) |
| `runtime.model` | (optional) | a host usually supplies it |
| `runtime.max_steps` | 40 | model steps per turn |
| `runtime.max_output_tokens` | 0 | 0 means the adapter default |
| `runtime.cache_ttl` | `1h` | empty, `5m` or `1h` prompt caching |
| `runtime.compact_at` | 110000 | token count at which older turns are summarised |
| `runtime.compact_keep_tokens` | 22000 | how much recent history compaction keeps whole |
| `runtime.daily_token_budget` | 2000000 | cap per UTC day; 0 means no cap |
| `runtime.turn_token_budget` | 0 | cap per turn including subtasks; 0 means no cap |
| `permissions.mode` | `default` | `default`, `accept-edits`, `plan` or `yolo` |

`runtime` and `permissions` are nested maps in the YAML file.

Safe defaults for a new agent: `max_steps` 40, `compact_at` 110000 with
`compact_keep_tokens` 22000, and always a `daily_token_budget` during pilots.
An embedding host's non-zero options override these.

## Approvals and auto mode

Tools that change something ask first. Agent-built tool packages ask per
package digest, stored outside the agent folder (the host's data dir). Auto
mode (`agent.Options.AutoApprove`, or Surface's `auto_approve`) turns asks
into allows and logs each one; explicit denies still deny. It is for testing,
and the owner decides.

## Stop and limits

At the daily cap, new turns and subtasks are refused with a clear error, and
the owner gets one notice at 80%. `nanite stop --all` (or `Agent.StopAll`)
cancels everything and refuses new work until the agent is reopened.
