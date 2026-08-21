# Migrate bootdir Layout implementations onto go-agent-wrapper's Planter

**Phase:** 2 — Nanite host migration (`TASKS/agent-host-acp`)
**Status:** implemented
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

## Work Log

**What was actually there before touching anything.** `bootdir_plant.go` already
converged the three providers' file-planting onto a *different* shared convergence
point than `plant.Planter` — `agentkit/agentlaunch`'s `InjectionSpec`/`NativeFile`
vocabulary plus a Nanite-local `plantInjectionSpec` writer (CW-20260515-0025). That
mechanism already did the path-safety validation (`agentlaunch.ValidateBootDirRelPath`)
and atomic-write (`fsutil.AtomicWriteFile`) work this task asked to converge onto
`plant.Planter` — this task's real job was swapping the *vocabulary* (InjectionSpec's
NativeFiles/BootDirOverlay → `plant.Spec`'s Files/MCPConfig/ProviderSettings/Hooks/
RecoveryPrompt) and the *call shape* (a bare Nanite func → a `plant.Planter.Plant(ctx,
bootDir, spec)` method call), not building path-safety/atomic-write machinery from
scratch. Confirmed `go-agent-wrapper`'s `plant` package ships no concrete `Planter`
implementation beyond `NoOpPlanter` (its own doc says "the wrapper's Planter
implementation composes [agentkit] packages" — apps write their own) — so Nanite
writing `claudePlanter`/`codexPlanter`/`opencodePlanter` is exactly what the contract
expects, not a workaround.

**Also confirmed, contradicting one clause of the task's stated plan**: item 3 says
"`Layout.Setup` currently both creates the boot directory AND populates it — separate
these concerns if they aren't already." Direct read of all three `Setup` bodies
(`bootdir_claude.go:68-80` pre-migration, and codex/opencode's equivalents) showed
this separation *already existed* — each `Setup` calls `makeBootDir` (mkdir only) then
`l.Populate(bootDir, params)` (write only), with `os.RemoveAll` cleanup on a `Populate`
failure. No `Setup`-body change was needed; the separation carries forward unchanged
into the migrated code. Noted per this project's "decision vs. rationale" rule — the
task's stated action (route Populate through Planter) is unaffected by this correction.

**What changed:**

- `internal/runtime/agent/bootdir_plant.go` — rewrote the convergence-point doc header
  to explain the `plant.Planter` migration (kept the still-valid "why NOT
  providerplant.Plant" reasoning, added "why plant.Planter is different and doesn't
  hit the same objections" + "what did NOT move and why"). Kept `writePlantedFile`
  (the shared path-safety-gate + atomic-write primitive) verbatim — it's reused
  unchanged. Kept `mcpOverlay` byte-for-byte unchanged (its signature/behavior is
  tested directly by `sandbox_content_mcp_test.go`, which this task does not touch)
  and added `mcpConfigBytes` as a thin adapter lifting its single `.mcp.json` entry
  into the `[]byte` shape `plant.Spec.MCPConfig` expects. Replaced
  `nativeFileRaw`/`plantInjectionSpec`/`sandboxNativeFiles`/`bootMDNativeFile`
  (InjectionSpec-shaped) with `plantConfig` (per-provider destination knowledge:
  which `Spec.ProviderSettings` key, which path, which file mode, plus a
  `fileModeOverrides` map for individual Files entries needing non-default
  permissions) and `plantSpec` (the shared `plant.Spec` writer every provider
  Planter delegates to) and `sandboxFiles` (Files-map equivalent of the old
  NativeFile-returning helper). `plantSpec` rejects a non-empty `Spec.Hooks` or
  `Spec.RecoveryPrompt` loudly (`fmt.Errorf`) rather than silently dropping them —
  nothing in this codebase populates either field today, so a non-empty value can
  only mean a future caller expected behavior this Planter doesn't implement yet;
  this mirrors the prior mechanism's "unsupported kind" guard for non-raw
  NativeFiles.
- `internal/runtime/agent/bootdir_claude.go` — added `claudePlanter` (implements
  `plant.Planter`; `var _ plant.Planter = claudePlanter{}` compile-time assertion)
  and `claudePlantSpec` (builds the `plant.Spec` from the same content-generation
  calls as before: `BuildCLAUDEMD`, `claudeProviderConfigContent`, `sandboxFiles`,
  `mcpConfigBytes`). `.claude/settings.json` content rides
  `Spec.ProviderSettings["claude"]`; CLAUDE.md/boot.md/.sandbox/* ride `Spec.Files`;
  `.mcp.json` rides `Spec.MCPConfig`. `Populate`/`RegenerateSystemPromptSlot` now
  build a `plant.Spec` and call `claudePlanter{}.Plant(context.Background(), ...)`
  in place of the old `plantInjectionSpec` call. `Setup`/`AmendEnv`/`SpawnWorkdir`/
  `BootPrompt`/`BootMode` bodies are byte-for-byte unchanged (only doc comments
  added explaining why the latter three are out of scope).
- `internal/runtime/agent/bootdir_codex.go` — added `codexPlanter` (config.toml at
  `Spec.ProviderSettings["codex"]`, mode `codexConfigFileMode` (0o600); auth.json
  rides `Spec.Files` with `fileModeOverrides["auth.json"] = codexConfigFileMode`
  since `plant.Spec.Files` carries no per-entry mode of its own — this is the one
  place a provider needs two separate 0o600 files, and `Spec.ProviderSettings` only
  has room for one entry per provider name, so auth.json rides Files with an
  explicit mode override rather than being force-fit into ProviderSettings) and
  `codexPlantSpec`. Same `Populate`/`RegenerateSystemPromptSlot` → `Planter.Plant`
  swap as claude; `Setup`/`AmendEnv`/`SpawnWorkdir`/`BootPrompt`/`BootMode` bodies
  unchanged.
- `internal/runtime/agent/bootdir_opencode.go` — added `opencodePlanter` and
  `opencodePlantSpec`. OpenCode has **no** `ProviderSettings` destination —
  `agents.json`/`opencode.json` are hand-rolled Nanite content (never sourced from
  go-providers' `BootDirSpec` the way claude/codex's provider-config files are), so
  they ride `Spec.Files` like every other planted file; `plantConfig.provider` is set
  to `"opencode"` but `providerSettingsPath` is deliberately left empty. Same
  `Populate`/`RegenerateSystemPromptSlot` swap; `Setup`/`AmendEnv`/`SpawnWorkdir`/
  `BootPrompt`/`BootMode` unchanged — `SpawnWorkdir` here is the provider where the
  task's "don't force this into Planter" instruction matters most concretely (it
  returns the *project* dir, not the boot dir; `plant.Spec` has no field that could
  represent that even if I wanted to force it in).
- `internal/runtime/agent/bootdir_provider_config.go` — one doc-comment fix
  (`renderProviderConfigFile`'s header referenced `plantInjectionSpec` by name;
  updated to reference the new `claudePlanter`/`codexPlanter` + this task). No
  behavioral change — `codexConfigTOMLContent`/`codexAuthJSONContent`/
  `claudeProviderConfigContent`/`renderProviderConfigFile` are untouched.
- `internal/runtime/agent/bootdir_common.go` — untouched. `makeBootDir`/`agentSlug`/
  `defaultIfEmpty` have no relationship to the InjectionSpec→Planter vocabulary
  swap.
- `internal/runtime/agent/bootdir.go` — untouched. Confirmed the `Layout` interface
  signature, `LayoutFor`, `HasBootdirLayout`, `composeBootdirParams`,
  `ResolveBootdirParams`, `bootdirLayoutFor`, `normalizeProviderName` are all
  byte-for-byte unchanged; `internal/recovery/broker/broker.go:342`'s
  `agent.HasBootdirLayout(ev.Provider)` call site needed no changes and
  `internal/recovery/broker`'s test suite passed unmodified.
- `internal/runtime/agent/bootdir_plant_test.go` — rewrote to test the new
  `plant.Spec`/`plantSpec`/`claudePlantSpec` plumbing directly (mirroring the old
  file's coverage: files+overlay writing → `TestPlantSpec_FilesMCPAndProviderSettings`;
  path-safety rejection → `TestPlantSpec_RejectsUnsafePath`; the claude-specific
  app-extras/MCP-shape check → `TestClaudePlantSpec_Shape`; the MCP-disabled-when-
  no-DBPath check → `TestMCPConfigBytes_DisabledWhenNoDBPath`). Added
  `TestPlantSpec_ProviderSettingsAndFileModeOverrides` (new: pins the
  `fileModeOverrides`/`providerSettingsMode` mechanism codex's auth.json/config.toml
  need, which had no direct-unit-test analogue before — it was only exercised
  indirectly through `TestCodexLayout_ConfigTOML_ApprovalPolicy`'s file-mode
  assertion, which still passes unmodified) and `TestPlantSpec_RejectsHooks`/
  `TestPlantSpec_RejectsRecoveryPrompt` (new: pin the loud-reject guard for the two
  `plant.Spec` fields Nanite doesn't have a plant target for yet).
  **Coverage intentionally NOT carried forward**:
  `TestPlantInjectionSpec_RejectsNonRawNativeFile` — it asserted rejection of a
  `NativeFileSkill`-kind `agentlaunch.NativeFile`, a concept that doesn't exist in
  `plant.Spec` (`Spec.Files` is homogeneous `map[string][]byte`, no "kind" tag), so
  there is no equivalent state to guard against.
  `bootdir_claude_test.go`/`bootdir_codex_test.go`/`bootdir_opencode_test.go` are
  **unmodified** — they exercise the public `Layout.Setup` → real-file-on-disk path
  and continued to pass without any changes, which is itself part of the
  file-content-identity verification (see below).

**Why `SpawnWorkdir`/`BootMode`/`BootPrompt` stayed Nanite-owned (per the task's own
Context, restated here so a reviewer doesn't read their absence from `Planter` as
incomplete work):** `plant.Planter`'s contract (`plant/plant.go` in
`go-agent-wrapper`) is exactly one method — `Plant(ctx, bootDir, Spec) (Result,
error)` — with `Spec{Files, MCPConfig, ProviderSettings, Hooks, RecoveryPrompt}` and
`Result{PlantedFiles}`. There is no field or return value that could carry "which
directory should the process actually spawn in" (`SpawnWorkdir` — genuinely
provider-divergent: claude/codex return the boot dir, opencode returns the *project*
dir with an env-var bridge back to the boot dir) or "how should the boot prompt be
delivered to the process" (`BootMode` — `"stdin"` for PTY claude, `""` for
subprocess-per-turn codex/opencode). Forcing either into `Planter` would mean
inventing Spec fields the upstream contract was never designed to carry, and both are
lifecycle/process-topology decisions — squarely "lifecycle policy (when to fire the
first turn, when to stop...)" and lifecycle-adjacent process-spawn mechanics per
`docs/engineering/architecture/16-agent-host.md`'s boundary, which the doc itself
names as Nanite-owned, not host-owned. `BootPrompt` returns the actual system-prompt
*string* — product content (agent roles/skills via `resolveBootPrompt` →
`composeSystemPrompt`), explicitly named in the same doc as something "a host does
not own." Only the *file* that carries that string (CLAUDE.md/AGENTS.md/
agents/<slug>.md) moved onto `Planter`; the string-composition logic itself did not
move and was not touched.

**Real boot-dir diff verification (not just "it compiles").** Used `git worktree add`
to check out the pre-migration commit (`7e78991b`, task 03's HEAD) into an isolated
scratch directory outside this worktree (`/private/tmp/.../scratchpad/pre-migration`)
— not a relative path against any real tracked `.nanite/agents/*.md` file, per this
task's process notes. Patched only that scratch copy's `go.mod` `replace` directives
to absolute paths (the relative `../../libs/...` paths are relative to the checkout's
own location, which doesn't hold at the scratch depth) — a throwaway, uncommitted
edit local to the scratch worktree, never touching the real repo. Added an identical
throwaway test (`zz_diff_dump_test.go`, not committed, removed from both trees after
use) to both the scratch pre-migration tree and this working tree that calls
`claudeLayout{}.Setup`/`codexLayout{}.Setup`/`opencodeLayout{}.Setup` with fixed,
identical `SetupParams` (agent profile, session/run IDs, MCP config, CLIWritableRoots)
and copies the resulting boot dir's full file tree (content + permission bits) to a
named scratch output directory. Ran it against both trees and compared:

- `diff -rq` between the two dumped trees: **zero differences** — every file
  (`CLAUDE.md`, `.claude/settings.json`, `.mcp.json`, `.sandbox/agent-context.md`,
  `.sandbox/envelope-schema.md`, `boot.md` for claude; `AGENTS.md`, `config.toml`,
  `auth.json`, plus the shared files for codex; `agents/<slug>.md`, `agents.json`,
  `opencode.json`, plus the shared files for opencode) is byte-for-byte identical
  between pre- and post-migration.
- File tree shape (`find -type f`, sorted) identical between both dumps.
- File permission bits (`stat -f %Lp`) identical between both dumps, including
  codex's `config.toml`/`auth.json` at `0600` vs. everything else at `0644`.

Cleaned up afterward: removed `zz_diff_dump_test.go` from this working tree
(confirmed absent via `git status --short` — only the six intended `Touches` files
remain modified) and removed the scratch worktree via `git worktree remove --force`.

**Verification commands run (from this worktree):**
- `go build ./cmd/nanite/` — clean.
- `go build ./...` — clean.
- `go vet ./internal/runtime/agent/...` — clean. `go vet ./...` (whole repo) reports
  two pre-existing findings in `internal/service/container.go` (`stopReaper`/
  `stopRuntimeReaper` possible-context-leak) — confirmed via `git log --oneline -1 --
  internal/service/container.go` that this file's last change predates this task
  entirely (`9178c086`, the Teams batch) and is untouched by this task's diff; not
  introduced by this work.
- `go test ./...` (full repo, `-count=1`) — all packages `ok`, including
  `internal/runtime/agent` (all pre-existing bootdir tests plus the new/updated
  `bootdir_plant_test.go` cases) and `internal/recovery/broker` (confirms
  `HasBootdirLayout`/`LayoutFor` dispatch is unaffected for that call site).

**Docs/glossary check:** `docs/engineering/architecture/16-agent-host.md` read in
full before starting (its boundary language — "a host does not own... lifecycle
policy... product identity" — is what backs the SpawnWorkdir/BootMode/BootPrompt
exclusion above). `docs/engineering/GLOSSARY.md` checked — no existing entries for
`Layout`, `Planter`, `plant.Spec`, or the new type names (`claudePlanter`/
`codexPlanter`/`opencodePlanter`/`plantConfig`/`plantSpec`); none of these are
cross-cutting product vocabulary warranting a glossary entry (they're internal,
unexported implementation types scoped to `internal/runtime/agent`), so no glossary
change made.
