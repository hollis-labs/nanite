# CW-20260515-0024 Handoff — Nanite boot-profile compiler port to go-agent-launch / go-agent-context

**Sprint:** SP-20260514-0008 Phase 6
**Branch:** `feat/cw-20260515-0034-phase6-nanite-adoption`
**Status:** complete — `go build ./...`, `go vet ./...`, `go test ./internal/bootprofile/...` all green.

---

## 1. What was replaced/wrapped vs kept

The task framing ("port the local compiler to shared primitives") and the
hard acceptance criteria ("no regression" on the Nanite YAML schema,
dropdown behavior, deferred requirements, provider alias normalization, and
the compiled LaunchSpec output shape) pull in opposite directions if read
naively. The shared `agentlaunch` package is NOT a drop-in replacement for
Nanite's `bootprofile` — it is a *declarative launch pipeline*
(`LaunchPlan → launcher.Compile → launcher.Prepare → providerplant.Plant`)
with its own slot taxonomy, renderer, and budget model. Nanite's
`bootprofile` is a *catalog loader + slot compiler* producing a flat,
already-pinned `LaunchSpec`.

Per the task rule "only mechanical compile/assembly moves to shared
primitives; prefer a thin adapter over reshaping app semantics", the port
keeps Nanite's schema/registry/encoding intact and moves the **mechanical
slot file/inline IO** onto the shared `go-agent-context` resolvers, plus adds
a **one-directional bridge** to the shared `go-agent-launch` types so the
downstream Phase-6 workstreams have a convergence point.

### Kept (Nanite UX / business logic — unchanged)

| File | Why kept |
|---|---|
| `internal/bootprofile/profile.go` | Nanite YAML schema (`Profile`, `Launch`, `Identity`, `SlotSource`, `Catalog`, `Vars`). Acceptance criteria pin this verbatim. |
| `internal/bootprofile/compiler.go` | `LaunchSpec`, `Requirement`, `Compile`, `CompileFromCatalog`, `renderDefaultPrompt`. The `LaunchSpec` output shape is pinned. |
| `internal/bootprofile/loader.go` | `LoadCatalog` / `LoadProfile` / `LoadLaunch` — catalog directory scan. Pure Nanite catalog layout. |
| `internal/bootprofile/registry.go` | `Registry`, `EncodeProviderID`/`DecodeProviderID`, dropdown encoding (`bootprofile:` prefix), `CompileFor`. Dropdown behavior is pinned. |
| `internal/bootprofile/requirements.go` | `ResolveRequirements` deferred-requirement model. Pinned. |

### Wrapped (mechanical IO now delegates to shared resolvers)

| File | Change |
|---|---|
| `internal/bootprofile/slots.go` | `resolveSlot` `text` branch now routes through the shared `agentcontext` inline resolver; `resolveStatic` single-file branch routes through the shared `agentcontext` static_file resolver. Var-substitution (`Substitute`), the empty-catalog-root guard (`resolvePath`), the `### filename` directory concat, and Requirement lifting stay Nanite-side. `resolveStatic` gained a `slotName string` first param. |

### Added (new files)

| File | Purpose |
|---|---|
| `internal/bootprofile/agentcontext_adapter.go` | Bridge to `go-agent-context` slot resolvers. `resolveInlineViaShared`, `resolveStaticFileViaShared`, package-level `staticFileResolver` / `inlineResolver` singletons. |
| `internal/bootprofile/launchplan_bridge.go` | One-directional `LaunchSpec → agentlaunch.*` projection. `ToBootProfileInline`, `ToProviderSpec`, `ToMCPSpec`, `ToLaunchPlan` (+ `LaunchPlanOptions`). |
| `internal/bootprofile/launchplan_bridge_test.go` | Tests for the bridge. |

---

## 2. Exact shared API surface now used

### From `github.com/hollis-labs/go-agent-context/agentcontext` (v0.1.0)
- `agentcontext.SlotSpec`, `agentcontext.SlotSource`, `agentcontext.SlotSourceKind`
- `agentcontext.SlotSourceKindInline` + `agentcontext.InlineSource`
- `agentcontext.SlotSourceKindStaticFile` + `agentcontext.StaticFileSource`
- `agentcontext.ResolverEnv` (`.Workdir` ← catalog root)
- `agentcontext.Resolver` (interface; `.Resolve(ctx, spec, env)`)

### From `github.com/hollis-labs/go-agent-context/agentcontext/resolvers` (v0.1.0)
- `resolvers.NewStaticFileResolver()` — single static file read
- `resolvers.NewInlineResolver()` — verbatim inline content

> NOTE: the shared `static_dir` resolver (`resolvers.NewStaticDirResolver`)
> is intentionally NOT used. It emits a plain `"\n\n"`-joined body; Nanite's
> directory-glob slot format is `### <filename>\n\n<body>` joined by
> `\n\n---\n\n` and is pinned by `TestResolveSlot_StaticDirGlob` + downstream
> prompt layout. Directory globbing therefore stays in Nanite's `resolveStatic`.

### From `github.com/hollis-labs/go-agent-launch/agentlaunch` (v0.1.0)
- `agentlaunch.LaunchPlan` (+ `.Validate()`)
- `agentlaunch.BootProfileRef`, `agentlaunch.BootProfileInline`
- `agentlaunch.ProviderSpec`, `agentlaunch.ProjectSpec`, `agentlaunch.AgentSpec`
- `agentlaunch.WorkspaceSpec`, `agentlaunch.WorkspaceMode` + constants
- `agentlaunch.RuntimeKind` + constants, `agentlaunch.LaunchMode` + constants
- `agentlaunch.MCPSpec`
- `agentlaunch.BootModeNone / BootModeStdin / BootModePlanted`
- sentinel `agentlaunch.ErrMissingProviderID` (used in a bridge test)

`launcher.Compile` / `launcher.Prepare` / `providerplant.Plant` are NOT
called from Nanite yet — see §6 (that is CW-0027's job).

---

## 3. Compiled LaunchSpec / CompiledLaunch shape + convergence point

**Nanite's canonical compiled output is still `bootprofile.LaunchSpec`**
(`internal/bootprofile/compiler.go`). Its shape is unchanged — JSON tags,
non-nil slice/map guarantees, `Requirements` list, `Identity` block, all
preserved. Existing consumers, all still passing:

- `internal/api/providers.go` — dropdown rows (`bootProfileProviderRow`, etc.)
- `internal/service/chat_bootprofile_resolve.go` — `resolveBootProfile`,
  `CompileFor`, `cliRoutableProvider`
- `internal/service/chat_bootprofile_recovery.go` — crash-recovery resolve
- `internal/service/chat_boot_drive.go` — **`applyLaunchSpecToBootOpts`**
  (line ~461): overlays `LaunchSpec` onto `runtimeagent.Options` before
  `runtimeagent.Boot`. **This is the existing runtime convergence point.**

**New shared-vocabulary convergence point: `LaunchSpec.ToLaunchPlan()`**
(`internal/bootprofile/launchplan_bridge.go`). It takes a compiled
`LaunchSpec` + a caller-supplied `LaunchPlanOptions` (project id, agent id,
`RuntimeKind`, `WorkspaceMode`, `LaunchMode`) and returns an
`agentlaunch.LaunchPlan` with the boot profile carried inline
(`BootProfileRef.Inline`). The plan is NOT pre-validated — callers run
`plan.Validate()`.

CW-0025 and CW-0027 should both build on `ToLaunchPlan` / `ToBootProfileInline`
/ `ToProviderSpec` rather than re-deriving shared types from `LaunchSpec`.

---

## 4. go.mod changes

```
require (
    github.com/hollis-labs/go-agent-context v0.1.0   // NEW — direct
    github.com/hollis-labs/go-agent-launch  v0.1.0   // NEW — direct
)
github.com/hollis-labs/go-agent-sessions v0.9.2 → v0.9.4   // BUMPED
```

- `go-agent-sessions` bumped to **v0.9.4** (transitive requirement of
  `go-agent-context v0.1.0`). The bump is compatible — full `go build ./...`,
  `go vet ./...`, and all touched packages' tests pass. NOT reverted.
- No `replace` directives were added for the two new modules — both tagged
  releases (`v0.1.0`) download and build cleanly from the module cache.
- The pre-existing `replace github.com/hollis-labs/go-modelsdev => ../../libs/go-modelsdev`
  was left untouched (see §5 — environment note).

---

## 5. Shared-package gaps + environment notes

### No shared-package gaps — nothing had to be patched locally.
Both `go-agent-launch v0.1.0` and `go-agent-context v0.1.0` had everything
needed. No local checkout edits, no `replace` directives for the shared
packages. **No new tagged release is required.**

### One deliberate non-use (NOT a gap, a semantics mismatch)
`resolvers.StaticDirResolver` concatenates directory files with a bare
`"\n\n"` separator. Nanite's directory-glob slot format
(`### <filename>` headings + `---` separators) is app-specific and pinned by
tests. Directory globbing therefore stayed in Nanite's `resolveStatic`. If a
future shared release wants to host this, it would need a configurable
per-file-header option on `StaticDirResolver` — but that is a Nanite
*presentation* concern and arguably should stay app-side. Flagging, not
requesting.

### Environment note (NOT committed — local-only)
This execution worktree lives at
`/Users/chrispian/agent-mux/workspaces/nanite/.../repo`. The repo's
`replace ... go-modelsdev => ../../libs/go-modelsdev` resolves to
`/Users/chrispian/agent-mux/workspaces/nanite/libs/go-modelsdev`, which does
not exist in this worktree layout (it exists in the canonical checkout). To
get a working `go build ./...` for verification, a **symlink** was created:
`/Users/chrispian/agent-mux/workspaces/nanite/libs/go-modelsdev →
/Users/chrispian/dev/hollis-labs/libs/go-modelsdev`. This is a local
environment fix only — `go.mod` was NOT modified for it, and the symlink is
outside the repo so it is not part of the commit. Canonical checkouts where
`libs/` is a sibling of the repo are unaffected.

---

## 6. Notes for later workstreams

### CW-0025 (provider bootdir)
- Use `LaunchSpec.ToBootProfileInline()` for the boot body + boot mode, and
  `LaunchSpec.ToProviderSpec()` for the provider id/env/flags.
- `sharedBootMode` maps Nanite's `file`/`inline` → `BootModePlanted`,
  `stdin` → `BootModeStdin`, empty/unknown → `BootModeNone`. If CW-0025 needs
  a finer mapping, change `sharedBootMode` in `launchplan_bridge.go`.
- The shared compiler's `bootDirIntentFor` (in `launcher/compile.go`) maps
  claude → `agentrc.yaml` + `.mcp.json`, codex → `config.toml` + `.mcp.json`,
  opencode → `OPENCODE.md` + `.mcp.json`. Nanite's existing bootdir layout
  must be reconciled against that — out of scope for 0024.

### CW-0026 (context / skills providers)
- Nanite's deferred slot types (`cmd`, `http`, `role_summary`, `skill_index`)
  still surface as `bootprofile.Requirement` entries and
  `ResolveRequirements` still errors on them (`ErrRequirementUnsupported`).
  The shared `agentcontext` resolvers package already ships `cmd`,
  `http_text`, `http_json`, `role_summary` resolvers and an opt-in
  `skill_index` resolver (`resolvers.WithSkillIndex`). CW-0026 should wire
  those: build an `agentcontext.ContextRequest` from the `Requirement` list,
  call `agentcontext.DefaultProvider.Assemble`, and fold the resolved slot
  bodies back into `LaunchSpec.Slots` + re-render `BootPrompt`. The
  `agentcontext_adapter.go` file is the natural home for that expansion —
  it already imports the resolvers package.
- Nanite's `SlotSource` → `agentcontext.SlotSource` kind mapping is
  straightforward: `text`→`inline`, `static`(file)→`static_file`,
  `static`(dir)→`static_dir`, `cmd`→`cmd`, `http`→`http_text`/`http_json`,
  `role_summary`→`role_summary`, `skill_index`→`skill_index`.

### CW-0027 (standalone launcher)
- `LaunchSpec.ToLaunchPlan(LaunchPlanOptions{...})` is the entry point. Pass
  the lifecycle knobs Nanite's catalog does not express (`RuntimeKind`,
  `WorkspaceMode`, `LaunchMode`, project/agent ids). Then run
  `launcher.Compile` → `launcher.Prepare` → `providerplant.Plant`.
- A prompt-only profile (compiled with `launch == nil`) yields a
  `LaunchPlan` whose `Provider.ID` is empty → `Validate()` returns
  `agentlaunch.ErrMissingProviderID`. Filter prompt-only profiles before
  building a plan — same "not bootable" rule `LaunchSpec` already documents
  for the dropdown.
- The bridge is one-directional. Nanite still owns catalog load + slot
  compile + dropdown encoding; `agentlaunch.LaunchPlan` is the handoff
  format, not a replacement source of truth.
