# Migrate bootdir Layout implementations onto go-agent-wrapper's Planter

**Phase:** 2 — Nanite host migration (`TASKS/agent-host-acp`)
**Status:** not-started
**Depends on:** `02` (Descriptor split), `03` (dependency wired)
**Touches:** `internal/runtime/agent/bootdir.go`, `bootdir_claude.go`, `bootdir_codex.go`,
`bootdir_opencode.go`, `bootdir_plant.go`, `bootdir_common.go`, `bootdir_provider_config.go`.
Repo: Nanite.

## Context

`docs/engineering/architecture/16-agent-host.md` names Nanite's `Layout` interface
(`bootdir.go`) as functionally overlapping `go-agent-wrapper`'s `plant.Planter` — file
planting (`CLAUDE.md`/`AGENTS.md`/`.mcp.json`/provider settings/hooks) is explicitly host-
owned work per both docs' stated boundary, not product-owned.

**Verified shape of both sides, directly against the code:**

`Layout` interface, `bootdir.go:17-56` — confirmed seven methods:
`Setup(params SetupParams) (string, error)` (`:21`),
`Populate(bootDir string, params SetupParams) error` (`:29`),
`RegenerateSystemPromptSlot(bootDir string, params SetupParams) error` (`:36`),
`AmendEnv(base map[string]string, bootDir string) map[string]string` (`:40`),
`SpawnWorkdir(bootDir, projectDir string) string` (`:45`),
`BootPrompt(profile *store.AgentProfile, opts Options) string` (`:50`),
`BootMode() string` (`:55`). `LayoutFor(provider string) Layout` (`:61-63`) is the dispatch
point; `HasBootdirLayout` (`:82`) gates CLI-vs-HTTP routing elsewhere.

Per-provider planted files, confirmed by direct read:
- **Claude** (`bootdir_claude.go`): `CLAUDE.md` (role-aware system prompt + envelope rules,
  `:14`), `.claude/settings.json` (provider config, sourced from go-providers `BootDirSpec`,
  `:20,57`), `.mcp.json` (`:21`). Builds an `agentlaunch.InjectionSpec` via
  `claudeInjectionSpec` (`:48`). `SpawnWorkdir` returns the boot dir itself (`:116`);
  `BootMode()` returns `"stdin"` (`:128`).
- **Codex** (`bootdir_codex.go`): `AGENTS.md` (codex's cwd-auto-loaded system-prompt slot,
  `:15`), `.mcp.json` (`:20`). `codexInjectionSpec` builds `agentlaunch.InjectionSpec`
  (`:63`). `BootMode()` returns `""` (`:155`).
- **OpenCode** (`bootdir_opencode.go`): `agents/<agentSlug>.md` (role-aware prompt, `:16`),
  `opencode.json` (agent→prompt-file descriptor, `:18`), `.mcp.json` (`:22`). **Notably
  different**: `SpawnWorkdir` returns the *project* dir, not the boot dir (`:152`), with
  `AmendEnv` (`:142`) setting `OPENCODE_CONFIG_DIR` to point back at the boot dir.

`go-agent-wrapper`'s `plant.Planter` (`plant/plant.go`): `Planter { Plant(ctx, bootDir
string, spec Spec) (Result, error) }`, `Spec{Files, MCPConfig, ProviderSettings, Hooks,
RecoveryPrompt}`, `Result{PlantedFiles []string}`. Its own package doc states the real
planting machinery lives in `agentkit/agentsessions` (bootdir), `agentkit/agentlaunch/
providerplant`, and `agentkit/agentruntime/bootdir` — `plant.Planter` is only the wrapper-
facing contract go-agent-wrapper composes.

**The real nuance a straight "delete Layout, use Planter" read misses**: `Planter`'s contract
is file-planting only — it has no concept of workdir selection or boot-mode signaling.
`Layout`'s `SpawnWorkdir`/`BootMode` are a real superset, and OpenCode's own workdir behavior
(returning the *project* dir, not the boot dir, plus an env var bridging the two) is
lifecycle-adjacent, tied to how Nanite decides where a process actually runs — not file-
planting. Per both docs' boundary ("a host does not own... session/task/team semantics" /
"lifecycle policy"), `SpawnWorkdir` and `BootMode` are Nanite-owned and should NOT be forced
into `Planter`'s contract. Only the actual file-writing portions of `Setup`/`Populate`/
`RegenerateSystemPromptSlot`/the file-planting half of `AmendEnv` move onto `Planter`.

## What to do

1. For each provider (claude/codex/opencode), implement `plant.Planter` by wrapping the
   existing per-provider file-planting logic currently inside `Populate`/
   `RegenerateSystemPromptSlot` — build a `plant.Spec{Files, MCPConfig, ProviderSettings,
   Hooks, RecoveryPrompt}` from what each provider currently writes (per the file lists
   above), and call `Plant(ctx, bootDir, spec)` in place of the direct file-write logic.
2. Keep `Layout.SpawnWorkdir`/`Layout.BootMode` as Nanite-owned methods, unchanged in
   behavior — they are not part of this migration. Document explicitly in your Work Log why
   (cite this task's Context) so a reviewer doesn't flag their absence from `Planter` as an
   incomplete migration.
3. `Layout.Setup` currently both creates the boot directory AND populates it — separate these
   concerns if they aren't already: directory creation stays Nanite-owned (it's tied to
   `Options`/session-scoped paths, a lifecycle concern), the populate step routes through the
   new `Planter`-based implementation from step 1.
4. `BootPrompt` (building the actual prompt string from `store.AgentProfile`+`Options`) stays
   entirely Nanite-owned — it's product content (agent roles/skills), explicitly named in
   both docs as something a host does not own.
5. Confirm `HasBootdirLayout` and `LayoutFor`'s dispatch behavior is unchanged for external
   callers (`internal/recovery/broker`'s `agent.HasBootdirLayout(ev.Provider)` call at
   `broker.go:342` — do not change this function's signature or behavior).

## Done means

- Claude/Codex/OpenCode all plant their files through `plant.Planter` rather than direct
  file-write calls, with identical resulting file content/layout to before this migration
  (verify via a real boot-dir diff, not just "it compiles").
- `SpawnWorkdir`/`BootMode`/`BootPrompt` remain Nanite-owned, unchanged in behavior, with an
  explicit Work Log note explaining why they weren't migrated (per this task's Context).
- `HasBootdirLayout`/`LayoutFor` unchanged for `internal/recovery/broker`'s call site.
- Existing bootdir tests (`bootdir_claude_test.go`, `bootdir_codex_test.go`,
  `bootdir_opencode_test.go`, `bootdir_plant_test.go`) pass with the migrated implementation,
  or are updated to assert against the new code path with equivalent coverage.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` clean.
