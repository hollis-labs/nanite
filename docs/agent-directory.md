# Agent directory

```text
my-agent/
  agent.md                required portable definition and instructions
  host.yaml               required local execution profile
  .mcp.json               optional MCP registrations
  permissions.yaml        optional tool rules and default mode
  skills/<name>/SKILL.md  optional skill metadata and instructions
  context/*.md            optional Markdown context entries
  .nanite/                generated private state
```

Run `nanite init ./my-agent` to create a starter and `nanite validate ./my-agent`
to check the authored directory. Nanite reads the authored files again at the
start of each turn. The loader requires real directories and regular files at
the authored paths, and limits each authored file to 1 MiB.

## Portable definition

`agent.md` has schema-2 YAML frontmatter between `---` lines and Markdown
instructions below it. The starter supplies `schema_version`, `definition_id`,
`revision`, `name`, `description`, `behavior.purpose`, `requirements`,
`harness_profile.permissions.profile`, and `continuity.mode`. The Markdown
body is shipped in the Agent slot. The schema also supports `title`,
`capabilities`, `extensions`, `presentation`, and `provenance`, plus optional
behavior, dependency, harness, and continuity declarations. The portable
permission profile is a name. Nanite's effective tool mode comes from the
local execution settings below.

## Host profile

| `kind` | `lifetime` | Required identity | Agent continuity |
| --- | --- | --- | --- |
| `stable` | `on-demand` | `seat_id` | `durable` |
| `task` | `until-task-end` | `task_id` | `durable` |
| `embedded` | `application` | `application_id` | `durable` or `ephemeral` |

Supply only the identity key for the selected kind.

```yaml
kind: embedded
lifetime: application
application_id: my-app
runtime:
  provider: anthropic
  model: claude-sonnet-5-5
  max_output_tokens: 4096
  max_steps: 50
  context_window: 200000
  tool_concurrency: 4
  env:
    PROJECT_MODE: development
permissions:
  mode: default
```

`runtime.provider` accepts `anthropic` (default) or `openai`. If you omit
`model`, Nanite uses `NANITE_DEFAULT_MODEL` or `claude-sonnet-5-5`. Set an
appropriate model explicitly when selecting OpenAI. Numeric limits accept
nonnegative values. Zero selects the engine or adapter default. The engine
defaults to 50 model steps and four concurrent tool calls. `env` supplies
literal values to both foreground and background Bash. Use shell variable
names and keep credentials out of this file.

### Provider credentials and gateway

The CLI reads `SUBSTRATE_CONFIG`, then `NANITE_CONFIG`, then the optional
`$XDG_CONFIG_HOME/nanite/nanite.yaml` (under `~/.config` by default).
It reads `nanite.gateway.url` (or `base_url`), `key_ref`, `auth`, and
`headers` from that configuration. `NANITE_GATEWAY_URL` and
`NANITE_GATEWAY_KEY_REF` override their respective settings when nonempty. Use the gateway root for Anthropic and an endpoint
with `/v1` for OpenAI. Anthropic supports its default key header or
`auth: bearer`. The secret resolver checks the configured key reference, then
`keychain://nanite/provider/<provider>`. Set `NANITE_PROVIDER_KEY_ENV=1` to
enable fallback to `ANTHROPIC_API_KEY` or `OPENAI_API_KEY`. An embedding host
configures these through `agent.Options.Gateway` or supplies a provider.

## MCP registrations

```json
{
  "mcpServers": {
    "local": {
      "command": "example-mcp",
      "args": ["--stdio"],
      "env": {"EXAMPLE_TOKEN": "${EXAMPLE_TOKEN}"}
    },
    "remote": {
      "type": "http",
      "url": "https://example.com/mcp",
      "headers": {"Authorization": "${EXAMPLE_AUTHORIZATION}"}
    }
  }
}
```

A command registration defaults to `stdio` and may supply `args` and `env`.
HTTP registrations use `type: http` for streamable HTTP or `type: sse` for
legacy SSE, a URL, and optional headers. A whole value in the form
`${VARIABLE}` resolves an environment variable. An environment or header
value can also be a secret reference supported by the configured resolver.
Tools are exposed as `mcp__<server>__<tool>`.

## Tool permissions

```yaml
permissions:
  mode: default
  rules:
    - tool: mcp__remote__*
      behavior: ask
    - tool: write_file
      pattern: "*.lock"
      behavior: deny
```

Each rule has a tool name or glob, an optional input pattern, and a behavior
of `allow`, `ask`, or `deny`. Deny takes priority over ask, then allow.
The first match within a behavior wins. The effective mode is
`host.yaml`'s `permissions.mode`, then the rule file's `mode`, then
`default`. Modes are `default`, `accept-edits`, `plan`, and `yolo`.
An embedding host can add a policy decision point and an approval gate.
File-tool confinement and Bash behavior are described in the
[architecture limitations](architecture.md#known-limitations).

## Skills and context

A skill lives one level below `skills/` and has a `SKILL.md` file. Optional
YAML frontmatter supplies `name` (defaults to the directory name) and
`description`. The Markdown body is fetched on demand with `skill_get`.
Directory skills take precedence over skills of the same name in
`~/.nanite/skills` or provided by the embedding host. Nanite adds each direct
Markdown file in `context/` to the Context slot. Other extensions are ignored.

## Generated state

- `agent.lock` holds the single-process lock for the data directory.
- `transcripts/<conversation ID>/` contains numbered JSONL journal segments.
- `nanite.db` and its SQLite sidecars hold the index, notices, and job state.
- `jobs/` contains bounded background job output logs.
- `events.jsonl` is the CLI's default runtime event log.

Keep this directory private and out of version control. Back up the transcripts
to preserve conversation history. `nanite rebuild-index` replays transcript
records into the index.
