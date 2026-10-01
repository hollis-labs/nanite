# Changelog

All notable user-facing changes land here. This file starts with the
2026-05-01 release notes for the pre-release sprint; earlier history
lives in the git log.

## Unreleased

### Breaking

- **Agent Workflows now use the shared `go-workflow v0.1.0` engine
  exclusively** (EP-20260904-0006 / CW-20260904-0061). The parallel local
  sequencer and its mutable engine-selection path have been removed. New
  launches, approvals, cancellation, scheduler activations, A2A requests,
  Loops, Teams, and external-framework steps all enter one SQLite-backed host
  with immutable definition revisions and run-to-plan bindings. Existing
  Hadron-pilot runs retain their frozen engine identity for recovery; historical
  legacy rows remain readable but cannot be launched or resumed. On first
  startup, active legacy runs require an explicit drained/canceled/failed
  disposition before the one-way `shared_only` cutover completes. See
  [`docs/architecture/workflow-engine.md`](docs/architecture/workflow-engine.md)
  for the ownership boundary and
  [`docs/workflow-operations.md`](docs/workflow-operations.md) for the startup,
  recovery, callback, and rollback procedure.

- **The HTTP server now binds to loopback by default** (GO-RUNTIME-002,
  AD-15). Upgrading a deployment that relied on the previous bind-all default
  can affect custom Docker port mappings, LAN or remote-development access,
  and reverse proxies targeting a non-loopback interface. The symptom is that
  Nanite starts normally but is no longer reachable from other hosts. Opt in
  to the former bind-wide behavior explicitly:

  ```sh
  nanite serve --bind-address 0.0.0.0
  ```

  `--bind-address` is host-only: it accepts ASCII hostnames, raw IPv4, or raw
  unbracketed IPv6, without a port or surrounding whitespace.
  For a persistent deployment setting, set `http.bind_address: 0.0.0.0` in
  `config/nanite.yaml`. Startup logs now always report `auth=enabled` or
  `auth=disabled`, with a warning when `NANITE_AUTH_USER` and
  `NANITE_AUTH_PASSWORD` are unconfigured. The checked-in
  `docker-compose.yaml` already supplies this explicit opt-in so its published
  `8090:8090` mapping continues to work. TLS is not built in; use a reverse
  proxy when exposing Nanite beyond the local machine.

- **User config moved to XDG-compliant location** (CW-20260430-0010). The
  user-level chat-harness config is now read from
  `$XDG_CONFIG_HOME/nanite/config.yaml` (default
  `~/.config/nanite/config.yaml`). The legacy `~/.nanite/nanite.yaml` path
  is no longer read — there is no compatibility shim, no deprecation log,
  and no automated migration. Pre-release migration is manual:

  ```sh
  mkdir -p ~/.config/nanite
  cp ~/.nanite/nanite.yaml ~/.config/nanite/config.yaml
  ```

  If `XDG_CONFIG_HOME` is set in your environment, substitute its value
  for `~/.config`. The `~/.nanite/` directory is preserved for non-config
  files (skills, roles, agents, plugin data) and is unaffected by this
  change. Project-level `./nanite.yaml` is unchanged.

- **On Linux, agents can no longer write Nanite's own config, state or data
  directories** (CW-20261001-0143).
  - **Most agents now run under `bwrap`, with those directories read-only:**
    Claude Code, OpenCode, Copilot and Pi, natively or over ACP.
  - **Codex is confined by its own `workspace-write` sandbox instead.** That
    sandbox is a bwrap of its own and cannot run nested inside Nanite's.
    Its writable roots (the work root and `dev_tools_allowed_paths`; path
    grants only with `NANITE_PATH_MENTION_LAUNCH_ROOTS=1`) are filtered so
    that none offers a protected directory:
    - A root that is, or is inside, one is dropped.
    - A root that holds one is replaced by its other subdirectories.
    Claude's `additionalDirectories` drop the same roots.
  - **Still writable:** the worktree root. The rest of the host filesystem
    stays as writable as before. The main database's directory is read-only
    to agents too (CW-20261001-0188; see [`SECURITY.md`](SECURITY.md)).
  - **What agents notice:**
    - An agent sees only its own processes (a private PID namespace).
    - Setuid programs such as `sudo` refuse to run ("no new privileges").
  - **Requires bubblewrap with unprivileged user namespaces.** Without it,
    agent launches now fail instead of running unprotected, and
    `nanite serve` logs an ERROR at startup. A missing `bwrap` reports
    "ProtectedPaths cannot be enforced". Install `bubblewrap`; on Ubuntu its
    AppArmor profile already permits the user namespace.
  - **`NANITE_SANDBOX_PROTECT=0` turns protection off.** Use it for a host
    whose sandbox backend misbehaves. While it is off, `nanite serve` logs a
    warning at startup and `/api/health` lists one under `warnings`.
  - **macOS** is unchanged for now (CW-20261001-0189).

- **A path named in a message no longer lets a launched agent write there**
  (CW-20261001-0232). Naming a path such as `~/.ssh` or `/etc` in a turn's
  text used to register a session path grant, and those grants were added to
  the writable roots of the Codex and Claude launches (Codex's
  `writable_roots`, Claude's `additionalDirectories`). Any local client of the
  unauthenticated loopback API could post such a turn. Now:
  - **Launch roots come only from configuration:** the work root,
    `dev_tools_allowed_paths` and operator or profile config. Path grants no
    longer feed them, and neither do a subagent's inherited grants. A root
    that is not an existing directory is dropped, because a missing writable
    root made every Codex command of the turn fail.
  - **Path grants are for the in-process `dev_*` tools only,** and only a
    chat turn mints them. A durable agent's wake or a subagent's turn mints
    none.
  - **A mention is refused as a grant** when its real path (symlinks
    resolved) is, is inside, or is a parent of a sensitive path: `~/.ssh`,
    `~/.gnupg`, `~/.codex`, `~/.claude*`, `~/.local/bin`, the systemd and
    autostart user directories, `~/.aws`, `~/.kube`, `~/.docker`, `~/.netrc`,
    the shell startup files, the Nanite, Torque, Tether, Tesseract, Hadron,
    Tangent and fragments-engine config, data and state directories, and this
    instance's own directories. Because a mention also grants its parent
    directory, a mention of `~/notes.txt` no longer grants `$HOME`, one of
    `~/.config/app.toml` no longer grants `~/.config`, and one of `/tmp` no
    longer grants `/`. It is also refused when its real path is outside `$HOME`
    and `dev_tools_allowed_paths`.
  - **`NANITE_PATH_MENTION_LAUNCH_ROOTS=1` restores the old fold** of path
    grants into launch roots, for an operator who relied on it. It is off by
    default, `nanite serve` logs a warning at startup while it is on, and the
    refusals above still apply to the grants it folds in.

- **Claude agents Nanite launches can now call Nanite's own MCP tools**
  (CW-20261001-0411). A launched Claude runs headless (`claude -p`, no TTY),
  so its permission gate had nobody to ask, and every call to a planted
  `mcp__nanite__*` tool was refused ("Claude requested permissions to use
  mcp__nanite__agent_list, but you haven't granted it yet"). Every native
  Claude launch now carries `--allowedTools=mcp__nanite__*`, and nothing else
  is allowed: a tool from any other MCP server, including the user-level
  servers that load again with `NANITE_CLAUDE_STRICT_MCP=0`, is still refused.
  - **What a chat agent can now do:** its `nanite` server exposes the harness's
    full self-tool set, including messaging, `subagent_spawn`, `task_execute`,
    `workflow_run` and `dispatch_executor`. These were inert before, because
    every call was denied. Subagent, background and one-shot launches keep the
    smaller bare-store set the server gives them, so the rule widens nothing
    for them. A `subagent_spawn` still waits for approval.
  - **Why argv and not `settings.json`:** Claude ignores a project
    `permissions.allow` in a workspace it has not trusted ("Ignoring 1
    permissions.allow entry from .claude/settings.json"), and every Nanite boot
    dir is untrusted. The planted `settings.json` is unchanged.

### Added

- **Subagent progress heartbeats + narration guidance** (CW-20260519-0068).
  A long-running subagent dispatch (`subagent_spawn`, sync mode) previously
  went silent on the parent session's SSE stream between the initial
  "running" dispatch event and the eventual terminal event — for a run
  approaching the 30-minute backstop that silence looked identical to a
  hang. `subagent.Service` now emits a periodic `subagent_run_status_changed`
  heartbeat (`heartbeat: true`, `elapsed_seconds`, `attempt`) while a
  runner call is in flight; cadence defaults to 30s and is tunable via
  `NANITE_SUBAGENT_HEARTBEAT_SECONDS` (`0` disables). The universal rules
  block (every agent's system prompt) also gained a Narration section
  instructing agents to state what they're dispatching before a slow tool
  call, report the outcome when it returns, and post a brief "still
  working on X" update on long silent stretches — the prompt-level half of
  the fix for CLI-launched sessions, where nanite's own harness loop
  cannot observe in-process tool calls.

- **Standalone agent launcher — `nanite launch <profile>`** (Phase 6,
  CW-20260515-0027/0028). Starts a Nanite-managed CLI agent (Claude /
  Codex / OpenCode) directly from a shared launch profile, with no chat
  server, no browser dropdown, and no Tether MCP in the loop.
  `--dry-run` compiles + resolves + validates a profile without a store
  or a provider binary (a cheap CI gate); `--no-wait` returns once the
  agent process is started. The launch persists an `agent_runtime` row
  with `meta.launch_source = "standalone-launcher"` so it is
  introspectable and orphan-sweepable exactly like a chat-spawned
  session. See `docs/standalone-launcher.md` and
  `docs/phase6-shared-launch-adoption.md`.

### Changed

- **Claude agents now load only the `.mcp.json` Nanite plants for them**
  (CW-20261001-0221). Every Claude agent Nanite launches (chat, one-shot,
  resumed, subagent, background) gets `--strict-mcp-config` with
  `--mcp-config <boot dir>/.mcp.json`. Before this, Claude also loaded
  whatever the operator's own Claude has configured in `~/.claude.json`,
  plugins and account connectors.
  - **Why:** a user-level `mux mcp --proxy` spawned a second `nanite mcp`
    inside the agent's sandbox with the real database open read-write, and
    handed every agent the operator's full tool set, Cerberus (deploy, ssh)
    included.
  - **What agents notice:** the only MCP server a Nanite Claude agent has is
    Nanite's own. Tools from the operator's other servers (Torque,
    Tesseract, Tether and so on, through the user-level `mux`) are gone.
    Anything an agent needs from them has to be planted deliberately.
  - **Kill switch:** `NANITE_CLAUDE_STRICT_MCP=0` restores the old
    behaviour and logs a startup warning. It is interim, and goes when the
    library option for strict MCP lands.
  - **Not covered:** Codex (reads no user-level config, because `CODEX_HOME`
    is the boot dir), OpenCode (still loads `~/.config/opencode`) and ACP
    launches.

- **An agent's `nanite mcp` now opens no database** (CW-20261001-0188). Every
  agent Nanite launches gets the server's loopback address in its planted
  `.mcp.json` (`NANITE_API_URL`), and its `nanite mcp` forwards self-tool
  calls there instead of opening the main database read-write inside the
  agent's sandbox. This is what lets Nanite make the database's directory
  read-only to agents (a follow-up to CW-20261001-0143).
  - **Subagent, background and one-shot launches** used to dispatch locally
    against the database. They now forward too, but keep exactly the tools
    they had (`NANITE_MCP_SELF_TOOLS=store`). Chat agents keep the full set.
    This restriction preserves each launch's tool surface; it is not a
    security boundary, since the loopback tool API takes no credentials.
  - **`dev_read(artifact_id=…)`** answers that the lookup is not available
    in this launch mode.
  - **`nanite mcp` without `NANITE_API_URL`** opens the database as before.

- **Boot-profile compiler and provider bootdirs now ride shared
  `go-agent-launch` / `go-agent-context` primitives** (Phase 6,
  CW-20260515-0024/0025/0026). The mechanical slot file/inline IO,
  bootdir file-set planting (path-safety gate + overlay ordering), and
  the deferred `cmd`/`http`/`role_summary`/`skill_index` slot resolution
  now delegate to the shared `agentcontext` resolvers and the shared
  `agentlaunch.InjectionSpec` write loop. Boot prompts and planted
  bootdir content are byte-identical to pre-Phase-6 — this is an
  internal convergence, not a user-facing behavior change. New direct
  dependencies: `go-agent-launch v0.1.0`, `go-agent-context v0.1.0`;
  `go-agent-sessions` bumped `v0.9.2 → v0.9.4` (transitive, compatible).
