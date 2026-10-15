# Native-session runtime for CLI agents (`runtime=cli`)

Status: design + spike, CW-20261014-0155 (epic EP-20261014-0002, slice B1).
Not implemented beyond the spike test. Implementation is B2.

## Problem

The definition flow (`POST /api/agent/v1/sessions` with `definition_ref` and
`host_settings`) only runs `runtime=api`. `CognitiveViews.Create`
(`internal/service/cognitive_views.go:129`) returns `ErrUnsupportedDefinition`
for any other runtime, and the API maps that to a 422 that does not say why.
Capabilities advertise `runtime_kinds: [api]` only (`internal/api/agent_v1.go`).

Owner decision 2026-10-14: CLI agents (Claude, Codex, OpenCode/agy) get their
own native-session runtime in the definition flow. Nanite stays standalone and
keeps its own CLI sessions (ruling 2026-10-13).

## What already exists

Most of the machinery is already in tree; the design is mostly *removing four
hard-coded "api" assumptions* and filling three gaps.

- `store.NativeHostSettings` already validates `runtime=cli` and the
  `protocol` / `transport` pair (`ValidateAgentACPFields`). Nothing refuses a
  CLI host document at write time.
- `store.projectAgentHost` (`internal/store/agent_projection.go`) turns host
  settings plus the pinned definition into an `AgentProfile`: definition text
  to `SystemPrompt`, `RuntimeKind`, `Protocol`, `Transport`, `DefaultProvider`,
  `DefaultModel`. `Store.GetAgent(hostID)` and `GetAgentBySlug(slug)` return it.
- `agentProfileResolver.GetOrDefault` (what `runtimeagent.Boot` calls with
  `Options.AgentProfile`) already resolves through those two. A host slug or ID
  is therefore a valid `AgentProfile` for `Boot` today.
- The chat generation pipeline already branches on `prov == nil` to
  `driveBootSession`, which owns Boot, recovery, routers and SSE projection.
  `resolveProvider` honours `runtimeKind == "cli"`.

## Design

### Host settings shape

Reuse `NativeHostSettings`. No new fields for the first cut.

| Field | `runtime=cli` meaning |
|---|---|
| `runtime` | `"cli"` |
| `provider` | registry runtime name: `claude`, `codex`, `opencode` (aliases `agy`, `claude-code` normalise) or an ACP-only one (`copilot`, `pi`) |
| `model` | passed to the CLI (see gap 1) |
| `protocol` / `transport` | empty = native mode; `acp` + `stdio` (or `tcp` for Copilot) selects ACP |
| `directories` | extra readable/writable roots (gap 3) |
| `mcp_servers` | extra MCP servers to plant (gap 3) |
| `harness_profile`, `native_loop`, `recall_*` | not applicable: the CLI owns its loop. Empty or default values pass; a non-empty value is rejected with a typed cause (open question 1) |

"Bootdir" is not a host setting. `Boot` plants an ephemeral per-session boot dir
(`bootdir_claude.go` etc.) and the host document stays free of paths. Work
directory comes from the session create request (`work_root`, currently
unsupported on the v1 route) or the project, as the legacy CLI chat path
resolves it in `bootSessionWorkdir`.

Native mode selection stays Nanite product policy (`nativeModes` in
`runtime_select.go`): Claude streaming-stdio, Codex and OpenCode
subprocess-per-turn, ACP when `protocol=acp`.

### Session lifecycle

All of it rides existing code paths.

1. **Create.** `CognitiveViews.Create` accepts `runtime=cli` when
   `runtimeagent.LaunchError(provider)` is nil (or the ACP selection is
   launchable). The view row stores `ChatDefinitionConfig` with
   `HostSettings` as today. The model authorizer for CLI checks the provider
   against the *launch registry*, not the API `provider.Registry`
   (`configuredNativeModel` would refuse `claude`).
2. **Turn.** `submitCognitiveTurn` stops requiring an API provider for a CLI
   host. `chat_generation_actions.go:1945` builds the transient `AgentProfile`
   with `RuntimeKind: "api"` hard-coded; for a CLI host it takes `RuntimeKind`
   from `cfg.HostSettings.Runtime`. `resolveProvider` then returns
   `(provider, nil)` and the existing `driveBootSession` runs.
3. **Boot.** `driveBootSession` currently passes `agent.Slug` as
   `Options.AgentProfile`. A defined view has no slug, so the design passes the
   host settings ID (`cfg.HostSettingsRef.ID`), which `GetOrDefault` resolves.
   The host revision is pinned on the view; Boot re-reads the host row, so a
   revision mismatch must fail the turn, not silently use a newer host (gap 4).
4. **Stream.** Unchanged: wrapper events to `runtimeEventBridgeSink` to
   `chatstream/v1` SSE, plus the host runtime feed.
5. **Stop / cancel.** Unchanged. Claude/Codex/OpenCode native runtimes report
   turn cancel unsupported, so cancel stops the wrapper and the next turn cold
   boots. ACP has a real turn cancel (`agent-runtime.md`).
6. **Resume.** `Options.ResumeProviderSessionID` from
   `AgentRuntimeProviderSessionID`, as in the legacy path. Note the Codex
   caveat in `providerSessionSurvivesBoot`.

### Permissions and tool grants

- Tools follow policy (decision `kill_developer_mode_tools_follow_policy_2026_10_14`).
  For CLI the policy surface is the planted boot dir: Claude
  `settings.json` permissions, the strict `.mcp.json`, and the allow rule for
  the planted Nanite MCP server (`claude_mcp_allow.go`, `strict_mcp.go`).
  Nanite-owned tools reach the CLI only through that MCP server, so
  `toolclient`/RPC authorization remains the gate for them.
- Yolo selects the Claude developer variant: `Dependencies.yoloMode()` reads
  the permission engine's mode, and only `ModeYolo` sets
  `launch.Selection.DeveloperMode` (with an AUDIT log line). It is a daemon
  permission-engine state, never a host setting or request field, so a CLI host
  needs nothing of its own. The spike runs in the default mode.
- ACP permission requests go through the existing approval bridge
  (`agent-runtime.md`, "ACP permission requests"). Best-effort, not a gate.
- `projectAgentHost` refuses any definition whose permission profile is not
  `default` ("no enforced read-only posture"). That is correct for CLI and
  should stay: a `read-only` definition cannot be honoured by a CLI process,
  so it is refused, with a stated cause (gap 5).

### MCP attachment

Unchanged mechanism: `Dependencies.MCPConfig` plants a per-session `.mcp.json`
naming the `nanite mcp` subprocess with the session ID and agent credential.
New: host `mcp_servers` are projected into `AgentProfile.MCPServers`
(`projectAgentHost` currently hard-codes `"[]"`), which the layouts already
read (`sandbox_content_claude.go`). Strict MCP stays on; a host-listed server
is added to the allow list explicitly.

### Reuse map

| Concern | Reused as-is |
|---|---|
| Process lifecycle, resume/new, cancel, close | `go-agent-wrapper` via `wrapper.Wrapper`; Nanite `SessionManager` binding |
| Adapter choice | `selectRuntime` / `launch.Select` (Claude streaming stdio, Codex/OpenCode per turn, ACP for Copilot/Pi) |
| Boot dir, MCP, skills, sandbox | `runtime/agent` bootdir and sandbox code |
| Events, SSE, runtime feed | `runtimeEventBridgeSink`, `HostRuntimeFeed` |
| Recovery | `observeSessionForRecovery`, recovery broker |

## Gaps B2 must close

1. **Model is not passed to the CLI.** `Options` and `launch.Selection` carry
   no model. `DefaultModel` reaches only the prompt text
   (`sandbox_content_claude.go:117`). Fix: add `Options.Model` and append
   `--model <m>` (Claude), the equivalent for Codex/OpenCode, to
   `Selection.ExtraArgs`. The spike asked for `haiku` and did not verify it
   reached the CLI.
2. **Admission and authorization hard-code API.** `cognitive_views.go:129`,
   `cognitive_admission.go` (`s.providers.Get`), `session.go:192-195`,
   `logical_agent_provision.go`. Introduce one `hostRuntimeKind(h)` helper and
   route on it.
3. **Host `mcp_servers` and `directories` are unprojected** (see above).
4. **Host revision pinning at Boot.** `driveBootSession` must verify the
   resolved host revision equals the view's `HostSettingsRef.Revision`.
5. **Error surface.** `ErrUnsupportedDefinition` hides the cause (N1 trial).
   Return a typed reason (`runtime_unavailable`, `read_only_unenforceable`,
   `provider_not_launchable`) and put it in the 422 body; advertise
   `runtime_kinds` from `runtimeKindOptions()` filtered by what is launchable
   on this host.
6. **Boot-dir trust warning.** Claude prints "workspace has not been trusted;
   ignoring permissions.additionalDirectories" for the ephemeral boot dir, so
   `CLIWritableRoots` are silently dropped on this Claude version (2.1.300).
   The spike hit it. Needs either a pre-seeded trust entry or passing the dirs
   as `--add-dir`.
7. **Provider session ID** was empty after one turn in the spike (see below).

## Spike

`internal/service/cli_runtime_spike_test.go`, opt-in
(`NANITE_SPIKE_CLAUDE=1`). On a scratch store it installs a pinned definition,
creates host settings `{runtime: cli, provider: claude, model: haiku}`, builds
dependencies with `BuildAgentDependencies`, then runs
`runtimeagent.Boot(AgentProfile: <host slug>)` followed by one
`SendInput("ping")` against the real `claude` CLI (2.1.300).

```
NANITE_SPIKE_CLAUDE=1 go test ./internal/service \
  -run TestSpikeCLIRuntimeClaudeTurn -v -count=1
```

Result: PASS in about 32 s. Events: `sandbox.applied, session.ready,
process.started, stdin.write, ... turn.started, session.processing,
agent.delta ("PONG"), turn.completed, session.idle`. This shows the
definition-to-host-settings-to-profile projection and `Boot` already compose
for `runtime=cli` with no production change.

Notes from the run:

- With the test binary's isolated `HOME` the turn fails `Not logged in`. The
  spike passes the real HOME in `Options.Env`; the daemon inherits it.
- `provider_session_id` read empty right after the turn. It is persisted by
  the chat path's observer, which the spike does not run. Unconfirmed.
- The spike calls `Boot` directly. It does not exercise `CognitiveViews.Create`
  or the HTTP route; those are the B2 gates above.

## Open questions, with recommendations

1. **`harness_profile` / `native_loop` on a CLI host: reject or ignore?**
   Recommend **reject, typed**. Reject only a non-empty value (the API-shaped
   default `native_loop: {}` and an empty `harness_profile` must pass, since
   the same host document shape is used for both runtimes), with a
   `cli_host_unsupported_field` cause naming the field. Silently ignoring a
   knob the author set is the "422 that hides the cause" problem again.
   The error vocabulary belongs with A3.
2. **Claude boot-dir trust dialog (gap 6).** Options:
   - *Pre-seed `hasTrustDialogAccepted` in `~/.claude.json`.* Works, but Nanite
     edits a user-owned file shared with interactive Claude sessions, and the
     key is per boot-dir path, so entries accumulate.
   - *Pass the writable roots as `--add-dir` launch args* (`workRootArgs`
     already does this for the work root). No user file touched, no state.
     Unverified that `--add-dir` is honoured without the trust entry; B2 must
     check it first.
   - *Give the boot dir its own `CLAUDE_CONFIG_DIR`.* Hermetic, but then the
     login in `~/.claude` is not visible, so the turn fails "Not logged in".
   Recommend **`--add-dir`**, falling back to documenting the limitation
   (writable roots dropped, work root still reachable) if it does not work.
   Never edit `~/.claude.json`.
3. **Where does the work directory come from for an API caller?**
   `work_root` is deliberately unsupported on the v1 create route, so the
   caller cannot name an arbitrary path. Recommend **host settings own it**:
   `directories` is the allow-list, the first entry is the default work root,
   and a later optional `work_root` on create is accepted only if it lies
   inside one of those entries (path-contained, symlinks resolved). A CLI
   process can read and write there, so filesystem authority stays with the
   host document, not the request. For B2, ship the default only; add the
   create-time field when something needs it. A host with no `directories`
   refuses CLI session create with a typed cause rather than defaulting to
   the daemon's cwd.
4. **Model flag per adapter.** Recommend B2 does **Claude only**
   (`--model`), verified by a launch-args unit test, and refuses a model on
   Codex/OpenCode hosts with a typed cause until each flag is confirmed
   against that adapter. A uniform `--model` is an assumption.
5. **`runtime_kinds` in capabilities.** Recommend listing **per provider**:
   kind `cli` with the providers `LaunchError` says are launchable on this
   host (claude yes, antigravity no). The UI and the 422 cause both need to
   say *which* provider is the problem, and a bare `cli` would promise more
   than the host can launch.
6. **ACP vs streaming stdio for Claude.** Recommend native streaming stdio as
   the default (it is what chat uses today and the spike proves); `protocol=acp`
   stays opt-in and unchanged.
