# Security policy

## Supported versions

Nanite is pre-release software. Security fixes land on `main`; there are no
maintained release branches and no backports. Run a build from a recent
`main` if you need a fix.

## Report a vulnerability

Do not include an exploit, API key, database, session transcript, or other
sensitive material in a public issue.

Use GitHub's private vulnerability-reporting flow when the repository's
Security tab offers it. If it is unavailable, contact a repository maintainer
privately through a contact channel published on the Hollis Labs organization
or maintainer profile. Include:

- the affected commit and operating system
- how `nanite serve` was bound (address, port) and whether basic auth was set
- reproduction steps and the security impact
- whether credentials, transcripts or project files may have been exposed
- a safe way to contact you about coordination

Maintainers will acknowledge a private report, investigate it, and coordinate
disclosure. Response times are best effort.

## Deployment boundary

Nanite is a single-user desktop application, not a multi-tenant service. The
safe default is `nanite serve` on its own machine, bound to `127.0.0.1:8090`.
`--port` changes the port; `--bind-address` or `http.bind_address` in
`config/nanite.yaml` changes the host.

**Authentication is off by default.** Setting `NANITE_AUTH_USER` and
`NANITE_AUTH_PASSWORD` enables HTTP Basic Auth on `/api/` routes, with these
exemptions:

- `GET /api/health`
- `/api/tools/call`, which the handler itself restricts to loopback callers
- `/api/example/task-updates`, a same-process demo endpoint

Non-API paths (the embedded web UI) are not covered by basic auth. Nanite
does not refuse a non-loopback bind without auth; it logs a warning at startup
and serves anyway.

**Anyone who can reach the API can act as you.** The API launches agent
sessions, runs shell commands in a session's working directory, installs and
starts plugins, and reads conversation history. Treat network access to the
listener as equivalent to a shell on the machine.

Nanite does not provide TLS. Basic-auth credentials sent over plaintext HTTP
can be read by anyone able to observe the connection. If you expose Nanite
beyond the local machine, put it behind TLS, a trusted TLS-terminating reverse
proxy, a VPN, or an SSH tunnel, and restrict it with firewall rules.

**CORS.** With no `http.cors_allowed_origins` configured, only
`http://localhost:5173` and `http://127.0.0.1:5173` (the Vite dev server) are
allowed. Matching is exact; `*` is accepted and disables credentialed CORS.
The allowlist controls which browser origins may read responses — the server
does not reject requests carrying a disallowed `Origin`, and it does not
validate the `Host` header.

**Execution isolation.** Commands Nanite itself runs on an agent's behalf
(code-execution and dev tools) go through `internal/sandbox`: a minimal
environment with secret-looking variables removed, a restricted `PATH`, no
network unless a domain allowlist is given (enforced through a local proxy),
and OS-level isolation — `sandbox-exec` on macOS, `bwrap` on Linux. On macOS
the profile restricts writes and network; reads are not restricted. If the
isolation tool is unavailable, execution fails closed unless you set
`NANITE_ALLOW_UNSANDBOXED_AGENT_EXEC=1`; the command denylist that then
remains is defense in depth, not a boundary. User-initiated shell commands
from the UI use the same denylist and secret filtering, and the session's
`yolo` shell mode skips OS isolation.

Agent CLIs that Nanite launches (Claude Code, Codex and the like) run as
your user. Their own permission systems apply. A launched Claude is allowed
the tools of Nanite's own MCP server (`--allowedTools=mcp__nanite__*`) and no
other server's; the server itself scopes its tools by launch mode, so a chat
agent gets the harness's self-tool set (messaging, `subagent_spawn`,
`task_execute`, `workflow_run`, `dispatch_executor` and the rest) and subagent,
background and one-shot launches a smaller one (CW-20261001-0411).

On Linux, Nanite also keeps its own config, state and data directories out
of agents' reach, so an agent cannot rewrite Nanite's configuration or
coordination state to grant itself authority:
- Most agents run under `bwrap` with those directories read-only.
- Codex runs under its own `workspace-write` sandbox instead, because that
  sandbox cannot run nested inside Nanite's. Nanite narrows Codex's writable
  roots so they leave those directories out.

**Paths named in a message.** A path mentioned in a chat turn ("edit
`~/proj/main.go`") grants the in-process `dev_*` tools access to it for that
session, and to its parent directory. The text of a turn is not proof that a
person wrote it, since any local client of the loopback API can post one, so:
such a grant never reaches an agent CLI's writable roots (those come from the
work root, `dev_tools_allowed_paths` and your configuration), only a chat turn
mints one, and it is refused when it would cover a sensitive path (`~/.ssh`,
`~/.gnupg`, `~/.codex`, `~/.claude*`, `~/.local/bin`, the systemd user
directory, Nanite's and its sibling apps' state) or anything outside `$HOME`
and `dev_tools_allowed_paths` (CW-20261001-0232).
`NANITE_PATH_MENTION_LAUNCH_ROOTS=1` restores the old behavior of folding
these grants into launch roots; leave it off.

Everything else stays as writable as your user can make it.
`NANITE_SANDBOX_PROTECT=0` turns this protection off; `/api/health` warns
while it is off. This has limits:

- **The main database is protected with them.** Its directory is read-only
  to agents, so an agent cannot write the database directly, including its
  permissions and approvals. That holds for a database outside Nanite's
  own directories (`--db`, `NANITE_DB_PATH`) too, unless its directory is
  `/` or contains your home directory, which Nanite will not make read-only.
  Each agent's `nanite mcp` forwards its tool calls to the running server
  and opens no database (CW-20261001-0188). A user-level MCP server of your
  own that opens the database is no longer loaded into a Claude agent
  (CW-20261001-0221) or an OpenCode agent (CW-20261001-0239). Codex reads no
  user-level config either, because its config home is the boot dir. Launches
  over ACP are not checked.
- **Only direct writes are stopped.** Protection does not stop an agent from
  asking another process running as your user, outside the sandbox, to
  write for it (for example `systemd-run --user`, a terminal multiplexer, or
  another app's API). Nor does it stop the agent planting something under
  your home directory that later runs outside the sandbox, such as a shell
  rc file or a git hook.
- **Only directories are protected.** Where a protected directory has to
  stay writable underneath (the worktrees root), Nanite protects the sibling
  directories instead. A loose file sitting
  directly in such a parent stays writable.
- **A launch's own work directory is not protected.** It comes from the
  session's or project's configuration, and an agent cannot choose it; one
  that lies inside Nanite's directories is writable.
- **If `bwrap` cannot run**, agent launches fail rather than run unprotected.
- **macOS** agents are not wrapped yet (CW-20261001-0189).

## Data at rest

Nanite has no built-in at-rest encryption. The SQLite database holds sessions,
transcripts, agent profiles, plugin settings, and MCP server configurations.
`nanite path` prints where it and Nanite's other state live (resolved from the
XDG directories, or `NANITE_DB_PATH`); `--db` overrides it for `nanite serve`.

Provider API keys and plugin configuration fields marked secret are stored in
the OS keychain (macOS Keychain, Windows Credential Manager, or the Linux
Secret Service), not in the database. When the keychain has no key for a
provider, Nanite falls back to the conventional environment variable
(`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`), which is how a container is
configured.

MCP server `env` values and HTTP `headers` are stored in the database in
plaintext. The API redacts them on read — key names stay visible, values are
replaced — and the API's `.mcp.json` export redacts env values and omits
headers. The CLI export, `nanite mcp export`, writes the complete file with
secrets included; protect its output like the database.

Per-session sandbox directories are created under `~/.nanite/sandboxes/`.

## External data processors

Nanite is not local-only once you configure a model provider. What leaves the
machine:

- **LLM providers you configure.** Anthropic and OpenAI API providers receive
  the prompts, context, tool results and conversation history of the sessions
  that use them. Agent CLIs you launch send data to their own providers under
  their own configuration.
- **models.dev.** Nanite fetches the public model catalog from
  `https://models.dev/api.json` for model metadata and pricing. The request
  carries no session data.
- **Plugin catalogs.** Browsing or installing from a catalog fetches its
  index (by default `https://plugins.nanite.hollislabs.dev/catalog.yaml`) and
  the plugin archives it names.
- **Tools, plugins and MCP servers.** Agent tools such as web fetch reach the
  URLs an agent asks for. Configured MCP servers and installed plugins receive
  whatever is sent to them and may make their own network calls.
- **OpenTelemetry.** Tracing is initialised at startup unless
  `NANITE_OTEL_DISABLED=1` is set or it is disabled in configuration; where
  spans go follows the standard OpenTelemetry exporter settings in the
  environment.

## Current security limitations

- authentication is off by default, and when enabled it is a single shared
  Basic Auth credential
- no built-in TLS
- no at-rest encryption; MCP server secrets are plaintext in the database
- subprocess plugins run with your user's privileges and inherit the full
  environment of the Nanite process, including any provider keys or
  `NANITE_AUTH_*` values set there
- no sandbox for plugins or for the agent CLIs Nanite launches
- plugins installed from a local path or archive are not signature-checked,
  and plugin signing should not be relied on as a trust boundary: a plugin is
  code you choose to run
- CORS does not reject cross-origin requests, and there is no `Host` header
  validation
- on macOS, sandboxed execution restricts writes and network but not reads

These are constraints of a local single-user tool, not hidden roadmap
promises. Operate within them, or place Nanite behind controls that provide
the missing boundary.
