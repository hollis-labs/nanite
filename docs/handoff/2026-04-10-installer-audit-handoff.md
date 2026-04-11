# Installer Audit Handoff — 2026-04-10

## Mission

Act on the installer deep-review audit. Two parallel tracks: fix the real bugs in `internal/service/install/` and `internal/agent/managed_section.go`, and address the cross-cutting concerns — most importantly installer-01 + sandbox-03 (must land together).

## Inputs

- **Audit:** `docs/audits/2026-04-10-installer/` — `index.md` plus finding files `01`–`11`.
- **Cross-audit finding:** `docs/audits/2026-04-10-sandbox-hardening/03-critical-linux-silent-sandbox-fallback.md`.
- **Audit index:** `docs/audits/INDEX.md` — cross-cutting themes and queued follow-ons.
- **Reviewer context:** `.nanite/agents/reviewer-backend.md` — trust boundaries and "DO NOT re-flag" list.

## Framing corrections

- **No time or session estimates.** Not hours, days, sessions, sprints, weeks.
- **No release-readiness gating.** No "beta-blocker" or "post-beta." Valid framing: "real bug, fix it."
- **Severity reflects technical risk only.** Ignore any stray phrasing that reads as a release judgment.
- **Execution is autonomous parallel agents.** Human-team sequencing does not apply.

## Real bugs in the installer

Four Highs:

- **01** — Installer does not validate sandbox prerequisites. No `exec.LookPath("bwrap")`, no `runtime.GOOS` branch, no preflight. `cmd/nanite/install_cmd.go:L20-197`, `install.go:L42-173`. **See cross-audit pair below.**
- **02** — Installer follows symlinks on every adapter target read/write. `os.ReadFile`/`os.WriteFile` with no `Lstat` or `O_NOFOLLOW`. Affects `UpdateCLAUDEmd`, `WriteManagedSection`, `snapshotAdapterTargets`, `persistAdapterList`, `RemoveManagedSection`. Arbitrary read/write primitive when a symlink sits in the project tree or archive dir. `internal/agent/managed_section.go:L28-78` et al.
- **03** — `WriteManagedSection` corrupts user content on marker ambiguity. First-match `strings.Index`, no marker-pair counting. Breaks on markers-in-user-prose, two managed blocks, end-before-start. Silent data loss; idempotency violated. `internal/agent/managed_section.go:L9-184`. Same parser is called by every adapter's `SyncProjectRoot` at session-prep, not just install.
- **04** — `ScaffoldNaniteDir` creates symlinks without verifying targets exist. No `os.Stat(target)`, no `globalHome` existence check, existing-link branch never re-validates dangling links. `scaffold.go:L60-73`, `install.go:L116-123`. Technical mechanism behind the known broken `.claude/commands`/`.claude/skills` symlinks in Nanite's own repo.

Mediums (`05`–`09`): YAML round-trip loses comments/anchors; fresh/adopt not atomic; `filepath.Base(projectDir)` templated unescaped; `carryOverFromArchive` follows symlinks inside archived `.agentrc/`; `NANITE_ARCHIVE_BASE` unvalidated. Low roundup in `10`.

## Cross-audit pair: installer 01 + sandbox 03

The installer audit definitively answered the sandbox audit's open question: **does the installer check for `bwrap`? No.** Nothing in `internal/service/install/` or `cmd/nanite/install_cmd.go`. Sandbox finding 03 stays Critical.

Either fix alone is insufficient. **Sandbox 03 alone** fails closed at `AgentExec` time — only after install completes and the user's first agent run errors with no hint what's missing. **Installer 01 alone** stops the install on Linux without bwrap, but leaves runtime silent-degradation alive when the sandbox binary was present at install and removed later.

Together the contract is coherent: installer fails closed at install time with platform-specific install instructions; `AgentExec` fails closed as a backstop. Treat as a single PR. See installer `01` and sandbox-hardening `03`.

## Cross-cutting themes

From the audit index. Only items the installer audit touched:

- **Symlink handling.** Installer 02, 04, 08 plus sandbox 01. A shared `safefs.go` (Lstat + regular-file check + optional `O_NOFOLLOW`) closes three installer findings at once. Sandbox 01 needs its own equivalent in `internal/sandbox/`; coordinate the shape.
- **Marker/parser injection.** Installer 03 (managed-section first-match parser, no marker-pair counting) and sandbox 01 (seatbelt profile string interpolation). Same root cause in two formats: untrusted string interpolated into a structured format without escaping. A "validate-and-refuse-on-ambiguous-input" pattern applies to both.
- **Shared validation pattern for trust boundaries.** Installer and sandbox both need a "validate at every entry to" rule for project paths, archive paths, adapter target files. Factor validators out so both packages consume them.

## Things that need verification before acting

- Confirm the four adapter plugins (`adapter-claude`, `-codex`, `-gemini`, `-opencode`) route all managed-section writes through `agent.WriteManagedSection` with no parallel `os.WriteFile` paths.
- Verify `internal/agent/managed_section.go` call sites — parser runs from adapter `SyncProjectRoot` at session-prep, not only at install. Fixes must land cleanly for both.
- Walk `internal/service/install/` for remaining `os.ReadFile`/`os.WriteFile`/`os.Stat` after safefs lands; `dirExists` at `install.go:L265-268` uses `os.Stat` (follows symlinks).
- `filepath.EvalSymlinks(projectDir)` at `install.go:L112-114` is the only project-root resolution site; it does not recurse.
- Check `NANITE_ARCHIVE_BASE` readers beyond `archiveBaseOverride` and `latestArchiveFor` (`install_cmd.go:L272-306`).
- `go test ./internal/service/install/... ./internal/agent/...` after each fix.

## Praise to preserve

From `11-info-praise-and-design-notes.md`. Do NOT regress these while fixing the bugs around them.

- **Migrate rollback contract.** `.install-state.json` phase-marker pattern in `state.go`/`migrate.go`/`resume.go`/`rollback.go` is the right shape for multi-step filesystem mutations. Finding 06 extends it to fresh/adopt — copy, don't rewrite.
- **Pre-edit snapshot rollback signal.** `snapshotAdapterTargets` writing `{archive}/{name}.pre-edit` as the "was this file pre-existing?" signal is load-bearing, tested by `integration_test.go:L21-172`. Finding 02 adds symlink protection; keep snapshot-presence semantics intact.
- **Tri-state `Adapters *[]string`.** `nil`/`&[]`/`&[...]` distinguishes unset/explicit-none/active. `adapters.go:L53-64`, flow-style at `adapter_persist.go:L65-68`. Finding 05 fixes YAML round-trip without breaking this model.
- **Test coverage and hermetic setup.** 21 test files, real embedded assets via `migrate_test.go:L11-22`, no `time.Sleep` sync. Keep the pattern.

## What's NOT in this handoff

- Time estimates of any granularity.
- Release-window judgments ("beta-blocker", "post-beta", "RC-ready").
- Analysis of whether work "fits in" any release.
- Sequencing motivated by human-team coordination.
- Tooling sweep (`go vet`, `-race`, `staticcheck`, `golangci-lint`, `govulncheck`) — deferred to the queued `installer-tooling-and-tests` scope. Follow-on `managed-section-parser` and `adapter-plugin-install-boundary` scopes also queued in `docs/audits/INDEX.md`.

## Suggested first move

Read installer findings `01`–`04` plus `sandbox-hardening/03`. Then pick one of two entry points (either is defensible; do not start Mediums until the four Highs have landed). **(1) Cross-audit pair:** fix `01 + sandbox 03` as a single PR — preflight helper in `internal/service/install/preflight.go`, `--allow-missing-sandbox` flag, fail-closed change in `internal/sandbox/os_linux.go`, matching `os_darwin.go` check for `/usr/bin/sandbox-exec`, regression test stubbing `exec.LookPath`. Closes a Critical and a High together. **(2) Symlink handling:** pick finding 02 — widest blast radius (every adapter target file, config writes, pre-edit snapshots); landing `safefs.go` also unblocks 04 and 08 in the same package.
