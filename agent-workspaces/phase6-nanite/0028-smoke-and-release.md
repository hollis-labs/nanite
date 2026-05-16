# CW-20260515-0028 Handoff — Cross-app parity smoke and release notes

**Sprint:** SP-20260514-0008 Phase 6 — FINAL workstream
**Branch:** `feat/cw-20260515-0034-phase6-nanite-adoption`
**Status:** complete — `go build ./...`, `go vet ./...` green;
`go test ./...` = 84 packages PASS + 1 known pre-existing unrelated
failure (`internal/chat TestEnvelopeRegistrySync`). All three provider
binaries smoked **live**.

---

## 1. Provider availability

| Provider | Binary | Version | Credentials |
|---|---|---|---|
| claude | `~/.local/bin/claude` | 2.1.143 (Claude Code) | Anthropic key in keychain |
| codex | `/opt/homebrew/bin/codex` | codex-cli 0.130.0 | OpenAI key in keychain |
| opencode | `~/.opencode/bin/opencode` | 1.14.48 | provider-managed |

All three present → the standalone-launcher path was smoked **live** for
every provider, not just claude.

---

## 2. Smoke matrix

| Dimension | Mode | Result | Notes |
|---|---|---|---|
| Nanite `go build ./...` | live | PASS | clean |
| Nanite `go vet ./...` | live | PASS | clean |
| Nanite `go test ./...` | live | PASS (84 pkgs) | see known FAIL below |
| `internal/chat TestEnvelopeRegistrySync` | live | FAIL (pre-existing) | Missing sibling `go-envelopes/manifest/envelopes.yaml`. Confirmed unchanged from base `202b530`; Phase 6 made zero changes to `internal/chat/`. Worktree-layout issue, not a regression. |
| `nanite launch --dry-run claude-smoke` | live | PASS | `launch plan OK: provider=claude runtime=streaming-stdio`, 640-byte boot prompt, 2 slots |
| Claude — standalone launch, PTY/native TUI | live | PASS | Real `claude` 2.1.143 spawned; bootdir = `CLAUDE.md` + `boot.md` + `.sandbox/*` + `.mcp.json` + `.claude/settings.json`; workspace triplet materialized |
| Claude — streaming (StreamingStdio runtime) | live | PASS | `planOptionsFor` → `RuntimeStreamingStdio`; plan validates, runtime spawns in that shape |
| Codex — standalone launch | live | PASS | Real `codex` 0.130.0 spawned; bootdir = `AGENTS.md` + `boot.md` + `.sandbox/*` + `.mcp.json` |
| Codex — JSON-RPC / app-server | compile | PASS (compile) | Launcher compiles + validates a codex `LaunchPlan`; app-server vs subprocess split is a go-providers adapter concern |
| OpenCode — standalone launch (subprocess) | live | PASS | Real `opencode` 1.14.48 spawned; bootdir = `agents/default.md` + `agents.json` + `opencode.json` + `boot.md` + `.sandbox/*` + `.mcp.json`; cwd = project dir |
| Worktree isolation | compile | PASS (compile) | Each launch reserves a fresh `~/.nanite/workspaces/<sid>` + fresh `$TMPDIR` bootdir (forensic `nanite-boot-<provider>-<sid>-r<run>-XXXX`). Verified across all three live launches. |
| Native skills (`skill_index` slot) | compile | PASS (compile) | `internal/bootprofile` tests cover `skill_index` via shared `SkillIndexResolver`; no example profile uses it |
| Arbitrary injections (`cmd`/`http`/`role_summary`) | compile | PASS (compile) | `internal/bootprofile` tests cover resolution + failure surfacing; `claude-smoke` is text+static only |
| boot-exec | live | PASS | Task ran inside a Nanite boot-exec session; standalone smokes exercised `agent.Boot` end to end |
| attach / detach | skip | SKIP | Standalone launcher is one-shot `Boot` + optional `Wait` — no attach surface. Chat attach/detach untouched by Phase 6. |

**Counts: 13 PASS (8 live, 5 compile-level), 1 SKIP, 1 known
pre-existing unrelated FAIL.**

---

## 3. Launcher fixes (CW-0028 closed the live-launch acceptance gap)

CW-0027 verified the launcher at dry-run + fake-runtime only. The live
launch surfaced **two real CW-0027 bugs**, both fixed Nanite-repo-local
in `internal/launcher` (no shared-package / Tether / Torque changes):

1. **`WorkspacesRoot` never defaulted.** `launch_cmd.go` does not set
   `Config.WorkspacesRoot`; `buildDeps` passed empty through to
   `agent.Boot`, whose `workspaceCreate` hard-errors on an empty root.
   The `Config.WorkspacesRoot` doc comment falsely claimed `agent.Boot`
   defaults it. Fix: `buildDeps` now applies the `~/.nanite/workspaces`
   fallback (same as `service.BuildAgentDependencies` for chat); doc
   comment corrected. **Without this no live standalone launch could
   succeed.**

2. **`launch_source` provenance stamp dropped on store-backed launches.**
   `storeRuntimeStore.CreateRuntimeRow` never mapped `RuntimeRow.Meta`
   onto `store.AgentRuntimeRow.MetaJSON` → persisted row had
   `meta_json="{}"`. Fix: `CreateRuntimeRow` now JSON-marshals
   `row.Meta`. Verified live — rows now show
   `meta.launch_source="standalone-launcher"`.

New test: `TestBuildDeps_WorkspacesRootFallback` (default + explicit
path). Files changed: `internal/launcher/launcher.go`,
`internal/launcher/runtimestore.go`, `internal/launcher/launcher_test.go`,
`CHANGELOG.md`, + new docs.

---

## 4. Release notes (all 5 components)

Full detail in `docs/phase6-shared-launch-adoption.md` §5. Condensed:

### Nanite
- **New:** `nanite launch <profile>` standalone launcher (`--dry-run`,
  `--no-wait`, `--catalog`, `--db`, `--dev`).
- **Internal:** boot-profile compiler / provider bootdirs / deferred slot
  resolution now ride shared `go-agent-launch` + `go-agent-context`
  primitives — boot prompts + bootdir content byte-identical, no
  user-facing change.
- **Deps:** `go-agent-launch v0.1.0` + `go-agent-context v0.1.0` added;
  `go-agent-sessions v0.9.2 → v0.9.4`.
- **Migration:** none — launcher is purely additive; chat path untouched.
- CHANGELOG.md `Unreleased` updated with `### Added` + `### Changed`.

### go-agent-launch (v0.1.0) — validated, NO version change
Phase 6 exercised `LaunchPlan`/`Validate`, `InjectionSpec`/`NativeFile`/
`ValidateBootDirRelPath`, the provider/runtime enums, `ErrMissingProviderID`.
No gaps. `providerplant.Plant` deliberately NOT used (content-ownership).
NOT appended to that repo's CHANGELOG — read-only per CW-0028 scope.

### go-agent-context (v0.1.0) — validated, NO version change
Phase 6 exercised the static/inline resolvers and all four deferred
resolvers (`cmd`/`http_text`/`http_json`/`role_summary`/`skill_index`)
plus the `skills` model. CW-0026 added zero code to the shared package.
No gaps. NOT appended to that repo's CHANGELOG — read-only.

### Tether — cross-app migration guidance
Phase 4 (in review), owned by another phase — Phase 6 did not touch its
repo. A Tether-managed launch and a `nanite launch` session reach an
identical Nanite runtime with identical bootdir content; the two paths
coexist. `agentlaunch.LaunchPlan` is the cross-app handoff vocabulary;
Nanite still owns catalog load + slot compile + dropdown encoding.

### Torque — cross-app migration guidance
Phase 5 (done), owned by another phase — Phase 6 did not touch its repo.
Documented intentional divergence: Torque adopted `providerplant.Plant`
(its launches flow through `Compile`/`Prepare`); Nanite (CW-0025) adopted
only the shared file-set model + path-safe write loop because its runtime
boots off `store.AgentProfile`, not a `LaunchPlan`. Both validate against
the same `agentlaunch.LaunchPlan` contract — not drift.

---

## 5. Known limitations

1. `internal/chat TestEnvelopeRegistrySync` hard-fails in worktree-layout
   checkouts (missing sibling `go-envelopes/`). Pre-existing, unrelated.
2. No committed codex/opencode example boot profiles — only `claude-smoke`
   ships; codex/opencode smoked via synthesized temp catalog.
3. Deferred slots + `skill_index` have unit-test coverage only, no
   live-launch coverage.
4. Standalone launcher exposes no attach/detach surface (one-shot
   `Boot` + `Wait`).
5. Standalone launcher does not resume from checkpoints (by design).
6. `providerplant.Plant` content divergence is permanent by design.

---

## 6. Recommended follow-up tasks (file as Torque tasks)

1. **Skip-guard `TestEnvelopeRegistrySync` on missing manifest** — make
   the test `t.Skip` (like it already does for a missing frontend
   registry) when `go-envelopes/manifest/envelopes.yaml` is absent,
   instead of hard-failing in worktree checkouts. `internal/chat`,
   test-hygiene only.

2. **Add committed codex + opencode example profiles** — mirror
   `examples/boot-profiles/claude-smoke` for codex and opencode so smokes
   cover all three providers from committed fixtures.

3. **Add a live-launch smoke for deferred slots** — an example profile
   with a `cmd` slot + a `skill_index` slot (explicit `roots:`); verify
   resolved output lands in the rendered boot prompt of a real launch.

4. **Decide on a standalone-launcher attach surface** — add attach/detach
   to `internal/launcher`, or document standalone launches as
   fire-and-`Wait` only. Product decision needed.

5. **Audit `storeRuntimeStore` field mappings** — CW-0028 found `Meta`
   was dropped; sweep `CreateRuntimeRow` for any other `RuntimeRow` field
   the store schema supports but the launcher does not map.

6. **Relay go-agent-launch / go-agent-context v0.1.0 validation** — Phase
   6 validated a broad slice of both libraries from a real consumer with
   no gaps; the owning teams may want a `validated-by` CHANGELOG note.
   CW-0028 kept their repos read-only.

---

## 7. Verification commands (reproducible)

```
go build ./...        → ok
go vet ./...          → ok
go test ./...         → 84 pkg PASS, 1 known unrelated FAIL

nanite launch --catalog ./examples/boot-profiles --dry-run claude-smoke
  → launch plan OK: provider=claude runtime=streaming-stdio

nanite launch --catalog ./examples/boot-profiles --db <tmp> --no-wait claude-smoke
  → launched: session=<ulid> provider=claude  (real claude process)
  → agent_runtime row: state=running, meta.launch_source=standalone-launcher

# codex / opencode smoked via a synthesized temp catalog (no committed
# example profile yet — see follow-up #2): both dry-run + live launch PASS.
```
</content>
