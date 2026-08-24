# Disambiguate `internal/config`'s three unrelated concerns sharing one generic package name

**Phase:** Wave 6 — Semantic duplication / migration drift
**Status:** reviewed
**Depends on:** none
**Touches:** `internal/config/config.go` (`Config`), `internal/config/appconfig.go` (`AppConfig`), `internal/config/layout.go` (XDG path resolution). Also, potentially, every caller of `config.Config` and `config.AppConfig` across the tree, depending on which direction the architect chooses.

```yaml
requires_architect_decision: true
```

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 6 — semantic duplication and migration drift · **Dispatch unit:** `W6a`
> - **Depends on:** Wave 5 complete
> - **Blocks:** none
> - **Parallel-safe with:** **none if AD-20 selects the tree-wide rename** — this task's own Touches warns it may reach every caller of `config.Config` and `config.AppConfig`. Otherwise parallel with all of Wave 6a.
> - **Gated on:** AD-20 — decide before scheduling the wave, not during it: the answer is the difference between a two-line rename and a tree-wide sweep.
> - **requires_security_review:** false · **requires_regression_test:** true

> ## ✅ AD-20 DECIDED (2026-08-22) — rename both types by role
>
> Give both `config.Config` and `config.AppConfig` names that state their role.
> `Config` inside a package called `config` carries no information, and
> renaming only one leaves the asymmetry.
>
> **⚠ This file's scope warning is wrong.** It says the fix "may reach every
> caller of `config.Config` and `config.AppConfig` across the tree." Measured at
> HEAD: **11 references total** — `Config` ×5, `AppConfig` ×6 — in
> `cmd/nanite/main.go` and `internal/service/{chat,container,slot_stash}.go`.
>
> Consequence: **this task no longer needs to run alone.** The batch README and
> this file's sequencing block both say it must if AD-20 picks a tree-wide
> rename. It didn't, because there is no tree-wide rename to pick.

## Context

### Findings addressed
- `GO-INFRA-001` — severity low, confidence high. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.2; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-INFRA-001`.

### A note on classification

This finding is included in this folder because it is thematically a "same term means different things" naming-collision problem, matching the spirit of this batch's Wave 6 grouping — but it is worth stating explicitly that it is **not really duplication in the code-copy sense** the way most of this folder's other items are. No logic is duplicated here; three genuinely distinct concerns simply grew under one generic package name over time. The closest fit in this batch's classification scheme is **(4) migration drift** — organic growth of unrelated concerns under one name, where the name stopped accurately describing the package's contents somewhere along the way, rather than an intentional design. Flagging this distinction explicitly so a future reader doesn't mistake this for a code-deduplication task; it's a naming/cohesion task.

### Root cause

`internal/config` bundles three distinct concerns under one generic package name:

1. **`Config`** (`internal/config/config.go`) — user/project runtime configuration, reads the project-root `./nanite.yaml`.
2. **`AppConfig`** (`internal/config/appconfig.go`) — checked-in tunables, reads `config/` + `brand.ConfigFileName` + `.yaml` = `config/nanite.yaml` — **a different file, with the same base filename**, in a different directory.
3. **`layout.go`** — pure XDG path resolution, no YAML parsing at all, a genuinely separate concern from either of the above.

The audit's cited evidence: `config.go:122` reads `'./nanite.yaml'`; `AppConfig` loads `'config/' + brand.ConfigFileName + '.yaml'` = `'config/nanite.yaml'` — two different files, two unrelated structs, same base filename. This is a **real confusion risk**, not merely cosmetic: an engineer told to "edit the nanite.yaml config" has a genuine, plausible chance of editing the wrong one, since both the package name (`config`) and the file's base name (`nanite.yaml`) are identical between the two concerns.

### Current behavior

`internal/config/config.go:122` — `Config` reads `./nanite.yaml` (project root).
`internal/config/appconfig.go` — `AppConfig` reads `config/nanite.yaml` (checked-in tunables directory), via `'config/' + brand.ConfigFileName + '.yaml'`.
`internal/config/layout.go` — pure XDG path resolution, no file-reading behavior of its own.

Confirm current line numbers via `grep -n` in all three files before editing — the audit's citation (`config.go:122`) is from commit `8feeee5c`.

### Desired invariant

An engineer or a future audit reading either the package name or a struct name should be able to tell, without opening the file, which of the three concerns (user/project runtime config, checked-in tunables, or XDG path resolution) they are looking at — and specifically should not be able to conflate "the nanite.yaml config" as a single referent when it actually names two structurally unrelated files.

## What to do

### Scope
- `internal/config/config.go` — `Config` struct and its file-loading logic.
- `internal/config/appconfig.go` — `AppConfig` struct and its file-loading logic.
- `internal/config/layout.go` — XDG path resolution helpers.
- Every caller of `config.Config` and `config.AppConfig` across the tree — the audit did not enumerate these; they must be found via `grep -rn 'config\.Config\|config\.AppConfig' --include='*.go'` (or equivalent) before deciding whether the disambiguation fix requires touching call sites or can be done package-internally.

### Proposed direction — do not resolve without architect sign-off

**This is the architect decision the audit itself flags** (`requires_architect_decision: true` in `findings.json`, recommendation: "Rename one file/struct to disambiguate, or split the package along the `Config`/`AppConfig` boundary"). Two directions, both viable, presented for the architect to choose between:

**Option A — rename to disambiguate, keep one package.**
Rename `AppConfig` (or `Config` — pick whichever reads more naturally against its actual role) to something that doesn't collide conceptually with the other, e.g. `AppConfig` → `TunablesConfig`/`CheckedInConfig`, or rename the file it reads (`config/nanite.yaml`) to a distinct base name so the "same file, different directory" confusion is also resolved at the filesystem level, not just in Go identifiers. Lower migration cost (no import-path changes for callers, only identifier renames), but keeps three concerns in one package, which is only a partial fix to the underlying cohesion issue.

**Option B — split the package along the `Config`/`AppConfig` boundary.**
Move `AppConfig` (and/or `layout.go`'s XDG resolution, if the architect judges it belongs with one side more than the other) into its own package with its own name, leaving `internal/config` to mean exactly one thing. Higher migration cost (every caller's import path changes), but resolves the cohesion problem completely rather than just renaming around it — consistent with the remediation guide's broader preference (§3, "root causes over occurrence counts") for fixing the underlying shape rather than papering over a symptom.

Both options should retain `layout.go`'s pure XDG-path-resolution helpers wherever makes most sense given the chosen direction — the audit does not treat `layout.go` as part of the naming collision itself (it has no file it "reads" to collide with either `Config` or `AppConfig`), but its co-location in the same package as the other two is part of why the package reads as a grab-bag; note this in Work log if the architect's chosen option affects where `layout.go` ends up.

### Non-goals
- Not changing the actual on-disk file locations or names (`./nanite.yaml`, `config/nanite.yaml`) unless the architect's chosen option specifically calls for it — this task is about code-level disambiguation first; a file-rename is a larger, more disruptive change (affects every deployed instance's existing config files) and should only be undertaken if explicitly decided, not assumed as part of the default fix.
- Not touching `internal/brand`'s `ConfigFileName` constant unless the chosen direction requires it.

## Tests required

- Existing tests for both `Config` and `AppConfig` loading must pass unchanged after the rename/split (whichever option is chosen) — this is an identifier/structural change, not a loading-behavior change.
- If Option B (package split) is chosen, confirm every caller across the tree compiles and passes its existing tests after the import-path change — a full `go build ./...` is the practical proof here, not a targeted subset.

## Prevention

The remediation guide's own principle of collapsing occurrences into underlying causes rather than treating this as a cosmetic nit: a package name that accurately describes its contents prevents exactly the kind of "which nanite.yaml did they mean" confusion the audit flagged as a real risk. No new lint rule is needed — package/identifier naming clarity is a one-time fix, not an ongoing enforcement surface.

## Verification

```bash
go build ./...
go vet ./internal/config/...
go test ./internal/config/... -v
grep -rn 'config\.Config\|config\.AppConfig' --include='*.go' .
```

Observable behavior required for PASS: whole-repo `go build` succeeds after the rename/split; existing `internal/config` tests pass unchanged; the `grep` for old identifier names (post-rename) returns nothing outside intentionally-preserved aliases, if any.

## Risk / rollback

Option A is low risk (identifier rename only, mechanical, caught immediately by `go build` if any call site is missed). Option B is medium risk purely due to blast radius — every caller's import path changes, which touches more files even though the change itself is mechanical; `go build ./...` will catch any missed call site immediately, so the actual risk of a silent break is low, but the review surface is larger. Rollback for either option is a revert of the rename/split commit(s); no data or schema is involved since this is purely a Go-identifier/package-structure change, not a change to the config files themselves.

## Done means

- [x] All callers of `config.Config` and `config.AppConfig` enumerated via grep before implementation.
- [x] Architect decision recorded: Option A (rename within one package) or Option B (split into separate packages).
- [x] Chosen option implemented; no lingering references to old identifier/package names.
- [x] `go build ./...` succeeds; `internal/config` tests pass unchanged.

## Work log

- 2026-08-24 worker pre-edit check:
  - Read `.claude/agents/worker.md`, this task, `docs/engineering/EXECUTION-PROCESS.md`, `docs/engineering/GLOSSARY.md`, and AD-20 in `TASKS/audit-remediation/ARCHITECT-DECISIONS.md`.
  - Architect decision recorded: AD-20 chooses Option A, rename both exported config structs by role within the existing `internal/config` package. No on-disk config file paths/names are in scope.
  - Re-derived current citations before editing: `internal/config/config.go:23` defines `Config`; `internal/config/config.go:122` reads project-root `nanite.yaml`; `internal/config/appconfig.go:12-13` defines `AppConfig` for checked-in `config/nanite.yaml`; `internal/config/appconfig.go:225-227` loads that checked-in file; `internal/config/layout.go:1` and `internal/config/layout.go:28` are XDG layout resolution only.
  - Re-enumerated callers with `.git` and `.claude` pruned:
    - `cmd/nanite/main.go:1009` `config.Config`
    - `cmd/nanite/main.go:1025` `config.Config`
    - `cmd/nanite/main.go:1054` `config.Config`
    - `cmd/nanite/main.go:1071` `config.Config`, `config.AppConfig`
    - `cmd/nanite/main.go:1403` `config.Config`
    - `internal/service/slot_stash.go:90` `config.AppConfig`
    - `internal/service/chat.go:105` `config.AppConfig`
    - `internal/service/chat.go:230` `config.AppConfig`
    - `internal/service/container.go:177` `config.AppConfig`
    - `internal/service/container.go:305` `config.AppConfig`
- 2026-08-24 implementation:
  - Renamed `internal/config.Config` to `RuntimeConfig` and `internal/config.AppConfig` to `TunablesConfig`.
  - Updated the bounded caller set in `cmd/nanite/main.go` and `internal/service/{chat,container,slot_stash}.go`.
  - Kept the existing `internal/config` package, XDG layout helpers, loader functions, and on-disk YAML paths/names unchanged.
  - Verification:
    - `go test ./internal/config/... -v` — PASS.
    - `go vet ./internal/config/...` — PASS.
    - `go build ./...` — PASS.
    - `grep` for `config\.(Config|AppConfig)\b` across active-tree Go files with `.git`/`.claude` pruned — no matches.
    - `go build ./cmd/nanite/` — PASS.
    - `go vet ./...` — PASS.
    - `go test ./...` — PASS on rerun. First full-suite attempt hit a transient `internal/llm/anthropic` usage-count failure; the single failing test, the full package with `-count=1`, and the full suite rerun all passed.
- 2026-08-24 review-fix round:
  - Read this task and the `docs/engineering/GLOSSARY.md` **Skill vendor store** entry before editing.
  - Updated the current canonical glossary reference from `config.AppConfig.Skills.VendorStorageDir` to `config.TunablesConfig.Skills.VendorStorageDir`, matching this task's role-clear rename. Left historical task/audit prose untouched.
  - Verification:
    - `find . -path './.git' -prune -o -path './.claude' -prune -o -path './vendor' -prune -o -name '*.go' -exec grep -nE 'config\.(Config|AppConfig)\b' {} +` — no matches in current worktree Go.
    - `find docs/engineering -name '*.md' ! -path 'docs/engineering/orchestrator-kickoffs/*' ! -path 'docs/engineering/TASKS.md' -exec grep -nE 'config\.(Config|AppConfig)\b' {} +` — no matches in current canonical engineering docs.
    - `find TASKS docs/engineering -name '*.md' -exec grep -nE 'config\.(Config|AppConfig)\b' {} +` — remaining matches are historical task/audit/kickoff/legacy-task prose only, left as instructed.
    - `go test ./internal/config/... -v` — PASS.
    - `go build ./cmd/nanite/` — PASS.

## Review notes

- 2026-08-24 fresh re-review PASS. Verified the AD-20 rename is complete in
  active Go (`RuntimeConfig`, `TunablesConfig`) and the stale canonical
  glossary reference now uses `config.TunablesConfig.Skills.VendorStorageDir`.
  No active Go or canonical engineering-doc references to `config.Config` or
  `config.AppConfig` remain outside historical/audit/task prose. Targeted
  config tests, `go vet`, `go build ./cmd/nanite/`, and broader build checks
  passed.
