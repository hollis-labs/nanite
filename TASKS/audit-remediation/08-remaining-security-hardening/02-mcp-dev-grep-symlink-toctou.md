# MCP dev-tool grep/glob walk callbacks don't re-validate per-entry symlinks

**Phase:** Wave 3 — Remaining security hardening (guide §4; sequenced 2026-08-21 — see the sequencing block below)
**Status:** implemented
**Depends on:** none
**Touches:** `internal/mcp/dev_tools.go` (`callGrep`, `callGlob`)
**Requires architect decision:** false

> **Divergence from `findings.json`:** the catalog flags this finding's `requires_architect_decision` as `true`. This task sets it to `false` because the audit itself already names two concrete, sufficient fix directions (see Proposed direction below) — the remaining choice is an implementer-level tradeoff (behavior-change-vs-cost), not an open architectural question. Escalate to architect review only if a reviewer disagrees with that framing.

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 3 — remaining security hardening · **Dispatch unit:** `W3`
> - **Depends on:** Wave 2 complete
> - **Blocks:** none
> - **Parallel-safe with:** `08/01`, `08/03`, `08/04`, `08/06`, `08/10`
> - **Gated on:** none
> - **requires_security_review:** true · **requires_regression_test:** true

## Findings addressed

- **GO-MCPTOOL-008** (medium severity, high confidence, security) — report §8.7.

## Context

**Root cause:** `resolveAllowed` correctly validates the top-level `dir` argument once, symlink-aware, via `pathsafe.ResolveUnder`, before `filepath.Walk` starts. But the `Walk` callback then calls `os.Open` on every discovered file (`dev_tools.go:690-871`, the `os.Open` call itself at line 779) with **no per-entry re-validation and no symlink check**. A symlink planted anywhere inside an already-granted directory is dereferenced, and its target's *content* is returned — a real boundary violation even though the boundary check itself was done correctly once, at the wrong granularity. `callGlob` has the identical structural gap but strictly lower severity, since it only returns metadata (paths), not file content.

**Trust classification (guide's Wave 3 instruction, applied explicitly):** exploiting this requires either (a) write access to the already-granted directory to plant the symlink, or (b) a pre-existing malicious symlink already sitting inside a directory scope that's been granted to the calling session. This is **agent-controlled** (an MCP dev-tool caller with an existing grant) combined with **OS-derived/internal** (the symlink itself is filesystem state, not network input) — **not** remotely triggerable via MCP input in the sense of an external network caller reaching it directly. This meaningfully lowers real-world urgency relative to a network-reachable bug at the same "medium" label; don't over-escalate the fix beyond what this boundary implies.

**Desired invariant:** every file the grep/glob walk actually opens — not just the walk's starting directory — must be confirmed inside the originally-granted directory tree at the moment it's opened; a symlink hop must not be able to redirect that open outside the grant.

## What to do

**Scope:** `internal/mcp/dev_tools.go`'s `callGrep` (primary, content-read risk) and `callGlob` (secondary, metadata-only risk) walk callbacks.

**Proposed direction** (audit names both; implementer picks one and logs the choice):

1. **Skip symlinked entries during the walk** — check `info.Mode()&os.ModeSymlink != 0` and exclude rather than open. Simplest, fully closes the gap, but changes behavior: symlinked files inside the grant (even ones that don't escape it) silently disappear from results.
2. **Re-run `pathsafe.ResolveUnder(grantedDir, discoveredPath)` per discovered file** immediately before `os.Open`. Preserves current behavior for symlinks that stay inside the grant, only rejects escapes; more code and higher per-file overhead on large trees, but more faithful to current semantics.

Given these dev tools are used interactively, weigh option 2's per-file overhead against option 1's simplicity — implementer's call per the task-file template's guidance on adjustable-not-locked decisions; log which was chosen and why.

**All production callers:** `callGrep` and `callGlob` are each independently reachable MCP dev-tool entry points. Confirm no other caller reuses the same walk helper before treating this as a two-site fix.

## Non-goals

Do not change `resolveAllowed`'s own top-level validation (already correct). Do not add a general filesystem-sandboxing layer beyond this specific walk callback.

## Tests required

- Regression test: plant a symlink inside a granted temp directory pointing to a file outside the grant; call `callGrep`; assert the symlinked file's content is **not** returned (skipped or errored cleanly, per whichever direction is chosen).
- Same for `callGlob`, asserting the path isn't listed.

## Prevention

No mechanism currently catches per-entry symlink escapes after a one-time top-level check — this is the same defense-in-depth gap class `pathsafe.ResolveUnder` was built to solve at entry points. Note in the fix's own comment that "walk callbacks must re-validate per entry, not just the walk root" as a reusable lesson, and spot-check `internal/mcp` for any other `filepath.Walk`-based tool with the same shape while in this area — if one is found, surface it rather than silently fixing it inside this task's scope.

## Verification

`go build ./...`; `go test ./internal/mcp/...` (new/updated `callGrep`/`callGlob` tests); `gosec ./internal/mcp/...` confirming the G122 hit at `dev_tools.go` is resolved or explicitly suppressed with a matching justification comment.

## Risk / rollback

Low — the fix only tightens what's readable, never expands it. Rollback is a straightforward revert. If option 1 is chosen, flag to reviewers that benign in-grant symlinks will stop appearing in results — a real, if minor, behavior change worth calling out explicitly.

## Done means

- [x] Chosen direction implemented in `callGrep` and `callGlob`
- [x] Regression tests above pass
- [x] `gosec` G122 finding at `dev_tools.go` resolved or justified
- [x] Spot-check confirms no other `filepath.Walk`-based MCP tool has the same gap (or, if found, it's flagged back rather than silently fixed here)

## Work log

- 2026-08-22: Chose proposed direction 2 (per-entry
  `pathsafe.ResolveUnder`) for both `callGrep` and `callGlob` because it
  preserves the existing behavior for symlinks whose targets remain inside
  the granted directory. The callbacks now resolve every matching file before
  inspecting it and perform the eventual stat/open through an `os.Root` rooted
  at the already-approved directory, so a filesystem swap between validation
  and use also cannot redirect the operation outside the grant. Top-level
  `resolveAllowed` behavior was left unchanged.
- Added regression coverage proving an outside-target symlink is neither read
  by `dev_grep` nor listed by `dev_glob`, plus positive coverage proving benign
  in-grant symlinks remain searchable/listed under the chosen approach.
- Enumerated `filepath.Walk`/`WalkDir` across the scoped MCP tool packages:
  `internal/mcp/dev_tools.go` has only the two production tool callbacks fixed
  here (`callGrep`, `callGlob`); no additional `internal/mcp`,
  `internal/mcpconfig`, `internal/mcpserver`, `internal/tool`, or
  `internal/selftools` walk-based tool was found. An expanded sibling search
  found `internal/toolclient/skills.go`'s operator-configured
  `LoadSkillsFromDir` loader; it is not an MCP tool entry point and is outside
  this task's stated package/scope, so it was surfaced but not changed.
- Verification: focused symlink regressions passed; `go test
  ./internal/mcp/... -count=1` passed; `go build ./...`, `go vet ./...`, and
  `go test ./...` passed. `gosec ./internal/mcp/...` no longer reports G122 at
  `dev_tools.go`; it exits nonzero on 18 pre-existing findings from other rules
  (including existing G703/G304/G301/G306 reports in `dev_tools.go`) that are
  outside this task.
- 2026-08-22 correction: added a narrow per-transport test hook at the exact
  boundary between per-entry `ResolveUnder` validation and the root-scoped
  stat/open. New deterministic grep and glob tests replace a validated regular
  file with an outside-target symlink at that boundary and prove `os.Root`
  blocks both the content read and metadata listing. The static grep escape
  fixture now puts its secret on line 2 after a nonmatching prefix, ensuring a
  vulnerable implementation fails for the intended leak instead of the
  unrelated pre-existing line-1 context panic. Mutation evidence: temporarily
  replacing `root.Stat`/`root.Open` with ordinary path-based `os.Stat`/`os.Open`
  made glob list `candidate.txt` and grep return `post-validation-secret`; the
  rooted operations were then restored with no mutation left. Post-restoration
  `go test ./internal/mcp/... -count=1`, focused `go test -race`, `go vet
  ./internal/mcp/...`, and `gosec -include=G122 ./internal/mcp/...` all passed.

## Review notes

<!-- Reviewer fills this in. -->
