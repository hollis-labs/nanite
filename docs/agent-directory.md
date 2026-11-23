# Agent directory

```text
my-agent/
  agent.md                required portable definition and instructions
  host.yaml               required local execution profile
  .mcp.json               optional MCP registrations
  identity.yaml           optional mesh identity and Warden auth
  permissions.yaml        optional tool rules and default mode
  skills/<name>/SKILL.md  optional skill metadata and instructions
  variants/<name>.md      optional subtask variants
  tools/<name>/tool.yaml  optional tool packages (agent-built tools)
  context/*.md            optional Markdown context entries
  .nanite/                generated private state
```

Run `nanite init ./my-agent` to create a starter and `nanite validate ./my-agent`
to check the authored directory. Nanite reads the authored files again at the
start of each turn. The loader requires real directories and regular files at
the authored paths, and limits each authored file to 1 MiB.

Run `nanite` inside the agent directory, or point at it from somewhere else
with `nanite -C ./my-agent` (also `--agent-dir`). With `-C`, the directory you
run from is the workspace: file tools and Bash resolve relative paths there
first, and the agent directory stays readable as a second root. Conversations
are stored in the agent directory either way.

In a directory that is not an agent directory, without `-C`, Nanite's home
(`~/.nanite`) is the agent instead, with per-session state and the project's
`AGENTS.md` and settings: see [working in any directory](workspace.md). The
rest of this page applies to the home too, since it is an agent directory.

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
  cache_ttl: 1h
  context_window: 200000
  tool_concurrency: 4
  subtask_limit: 4
  turn_token_budget: 200000
  daily_token_budget: 1000000
  subtask_result_cap: 6000
  compact_at: 110000
  compact_keep_tokens: 22000
  compact_input_tokens: 100000
  idle_timeout: 3m
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
means about 5,900, the most one tool result shows in full). `compact_at` is the request size in tokens at which the root conversation
compacts (zero uses 80% of `context_window`); `compact_keep_tokens` is about
how much of the end of the conversation a compaction keeps whole, by turns
(zero keeps the last four messages; it must be below `compact_at`);
`compact_prompt` replaces the summary prompt; `compact_input_tokens` bounds a
summary call's input (zero means 100,000). `idle_timeout` is how long a
model stream may send nothing before the turn gives up, as a Go duration
(default `90s`). These are read when the agent opens. An embedding
host's non-zero `agent.Options` value wins over these (`CompactAt`,
`CompactKeepTokens`, `CompactionPrompt`, `CompactionInputTokens`,
`IdleTimeout`). `env` supplies
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
  desk:       {provider: local, model: llama-3.2-1b}
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
  Anthropic wire, `none` for a server without auth, or `warden` for a
  short-lived Warden token (see [Identity and Warden auth](#identity-and-warden-auth)).
- With `auth: warden`, `audience` (the gateway's app name, such as
  `llm-gateway`) and optional `scopes` (such as `[llm-gateway.chat]`). No
  `key_ref` or `key_env`.
- Optional `headers`: sent on every request. A header whose name looks like
  a credential (containing `key`, `token`, `secret`, `auth`, `password` or
  `credential`) is refused; put the credential behind `key_ref`.
- Optional `disable_anthropic_beta` and `disable_cache_control`: for an
  Anthropic-compatible endpoint that rejects them.

The names `anthropic` and `openai` are the built-in providers above and cannot
be redefined.

`use` selects a provider and model for `chat` (the conversation and argument
repair), `summarizer` (compaction summaries), `subtask` (subtask
conversations) and `desk` (the front desk's planner, see
[embedding](embedding.md)). A use without its own entry falls back to `chat`.
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

A tool that edits an agent directory can check `providers.yaml` with
`github.com/hollis-labs/nanite/agent/authored`: `ParseProviders` takes the
content, `LoadProviders` reads the file. Nanite loads it through the same
functions, so they apply exactly the engine's rules.

## Identity and Warden auth

`identity.yaml` says who the agent is on the mesh and lets it get short-lived
Warden tokens for providers and MCP servers instead of static keys. It is
Nanite's own file: `host.yaml` and `.mcp.json` stay as other tools read them.
Nanite reads it when the agent opens, and `nanite validate` checks it.

```yaml
principal: agent:research-lead          # agent:, session:, app: or machine:
warden:
  url: https://warden.example.test      # https; http only for a loopback address
  credential_file: ~/.config/nanite/research-lead.cred   # 0600, outside the agent directory
  purpose: Research lead agent           # optional, shown on grant requests
mcp:
  gateway:                              # a server name in .mcp.json (http or sse)
    audience: mcp-gateway
    scopes: [mcp.profile.standard]
    correlation: true                   # send claimed id headers
```

With a provider in `providers.yaml` set to `auth: warden` (and `use` selecting
it), every request carries `Authorization: Bearer <token>` for the provider's
`audience` and `scopes`, and no other key:

```yaml
providers:
  gateway:
    wire: openai-chat
    base_url: https://llm-gateway.example.test/v1
    auth: warden
    audience: llm-gateway
    scopes: [llm-gateway.chat]
use:
  chat: {provider: gateway, model: claude-sonnet-5-5}
```

An HTTP server in `.mcp.json` named under `mcp` with an `audience` gets the
same per request; it may not also set an `Authorization` header. With
`correlation: true` it also gets `X-Nanite-Principal` (the principal) and
`X-Nanite-Conversation` (the conversation id). These are claimed, not
verified, ids.

How tokens are minted:

- The credential file holds the agent's parent credential, placed when the
  agent is enrolled. It rotates on every token request: Nanite takes an
  exclusive lock on `<credential_file>.lock`, writes
  `<credential_file>.inflight` (holding the hash of the parent it presents),
  sends the request, writes the next parent atomically and removes the marker
  before the token is used. A mint runs to completion even if the turn that
  needed it is cancelled, bounded by its own 30-second timeout, and Warden
  redirects are never followed.
- If a request's answer is lost (a timeout, a cut connection, a server error,
  a redirect or an answer that is not an OAuth refusal), the old parent may
  already be spent, and presenting it again would revoke the whole credential
  family. The marker stays and every later token request fails with
  `agent.ErrReenroll` ("re-enrollment needed") without contacting Warden.
  Recover by placing a new parent credential and removing the marker; a
  marker whose hash no longer matches the file's parent is cleared on its own,
  since that rotation was saved. A request whose connection was never made
  clears the marker, and an OAuth refusal (`invalid_scope`, `invalid_request`
  and the like) leaves the parent unspent; `invalid_grant` means the parent
  was refused and needs re-enrollment.
- The Warden Go client (and the `warden` CLI) takes the same lock but does not
  honor the `.inflight` marker, so do not share a credential file with it.
- Tokens are cached per audience and scope set and renewed at two thirds of
  their life. A failed renewal is retried after 1 second, doubling to 30
  seconds, while a still-valid token keeps being used. A server answering 401
  drops the token it was sent (not a newer one) and the request is retried
  once with a fresh token.
- If Warden says the agent has no grant for the audience (`invalid_scope`),
  Nanite files a grant request for it (audience, scopes and purpose, with a
  `warden` token for `warden.request` and `warden.read`, which every enrolled
  principal has), reports it to `agent.Options.OnAuthWaiting`, and fails the
  request with an error naming the request id. It checks the request again at
  most every 30 seconds and mints once it is approved; a denied or expired
  request is filed afresh on the next attempt.
- A token or parent credential never appears in an error, log line, event,
  trace or transcript: each one minted is added to the agent's redaction
  scope as well.

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
the variant's name: it starts with a lower-case letter, uses lower-case
letters, digits and dashes, is at most 40 characters, and can't be `chat`
or a built-in variant's name. Optional YAML
frontmatter sets its description, tools, model, budgets and defaults, and
the Markdown body is its role prompt. Nanite reads the files again at the
start of each turn, and `nanite validate` checks them. `nanite init` writes a
commented example, `variants/reviewer.md`. The fields, the built-in variants
and how subtasks work are in [code-mode.md](code-mode.md).
`agent/authored` exports the same checks for editors: `ParseVariant(name,
content)`, `LoadVariants(dir)` and `CheckVariantName`; Nanite loads variants
through them. Variants are read through an `os.Root` on the agent
directory, so `variants/` and its files must be inside it: a symlink that
points outside (including a `variants/` linked to a shared directory) is an
error, and the agent runs with the built-in variants only.

## Tool packages

A tool package is a tool written for the agent, often by the agent itself:
`tools/<name>/` holds a `tool.yaml` manifest and an executable entry. Nanite
reads the packages again at the start of each turn, so an agent that can
write files and run Bash can write a tool in one turn, check it with
`tool_validate`, and call it in the next. `nanite validate` checks them too.

```yaml
# tools/queue-peek/tool.yaml
description: Show the oldest items waiting in the work queue.
version: 0.1.0
kind: command            # or mcp
entry: run.sh            # executable, inside tools/queue-peek/
input_schema:            # command only; defaults to any object
  properties:
    limit: {type: integer}
timeout: 30s             # command only; default 60s, at most 10m
background: false        # command only
network: true            # declared, shown to the model and approver; not enforced
env: [QUEUE_URL]         # host variables passed through
needs:                   # Warden tokens, one variable each
  - audience: queue
    scopes: [queue.read]
    env: QUEUE_TOKEN
```

- The directory name is the package name: lower-case letters, digits and
  dashes, starting with a letter, at most 40 characters. `name`, if set,
  must match it. `description` and `kind` are required.
- `entry` is a clean relative path inside the package, and must be a regular
  executable file: not a symlink, not a directory, and with no other hard
  links.
- **The package's digest.** Nanite hashes the whole package directory (each
  file's path, mode and content, each symlink's target). Approvals are for
  one digest: any change to any file in the package asks again. Just before
  a package runs, Nanite reads it again and refuses it if the digest
  changed since the turn loaded it. MCP packages are checked before each
  start, including reconnects. A bounded metadata cache avoids rehashing
  unchanged contents; file reads refuse special files and enforce the byte
  limit even if a file grows. A change in the moment between that
  check and the process starting is still possible.
- **`kind: command`** is offered as the tool `tool__<name>`. Each call runs
  the entry from the first workspace root (or the package directory when
  there is none), with the call's JSON arguments on standard input. It must
  write a JSON result on standard output and exit 0. A non-zero exit, a
  result that is not JSON, more than 64 KiB of output or a call past
  `timeout` is an error result (with up to 8 KiB of standard error). The
  process runs in its own process group, which is ended with it. Nothing
  runs and no token is minted (or grant requested) until the call is
  approved.
- **`background: true`** runs each call as a background job instead: the
  call returns the job (`background_status` and `background_cancel` work on
  it), and its finish posts a notice. At most 4 background tool jobs run at
  once across the agent; a call over the limit is refused. A job is stored
  as a shell job whose spec names the tool.
- **`kind: mcp`** runs the entry as a stdio MCP server, but only once a
  person has approved that exact package (by digest). Until then the
  package is offered as one tool, `tool__<name>`: calling it asks, with the
  package's details. Once approved (the approval is kept across restarts),
  the server starts at the next turn, with its tokens, and its tools are
  offered as `tool__<name>__<tool>`; the approval tool is no longer offered.
  The server's read-only hints are not trusted. It also gets
  `NANITE_TOOL_DIGEST`, so an edited package restarts it. An `.mcp.json`
  server may not use the `tool:` prefix. Approvals live outside the agent
  directory in the host data directory or OS user config directory; see
  [embedding.md](embedding.md). Old agent-local approvals are ignored.
- **Environment.** A package gets Nanite's base set (`PATH`, `HOME`, `USER`,
  `LANG`, `TERM`, `TMPDIR`, `SHELL`, `LC_*`), the host variables named in
  `env` that are set, and one variable per `needs` entry holding a fresh
  Warden token for that audience and scope set (see "Identity and Warden
  auth"). It never gets the parent credential through its environment, or
  any other host variable. Variable names are upper-case; the base set,
  `NANITE_*`, and the dynamic loader's variables are reserved. `env` may not
  name a provider key variable (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY` and
  the like) or a name that looks like a credential (containing `KEY`,
  `TOKEN`, `SECRET`, `AUTH`, `PASSWORD` or `CREDENTIAL`, or named
  `DATABASE_URL`, `PAT`, `DSN`, or ending in `_PAT` or `_DSN`). URL variables
  (`URL` or names ending in `_URL`) with user information or credential query
  parameters are also refused. These restrictions apply unless the host
  lists it in `Options.ToolPackageEnv`; ask for a Warden token with `needs`
  instead. A package with `needs` and no Warden identity fails with a clear
  error. Passed-through values and tokens are redacted wherever they
  appear. An MCP package's environment is fixed when its server starts:
  when a token is renewed, the server restarts with the new one at the next
  turn.
- **Not a sandbox.** A package runs as the same OS user as the agent, so it
  can read any file that user can, including the Warden parent credential
  file. Approving a package is trusting that code; isolation (a separate
  user, a container) is a later item.
- **Permissions.** Package tools are the agent's own code, so policy never
  treats them as read-only: in the `default` and `accept-edits` modes each
  call asks first, and the prompt names the package, its digest, the entry,
  and the names (never the values) of the host variables and Warden tokens
  it gets. A conversation-scope approval is for the package's digest, so a
  rewritten package asks again. Allow one in `permissions.yaml` (for
  example `tool: tool__queue-peek`) once you trust it.
- **Offered with** the `tools` built-in group (`agent.BuiltinTools`), which
  also holds `tool_validate`. An embedded agent gets it only when the host
  names it.

`agent/authored` exports the same checks: `ParseTool(name, content)`,
`LoadTools(dir)`, `LoadTool(dir, name)`, `CheckToolName` and `CheckToolEnv`. A package that does not load is left
out of the turn with a warning.

## Generated state

- `agent.lock` holds the single-process lock for the data directory.
- `transcripts/<conversation ID>/` contains numbered JSONL journal segments.
- `nanite.db` and its SQLite sidecars hold the index, notices, and job state.
- `jobs/` contains bounded background job output logs.
- `events.jsonl` is the CLI's default runtime event log.

Keep this directory private and out of version control. Back up the transcripts
to preserve conversation history. `nanite rebuild-index` replays transcript
records into the index.

### Prompt-cache lifetime

Set `runtime.cache_ttl` to `1h` to retain Anthropic's system and tool prompt
caches for one hour, or `5m` for five minutes. Empty keeps the provider default
and omits TTL from the wire request. Other values fail validation. Recent
message markers retain their provider default. TTL travels with each request;
conversations sharing a provider do not change one another's hints. OpenAI
ignores the hint.

Custom Anthropic cache hints must put one-hour markers before five-minute
markers in logical prompt order (tools, system, then messages). If hints
violate that order, the adapter uses five minutes for the request’s cache
markers, preserving omitted TTL fields and avoiding a longer billed lifetime.

`runtime.daily_token_budget` caps agent tokens per UTC day (zero disables it).
The persisted count includes cached tokens and all root/background/subtask
usage; helper completions and compaction use estimates. The owner receives one
80% warning each day. At the cap, new work and model requests are refused, but
usage from requests already running may cross it. This local token cap is not a
dollar or gateway billing ceiling.

Run `nanite stop --all --dir <agent-directory>` to cancel the running agent's
turns, subtasks and jobs. It stays stopped until closed and reopened.
