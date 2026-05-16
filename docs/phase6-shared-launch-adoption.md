# Phase 6 — Nanite shared-launch adoption

> Sprint `SP-20260514-0008`, Phase 6. Branch
> `feat/cw-20260515-0034-phase6-nanite-adoption`. Tickets CW-20260515-0024
> through CW-20260515-0028.

This document is the release / migration reference for the Phase 6 work
that moved Nanite's agent-launch machinery onto the shared
`go-agent-launch` and `go-agent-context` libraries, plus the cross-app
parity smoke that closed the workstream (CW-0028).

It also carries the cross-app migration guidance for Tether and Torque
(whose repos are owned by other phases) and the validation notes for the
two shared libraries.

---

## 1. What shipped (CW-0024..0027)

| Ticket | Workstream | Net effect |
|---|---|---|
| CW-0024 | Boot-profile compiler port | Mechanical slot file/inline IO now delegates to `go-agent-context` resolvers; added a one-directional `LaunchSpec → agentlaunch.LaunchPlan` bridge. Nanite's YAML schema, registry, dropdown encoding, and `LaunchSpec` output shape are unchanged. |
| CW-0025 | Provider bootdirs | Each provider `Layout` (claude/codex/opencode) declares its bootdir as an `agentlaunch.InjectionSpec`; planting rides the shared path-safe write loop (`ValidateBootDirRelPath`, native-files-then-overlay ordering). Planted file content is byte-identical to pre-Phase-6. |
| CW-0026 | Skills/context providers | The four deferred slot kinds (`cmd`, `http`, `role_summary`, `skill_index`) now resolve through the shared `agentcontext` resolvers instead of erroring with `ErrRequirementUnsupported`. |
| CW-0027 | Standalone launcher | New `nanite launch <profile>` CLI + `internal/launcher` package — start a Nanite-managed CLI agent from a shared launch profile with no chat server. |
| CW-0028 | Parity smoke + release notes | This document, the smoke matrix, and the launcher fixes that closed the live-launch acceptance gap (see §4). |

### Design posture (unchanged across all four)

Only **mechanical assembly** moved to the shared libraries. Nanite's
product semantics stayed Nanite-side: the YAML schema, dropdown encoding,
`{{var}}` substitution, the `### filename` static-dir concat, content
renderers (`BuildCLAUDEMD`, envelope schema, agent-context doc,
subprocess-spawn `.mcp.json`), and the forensic bootdir naming scheme. The
shared libraries provide the *file-set model*, the *path-safety gate*, the
*resolver IO*, and the *plan validation contract* — not the content.

---

## 2. Dependency changes

```
require (
    github.com/hollis-labs/go-agent-launch  v0.1.0   // NEW — direct
    github.com/hollis-labs/go-agent-context v0.1.0   // NEW — direct
)
github.com/hollis-labs/go-agent-sessions v0.9.2 → v0.9.4   // BUMPED (transitive)
```

- `go-agent-sessions` bumped to v0.9.4 — a transitive requirement of
  `go-agent-context v0.1.0`. Compatible: full `go build`, `go vet`, and
  the touched packages' tests pass.
- No `replace` directives were added for the two new modules — both
  tagged `v0.1.0` releases download and build cleanly.

---

## 3. Cross-app parity smoke matrix (CW-0028)

### Provider availability in the smoke environment

| Provider | Binary | Version | Credentials |
|---|---|---|---|
| claude | `~/.local/bin/claude` | 2.1.143 (Claude Code) | Anthropic key in keychain |
| codex | `/opt/homebrew/bin/codex` | codex-cli 0.130.0 | OpenAI key in keychain |
| opencode | `~/.opencode/bin/opencode` | 1.14.48 | (provider-managed) |

All three provider binaries and credentials were present, so the
launcher path was smoked **live** for every provider.

### Matrix

| Dimension | Mode | Result | Notes |
|---|---|---|---|
| Nanite `go build ./...` | live | PASS | clean |
| Nanite `go vet ./...` | live | PASS | clean |
| Nanite `go test ./...` | live | PASS (84 pkgs) | 1 known unrelated failure — see below |
| `internal/chat TestEnvelopeRegistrySync` | live | FAIL (pre-existing) | Missing sibling `go-envelopes/manifest/envelopes.yaml` checkout. Confirmed unchanged from base commit `202b530` (Phase 6 made zero changes to `internal/chat/`). Worktree-layout issue, not a code regression. |
| `nanite launch --dry-run` (claude-smoke) | live | PASS | `launch plan OK: provider=claude runtime=streaming-stdio`, 640-byte boot prompt, 2 slots |
| Claude — standalone launch, PTY/native TUI | live | PASS | Real `claude` 2.1.143 process spawned via `nanite launch --no-wait claude-smoke`; bootdir planted with `CLAUDE.md` + `boot.md` + `.sandbox/*` + `.mcp.json` + `.claude/settings.json`; workspace triplet (prompts/state/logs) materialized under `~/.nanite/workspaces/<sid>`. |
| Claude — streaming (StreamingStdio runtime) | live | PASS | `planOptionsFor` selects `RuntimeStreamingStdio`; the launch plan validates and the runtime spawns in that shape. |
| Codex — standalone launch | live | PASS | Real `codex` 0.130.0 process spawned; codex bootdir layout (`AGENTS.md` + `boot.md` + `.sandbox/*` + `.mcp.json`). |
| Codex — JSON-RPC / app-server | compile | PASS (compile) | Launcher compiles + validates a codex `LaunchPlan`; the app-server vs subprocess split is a go-providers adapter concern, not exercised by a standalone-launch smoke. |
| OpenCode — standalone launch (subprocess) | live | PASS | Real `opencode` 1.14.48 process spawned; opencode bootdir layout (`agents/default.md` + `agents.json` + `opencode.json` + `boot.md` + `.sandbox/*` + `.mcp.json`); cwd = project dir, `OPENCODE_CONFIG_DIR` env. |
| Worktree isolation | compile | PASS (compile) | Each launch reserves a fresh workspace dir under `~/.nanite/workspaces/<session-id>` and a fresh `$TMPDIR` bootdir keyed by the forensic `nanite-boot-<provider>-<sid>-r<run>-XXXX` scheme. Verified by inspecting the three live launches. Nanite's worktree feature itself (`internal/worktree`) is unchanged by Phase 6. |
| Native skills (`skill_index` slot) | compile | PASS (compile) | `internal/bootprofile` tests cover `skill_index` resolution via the shared `SkillIndexResolver`. No example boot profile uses a `skill_index` slot, so not exercised end-to-end in a live launch. |
| Arbitrary injections (`cmd`/`http`/`role_summary` slots) | compile | PASS (compile) | `internal/bootprofile` tests cover cmd/http/role_summary resolution and failure surfacing. The `claude-smoke` example profile is text+static only, so deferred resolvers were not on the live path. |
| boot-exec | live | PASS | This very task ran inside a Nanite boot-exec session; the standalone-launcher smokes additionally exercised `agent.Boot` end to end. |
| attach / detach | skip | SKIP | The standalone launcher does not expose an attach/detach surface (it is a one-shot `Boot` + optional `Wait`). Attach/detach is a chat-runtime / agentsessions concern outside the CW-0027 launcher scope. No regression — chat attach/detach is untouched by Phase 6. |

**Summary: 13 PASS (8 live, 5 compile-level), 1 SKIP, 1 known
pre-existing unrelated FAIL.**

### What "live" vs "compile" means here

- **live** — a real provider binary was spawned (or a real build/test/CLI
  invocation was run) and its observable effect verified.
- **compile** — the launcher compiled + projected + `Validate`-d a real
  `agentlaunch.LaunchPlan` (and/or package tests cover the path), but no
  provider process was spawned for that specific dimension.
- **skip** — the dimension is out of scope for the artifact under test;
  reason given inline.

---

## 4. Launcher fixes made in CW-0028

CW-0027 verified the standalone launcher at **dry-run + fake-runtime**
only. Closing the live-launch acceptance gap surfaced two real bugs in
the CW-0027 launcher code; both were fixed in `internal/launcher`
(Nanite-repo-local, no shared-package or other-app changes):

1. **`WorkspacesRoot` was never defaulted.** `launch_cmd.go` does not set
   `Config.WorkspacesRoot`, and `buildDeps` passed the empty value
   straight to `agent.Boot`, whose `workspaceCreate` **hard-errors** on an
   empty root (`agent.workspaceCreate: WorkspacesRoot is required`). The
   `Config.WorkspacesRoot` doc comment incorrectly claimed `agent.Boot`
   falls back to `~/.nanite/workspaces` — it does not; only the chat
   path's `service.BuildAgentDependencies` resolves that default. Fix:
   `buildDeps` now applies the same `~/.nanite/workspaces` fallback, and
   the doc comment was corrected. Without this, **no live standalone
   launch could ever succeed.**

2. **The `launch_source` provenance stamp was dropped on store-backed
   launches.** `bootOptionsFor` stamps
   `SessionMeta["launch_source"] = "standalone-launcher"`, and `agent.Boot`
   carries it onto the `RuntimeRow.Meta`. But
   `storeRuntimeStore.CreateRuntimeRow` never mapped `RuntimeRow.Meta`
   onto `store.AgentRuntimeRow.MetaJSON` — so the persisted
   `agent_runtime` row had `meta_json = "{}"`. Fix: `CreateRuntimeRow`
   now JSON-marshals `row.Meta` into `MetaJSON`. The CW-0027 handoff and
   CHANGELOG both promised an introspectable `launch_source` row; this
   makes that true.

Both fixes are covered by a new test
(`TestBuildDeps_WorkspacesRootFallback`) and were verified by the live
launches in §3 (the persisted rows show
`meta.launch_source = "standalone-launcher"`).

---

## 5. Release notes per component

### Nanite

See the `CHANGELOG.md` `Unreleased` section. Headline user-facing items:

- **New:** `nanite launch <profile>` standalone launcher (`--dry-run`,
  `--no-wait`, `--catalog`, `--db`, `--dev`). Operator docs:
  `docs/standalone-launcher.md`.
- **Internal:** boot-profile compiler, provider bootdirs, and deferred
  slot resolution now ride shared `go-agent-launch` / `go-agent-context`
  primitives. No user-facing behavior change — boot prompts and bootdir
  content are byte-identical.
- **Deps:** `go-agent-launch v0.1.0` + `go-agent-context v0.1.0` added;
  `go-agent-sessions v0.9.2 → v0.9.4`.

Migration for Nanite operators: none required. The standalone launcher is
purely additive; the chat boot-profile dropdown, recovery, and chat
behavior are untouched. Existing boot-profile catalogs work unchanged.

### go-agent-launch (v0.1.0) — validated, no version change

Phase 6 exercised the following `v0.1.0` surface from Nanite and found no
gaps:

- `agentlaunch.LaunchPlan` + `.Validate()` — the standalone launcher
  builds and validates a real plan per launch (CW-0024 bridge +
  CW-0027 launcher).
- `agentlaunch.InjectionSpec` / `NativeFile` / `NativeFileRaw` /
  `BootDirOverlay` / `ValidateBootDirRelPath` — every provider bootdir
  plant rides these (CW-0025).
- Provider/runtime/workspace/launch-mode enums and the
  `ErrMissingProviderID` sentinel (prompt-only-profile rejection).
- **Deliberately NOT used:** `providerplant.Plant` — it renders
  go-providers' own `CLAUDE.md`/`.mcp.json`, which diverges
  byte-for-byte from Nanite's content. Nanite plants via `agent.Boot`'s
  `Layout` impls instead (which still use the shared `InjectionSpec`
  write loop). This is a content-ownership decision, not a library gap.

**Guidance for a future go-agent-launch release:** none required for
Phase 6. One *flag, not a request*: `resolvers.StaticDirResolver`
concatenates directory files with a bare `"\n\n"`; Nanite's directory-glob
slot format (`### <filename>` headings + `---` separators) is app-specific
and stays Nanite-side. If a future release wants to host that, it would
need a configurable per-file-header option — but it is arguably a
presentation concern that belongs app-side.

> Note: this Nanite-side validation is **not** appended to the
> `go-agent-launch` repo's own CHANGELOG — that repo is owned by another
> phase and the CW-0028 scope is read-only on it. Flagging here so the
> orchestrator can relay the validation result if desired.

### go-agent-context (v0.1.0) — validated, no version change

Phase 6 exercised:

- `agentcontext.Resolver` + `resolvers.NewStaticFileResolver()` /
  `NewInlineResolver()` — compile-time `text`/`static` slot IO (CW-0024).
- `resolvers.CmdResolver`, `HTTPTextResolver`, `HTTPJSONResolver`,
  `RoleSummaryResolver`, and the opt-in `SkillIndexResolver`
  (`WithSkillIndex`) — the four deferred slot kinds (CW-0026).
- `agentcontext.DefaultProvider.Assemble` + `ContextRequest` / `SlotSpec`.
- The `skills` model (`skills.Discover`, `skills.Skill`) behind
  `SkillIndexResolver`.

All four deferred resolvers are app-neutral (no Nanite imports), so
CW-0026 added **zero** code to the shared package — it simply consumed
resolvers that already shipped in `v0.1.0`. No gap, no version change.

> Same read-only caveat as go-agent-launch: not appended to the
> `go-agent-context` repo CHANGELOG.

### Tether — cross-app migration guidance

Tether's shared-launch adoption is **Phase 4** (in review at the time of
writing) and is owned by that phase. Phase 6 did not touch the Tether
repo. Migration guidance relevant to the Tether/Nanite boundary:

- Nanite's standalone launcher runs with **no Tether MCP in the loop** —
  it is the operator-facing path for starting a Nanite-managed agent
  without an orchestrator. A Tether-managed launch and a
  `nanite launch`-launched session reach an **identical** Nanite runtime
  (`agent.Boot`) with **identical** (shared-`InjectionSpec`-planted)
  bootdir content. The two paths can coexist.
- If Tether builds an `agentlaunch.LaunchPlan` to hand to Nanite, the
  CW-0024 bridge (`bootprofile.LaunchSpec.ToLaunchPlan`) is the format
  Nanite produces; `agentlaunch.LaunchPlan` is the cross-app handoff
  vocabulary. Tether should treat it as the convergence/validation
  artifact, not a replacement source of truth — Nanite still owns
  catalog load + slot compile + dropdown encoding.

### Torque — cross-app migration guidance

Torque's shared-launch adoption is **Phase 5** (done) and is owned by
that phase. Phase 6 did not touch the Torque repo. Cross-app notes:

- Phase 5 (Torque) adopted `providerplant.Plant` because Torque launches
  flow through `LaunchPlan → launcher.Compile → launcher.Prepare`.
  Nanite's runtime boot path (`agent.Boot`, the `Layout` interface) does
  **not**, so Nanite (CW-0025) deliberately adopted only the shared
  *file-set model* + *path-safe write loop*, not `providerplant.Plant`.
  This is a documented, intentional divergence — both apps use the same
  shared `agentlaunch` primitives, just at different layers. A team
  comparing the two adoptions should expect this and not treat it as
  drift.
- Both apps validate against the same `agentlaunch.LaunchPlan` contract,
  so a plan that validates for Torque validates for Nanite.

---

## 6. Known limitations

1. **`internal/chat TestEnvelopeRegistrySync` fails in worktree-layout
   checkouts.** The test reads a sibling `../go-envelopes/manifest/
   envelopes.yaml` that does not exist when the repo is checked out
   without `go-envelopes` as a sibling. Pre-existing, unrelated to
   Phase 6 (confirmed identical on base commit `202b530`). Same class as
   the `go-modelsdev` symlink note in the CW-0024..0027 handoffs.
2. **No example boot profiles for codex / opencode.** The repo ships only
   `examples/boot-profiles/claude-smoke`. Codex and opencode were
   smoked live via a synthesized temp catalog. A committed example
   catalog covering all three providers would make the smoke
   reproducible without ad-hoc fixtures.
3. **Deferred slots (`cmd`/`http`/`role_summary`/`skill_index`) and
   `skill_index` discovery have no live-launch coverage.** They are
   covered by `internal/bootprofile` unit tests, but no example profile
   exercises them through a real `nanite launch`. A live launch of a
   profile with a `cmd` slot + a `skill_index` slot would close that gap.
4. **The standalone launcher exposes no attach/detach surface.** It is a
   one-shot `Boot` + optional `Wait`. An operator who wants to attach to
   a running standalone agent has no launcher-level affordance. Out of
   scope for CW-0027; the chat runtime / agentsessions own attach.
5. **The standalone launcher does not resume from checkpoints.**
   `memoryRuntimeStore.GetCheckpoint` errors by design; standalone
   launches always start fresh.
6. **`providerplant.Plant` content divergence is permanent by design.**
   go-providers' bootdir renderers produce different content from
   Nanite's. Any future move to `providerplant.Plant` at the Nanite
   runtime layer would require a Nanite-renderer overlay, not the stock
   `DefaultResolver`. This is a documented constraint, not a bug.

---

## 7. Recommended follow-up tasks

Each is scoped tightly enough to file directly as a Torque task.

1. **Commit a sibling-independent fixture or skip-guard for
   `TestEnvelopeRegistrySync`.** The test should `t.Skip` with a clear
   message when the `go-envelopes` manifest is absent (it already
   `t.Skip`s on a missing frontend registry — apply the same pattern to
   the manifest read) rather than hard-failing in worktree-layout
   checkouts. Pure test-hygiene change in `internal/chat`.

2. **Add committed codex + opencode example boot profiles + launches.**
   Mirror `examples/boot-profiles/claude-smoke` for codex and opencode so
   `nanite launch --dry-run` (and a live smoke) can cover all three
   providers from committed fixtures, with no synthesized temp catalog.

3. **Add a live-launch smoke for deferred slots.** Create an example
   profile with a `cmd` slot (e.g. `git log -1`) and a `skill_index`
   slot (with an explicit `roots:` list), and verify the resolved cmd
   output + skill index land in the rendered boot prompt of a real
   `nanite launch`. Closes the deferred-resolver live-coverage gap.

4. **Decide whether the standalone launcher needs an attach surface.**
   Scope: if operators need to interact with a running standalone agent,
   add an attach/detach affordance to `internal/launcher` (or document
   that standalone launches are fire-and-`Wait` only). Currently
   undecided — flag for product.

5. **Audit other `RuntimeRow` → `store.AgentRuntimeRow` field mappings in
   `storeRuntimeStore`.** CW-0028 found `Meta` was silently dropped.
   `storeRuntimeStore.CreateRuntimeRow` should be reviewed for any other
   `RuntimeRow` field (e.g. `ProviderSessionID` at create time) that the
   store schema supports but the launcher does not map. Defensive sweep.

6. **Relay the go-agent-launch / go-agent-context v0.1.0 validation
   result to those repos.** Phase 6 validated a broad slice of both
   libraries' `v0.1.0` surface from a real consumer with no gaps found.
   The owning phase/team for those repos may want a CHANGELOG note or a
   `validated-by` reference. CW-0028 left their repos read-only — this
   follow-up is the hand-off of that validation signal.
</content>
</invoke>
