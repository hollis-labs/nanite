# Agent runtime ownership

Nanite compiles product inputs (agent/boot profile, work directory, child
environment, sandbox profile, recovery metadata, and UI sinks), then hands the
resolved launch to `go-agent-wrapper` v0.9.1. `wrapper.Wrapper` owns native and
ACP start, resume/new, prompt, turn cancellation, close, provider session ID,
event draining, process exit, and cleanup. ACP wrappers share one
`acp.Manager`; Nanite's `SessionManager` is only the runtime-ID-to-wrapper
binding used by chat lookup, recovery replacement, orphan checks, and daemon
shutdown. It does not keep a second lifecycle state.

`SessionManager` admits a launch before `Wrapper.Run` begins. Shutdown closes
that admission boundary first, stops all de-duplicated wrappers concurrently,
and waits for ready, pre-ready, and recovery-pending Run tails under one shared
deadline, so a concurrent Boot cannot escape the stop set. Non-chat Boots
retire their exact pointer in the Run tail. Chat explicitly transfers that
retirement to its recovery observer, which may need to claim a same-ID recovery
lease through broker adoption. Stale observers neither clear successor state
nor dispatch recovery over it; runtime retirement never deletes turn-owned
slot, tool-partition, or router state.

Every normalized event reaches Nanite's canonical sink before the small legacy
subset is projected onto chat SSE/provider callbacks. The canonical sink now
also feeds the bounded, allowlisted [host runtime feed](host-runtime-feed.md).
The source event itself is never persisted: prompts, tool payloads, stdout,
stderr, and model deltas may contain secrets. The public feed stores metadata
and explicitly safe per-kind state only, with independent cursor, retention,
gap, redaction, and runtime-generation semantics.

Native selection remains Nanite product policy: Claude uses streaming stdio;
Codex and OpenCode use subprocess-per-turn. The selection is expressed through
`adapters.Select` with Nanite's already-configured `provider.CLIAdapter`.
Per-session environment isolation uses `wrapper.ChildEnvironment` in replace
mode, so no shell or generated process wrapper is involved. ACP selection uses
the wrapper's shipped Claude, Codex, OpenCode, Copilot, and Pi ACP adapters.

A turn sent while another is running on the same session queues behind it:
it starts once the running turn has finished, and the agent receives it as its
next turn. A mid-run message therefore steers at the next turn boundary; it
never interrupts the running turn or loses its reply.

Interrupting is explicit. User stop cancels the queued turns and the running
one, and requests `Wrapper.CancelTurn` against the exact wrapper and router
token bound when the running generation admitted its prompt. No delayed
cancellation or router cleanup resolves a mutable session-ID binding. The
request is bounded and does not block the API caller; a canceled generation
remains a barrier until the provider request and predecessor terminal/cleanup
boundary complete. The interrupted turn's output so far is saved, marked
`interrupted` in its metadata.
ACP uses its real turn-scoped cancel. Native runtimes honestly report that
turn cancellation is unsupported, so Nanite stops that exact wrapper and the
next turn cold-boots instead of pretending a wire-level cancel occurred.

## Defined CLI sessions

A pinned definition plus host settings with `runtime: "cli"` runs a CLI agent
through `POST /api/agent/v1/sessions`, the same route and event stream as a
native API agent. Claude, Codex, OpenCode and Antigravity (`agy`) run this way.

```json
{"version":"1","runtime":"cli","provider":"claude","model":"haiku",
 "directories":["/abs/path/to/work"],"native_loop":{}}
```

- **Work root and directories.** `directories` is required. The first entry is
  the session's work root; every entry is granted to the CLI on its command
  line (`--add-dir`), not only in the boot dir's `settings.json`, because Claude
  ignores `additionalDirectories` from a workspace it has not been trusted in
  and the ephemeral boot dir never is. Filesystem authority comes from the host
  document, not the request.
- **Model.** `model` is required for a CLI host and is passed to the CLI as
  `--model`, the flag each of the four CLIs documents (`modelFlags` in
  `factory.go`). OpenCode takes `provider/model`. A provider outside that table
  (an ACP-only one such as Copilot or Pi) is refused when it names a model.
- **Boot.** The view boots the host settings' projected profile (definition text
  as the system prompt, provider, model, directories), resolved by host settings
  ID, on the same `driveBootSession` path as other CLI chat. The view pins the
  host revision it was admitted under; a turn after the host is edited or
  disabled fails with `host_revision_changed`.
- **Resume.** For Claude, OpenCode and Antigravity the provider session ID is
  persisted, so a turn after a server restart resumes the CLI's own session. A
  Codex session cannot be resumed by a later boot (it keeps its rollout under a
  per-boot `CODEX_HOME`), so after a restart its context comes from Nanite's
  recovery pack, not from Codex.
- **Directories.** Claude and Antigravity are given each directory with
  `--add-dir`; Codex gets them as `writable_roots` in its planted `config.toml`.
- **Nanite MCP server.** Claude reads it from the planted `.mcp.json` (with the
  strict-MCP flags and an allow rule); Codex reads it from `[mcp_servers.*]` in
  the planted `config.toml`; OpenCode reads it from the `mcp` key of the planted
  `opencode.json` (`type: local`, argv `command`, `environment`). agy discovers the plugin's `mcp_config.json`, but
  its headless mode auto-denies a tool call that needs the `mcp` permission
  (exit 0, empty response, a stderr hint). agy reads allow rules only from
  `~/.gemini/antigravity-cli/settings.json` (and an admin file), never from the
  workspace, so Nanite launches agy with its own **private HOME** and writes
  the rules there; see "Antigravity private HOME" below. The Nanite server gets
  the rule `mcp(tether_nanite/*)` (all of its tools, as Claude's
  `mcp__nanite__*`: the server's tool list is set when agy connects, not at
  plant time). `--dangerously-skip-permissions` is still added only when the
  permission engine's mode is yolo (the counterpart of Claude's developer
  variant, with the same audit log line).
- **Host `mcp_servers` (Claude and Antigravity).** Names in the host settings' `mcp_servers`
  are registry servers (`/api/mcp-servers`) planted into the same `.mcp.json`.
  A CLI calls its servers itself, so Nanite's permission engine never sees the
  call; the grant is enforced by Claude's own headless gate instead. For each
  tool the turn's execution capability may use, the launch adds the allow rule
  `mcp__<server>__<tool>` to the `--allowedTools` flag. The tools come from the
  same decider as the API runtime (`NativeGrants`): the host's native authority
  grants, the submitting credential's standing and tools scope, and the turn's
  narrowing. A launch with no capability gets no tool. A server with no allowed
  tool is loaded and every call to it is refused. The launch also passes
  `--setting-sources project,local`, so the operator's own
  `~/.claude/settings.json` allow rules do not widen the grants; the side
  effect is that user-scope `env`, `model` and `apiKeyHelper` settings are not
  read for that launch (not verified against a live Claude here).
  Limits, stated plainly:
  - **Yolo mode is refused.** In yolo mode Claude is launched with
    `--dangerously-skip-permissions`, which removes the gate the allow rules
    feed. A host with `mcp_servers` fails its boot there rather than run every
    tool.
  - **The gate is advisory against a shell-capable agent.** `.mcp.json` is
    mode 0644 and the server command is an ordinary executable, so an agent
    with Bash can run the server (or the `tether mcp --token-file` gateway)
    itself and call tools that were not granted. Nothing in Nanite prevents that.
  - **Grants are per tool name, not per server.** A granted name is allowed on
    every listed server that exposes it.
  - **A revoke takes effect at the next launch.** The rules are argv, fixed
    for the life of the process.
  - **Not projected:** a row's `trust_tier` and `env_allowlist` apply to
    servers Nanite spawns itself and are ignored here; the CLI spawns the
    server with the row's `env` as written. Those values land in the boot dir,
    which the agent can read: put credentials in a file the server reads.
  - Only enabled stdio servers are projected, and none may be named `nanite`.
  Antigravity carries them with the same per-tool grants as
  `mcp(tether_<server>/<tool>)` rules in its private settings (below); hooks
  stay refused for it. Codex and OpenCode are refused
  (`cli_host_unsupported_field`) because Nanite has no per-tool gate it can set
  on them. Admission checks the
  servers exist; launch re-reads them and the grants, and fails the boot
  rather than plant a server without its gate.
- **Antigravity private HOME.** The boot dir carries `AGENTS.md`, `boot.md` and
  a workspace plugin `tether` whose `mcp_config.json` (0600) names the Nanite MCP
  server and the projected host servers. agy honours `$HOME` for its config, so
  `AmendEnv` sets `HOME=<bootDir>/agy-home`, a symlink farm of the real home's
  entries minus `.gemini`, `.agents`, `.DS_Store`, `.CFUserTextEncoding` and
  `.Trash` (so `~/.ssh`, `~/.gitconfig`, `~/.nanite` resolve as usual; the
  operator's global agy plugins and skills do not load). `agy-home/.gemini` links
  to a **private `.gemini`, persistent per agent** at
  `~/.nanite/agy-gemini/<agent slug>/` (not per boot, so agy's first-run
  consent answers and conversation history survive restarts; an agent with no
  usable slug gets a per-boot dir in the boot dir). It holds symlinks to the
  real auth files that exist (`oauth_creds.json`, `google_accounts.json`,
  `jetski-standalone-oauth-token`, `installation_id`,
  `antigravity-cli/antigravity-oauth-token`) and its own
  `antigravity-cli/settings.json`. The real `~/.gemini` and `~/.agents` are never
  written by Nanite (agy may write through an auth symlink when it refreshes a
  token).
  - **Rules.** `permissions.allow` gets `mcp(tether_nanite/*)` for the planted
    server and `mcp(tether_<host server>/<tool>)` for each granted tool of each
    projected host server. agy names a server `<plugin>_<server>` and the plugin
    is always `tether`. Tool or server names containing space, comma, `*`, `(`,
    `)` or `/` get no rule.
  - **Regeneration.** Every launch merges into the existing private file: keys
    and allow entries Nanite did not write are kept; the rules it wrote last
    time (recorded in the sidecar `antigravity-cli/nanite-owned-rules.json`) are
    replaced, so a revoked grant is gone at the next launch. Mode 0600, atomic
    rename. An unparsable file is kept as `settings.json.bak-<time>`.
  - **Not covered.** Other agy tools (`run_command`, file read/write, `read_url`)
    keep being auto-denied headless: Nanite maps nothing for them. Hooks are
    refused (an agy workspace hook's allow is advisory and does not approve).
    Concurrent launches of one agent share the private dir and write the same
    rules. macOS/Unix only (symlinks); coupled to agy 1.4.x's settings loader.
  - Proposed follow-up (not built): map an explicit per-agent allowlist from
    Nanite's tool policy to agy's `command(<prefix>)`, `read_file(<path>)`,
    `write_file(<path>)` and `read_url(<domain>)` rules, default deny, no
    wildcards.
  See [the adoption guide](../guides/agy-per-agent-mcp-allowlist.md).
- **Permissions.** The CLI runs under the boot dir's Claude permissions and the
  planted strict `.mcp.json`; Nanite's tools reach it only through the Nanite
  MCP server, so tool authorization is unchanged. The developer variant
  (`--dangerously-skip-permissions`) is selected only when the permission
  engine's mode is yolo, with an audit log line. A definition whose permission
  profile is not `default` is refused: a CLI process cannot enforce read-only.
- **Refusals.** A 422 from session create carries `error.cause`:
  `runtime_unavailable`, `provider_not_launchable`, `read_only_unenforceable`,
  `cli_host_unsupported_field` (`harness_profile`, a non-empty `native_loop`,
  `recall_*`, `mcp_servers` on a provider other than Claude or naming a server that cannot be projected, a relative directory, or a model the provider's
  CLI does not take), `host_directories_missing`.
- **Capabilities.** `runtime_kinds` lists `cli` with the providers this host can
  launch.

## Planted files by runtime

A CLI agent's boot dir is planted from the shared harness layout table
(`adapters/layout/plan`), the table Tether plants from, so one agent produces
the same files in both products. `TestPlantedFilesMatchTheSharedLayoutTable`
fails when Nanite stops planting a path the table names, or plants it with a
different mode; `sharedSkillRoot` reads the skill placement from the same
table.

| Runtime | Instructions | Kickoff | Native config | MCP | Skills (`<root>/<name>/SKILL.md`) |
|---|---|---|---|---|---|
| Claude | `CLAUDE.md` | `boot.md` | `.claude/settings.json` | `.mcp.json` (0600) | `.claude/skills` |
| Codex | `AGENTS.md` | `boot.md` | `config.toml` (0600, holds `[mcp_servers.*]`) | `.mcp.json` (0600, not read by Codex) | `skills` |
| OpenCode | `agents/<slug>.md` | `boot.md` | `opencode.json` (0600, holds `mcp`) | `.mcp.json` (0600, not read by OpenCode) | `skills`, and `.opencode/skills` |
| Antigravity | `AGENTS.md` | `boot.md` | `.agents/plugins/tether/plugin.json` | `.agents/plugins/tether/mcp_config.json` (0600) | `.agents/skills` |

Differences from the table, all deliberate: Nanite also plants `.sandbox/*` and
`.materialize/manifest.json` in every boot dir; OpenCode skills go to the
project-local `.opencode/skills` as well, because it is not settled which of the
two locations `OPENCODE_CONFIG_DIR` reaches; Codex's `auth.json` is a link to
the host login made at Setup, only when the host is logged in. Gemini, Copilot,
Aider and Junie have no layout. A file that carries the session credential is
written 0600 whatever the table's default. Skills are planted additively at
boot; a grant made later needs a fresh boot.

An API-backed session has no boot dir: it is offered its skills as a listing in
the skills slot and loads a body with `skill_get`. The rule for which skills an
agent has is the same on both paths, `store.SkillGrantCurrent` — a grant
approved against the skill's present content hash. A CLI boot plants only those
skills, `skill_get` refuses any other, and the listing now offers only those,
so a never-approved or stale grant is neither planted nor listed. Delivery
differs by design (files up front versus a listing and on-demand load).

## API-backed and CLI-backed sessions: where they differ

CW-20261022-0007 made an API-backed (native loop) session run like a CLI one
wherever the difference was an accident. What remains is listed here, each as
intentional or as an open gap. Update the list when one changes.

Intentional:

- **Boot material.** A CLI session is booted from a boot dir (system prompt
  file, `boot.md`, provider config, planted skills) in `$TMPDIR`, removed when
  the session ends. An API session has no boot dir; its context is assembled
  each turn in slots. Both have the durable session workspace
  `<WorkspacesRoot>/<session>/{prompts,state,logs}`.
- **Skill delivery.** CLI: files planted at boot, so a grant made later needs a
  fresh boot. API: a listing in the skills slot, rebuilt each turn, and bodies
  through `skill_get`. The rule for which skills qualify is shared
  (`store.SkillGrantCurrent`; see above).
- **Chat-harness services.** The result cache, truncation and per-turn
  ceilings apply to an API session's tool calls and not to a CLI process's
  calls to Nanite's tools (see "Subagent runtime").
- **Scratchpad.** It is a tool of the chat harness, kept in
  `state/scratchpad.json` for an API session so it survives turns. A CLI
  process keeps its own working files in its directories.

Open:

- **Disabled skills.** A CLI boot no longer plants a disabled skill
  (`ResolvePlantableSkills` checks `Enabled`, CW-20261024-0009). The API
  listing still offers one, though `skill_get` refuses it: the shared grant rule
  (`store.SkillGrantCurrent`) does not check `Enabled`, and adding it changes
  `Gate.Authorize`'s contract and many test fixtures, so that is left for its own
  change.
- **Tool roots and `directories`.** An API session's file and shell tools are
  rooted in the host's `directories` (first entry first) and the session
  workspace (`runtimeagent.SessionToolRoots`), as a CLI launch's roots are. A
  create-time `work_root` inside those directories leads them for either runtime.
  Control-plane directories are protected, and a host directory that contains one
  withholds `dev_bash` (`SessionToolProtection`).

## Subagent runtime

A subagent's runtime follows its parent. A CLI process's calls to Nanite's own
tools do not pass through the chat harness, so none of the result cache, the
truncation or the per-turn ceilings apply to them; a subagent run through the
chat harness gets all of it. From an API-driven parent — one whose provider has
no CLI adapter — a subagent therefore runs through the harness unless the user
or app opted into CLI subagents.

The opt-in is `subagent_runtime` (`api` or `cli`): the override on the **root**
session of the spawn tree, then the app default in user settings, then `api`.
The root, not the immediate parent, so the choice made where the tree started
holds at every depth: a CLI opt-in on the root reaches a grandchild spawned by
an API child, and an API root keeps API under a CLI app default. Session create
accepts the override on both `POST /api/sessions` and the harness v1 create. A
model-supplied `provider` argument and a role's `DefaultProvider` never count as
the opt-in. When either names a CLI provider without it, the child runs on the
parent's provider and model, the log records a warning, and the result the
parent reads opens with a note saying so. Nanite has no default provider or
model, so a parent session with no API provider of its own, or one that is
itself a CLI provider, makes the spawn fail saying so rather than running with
an empty provider. A CLI-driven parent keeps booting CLI children. The parent's
dispatch interface is the same either way.

`subagent_runtime` says how a *child* of an API-driven parent runs. It is
separate from an agent's `runtime_kind`, which says how that agent's own session
runs; the subagent path routes by the child's provider and does not read the
role's `runtime_kind`.

This is an interim home for the setting; it moves onto the launch-assignment
limits once those exist.

## ACP permission requests

Nanite configures `ACPBestEffortPermissionRequestResponder` only when both the
existing permission engine and the session SSE sink are available. When an ACP
provider sends `session/request_permission`, the bridge creates the same
`approval_request` consumed by the existing API/UI and waits for its
`allow|deny` plus `once|session` response. It returns only an exact option ID
that the provider offered:

- allow/once selects `allow_once`; it never widens to `allow_always`;
- allow/session prefers `allow_always`, then safely narrows to `allow_once`;
- deny/once selects `reject_once`; it never widens to `reject_always`;
- deny/session prefers `reject_always`, then narrows to `reject_once`.

If no semantically safe offered option exists, the bridge returns the zero
selection and the wrapper cancels the request. If Nanite's approval engine or
SSE sink is absent, the responder remains nil: wrapper's documented safe
default applies (Claude/Codex/OpenCode/Pi answer canceled; Copilot retains its
method-not-found fallback).

This is best-effort provider cooperation, not a general execution gate. It can
act only when the provider asks. Some ordinary provider operations may not send
an ACP permission request. Nanite's toolclient/RPC authorization remains the
authoritative pre-execution gate for host-owned tools and is not routed through
the wrapper policy-observer API.

The measured provider coverage is deliberately narrow (the canonical details
live with the v0.9.1 wrapper API):

| ACP adapter | Measured provider behavior |
|---|---|
| Claude | One ordinary Bash call executed internally without asking; other operation classes are unmeasured. |
| Codex | One ordinary shell call executed internally without asking; other operation classes are unmeasured. |
| OpenCode | One shell shape executed internally without asking; this is not exhaustive. |
| Pi | No request was observed; `pi-acp` documents local filesystem and terminal execution. |
| Copilot | Copilot CLI 1.0.12 asked before one non-mutating `pwd`/`execute` shape; other shapes are unmeasured. |

## Trying a real CLI turn

`internal/service/cli_runtime_spike_test.go` boots one real Claude CLI turn from a
pinned definition and `runtime=cli` host settings on a scratch database. It is
opt-in because it starts the real binary and spends a turn:

```sh
NANITE_SPIKE_CLAUDE=1 go test ./internal/service -run TestSpikeCLIRuntimeClaudeTurn -v -count=1
```

The test's isolated `HOME` makes Claude report `Not logged in`; the spike passes
the real `HOME` through the boot options, as the server inherits it.
`scripts/smoke-agents.sh` covers the same ground end to end through the HTTP API
(`SMOKE_CLI=1`).
