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
- how `nanite serve` was bound (address, port) and whether `--insecure-no-auth` was used
- reproduction steps and the security impact
- whether credentials, transcripts or project files may have been exposed
- a safe way to contact you about coordination

Maintainers will acknowledge a private report, investigate it, and coordinate
disclosure. Response times are best effort.

## Deployment boundary

Nanite is a single-user desktop application, not a multi-tenant service. The
safe default is `nanite serve` on its own machine, bound to `127.0.0.1:8090`.
`--port` changes the port.

**Nanite refuses every non-loopback bind.** It does not terminate TLS, and a
bearer credential must not cross a network in the clear. `--bind-address` and
`http.bind_address` accept only `127.0.0.1`, `::1` or `localhost`. To reach
Nanite from another machine, use an SSH forward, Tailscale Serve or a
TLS-terminating proxy on the same host; see
[docs/architecture/remote-access.md](docs/architecture/remote-access.md).
A proxy that forwards a DNS name (for example a tailnet hostname) must have that
name listed in `http.allowed_hosts`; the Host check otherwise answers 403.

**Authentication is on for every `/api/` route, loopback included.**
Loopback is not authentication: any local process, and any web page through a
drive-by request or DNS rebinding, can reach a loopback listener. See
[docs/architecture/api-auth.md](docs/architecture/api-auth.md) for the model.
In short:

- Credentials are bearer secrets (`nan_<kind>_…`), stored only as SHA-256
  hashes in a separate auth database, and carry scopes (`read`, `operate`,
  `tools`, `maintain`, `admin`). Kinds: `operator` (you), `client` (an embedded
  client, never admin) and `agent` (minted per session, `tools` only, expires,
  dies with the session).
- The first start creates the operator credential and writes its secret once to
  a mode-0600 file (`nanite auth token-path`). The CLI reads it; the browser
  signs in with it, or with a one-time link from `nanite auth login-link`, and
  then holds an HttpOnly, SameSite=Strict session cookie with CSRF protection.
- Every route maps to a required scope; a route no rule names is admin-only for
  writes. Revoking a credential closes its open streams.
- Caller identity comes from the credential. The `X-Nanite-Caller-*` headers
  are honored only for an agent credential bound to that exact session.
- The `Host` header is validated on every request.
- Public: `GET /api/health`, `GET /api/agent/v1/initialize`, the agent card,
  the static UI shell (no data), `POST /api/auth/login` and
  `GET /api/auth/link` (which do their own checking). Plugin host query and
  wake routes carry their own per-plugin bearer.
- `nanite serve --insecure-no-auth` turns authentication off for development.
  It warns loudly at every start; it is never used by the service unit.
- `NANITE_AUTH_TOKEN` is an explicit operator-credential override for headless
  runs; it must be a `nan_operator_…` secret.

**The operator token file is readable by processes that run as you.** The
database holds only hashes, but the file holds the secret. The agent sandbox
does not currently hide it from an agent CLI running as your user.

**Anyone holding an operator credential can act as you.** The API launches
agent sessions, runs shell commands in a session's working directory, installs
and starts plugins, and reads conversation history.

**Legacy route CORS.** With no `http.cors_allowed_origins` configured, only
`http://localhost:5173` and `http://127.0.0.1:5173` (the Vite dev server) are
allowed. Matching is exact; `*` is accepted and disables credentialed CORS.
The allowlist controls which browser origins may read responses — the server
does not reject requests carrying a disallowed `Origin`, and it does not
validate the `Host` header on legacy routes.

**Admin Origin policy.** `/api/admin` and `/api/admin/` use credentialed CORS
for same-origin requests or exact operator-configured `cors_allowed_origins`
entries only. The injected port-5173 dev defaults and `*` grant no admin
access. Every admin POST, including validate, update and reset, requires one
well-formed HTTP(S) Origin before body decoding, as well as an operator
credential (the `admin` scope); missing, null, malformed, duplicate and foreign
Origins are rejected.
Same-origin uses the actual request authority and TLS scheme, without trusting
forwarded headers. CLI clients must send an approved Origin; a TLS-terminating
proxy's external HTTPS Origin must be explicitly configured. Admin CORS
allows If-Match and exposes ETag. This exception does not change legacy CORS or
authentication behavior.

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

Plugin configuration fields marked secret are stored in the OS keychain
(macOS Keychain, Windows Credential Manager, or the Linux Secret Service), not
in the database.

A provider API key is a *reference*, resolved when the provider is first used,
never a value in the database or a config file. By default Nanite looks in the
OS keychain; a provider can instead name another source with
`nanite provider set-ref <id> <ref>` (`keychain://`, `helper://`, `file://`,
`op://`). Save a key with `nanite provider set-key <id>`, which reads it from
standard input so it never appears in a process list or shell history.
`nanite provider status` shows, for each provider, whether a key resolves and
from where, and never prints a key.

The conventional environment variables (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`)
are **not** read unless you set `NANITE_PROVIDER_KEY_ENV=1`, which is how a
container without a keychain is configured. With it on, the environment is
consulted last and a provider running on it reports `env:<VARIABLE>` as its
source. Resolved key values are scrubbed from Nanite's logs and error messages.

MCP server `env` values and HTTP `headers` are stored in the database in
plaintext. The API redacts them on read — key names stay visible, values are
replaced — and the API's `.mcp.json` export redacts env values and omits
headers. The CLI export, `nanite mcp export`, writes the complete file with
secrets included; protect its output like the database.

Per-session sandbox directories are created under `~/.nanite/sandboxes/`.

Placed artifacts from HTTP clients and plugins must name an existing regular
file under the configured artifacts storage root. Both paths apply the same
confinement rule, resolve symlinks, and record the file size. Download re-checks
confinement so a legacy or externally modified row cannot serve an outside file.
This limits artifact registration; it does not sandbox the plugin process.

Subprocess plugins inherit only `PATH`, `HOME`, `TMPDIR`, `TMP`, `TEMP`,
`USER`, `LOGNAME`, `LANG`, `LC_ALL`, and `XDG_RUNTIME_DIR`. A reviewed `ssh_agent` or `docker_socket` capability may add its named
connection variables. Host code may explicitly provide additional launch entries. Provider keys, Nanite auth
credentials, SSH agent sockets, Docker handles, proxy settings, and runtime
injection variables are not inherited. Declared secrets resolve from their named environment variable or the
plugin-specific OS keychain key, arrive through init configuration, and are
scrubbed from subprocess stderr and RPC error logs. This environment policy does not restrict files
a plugin can read with your user privileges.

Installing a subprocess plugin requires reviewing the actual staged bundle.
The GUI shows capabilities, secret names and tool effects; the CLI requires
entering the plugin ID. An upgrade shows changes from the previous approval.
The receipt stores a digest of every regular file's path, permissions and bytes
outside the bundle. Loads and supervised restarts refuse changed or unapproved
bundles. Symlinks and oversized bundles are refused. Local installs copy files;
linked development directories cannot retain an approval. Runtime calls have a
30-second host deadline and bounded request/response frames (8 MiB/16 MiB).
These checks detect changed code; plugins still run with your user's privileges.

Read-only plugin queries require a reviewed `readonly.query` scope and a
connection bearer credential delivered through subprocess init. Approval shows
the allowed resources and session IDs (or workspace-wide access); raw captured
context requires separate `include_content` approval. The query route exposes
fixed read projections with bounded responses and cancels read leases when the
connection is revoked. It does not confer SQL, tool or mutation authority.
Plugins retain their process privileges and access to the user's broader local
API under the deployment boundary described above.

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
  index (by default the `hollis-labs/plugins-catalog` latest release feed) and
  the plugin archives it names.
- **Tools, plugins and MCP servers.** Agent tools such as web fetch reach the
  URLs an agent asks for. Configured MCP servers and installed plugins receive
  whatever is sent to them and may make their own network calls.
- **OpenTelemetry.** Tracing is initialised at startup unless
  `NANITE_OTEL_DISABLED=1` is set or it is disabled in configuration; where
  spans go follows the standard OpenTelemetry exporter settings in the
  environment.

## Current security limitations

- no built-in TLS, so Nanite listens on loopback only
- the browser session cookie is marked `Secure` only when you set
  `http.secure_cookies: true`; Nanite does not infer HTTPS from forwarded
  headers
- the operator token file is readable by anything running as your user; the
  agent sandbox does not hide the auth directory from an agent CLI
- no at-rest encryption; MCP server secrets are plaintext in the database
- subprocess plugins run with your user's privileges
- no sandbox for plugins or for the agent CLIs Nanite launches
- plugin archives from catalogs are checked against their SHA256 checksum;
  checksums establish byte integrity, not publisher trust. A plugin is code
  you choose to run
- legacy CORS does not reject cross-origin requests, and there is no general
  `Host` header validation; admin POSTs enforce the Origin policy described above
- on macOS, sandboxed execution restricts writes and network but not reads

These are constraints of a local single-user tool, not hidden roadmap
promises. Operate within them, or place Nanite behind controls that provide
the missing boundary.

Plugin integrations requesting `durable_agent.wake` may submit prompts to an
explicit reviewed allowlist of existing durable instance slugs. This can start
real agent turns with those agents' own tool permissions. The capability grants
no provisioning, profile edits or scheduling. It uses a separate connection
bearer credential; read credentials cannot wake agents. The host bounds and
validates input and cancels the lease on unload. Success reports queued work,
not completion. Plugins remain unsandboxed subprocesses under the desktop user.
