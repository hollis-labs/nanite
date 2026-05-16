# CW-20260515-0025 Handoff — Nanite provider bootdirs onto shared BootDirSpec planting

**Sprint:** SP-20260514-0008 Phase 6
**Branch:** `feat/cw-20260515-0034-phase6-nanite-adoption`
**Status:** complete — `go build ./...`, `go vet ./...` green; `go test ./internal/runtime/agent/... ./internal/service/... ./internal/bootprofile/...` all pass. One unrelated pre-existing failure (`internal/chat` `TestEnvelopeRegistrySync`) — see §6.

---

## 1. RECON — where Nanite builds provider bootdirs

All provider bootdir code lives in **`internal/runtime/agent/`**, behind the
`Layout` interface (`bootdir.go`). There is NO `bootdir`/`providerboot`
sub-package. The dispatch table `bootdirLayoutFor(provider)` returns one of
three concrete `Layout` impls (`claudeLayout`, `codexLayout`,
`opencodeLayout`) or an `unsupportedLayout` stub.

`Layout` has **three** plant entry points (this matters — see §3):

| Method | Caller | Purpose |
|---|---|---|
| `Setup` | `agent.Boot` (`agent.go:336`) | roll the `$TMPDIR` bootdir + plant everything |
| `Populate` | recovery `BootDirOps` (`internal/service/agent_bootdir_adapter.go:122`) | re-plant the full file-set into an existing dir (crash recovery) |
| `RegenerateSystemPromptSlot` | recovery `BootDirOps` (`agent_bootdir_adapter.go:155`) | rewrite only the system-prompt file (watchdog_kill remediation) |

### Per-provider bootdir layout (pre-change, all unchanged in content)

- **claude** — `CLAUDE.md`, `boot.md`, `.sandbox/{agent-context,envelope-schema}.md`, `.claude/settings.json`, `.mcp.json`; cwd = bootDir; `--add-dir`.
- **codex** — `AGENTS.md`, `boot.md`, `.sandbox/*`, `.mcp.json`; cwd = bootDir; `--cd`.
- **opencode** — `agents/<slug>.md`, `agents.json`, `opencode.json`, `boot.md`, `.sandbox/*`, `.mcp.json`; cwd = projectDir; `OPENCODE_CONFIG_DIR` env.

---

## 2. Provider bootdir code: replaced vs kept

### Replaced — mechanical assembly now rides shared `agentlaunch` primitives

| File | Change |
|---|---|
| `internal/runtime/agent/bootdir_plant.go` | **NEW.** The convergence point. `plantInjectionSpec` writes an `agentlaunch.InjectionSpec` (NativeFiles + BootDirOverlay) into a bootdir using the shared `agentlaunch.ValidateBootDirRelPath` path-safety gate and the providerplant overwrite ordering (native files, then overlay-wins-last). Helpers: `nativeFileRaw`, `sandboxNativeFiles`, `bootMDNativeFile`, `mcpOverlay`. |
| `bootdir_claude.go` | `Populate` / `RegenerateSystemPromptSlot` now build a `claudeInjectionSpec` and route through `plantInjectionSpec`. Hand-coded `os.MkdirAll`+`AtomicWriteFile` loop removed. |
| `bootdir_codex.go` | Same: `codexInjectionSpec` + `plantInjectionSpec`. `codexAgentsMD` extracted (still uses `provider.AgentsMD`). |
| `bootdir_opencode.go` | Same: `opencodeInjectionSpec` + `plantInjectionSpec`. `agents.json`/`opencode.json` JSON marshalling kept (app-specific) but output now rides the spec. |
| `bootdir_common.go` | `plantSandboxFiles` + `plantBootMD` **removed** (dead — folded into `sandboxNativeFiles` / `bootMDNativeFile`). `makeBootDir` kept (forensic naming, §5). |
| `sandbox_content_mcp.go` | `writeMCPJSON` split: new pure `renderMCPJSON` returns the JSON body; the directory-writing `writeMCPJSON` wrapper **removed** (unused after refactor). |

### Kept Nanite-side — app business logic, NOT pushed into shared packages

- **Content renderers** — `BuildCLAUDEMD`, `BuildAgentContext`, `envelopeSchemaContent` (`sandbox_content_*.go`), `codexAgentsMD`, `opencodeAgentMD`, `agents.json`/`opencode.json` marshalling, `renderMCPJSON`. These are Nanite product surface (envelope schema, agent identity doc, subprocess-spawn MCP shape) and differ materially from go-providers' own `BootDirSpec` renderers — swapping to those would regress the "functionally equivalent" bar.
- **`Layout` interface + dispatch** (`bootdir.go`), `makeBootDir` forensic naming, `AmendEnv`/`SpawnWorkdir`/`BootPrompt`/`BootMode`, the recovery adapter (`agent_bootdir_adapter.go`) — all unchanged.

No file content changed: the same bytes land at the same paths. Existing
`bootdir_*_test.go` (claude/codex/opencode/alias) pass unmodified.

---

## 3. Shared mechanism chosen — and why we diverged from Phase 5

**Phase 5 (Torque) used `providerplant.Plant`. CW-0025 did NOT. Deliberate divergence.**

`providerplant.Plant` operates on a fully Compiled + Prepared
`agentlaunch.LaunchPlan`: it resolves a go-providers adapter, renders that
adapter's `BootDirSpec`, and rewires a `PreparedLaunch`'s Env/Argv/Workdir.
Torque adopted it because Torque launches already flow through
`LaunchPlan → launcher.Compile → launcher.Prepare`.

Nanite's **runtime** boot path (`agent.Boot`, the `Layout` interface) does
not. Three blocking mismatches:

1. **No `LaunchPlan` in scope.** `agent.Boot` plants off a
   `store.AgentProfile`. The CW-0024 bridge (`LaunchSpec.ToLaunchPlan`)
   exists only on the dropdown-driven boot-profile path, not the runtime.
   Synthesizing a `LaunchPlan` per boot purely to satisfy `Plant` would
   reshape the runtime — the task rule forbids that ("only mechanical
   assembly moves, not app semantics").
2. **`Plant` is one-shot.** The `Layout` contract needs slot-only
   re-planting (`RegenerateSystemPromptSlot`) and full re-plant against an
   *existing* dir (`Populate`) for crash recovery. `Plant` also appends
   argv on each call — not idempotent in mutation.
3. **Content regression.** go-providers' `BootDirSpec` renderers
   (`renderClaudeMD`, `renderMCPJSON`, codex `config.toml`/`auth.json`)
   produce different content from Nanite's planted files. Using them
   verbatim would break the "functionally equivalent" acceptance bar.

**What CW-0025 adopted instead:** the genuinely mechanical, app-agnostic
part — the *file-set model* and the *path-safe write loop*. Each `Layout`
declares its full bootdir as an `agentlaunch.InjectionSpec`; `plantInjectionSpec`
writes it using the same shared primitives `providerplant` uses internally:

- `agentlaunch.InjectionSpec` / `agentlaunch.NativeFile` — the shared
  vocabulary for bootdir extra files.
- `agentlaunch.ValidateBootDirRelPath` — the shared bootdir path-safety
  gate (rejects `..`, absolute paths, reserved `.git/`/`.ssh/` etc.).
- The documented planting order: native files first, then `BootDirOverlay`
  in sorted key order (overlay-wins-last), mirroring `providerplant.Plant`.

This satisfies the task's explicit instruction to represent app extras via
`InjectionSpec.NativeFiles` / `BootDirOverlay` "so they ride the shared
planting path", without reshaping the runtime.

`go-agent-sessions` `AutoPlantBootDir` is **not** used — see §4.

---

## 4. How Nanite app-extra files are planted; double-planting / leaks

- **Sandbox files** (`.sandbox/agent-context.md`, `.sandbox/envelope-schema.md`)
  — `sandboxNativeFiles()` returns them as `agentlaunch.NativeFile` (raw
  kind). Planted by every layout's `InjectionSpec`.
- **Envelope schema** — content (`envelopeSchemaContent`) stays a Nanite
  constant; only the *planting* is shared.
- **agent-context** — `BuildAgentContext` stays Nanite-side; planted as a
  native file.
- **`.mcp.json`** — modelled as a `BootDirOverlay` entry (`mcpOverlay()`)
  so it plants last (overlay-wins-last). Subprocess-spawn descriptor shape
  is Nanite-specific and kept.
- **`boot.md`, `CLAUDE.md`, `.claude/settings.json`, `AGENTS.md`,
  `agents/*.md`, `agents.json`, `opencode.json`** — all native files.

**Double-planting:** none. Nanite never calls `providerplant.Plant` nor
`AutoPlantBootDir`; the only planter is `plantInjectionSpec`, called once
per `Setup`/`Populate` (and a single-file spec for
`RegenerateSystemPromptSlot`).

**Leak guard (Phase 5's pre-Start leak concern):** unchanged and still
intact. `Layout.Setup` `os.RemoveAll`s the bootdir on any post-mkdir
failure; `agent.Boot`'s deferred `cleanup` removes it on any later failure
up to and including `SessionsManager.Start`. Cleanup/lifecycle is fully
**Nanite-app-owned** — same posture Phase 5 chose for Torque, just without
borrowing `providerplant`.

---

## 5. Shared-package gaps

**None.** No local edits to `go-agent-launch`, no `replace` directive. The
`agentlaunch` types used (`InjectionSpec`, `NativeFile`, `NativeFileRaw`,
`ValidateBootDirRelPath`) are all in the tagged `v0.1.0` already in
`go.mod`. `go.mod` is unchanged by this ticket.

One **deliberate non-use** (not a gap): `go-agent-launch`'s
`launcher.allocateBootDir` keys the bootdir name on a plan hash. Nanite's
`makeBootDir` keeps its `nanite-boot-<provider>-<sessID>-r<runID>-XXXXXX`
forensic scheme — operators correlate `$TMPDIR` entries to sessions by
this prefix, and `bootdir_*_test.go` pins it. Kept Nanite-side.

Environment note (same as CW-0024, not committed): this worktree needs the
`libs/go-modelsdev` symlink for `go build` to resolve the repo's existing
`replace` directive. Unrelated to this ticket.

---

## 6. Verification

```
go build ./...   → ok
go vet ./...      → ok
go test ./internal/runtime/agent/...      → ok (incl. new bootdir_plant_test.go)
go test ./internal/runtime/agent/recovery → ok
go test ./internal/service/...            → ok
go test ./internal/bootprofile/...        → ok
```

New tests: `bootdir_plant_test.go` covers `plantInjectionSpec` (native +
overlay + overlay-wins-last + intermediate dirs), the path-safety
rejection, the non-raw-kind rejection, claude app-extras riding the shared
path, and MCP-disabled-when-no-DBPath.

**Pre-existing unrelated failure:** `internal/chat` `TestEnvelopeRegistrySync`
fails reading a sibling `go-envelopes/manifest/envelopes.yaml` checkout
absent in this worktree layout. Confirmed identical failure on the base
commit (`git stash` + re-run) — NOT caused by this change. Same class of
worktree-layout issue as the CW-0024 `go-modelsdev` symlink note.

---

## 7. Notes for later workstreams

### CW-0027 (standalone launcher)
- The standalone launcher path SHOULD use `providerplant.Plant` /
  `PrepareAndPlant` — it builds a real `LaunchPlan` via
  `LaunchSpec.ToLaunchPlan` (CW-0024 bridge), so the Phase-5 mechanism
  fits there. CW-0025's `plantInjectionSpec` is the *runtime* (`agent.Boot`)
  planter; the two coexist — runtime boots off `store.AgentProfile`,
  standalone launches off a compiled plan.
- If CW-0027 wants the standalone launcher to plant Nanite's `.sandbox/*`
  / `.mcp.json` extras, build them as `agentlaunch.NativeFile` /
  `BootDirOverlay` entries on the `LaunchPlan.Injection` and let
  `providerplant.Plant` write them — the `sandboxNativeFiles` /
  `mcpOverlay` helpers in `bootdir_plant.go` are reusable for that (they
  take a `SetupParams` today; a small adapter from plan fields would do).
- Note the content divergence: `providerplant.Plant` renders go-providers'
  `CLAUDE.md`/`.mcp.json`, NOT Nanite's. If the standalone launcher must
  match runtime bootdir content byte-for-byte, it needs the same
  Nanite-renderer overlay treatment, not the stock `DefaultResolver`.

### CW-0028 (smoke)
- Bootdir contents are byte-identical to pre-CW-0025: same files, same
  paths, same content. Smoke assertions written against the old layout
  still hold.
- The recovery paths (`Repopulate`, `RegenerateCLAUDEMD`) are exercised by
  `internal/runtime/agent/recovery` tests and pass — but a smoke that
  triggers a real watchdog_kill remediation would confirm
  `RegenerateSystemPromptSlot` end-to-end through the new single-file
  `plantInjectionSpec` path.
- No env/argv/cwd behavior changed: `AmendEnv`, `SpawnWorkdir`, `BootMode`
  are untouched.

### General
- `bootdir_plant.go` is the home for any future "more of the bootdir
  rides shared machinery" work. If a later ticket moves Nanite onto a
  full `LaunchPlan` at the runtime layer, `plantInjectionSpec` can be
  retired in favour of `providerplant.Plant` — but that is a runtime
  reshape, explicitly out of scope here.
