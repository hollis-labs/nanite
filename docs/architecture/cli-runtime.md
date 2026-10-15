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
| `harness_profile`, `native_loop`, `recall_*` | ignored for CLI. The CLI owns its loop. Validation should warn, not reject |

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
- Yolo/developer posture is `Dependencies.DeveloperMode`, which selects the
  Claude developer variant. It stays a daemon-level dependency, never a host
  setting or a request field. The spike ran with it on.
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

## Open questions

1. Where does the CLI session's work directory come from for an API caller
   (`work_root` is currently unsupported on the v1 create route)?
2. Should `harness_profile` / `native_loop` on a CLI host be rejected or
   ignored? Proposed: ignored with a warning in the create response.
3. Codex and OpenCode model flags: confirm per adapter before B2 relies on a
   uniform `--model`.
4. Trust dialog handling (gap 6): pre-seed `~/.claude.json` (touches a user
   file) or pass `--add-dir`?
5. Should `runtime_kinds` list per-provider launchability, or only the kind?
6. ACP-for-Claude vs streaming stdio: both are reachable; B2 keeps native
   streaming stdio as the default and treats `protocol=acp` as opt-in.
