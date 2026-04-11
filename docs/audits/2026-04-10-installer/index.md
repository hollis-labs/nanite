# Deep Review — Installer

**Date:** 2026-04-10
**Reviewer:** `nanite-reviewer-backend` (code-review + go roles, deep-review skill)
**Scope slug:** `installer`

## Scope

The user asked for a deep review of the Nanite installer end-to-end. The installer is the entry point for every project's setup and will be exercised by Phase 4 Task 16 (running `nanite install --project ~/Projects-apps/nanite` to repoint Nanite's own project-level symlinks). A bonus task was bundled: resolve the open question from the prior sandbox audit's finding 03 about whether the installer gates on `bwrap` presence.

**Interpreted as:** full read of `internal/service/install/` and `cmd/nanite/install_cmd.go`, plus every adapter that writes project-root files through the installer's filters, plus the `internal/agent` managed-section parser they all share, plus the `internal/assets` embedded framework extractor. Both security and correctness categories apply.

### Files read in full

- `cmd/nanite/install_cmd.go`
- `internal/service/install/install.go`
- `internal/service/install/scaffold.go`
- `internal/service/install/claudemd.go`
- `internal/service/install/adopt.go`
- `internal/service/install/migrate.go`
- `internal/service/install/archive.go`
- `internal/service/install/rollback.go`
- `internal/service/install/resume.go`
- `internal/service/install/state.go`
- `internal/service/install/adapters.go`
- `internal/service/install/adapter_select.go`
- `internal/service/install/adapter_detect.go`
- `internal/service/install/adapter_persist.go`
- `internal/service/install/adapter_prompt.go`
- `internal/service/install/adapter_cleanup.go`
- `internal/service/install/integration_test.go`
- `internal/agent/managed_section.go`
- `internal/agent/adapter.go`
- `internal/assets/framework.go`
- `internal/assets/framework/templates/nanite-config.yaml.tmpl`
- `docs/audits/2026-04-10-sandbox-hardening/03-critical-linux-silent-sandbox-fallback.md` (cross-audit)

### Files sampled

- `internal/plugin/builtin/adapter-claude/plugin.go` — confirmed `SyncProjectRoot` path and `WriteManagedSection` caller
- `internal/plugin/builtin/adapter-codex/plugin.go`, `adapter-gemini/plugin.go`, `adapter-opencode/plugin.go`, `adapter-nanite-native/plugin.go` — grep-checked for the same managed-section write pattern
- `internal/service/install/install_test.go`, `migrate_test.go`, `scaffold_test.go`, `adapter_persist_test.go`, `adapters_test.go`, `resume_test.go`, `rollback_test.go`, `adapter_select_test.go`, `adapter_detect_test.go`, `adapter_cleanup_test.go`, `archive_test.go`, `claudemd_test.go`, `state_test.go`, `adapter_prompt_test.go` — skimmed for coverage gaps cited in individual findings
- `.nanite/agents/reviewer-backend.md` — full read (context file)

### Files NOT read (explicit blind spots)

- `internal/plugin/*` internals beyond the adapter `SyncProjectRoot` methods — plugin system was audited separately
- `internal/sandbox/*` beyond the bwrap-check resolution — already audited
- `internal/agent/discovery.go`, `parser.go`, `convert.go`, `override/*` — agent discovery is downstream of install; didn't touch it unless the installer was writing something the discovery code would read
- `internal/store/*`, migrations — trust boundary elsewhere
- `internal/assets/framework/` content files (roles, skills, commands) — the installer extracts these verbatim; their correctness is a separate concern
- Anything requiring runtime execution to verify (install against a real filesystem, sandbox binary presence check on a real host, YAML library duplicate-key behavior)

## Methodology

Followed the six-step methodology in `~/.nanite/skills/deep-review.md`:

1. **Scope parsing:** `installer` → subsystem + end-to-end flows (fresh, adopt, migrate, rollback, resume, refresh, reconfigure). Read every file in the install package and the CLI entry point.
2. **Category enumeration:** Security (primary focus — symlink TOCTOU, path handling, managed-section parser, env var trust, template injection), Correctness (atomicity, idempotency, state recovery), Error handling (propagation, wrapping, user-visible messages), Test quality (coverage gaps cited in specific findings). Go idioms / antipatterns / standards tooling **deferred** to a follow-up scope per the scoped-review tooling rule in the skill.
3. **Evidence gathering:** read actual code, cited `file:L<start>-<end>` ranges, quoted code excerpts, ran targeted `grep` passes to confirm "not present" claims (e.g., no sandbox preflight exists anywhere in the installer).
4. **Severity classification:** applied the rubric strictly. Erred toward higher severity for security issues with an attacker-controlled filesystem precondition, lower severity for issues that require adversarial environment variables or unusual directory names.
5. **Write findings:** one file per topic cluster. Smaller items grouped in `10-low-observations.md`. Praise and design notes in `11-info-praise-and-design-notes.md`.
6. **Assemble index:** this file. Per-finding links in both "By severity" and "By topic" groupings.

**Tooling deferred (per the scoped-review rule):** `go vet`, `go test -race`, `golangci-lint`, `staticcheck`, `errcheck`, `govulncheck`, `go mod tidy`. The installer has a healthy test suite (21 test files in the install package alone), but no tooling commands were run for this pass. Recommend a follow-up `installer-tooling-and-tests` scope that runs them all against the install package and its dependencies.

**No code changes.** Read-only audit. No commits.

## Findings

### By severity

**Critical (0)**
- _none_

**High (4)**
- [01 — Installer does not validate sandbox prerequisites](01-high-installer-does-not-validate-sandbox-prerequisites.md)
- [02 — Symlink following on adapter target files](02-high-symlink-following-on-adapter-target-files.md)
- [03 — Managed-section end-marker confusion](03-high-managed-section-end-marker-confusion.md)
- [04 — Scaffold symlinks created without target validation](04-high-scaffold-symlinks-created-without-target-validation.md)

**Medium (5)**
- [05 — YAML round-trip discards comments and unrelated nodes](05-medium-yaml-round-trip-discards-comments-and-unrelated-nodes.md)
- [06 — Fresh scaffold not atomic; no rollback on partial failure](06-medium-fresh-scaffold-not-atomic-no-rollback-on-partial-failure.md)
- [07 — Project-name template injection into YAML / markdown](07-medium-project-name-template-injection.md)
- [08 — Carryover follows symlinks inside archived `.agentrc/`](08-medium-carryover-follows-symlinks-inside-archived-agentrc.md)
- [09 — `NANITE_ARCHIVE_BASE` env var lets an external process steer archive path](09-medium-archive-base-path-escape-via-env-var.md)

**Low (1)**
- [10 — Roundup of smaller installer observations (13 items)](10-low-observations.md)

**Info (1)**
- [11 — Praise and design notes](11-info-praise-and-design-notes.md)

### By topic

**Security — symlink / path handling**
- [02 — Symlink following on adapter target files](02-high-symlink-following-on-adapter-target-files.md)
- [04 — Scaffold symlinks created without target validation](04-high-scaffold-symlinks-created-without-target-validation.md)
- [08 — Carryover follows symlinks inside archived `.agentrc/`](08-medium-carryover-follows-symlinks-inside-archived-agentrc.md)
- [09 — `NANITE_ARCHIVE_BASE` env var lets an external process steer archive path](09-medium-archive-base-path-escape-via-env-var.md)

**Security — parser / injection**
- [03 — Managed-section end-marker confusion](03-high-managed-section-end-marker-confusion.md)
- [07 — Project-name template injection into YAML / markdown](07-medium-project-name-template-injection.md)

**Security — prerequisite enforcement**
- [01 — Installer does not validate sandbox prerequisites](01-high-installer-does-not-validate-sandbox-prerequisites.md)

**Correctness — atomicity / recovery**
- [06 — Fresh scaffold not atomic; no rollback on partial failure](06-medium-fresh-scaffold-not-atomic-no-rollback-on-partial-failure.md)

**Correctness — data integrity**
- [03 — Managed-section end-marker confusion](03-high-managed-section-end-marker-confusion.md)
- [05 — YAML round-trip discards comments and unrelated nodes](05-medium-yaml-round-trip-discards-comments-and-unrelated-nodes.md)

**Error handling / UX**
- [10 — Roundup of smaller installer observations](10-low-observations.md) (items L2, L4, L6, L7, L9, L12)

**Test quality**
- [10 — Roundup of smaller installer observations](10-low-observations.md) (items L8, L11)
- [01 — Installer does not validate sandbox prerequisites](01-high-installer-does-not-validate-sandbox-prerequisites.md) (no sandbox-prereq tests exist to fail)

**Design — positive observations**
- [11 — Praise and design notes](11-info-praise-and-design-notes.md)

## Cross-audit resolutions

### Sandbox Finding 03 — `bwrap` check at install time: **NO**

The prior sandbox-hardening audit left this open question:

> Does the Nanite installer check for `bwrap` before completing? If yes, finding 03 drops from Critical to Medium.

**Definitive answer: NO.** The installer performs no sandbox prerequisite checks on any platform. `grep -rni 'bwrap\|bubblewrap\|sandbox-exec\|LookPath\|preflight\|prerequisite' internal/service/install/ cmd/nanite/install_cmd.go` returns zero results. The install dispatch in `cmd/nanite/install_cmd.go:L20-197` has no `runtime.GOOS` branch, no `exec.LookPath` call, and no preflight phase. The service layer in `internal/service/install/install.go:L42-173` does not import `internal/sandbox` and has no equivalent check.

**Recommendation for sandbox Finding 03:** stays **Critical**. The installer is NOT the mitigation; the silent-fallback-to-no-OS-sandbox behavior is the real bug, and it remains unmitigated on every platform where the sandbox binary is not pre-installed.

**This audit files finding 01** ([01 — Installer does not validate sandbox prerequisites](01-high-installer-does-not-validate-sandbox-prerequisites.md)) as a High. It is the installer-side companion to sandbox Finding 03: even if the sandbox code is fixed to fail closed at `AgentExec` time, the installer should be the first place the user hears about a missing prerequisite, not the tenth (when their first agent run mysteriously errors). The two findings should be fixed together — sandbox 03 to fail closed at runtime, installer 01 to fail closed at install time with an actionable installation hint per platform.

## Recommended next steps

Priority order is technical-risk-first; no timing guidance.

1. **Fix install-time sandbox validation** — address finding 01 alongside sandbox Finding 03 as a paired fix. The install-time check should be the first one added because it changes the user's mental model of the install's success/failure contract.
2. **Symlink defenses** — findings 02, 04, 08 are all the same class of issue in three different flows. A single `safefs.go` helper covers all three, and once it exists, grep for every remaining `os.ReadFile` / `os.WriteFile` in the install package to audit callers. Finding 04 in particular is load-bearing for Phase 4 Task 16 because that task adopts an existing `.nanite/` with known broken symlinks.
3. **Managed-section parser hardening** — finding 03 is the one that will actually break user files during the beta. Count-and-refuse is a cheap fix and the right default.
4. **Atomicity for fresh and adopt** — finding 06. Extends the existing migrate-state-marker pattern to the other two flows. Larger than fixes above, but the migrate implementation is a working template.
5. **YAML round-trip** — finding 05. Decide between the line-editor and the full yaml.Node preserve paths, then rewrite `persistAdapterList`. Low urgency relative to the security items; high user-visibility.
6. **Env var and template injection** — findings 07, 09. Smaller scope, cheaper fixes.
7. **Follow-up review scopes** to consider after this audit's findings land:
   - `installer-tooling-and-tests` — run `go vet`, `go test -race`, `golangci-lint`, `staticcheck`, `errcheck`, `govulncheck` against the install package and its direct dependencies. Deferred from this pass per the scoped-review tooling rule.
   - `managed-section-parser` — a dedicated pass over `internal/agent/managed_section.go` plus `claudemd.go`'s `RemoveAgentrcSection`, with fuzz testing for marker-confusion input. The current audit flagged the confusion case but a dedicated pass would cover the agentrc-heading stripper's similar issues in depth.
   - `adapter-plugin-install-boundary` — every built-in adapter plugin's `SyncProjectRoot` writes to the project tree via `WriteManagedSection`. A dedicated pass over the four adapter packages would verify none of them have their own file-handling bugs upstream of the installer's unified writer.

## Known issues skipped

From the reviewer-context `## Pre-existing known issues — DO NOT re-flag` list (`.nanite/agents/reviewer-backend.md`):

- Plugin scaffold broken imports (P0-1)
- `adapter-opencode` unverified format — mentioned in context as a plugin issue, not an installer issue; not re-flagged here
- `oembed`, `fragments-engine` chat-header, `Host.Shutdown()` deadlock — all plugin-system issues, not installer
- Broken `.claude/commands` and `.claude/skills` symlinks in Nanite's own repo — explicitly deferred to Phase 4 Task 16 (which this audit is gating for). Finding 04 is the technical mechanism behind that known issue but does not re-file the symptom.
- `shadcn-ui` config.yaml typo — out of scope
- `.agentrc/` vs `.nanite/` path drift — out of scope; same as above

## Noticed but out of scope

Observations made while traversing the installer that fall outside the current scope but may be worth a future pass:

- **`internal/agent/managed_section.go` is imported by every adapter plugin and by the installer.** The parser bugs in finding 03 are installer-visible, but the same parser is called from `adapter-claude/plugin.go:L181`, `adapter-codex/plugin.go:L148`, `adapter-gemini/plugin.go:L148`, `adapter-opencode/plugin.go:L151` — every `SyncProjectRoot` call site at session-prep time, not just install time. A dedicated "managed-section parser" review scope would cover both sets of callers.
- **`readGlobalConfigWithFallback` and `readProjectConfigWithFallback`** in `adapter-nanite-native/plugin.go:L109-112` silently fall back when the primary path fails. That's adjacent to the installer because the installer writes the files those fallbacks are looking for, but the reading side is the adapter's concern.
- **`agent.ParseMDFile`** (used in `adapter-claude/plugin.go:L114`) has no test coverage visible from this pass. A malformed agent markdown file is the trigger for the adapter's silent-skip branch at line 115-118. Worth a separate look if adapter-claude becomes higher priority.
- **`cmd/nanite/install_cmd.go:L272-306` (`latestArchiveFor`)** reads `NANITE_ARCHIVE_BASE` directly, duplicating the logic in `archiveBaseOverride`. Two different code paths reading the same env var is a minor consistency issue; finding 09 covers the broader env-var concern.
- **`internal/assets/framework/` content** — the embedded assets are extracted verbatim by `ExtractTo`. I did not audit their content for correctness. The installer would happily extract a malformed template and fail at first use. Out of scope for this pass; worth a "framework-content audit" scope if one hasn't been done.
- **The adapter plugin registration order** in `newBuiltinAdapterRegistry` (`adapters.go:L41-49`) uses `reg.Register` calls whose order determines deterministic priority ties. Not a bug given the explicit priority values, but fragile to future changes.
- **`Resume` and `Restart` flows touch both the archive dir and the project dir** — if an attacker can race between phases by modifying the archive dir between `ReadState` and the subsequent writes, the state could diverge. Not currently exploitable because the archive dir is not a shared trust boundary in the normal config, but if finding 09's env var issue lands, the archive base becomes attacker-controllable and this observation upgrades to a finding.
- **The `.install-state.json` marker has no integrity check.** A user who pokes at the file between resume attempts can break invariants. Low priority; resume is already guarded by "refuse if no marker" in rollback.

None of the above were flagged as findings in this pass. They are captured here so they surface in the next relevant scope's planning.
