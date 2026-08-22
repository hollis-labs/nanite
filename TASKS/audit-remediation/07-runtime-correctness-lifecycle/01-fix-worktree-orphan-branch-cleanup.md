# Fix `worktree.gitManager.CleanupOrphaned` deleting the wrong (truncated) branch name

**Phase:** Wave 2 — Correctness, lifecycle, concurrency
**Status:** not-started
**Depends on:** none
**Touches:** `internal/worktree/manager.go` (`Create`, `CleanupOrphaned`); `internal/worktree/manager_test.go` (`TestCleanupOrphaned`). No other packages need code changes — `CleanupOrphaned` has exactly one production caller (see Context) and its call signature does not change.

## Context

`requires_architect_decision: false` — a clear, small, well-understood bug fix with no design ambiguity. Per the remediation guide's own explicit instruction for this folder's grouping ("Runtime correctness/lifecycle... Prioritize observable behavioral defects such as wrong orphan branch cleanup and unbounded retained job state over informational idempotency/comment issues"), this is the **highest-priority task in this folder** — the clearest real, observable behavioral defect among the five findings reviewed together here.

### Findings addressed
- `GO-RUNTIME-003` — severity **medium**, confidence **high**. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.13; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-RUNTIME-003`.

### Root cause

`CleanupOrphaned` independently re-derives the branch name for a given session ID instead of sharing one code path with `Create` (the function that actually made the branch). The two derivations disagree:

- `Create` (`internal/worktree/manager.go:77-105`) builds the branch name from the **full** session ID: `branch := "worker-" + sessionID` (`manager.go:86`).
- `CleanupOrphaned` (`internal/worktree/manager.go:142-194`) independently truncates the session ID to 8 characters before building the branch name:

  ```go
  short := sessionID
  if len(short) > 8 {
      short = short[:8]
  }
  branch := "worker-" + short
  ```

  (`manager.go:166-170`)

Session IDs in production are full ULIDs (26 characters), so for essentially every real session, `short[:8]` never equals the full `sessionID` `Create` used — the reconstructed branch name never matches the branch `Create` actually made. `CleanupOrphaned` then runs `git branch -D branch` against this wrong name (`manager.go:181-183`) and discards the command's result entirely (`cmd.Run() // best-effort`) — so the delete fails silently every time, and nothing anywhere reports it. This is `GO-RUNTIME-003`'s `duplication` category tag in `findings.json`: the branch-naming rule exists in two places and only one of them is correct.

### Current behavior

`CleanupOrphaned`'s directory removal is unaffected by this bug — it operates on `wtPath := filepath.Join(m.baseDir, sessionID)` (`manager.go:165`), built from the untruncated `sessionID` directly, so directories are removed correctly. Only the branch-name computation is wrong. This matters because `CleanupOrphaned` runs unconditionally on **every** `nanite serve` daemon boot, at `cmd/nanite/main.go:709-714`:

```go
// Clean up orphaned worktrees from previous runs.
if container.Worktrees != nil {
    if cleaned, wtCleanErr := container.Worktrees.CleanupOrphaned(nil); wtCleanErr != nil {
        slog.Warn("worktree orphan cleanup", "err", wtCleanErr)
    } else if cleaned > 0 {
        slog.Info("worktree cleanup: removed orphaned worktrees", "count", cleaned)
    }
}
```

This is `CleanupOrphaned`'s **only production call site** — called with a `nil` `activeSessionIDs` map, so every worktree directory found under the base dir on boot is treated as orphaned (the `if activeSessionIDs != nil && activeSessionIDs[sessionID]` guard at `manager.go:161` never skips anything when the map is `nil`). Every crash or restart that leaves a worktree behind gets its directory correctly removed but its branch permanently orphaned in the host git repo — a stray branch that accumulates unboundedly across restarts/crashes, forever, with `slog.Info("worktree cleanup: removed orphaned worktrees", ...)` misleadingly implying a full cleanup happened.

The existing regression test, `TestCleanupOrphaned` (`internal/worktree/manager_test.go:125-157`), creates two worktrees (`"active-session"`, `"orphan-session"`), runs `CleanupOrphaned` with only `"active-session"` marked active, and asserts `cleaned == 1` plus that `List()` no longer contains the orphan — but it never inspects git branch state at all, so it structurally cannot catch this bug. Notably, even this test's own session ID `"orphan-session"` (14 characters) is long enough to trigger the truncation mismatch (`"orphan-session"[:8]` = `"orphan-s"`, not equal to the full string) — the test would already have caught this if it had asserted on the branch.

### Desired invariant

`CleanupOrphaned` must delete the same branch name `Create` actually created for a given `sessionID` — branch-name derivation must be a single source of truth, not independently re-derived with different logic in two places. After `CleanupOrphaned` processes a genuinely orphaned worktree, the branch `Create` made for that session must actually be gone from the host repo (`git branch --list "worker-<sessionID>"` returns empty), not just the directory.

## What to do

1. Eliminate the truncation and make branch-name derivation identical between `Create` and `CleanupOrphaned`. Prefer extracting a small shared helper (e.g. `func workerBranchName(sessionID string) string { return "worker-" + sessionID }`) used by both `Create` (replacing the inline `"worker-" + sessionID` at `manager.go:86`) and `CleanupOrphaned` (replacing the `short`/truncation block at `manager.go:166-170`) — a shared helper makes a future re-divergence structurally impossible, not just currently fixed, which matches the guide's "fix every sibling path" principle applied at the smallest possible scale (two call sites, one package).
2. Re-verify the current line numbers before editing — this task cites `manager.go:86` (`Create`) and `manager.go:166-170` (`CleanupOrphaned`) from direct reading during this task's authoring pass; confirm they haven't shifted from unrelated churn before assuming them exact.
3. Leave `Cleanup` (the single-session path, `manager.go:107-140`) untouched — it already reads the branch name from the stored `*Worktree` record (`wt.Branch`, set at creation time in `Create`) rather than re-deriving it, so it is not affected by this bug.
4. Do not change the `cmd.Run() // best-effort` discard-return-value pattern on the `git branch -D` / `git worktree remove` / `git worktree prune` calls (`manager.go:173-175`, `181-183`, `189-191`) — that best-effort design for a boot-time sweep is a deliberate, separate choice from the finding here, which is specifically about the wrong branch name being targeted, not about how failures of a correctly-targeted delete are handled.

## Tests required

- Extend `TestCleanupOrphaned` (`internal/worktree/manager_test.go:125-157`) to assert the actual git branch is gone after `CleanupOrphaned` runs, not just directory/`List()` state — e.g., run `git branch --list "worker-orphan-session"` inside `repoDir` (mirroring the `exec.Command("git", ...)` pattern `manager.go` itself already uses) and assert the output is empty after `CleanupOrphaned`, and non-empty before it runs (to prove the test actually exercises branch creation, not a vacuous check).
- Confirm the extended assertion fails against the pre-fix code and passes after the fix — this is the whole point of the task: the existing test structurally could not have caught `GO-RUNTIME-003`, and the new assertion must actually reproduce it.
- Keep the existing assertions (`cleaned` count, `List()` state) — they remain valid, just insufficient alone.

## Prevention

The extended regression test is the primary prevention mechanism — it fails if branch-name derivation between `Create`/`CleanupOrphaned` ever re-diverges again. If the shared-helper direction (step 1) is taken, that additionally makes the specific failure mode (two independent, inconsistent derivations) structurally impossible rather than just currently correct.

## Verification

```bash
go build ./internal/worktree/...
go vet ./internal/worktree/...
go test ./internal/worktree/... -run TestCleanupOrphaned -v
```

Observable behavior required for PASS: the extended `TestCleanupOrphaned` fails on a `git stash` of the fix (proving it genuinely reproduces `GO-RUNTIME-003` against the unfixed code) and passes after; all other tests in `internal/worktree` continue to pass unchanged.

## Risk / rollback

Low risk, single-file (plus one test file) change, narrowly scoped. The fix can only ever make `CleanupOrphaned` target the *correct* branch instead of a wrong one it was silently failing against anyway — there is no plausible regression path where this makes cleanup worse than today's silent no-op. The one thing to double-check during implementation: confirm the corrected branch name cannot collide with a still-active session's branch under the boot-time sweep's `nil`-map "treat every directory as orphaned" call pattern (it can't, by construction — `CleanupOrphaned` only iterates directories actually present under the worktree base dir, and an active session's directory wouldn't be a stray orphan directory in the first place unless something else is already wrong). Rollback is a single-commit revert.

## Done means

- [ ] `CleanupOrphaned` computes the exact same branch name `Create` used for a given `sessionID` (no truncation), ideally via one shared helper used by both.
- [ ] Extended `TestCleanupOrphaned` asserts the git branch itself is gone after cleanup, not just the directory.
- [ ] The extended test is confirmed to fail against pre-fix code and pass against the fix.
- [ ] `go build`, `go vet`, and `go test ./internal/worktree/...` all pass.
- [ ] No change to `Cleanup`'s (single-session) branch handling, which was already correct.

## Work log

<!-- Worker fills in: what was actually done, any deviation from plan and why. -->

## Review notes

<!-- Reviewer fills in: pass/fail, what was independently re-verified. -->
