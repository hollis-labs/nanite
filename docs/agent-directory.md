# Agent directory

```text
my-agent/
  agent.md                required portable definition and instructions
  host.yaml               required local execution profile
  .mcp.json               optional MCP registrations
  permissions.yaml        optional tool rules and default mode
  skills/<name>/SKILL.md  optional skill metadata and instructions
  variants/<name>.md      optional subtask variants
  context/*.md            optional Markdown context entries
  .nanite/                generated private state
```

Run `nanite init ./my-agent` to create a starter and `nanite validate ./my-agent`
to check the authored directory. Nanite reads the authored files again at the
start of each turn. The loader requires real directories and regular files at
the authored paths, and limits each authored file to 1 MiB.

Run `nanite` inside the agent directory, or point at it from somewhere else
with `nanite -C ./my-agent`. With `-C`, the directory you run from is the
workspace: file tools and Bash resolve relative paths there first, and the
agent directory stays readable as a second root. Conversations are stored in
the agent directory either way.

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
  subtask_limit: 4
  turn_token_budget: 200000
  subtask_result_cap: 6000
  env:
    PROJECT_MODE: development
permissions:
  mode: default
```

`runtime.provider` accepts `anthropic` (default) or `openai`. If you omit
`model`, Nanite uses `NANITE_DEFAULT_MODEL` or `claude-sonnet-5-5`. Set an
appropriate model explicitly when selecting OpenAI. Numeric limits accept
nonnegative values. Zero selects the engine or adapter default. The engine
defaults to 50 model steps and four concurrent tool calls. `subtask_limit`
is how many subtasks run at once, separately for leads and other subtasks
(default 4, at most 8; a higher value is refused when the agent opens).
`turn_token_budget` is a token ceiling a turn shares with its subtasks (zero
is unlimited). `subtask_result_cap` caps a subtask's result in tokens (zero
means about 5,900, the most one tool result shows in full). An embedding
host's non-zero `agent.Options` value wins over these. `env` supplies
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

### Named providers

`providers.yaml` in the agent directory defines more providers and picks one
per use. Any OpenAI-compatible server (llama.cpp, vLLM, Ollama, OpenRouter)
works through the `openai-chat` wire, and a model gateway is one more entry.
The file is optional.

```yaml
providers:
  local:
    wire: openai-chat               # Chat Completions only
    base_url: http://127.0.0.1:8080/v1
    auth: none                      # a local server without auth
  router:
    wire: openai-chat
    base_url: https://openrouter.ai/api/v1
    key_ref: keychain://nanite/provider/router
  gateway:
    wire: anthropic
    base_url: https://gateway.example/api/llm-gateway
    auth: bearer
    headers: {X-Team: research}
use:
  chat:       {provider: local, model: llama-3.1-8b-instruct}
  summarizer: {provider: anthropic, model: claude-haiku-5-5}
  subtask:    {provider: gateway, model: claude-sonnet-5-5}
```

Each provider entry has:

- `wire`: `anthropic` (Messages API), `openai` (chooses Chat Completions or
  Responses per request, like the built-in), `openai-chat` or
  `openai-responses`.
- `base_url`: the endpoint root. Use it without `/v1` for `anthropic` and with
  `/v1` for the OpenAI wires.
- Optional `key_ref`: a secret reference. Without one the resolver checks
  `keychain://nanite/provider/<name>`.
- Optional `key_env`: an environment variable, read only when
  `NANITE_PROVIDER_KEY_ENV=1`.
- Optional `auth`: empty for the wire's own scheme, `bearer` for the
  Anthropic wire, or `none` for a server without auth.
- Optional `headers`: sent on every request. A header whose name looks like
  a credential (containing `key`, `token`, `secret`, `auth`, `password` or
  `credential`) is refused; put the credential behind `key_ref`.
- Optional `disable_anthropic_beta` and `disable_cache_control`: for an
  Anthropic-compatible endpoint that rejects them.

The names `anthropic` and `openai` are the built-in providers above and cannot
be redefined.

`use` selects a provider and model for `chat` (the conversation and argument
repair), `summarizer` (compaction summaries) and `subtask` (subtask
conversations). A use without its own entry falls back to `chat`.
Without `use.chat`, chat uses `runtime.provider` and `runtime.model` from
`host.yaml`. `providers.yaml` is read again each turn, like the other authored
files. A use may name a provider only the host defines.

An embedding host can add providers with `agent.Options.Providers`, and a
host entry wins over a file entry with the same name. When the host injects
`agent.Options.Provider`, that provider serves every use except one that
names a provider in `agent.Options.Providers`, so an agent directory cannot
route the host's traffic elsewhere.

A provider with a custom endpoint, whether named or the built-in one behind
a gateway, receives only the key Nanite resolved. The SDKs' environment
defaults (`ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `OPENAI_ORG_ID`,
`OPENAI_PROJECT_ID`, `OPENAI_CUSTOM_HEADERS`) are not sent to it.

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

| Mode | What it does |
|---|---|
| `default` | Read-only tools run. Tools that change something ask first. |
| `accept-edits` | As `default`, and `write_file` and `edit_file` run without asking. |
| `plan` | Read-only tools run. Everything else is denied; rules cannot allow it. |
| `yolo` | Everything runs, including tools a rule denies. |

Something must answer an "ask". The interactive terminal prompts with
`[y/N/always]`; `always` allows that tool for the rest of the conversation. A
one-shot run (`nanite -p`) has nobody to ask, so an "ask" is denied and the
model is told so. Allow the tool with a rule if a one-shot run needs it.

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

## Subtask variants

A file in `variants/` defines a subtask variant: a kind of child
conversation the agent can start with the `subtask` tool. The file name is
the variant's name (lower-case letters, digits and dashes). Optional YAML
frontmatter sets its description, tools, model, budgets and defaults, and
the Markdown body is its role prompt. Nanite reads the files again at the
start of each turn, and `nanite validate` checks them. `nanite init` writes a
commented example, `variants/reviewer.md`. The fields, the built-in variants
and how subtasks work are in [code-mode.md](code-mode.md).

## Generated state

- `agent.lock` holds the single-process lock for the data directory.
- `transcripts/<conversation ID>/` contains numbered JSONL journal segments.
- `nanite.db` and its SQLite sidecars hold the index, notices, and job state.
- `jobs/` contains bounded background job output logs.
- `events.jsonl` is the CLI's default runtime event log.

Keep this directory private and out of version control. Back up the transcripts
to preserve conversation history. `nanite rebuild-index` replays transcript
records into the index.
