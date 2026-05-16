# CW-20260515-0027 Handoff — Nanite standalone launcher on shared launch profiles

**Sprint:** SP-20260514-0008 Phase 6
**Branch:** `feat/cw-20260515-0034-phase6-nanite-adoption`
**Status:** complete — `go build ./...`, `go vet ./internal/launcher/... ./cmd/nanite/...` green; `go test ./internal/launcher/... ./cmd/nanite/... ./internal/bootprofile/... ./internal/runtime/agent/...` all pass. One unrelated pre-existing failure (`internal/chat` `TestEnvelopeRegistrySync`) — confirmed identical on base via `git stash`, see §5.

---

## 1. What this ticket added

A **standalone agent-launch path**: an operator can start a Nanite-managed
CLI agent (Claude / Codex / OpenCode) from a shared launch profile with
**no chat server, no browser dropdown, and no Tether MCP** in the loop.
This is an *additional* path — the chat boot-profile dropdown, recovery,
and chat behavior are untouched.

### Entrypoint + package

- **CLI:** `nanite launch <profile-id> [flags]` — new subcommand, wired
  into `cmd/nanite/main.go`'s dispatch switch. Implemented in
  `cmd/nanite/launch_cmd.go`.
- **Package:** `internal/launcher` — the orchestration logic, independent
  of `internal/service` (the chat service). Two public entry points:
  - `launcher.Plan(ctx, Config)` — the no-side-effects half: load
    catalog → compile → resolve → project + validate a shared
    `agentlaunch.LaunchPlan`. Backs `--dry-run`.
  - `launcher.Launch(ctx, Config)` — `Plan` + start the agent through
    Nanite's runtime; optionally blocks until the process exits.

### Changed / added paths (Nanite repo only — NO shared-package changes)

| File | Change |
|---|---|
| `internal/launcher/launcher.go` | **NEW.** `Config`, `Result`, `Plan`, `Launch`, the `LaunchSpec → agent.Options` projection (`bootOptionsFor`), the `LaunchPlanOptions` defaults (`planOptionsFor`), minimal `Dependencies` assembly (`buildDeps`), `defaultProfiles` resolver, `ResolveBinaryPath`. |
| `internal/launcher/runtimestore.go` | **NEW.** `memoryRuntimeStore` (in-memory `RuntimeStore` + `StateSink` for store-free launches/tests) and `storeRuntimeStore` / `storeStateSink` (`*store.Store`-backed, persists into the `agent_runtime` table). |
| `internal/launcher/launcher_test.go` | **NEW.** Compile-level + fake-runtime tests — see §4. |
| `cmd/nanite/launch_cmd.go` | **NEW.** `cmdLaunch` — flag parsing, catalog resolution, `--dry-run`, store open, provider-adapter construction (reuses `initProviders`), `launcher.Launch` call. |
| `cmd/nanite/main.go` | Added `launch` to the usage line and the dispatch `switch`. |
| `docs/standalone-launcher.md` | **NEW.** Operator docs — usage, the pipeline, the providerplant content caveat, the "when to use standalone vs Tether-managed" table. |

---

## 2. How the launcher chains compile → resolve → plant → start

```
bootprofile.LoadCatalog          load <catalog>/boot-profiles + /launches
bootprofile.CompileFromCatalog   compile profile + launch → LaunchSpec
bootprofile.ResolveRequirementsContext   drain deferred cmd/http/role/skill slots
LaunchSpec.ToLaunchPlan          CW-0024 bridge → agentlaunch.LaunchPlan
  + LaunchPlan.Validate          shared-plan gate
agent.Boot                       Nanite runtime start
  (+ Session.Wait when cfg.Wait) block until process exits
```

- **Resolve before bridge.** Per the CW-0026 handoff §7, the launcher
  calls `ResolveRequirementsContext` *before* `ToLaunchPlan` — an
  unresolved spec has an empty `BootPrompt`, so the inline boot profile
  would carry an empty boot body. The launcher threads the CLI's
  signal-cancellable context into the resolvers.
- **`planOptionsFor`** supplies the lifecycle knobs the Nanite catalog
  does not express (the CW-0024 bridge requires the caller to be
  explicit): `Runtime = RuntimeStreamingStdio` (the long-lived shape
  Nanite registers for claude), `WorkspaceMode = WorkspacePersistent`,
  `Mode = LaunchInteractive`. `ProjectID` / `AgentID` come from the
  profile identity so the validated plan is self-describing.
- **`bootOptionsFor`** projects the compiled `LaunchSpec` onto
  `agent.Options` — provider, workdir, role, env, extra args, boot-prompt
  override — mirroring `service.applyLaunchSpecToBootOpts` but from an
  empty `Options` (no chat-layer caller fields), so the precedence
  collapses to "spec wins". It also stamps `SessionMeta.launch_source =
  "standalone-launcher"` for provenance.

### Why NOT `providerplant.Plant` / `PrepareAndPlant`

The CW-0025 handoff §7 suggested the standalone path *could* drive
`providerplant.Plant` on the `LaunchPlan`. **The launcher deliberately
does not**, and this is the resolution of the CW-0025 content caveat:

- `providerplant.Plant` renders go-providers' *own* `CLAUDE.md` /
  `.mcp.json`. Nanite's bootdir content diverges byte-for-byte (envelope
  schema, agent-context doc, subprocess-spawn MCP descriptor naming the
  `nanite` binary). Planting go-providers' content would regress the
  "functionally equivalent to a chat-spawned session" bar.
- `agent.Boot` already plants the bootdir through Nanite's per-provider
  `Layout` impls, which **already ride the shared
  `agentlaunch.InjectionSpec` path-safe write loop** (CW-0025's
  `bootdir_plant.go`). So a standalone launch gets shared bootdir
  *behavior* with Nanite-correct *content*.
- The `LaunchPlan` is still built and `Validate`-d, so the standalone
  path **does** exercise the shared-plan contract (matrix
  provider×runtime lookup, path expansion, the `ErrMissingProviderID`
  sentinel for prompt-only profiles). It is the convergence/validation
  artifact, just not the planting mechanism.

`agent.Boot` is the right convergence point: it is the same runtime entry
the chat path uses, so standalone and chat launches reach an identical
runtime with identical (shared-`InjectionSpec`-planted) bootdirs.

---

## 3. Standalone-ness — why `buildDeps` is local, not `service.BuildAgentDependencies`

`service.BuildAgentDependencies` is the chat composition root's deps
factory. It **requires** the chat service's `*StreamManager` and wires the
recovery broker, the `agentEventBridge`, and the boot-profile recovery
adapter — none of which a one-shot standalone launch needs, and importing
it would couple `internal/launcher` to `internal/service`.

`internal/launcher.buildDeps` assembles a **minimal**
`runtimeagent.Dependencies` locally: an `agentsessions.Manager`, a
`RuntimeStore`, an `AgentProfiles` resolver, and the provider-adapter
lookup. Recovery / EventFanout / TypedEventCallback are left nil — all
optional in `agent.Boot`. This keeps the launcher genuinely independent of
the chat service.

- **With a store** (`Config.Store != nil`, the CLI default): the runtime
  lifecycle row persists into the `agent_runtime` table via
  `storeRuntimeStore` / `storeStateSink` — a standalone launch is
  introspectable / orphan-sweepable exactly like a chat-spawned session.
- **Without a store** (`Config.Store == nil`, tests): `memoryRuntimeStore`
  records rows + state transitions in-process; the launch still works,
  it just is not persisted.

---

## 4. Verification — what it was run against

```
go build ./...                                 → ok
go vet ./internal/launcher/... ./cmd/nanite/... → ok
go test ./internal/launcher/...                 → ok
go test ./cmd/nanite/...                         → ok
go test ./internal/bootprofile/...               → ok
go test ./internal/runtime/agent/...             → ok
```

**Tests (`internal/launcher/launcher_test.go`):**

- `TestPlan_CompilesAndValidates` — compile-level acceptance: a temp
  catalog compiles → resolves → projects a valid `LaunchPlan`; the
  inline boot prompt matches the compiled spec.
- `TestPlan_PromptOnlyProfileRejected` — a profile with no launch
  provider is rejected with a wrap of `agentlaunch.ErrMissingProviderID`.
- `TestPlan_MissingProfile`, `TestPlan_RequiredFields` — error paths.
- `TestLaunch_FakeRuntime` — **the full pipeline end-to-end** (Plan →
  `buildDeps` → `agent.Boot` → `Wait`) against a **fake claude adapter**
  whose binary is `/usr/bin/true`. No real provider CLI, no store
  (in-memory runtime store). The process spawns, the workspace + bootdir
  are materialized, the manager starts it, `Wait` returns clean. This is
  the **fake-runtime verification** the task accepts in lieu of a live
  provider launch.
- `TestLaunch_NoAdapters`, `TestBootOptionsFor` — guard + projection
  checks.

**Live CLI dry-run** (real example catalog, not a fake):

```
$ nanite launch --catalog ./examples/boot-profiles --dry-run claude-smoke
launch plan OK: profile=claude-smoke provider=claude runtime=streaming-stdio workdir=/tmp/nanite-smoke-workdir
  boot prompt: 640 bytes, 2 slot(s), 0 mcp allow-list entr(ies)
```

A **live provider launch** (real `claude` binary) was NOT run — no
provider binary in this execution worktree. `TestLaunch_FakeRuntime`
covers the spawn path with `/usr/bin/true`; CW-0028 smoke should do the
live launch.

**Pre-existing unrelated failure:** `internal/chat`
`TestEnvelopeRegistrySync` fails reading a sibling
`go-envelopes/manifest/envelopes.yaml` checkout absent in this worktree
layout. Confirmed identical failure on the base commit (`git stash` +
re-run). Same worktree-layout class as the CW-0024/0025/0026 notes.

---

## 5. Shared-package gaps + environment notes

**No shared-package gaps.** No edits to `go-agent-launch` /
`go-agent-context`, no `replace` directives, no `go.mod` change. The
`agentlaunch` types used (`LaunchPlan`, `RuntimeStreamingStdio`,
`WorkspacePersistent`, `LaunchInteractive`, `ErrMissingProviderID`) are
all in the tagged `v0.1.0` already in `go.mod` from CW-0024.

**Environment note (same as CW-0024/0025/0026, not committed):** this
worktree needs the `libs/go-modelsdev` symlink
(`/Users/chrispian/agent-mux/workspaces/nanite/libs/go-modelsdev →
/Users/chrispian/dev/hollis-labs/libs/go-modelsdev`) for `go build` to
resolve the repo's existing `replace` directive. Already present;
unrelated to this ticket.

---

## 6. Notes for CW-0028 (smoke)

- **Live launch.** The acceptance gap to close: run
  `nanite launch --catalog <dir> <profile>` against a real `claude`
  binary and confirm the agent process starts, the bootdir is planted
  with Nanite content, and the workspace dir is reserved. The
  `examples/boot-profiles/claude-smoke` profile is a ready fixture (it
  compiles with text+static slots only — no deferred resolvers).
- **`--dry-run` is a cheap CI gate.** `nanite launch --dry-run <profile>`
  compiles + resolves + validates without a store or a provider binary —
  a smoke can assert exit 0 + the `launch plan OK:` line for every
  profile in a catalog.
- **Bootdir content parity.** Because the standalone launcher starts via
  `agent.Boot`, the planted bootdir is **byte-identical** to a
  chat-spawned session for the same profile (CW-0025 handoff §"CW-0028"):
  same files, same paths, same content. A smoke can reuse chat-bootdir
  assertions.
- **Runtime row.** A store-backed standalone launch writes an
  `agent_runtime` row with `Meta.launch_source = "standalone-launcher"`
  — a smoke can assert that row exists + is in `running`/`done`.
- **`--no-wait`.** For a smoke that does not want to block on the agent
  process, `--no-wait` returns once the process is started; the smoke
  then asserts on the printed session id / bootdir.
- **Prompt-only profiles** are rejected at `Plan` time with
  `ErrMissingProviderID` — a smoke feeding a prompt-only profile should
  expect a non-zero exit with a pointed message.

### General

- `internal/launcher` is the home for any future "standalone launch does
  more" work. If a later ticket wants the launcher to also handle
  `ModeBackground` / `ModeOneShot` lifecycles, `bootOptionsFor`'s `Mode`
  and `planOptionsFor`'s `LaunchMode` are the two seams to touch.
- The launcher does **not** resume from checkpoints — `memoryRuntimeStore
  .GetCheckpoint` errors by design. Standalone launches start fresh.
