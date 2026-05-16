# Standalone Launcher — `nanite launch`

The standalone launcher starts a Nanite-managed CLI agent (Claude / Codex /
OpenCode) directly from a **shared launch profile**, without a running
Nanite chat server, a browser dropdown, or a Tether orchestrator in the
loop.

It is an **additional** launch path, not a replacement. Nanite's primary
launch path — the chat runtime's boot-profile dropdown — is unchanged.

> Added by CW-20260515-0027 (Phase 6, SP-20260514-0008): shared
> launch-profile adoption.

## Usage

```
nanite launch <profile-id> [flags]
```

| Flag | Default | Purpose |
|---|---|---|
| `--catalog <dir>` | agentrc `boot_profile_catalog_path` | Boot-profile catalog root. The launcher reads `<dir>/boot-profiles/*.yaml` and `<dir>/launches/*.yaml`. |
| `--dry-run` | off | Compile + resolve + validate the launch plan, then exit **without** starting the agent. A pure compile-level check; opens no store and needs no provider binary. |
| `--no-wait` | off | Return as soon as the agent process is started instead of blocking until it exits. |
| `--db <path>` | `./nanite.db` | SQLite DB used to persist the runtime lifecycle row and to point the planted `.mcp.json` subprocess descriptor at a real database. |
| `--dev` | off | Use the dev provider adapters (permissions skipped). |

### Examples

```sh
# Compile-check a profile without launching anything:
nanite launch --catalog ./examples/boot-profiles --dry-run claude-smoke

# Launch a Nanite-managed Claude agent from a profile, blocking until exit:
nanite launch --catalog ./examples/boot-profiles claude-smoke

# Fire-and-forget (return once the process is started):
nanite launch --catalog ./examples/boot-profiles --no-wait claude-smoke
```

## What it does — the pipeline

The launcher chains the same compile machinery the chat dropdown uses, then
projects the result onto the **shared `go-agent-launch` vocabulary** before
handing off to Nanite's runtime:

```
bootprofile.LoadCatalog          load the on-disk catalog
bootprofile.CompileFromCatalog   compile profile + paired launch → LaunchSpec
bootprofile.ResolveRequirements  drain deferred cmd/http/role_summary/skill_index slots
LaunchSpec.ToLaunchPlan          project onto shared agentlaunch.LaunchPlan
  + LaunchPlan.Validate          shared-plan gate: provider×runtime matrix,
                                 path expansion, sentinel-error contract
agent.Boot                       Nanite runtime start (shared bootdir planting)
```

- **Catalog + compile** — identical to the chat boot-profile path
  (`internal/bootprofile`). Same YAML schema, same compiler, same slot
  resolution.
- **Deferred slots** — `cmd` / `http` / `role_summary` / `skill_index`
  slots are resolved through the shared `go-agent-context` resolvers
  (CW-0026) before the launch plan is built.
- **Shared plan** — the compiled spec is projected onto an
  `agentlaunch.LaunchPlan` and `Validate`-d. This is the convergence gate
  with the rest of the portfolio (Tether, Torque): the provider×runtime
  matrix lookup, path expansion, and sentinel-error contract all run here.
- **Runtime start** — the launch starts through Nanite's own runtime
  (`internal/runtime/agent.Boot`), which materializes the workspace and the
  ephemeral boot directory and starts the provider process.

### Bootdir content — why not `providerplant`

The shared `go-agent-launch` package ships a `providerplant` planter that
can materialize a bootdir directly from a `LaunchPlan`. The standalone
launcher **deliberately does not use it for planting**.

`providerplant.Plant` renders go-providers' *own* `CLAUDE.md` / `.mcp.json`
content. Nanite's bootdir content diverges byte-for-byte: it carries the
envelope schema, the Nanite agent-context document, and a subprocess-spawn
MCP descriptor that names the `nanite` binary. Planting go-providers'
content would regress the "functionally equivalent to a chat-spawned
session" bar.

Instead, `agent.Boot` plants the bootdir through Nanite's per-provider
`Layout` implementations, which **already ride the shared
`agentlaunch.InjectionSpec` path-safe write loop** (CW-0025's
`bootdir_plant.go`). So a standalone launch gets shared bootdir *behavior*
(the shared path-safety gate, the shared native-file / overlay model) with
Nanite-correct bootdir *content*. The `LaunchPlan` is still built and
`Validate`-d so the standalone path exercises the shared-plan contract — it
just is not the planting mechanism.

## When to use which

| | Standalone launcher (`nanite launch`) | Chat-managed launch (dropdown) |
|---|---|---|
| **Entry point** | `nanite launch <profile>` CLI | Nanite chat UI provider dropdown |
| **Needs a running server** | No | Yes (`nanite serve`) |
| **Driven by** | A shared launch profile on disk | A `bootprofile:<id>` dropdown selection |
| **Tether / orchestrator** | None — fully standalone | None required, but coexists with orchestration |
| **Interaction model** | Operator runs an agent in front of them; foreground process | Interactive chat session in the browser |
| **Use it for** | Scripted launches, CI smoke checks (`--dry-run`), operator one-offs, headless boxes with no GUI | Day-to-day interactive chat work |

Use **Tether-managed launch** when the launch is part of a larger
orchestrated workflow that Tether coordinates (multi-agent fan-out,
cross-app handoffs). Use the **Nanite standalone launcher** when you want a
single Nanite-managed agent from a profile and nothing else — no server, no
browser, no orchestrator. Both paths consume the *same* shared launch
profiles, so a profile authored for one works in the other.

## Profile shape

The launcher consumes the standard Nanite boot-profile catalog layout:

```
<catalog-root>/
  boot-profiles/<id>.yaml   # the profile: identity + slots + launch ref
  launches/<id>.yaml        # the launch: provider + workdir + env + args
```

A **prompt-only** profile (no `launch:` field, hence no provider) is
rejected — it has no runtime target. `nanite launch --dry-run` surfaces
this as a pointed error.

See `examples/boot-profiles/` for a working `claude-smoke` profile.
