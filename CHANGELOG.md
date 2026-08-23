# Changelog

All notable user-facing changes land here. This file starts with the
2026-05-01 release notes for the pre-release sprint; earlier history
lives in the git log.

## Unreleased

### Breaking

- **The HTTP server now binds to loopback by default** (GO-RUNTIME-002,
  AD-15). Upgrading a deployment that relied on the previous bind-all default
  can affect Docker port mappings, LAN or remote-development access, and
  reverse proxies targeting a non-loopback interface. The symptom is that
  Nanite starts normally but is no longer reachable from other hosts. Opt in
  to the former bind-wide behavior explicitly:

  ```sh
  nanite serve --bind-address 0.0.0.0
  ```

  For a persistent deployment setting, set `http.bind_address: 0.0.0.0` in
  `config/nanite.yaml`. Startup logs now always report `auth=enabled` or
  `auth=disabled`, with a warning when `NANITE_AUTH_USER` and
  `NANITE_AUTH_PASSWORD` are unconfigured. TLS is not built in; use a reverse
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
