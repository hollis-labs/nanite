# [Medium] Track A's fragments-engine removal mixed with P0 fixes creates a risky intermediate state

**Scope:** plan sequencing
**Topic:** plan-sequencing
**Date:** 2026-04-10

## Problem

Track A bundles three very different kinds of work: drift deletion (A.1), P0 bug fixes (A.2), fragments-engine removal (A.3), git repo rationalization (A.4). The gate (A.5) requires `go build`, `go vet`, `go test ./...` all clean before leaving Track A. In practice, executing A.1 + A.3 first (deletions) leaves the tree in a broken state until A.2 fixes are applied AND every cross-reference is updated AND tests are rewritten. If the execution agent hits a problem partway through, the repo is in an uncommittable state and rollback is painful.

## Evidence

Plan §3 (Track A) includes all of:

- **A.1 delete drift** — removes `nanite/internal/plugin/builtin/giphy/`, `/oembed/`, `nanite/plugins/giphy/`, `/oembed/`, `/session-stats/`, `framework/plugins/nanite/fragments-engine/`, `nanite/plugins/fragments-engine/`

- **A.2 P0 fixes** — Host.Shutdown deadlock, protocol version check, scaffold template imports. These are bugfixes, not deletions.

- **A.3 fragments-engine total removal** — delete the directory, delete the blank import from `allplugins/allplugins.go`, delete `chat.RegisterEnvelopeType("sprint-planning-review"|...)` references at `plugins/fragments-engine/plugin.go:50-52`, sweep emission sites at `self_tools_transport.go:575-586` and `:698-707`, delete frontend components (`SprintPlanningReviewCard.tsx`, etc.), update `scripts/generate-plugin-imports.mjs`, remove `config/envelopes.yaml` entries. Run full test suite after.

- **A.4 git-repo rationalization for support-ticket** — rename a GitHub repo, update local remote, delete a snapshot directory, delete an abandoned GitHub repo.

- **A.5 gate** — everything clean.

The ordering is implicit: "do all of A before leaving A." But the gate requires a working `go build` and `go test` at the end, which cannot be reached by any ordering that has the tree in a broken state between steps. Example: if A.1 deletes `internal/plugin/builtin/giphy/` first (as listed), then `allplugins.go` still has its blank import and the build breaks immediately, even though A.3 will eventually delete that import. The agent has to interleave A.1 and A.3 to keep the tree compilable.

Similarly A.3 requires deleting frontend components and core emission sites. Each is a separate commit-worthy change, and each needs its own validation. Lumping them as one track means "you have to finish everything before you can commit anything," which is a recipe for a giant unmergeable diff.

The section also mixes "mechanical cleanup" (which is safe and reversible) with "bugfixes" (which require care) with "remove a major subsystem" (which has far-reaching effects).

## Impact

- Execution agent writes a giant A-track diff, hits a problem at A.3, and has no clean rollback because A.1 already deleted files.
- Test suite is broken for the entire duration of Track A because A.3 touches core emission sites without touching the tests that call them.
- Git history for Track A is one giant commit ("execute plan track A"), losing the "delete drift" vs "remove fragments-engine" distinction that matters during code review and bisects.
- Any partial failure in A leaves the tree uncommittable; the next session starts from an unknown state.

## Recommendation

Restructure Track A as explicit sub-tracks with their own gates, in dependency order:

**A0 — scaffold template fix** (safe, no deletions). Fix scaffold imports (currently in A.2 third bullet). Commit.

**A1 — P0 bugfixes** (safe, surgical). Fix Host.Shutdown deadlock. Add protocol version check. Both fixes from A.2 first two bullets. Also fix UnloadPlugin deadlock (see finding 01 in this audit). Commit each separately.

**A2 — fragments-engine removal** (isolated major change, own commit). Full A.3 content. Starts from a clean tree. Ends with a clean test run. Commit.

**A3 — giphy/oembed drift cleanup** (mechanical). Delete `nanite/internal/plugin/builtin/giphy/`, `oembed/`, `nanite/plugins/giphy/`, `oembed/`, `session-stats/` (yaml orphans only; code lives elsewhere). Delete `framework/plugins/nanite/fragments-engine/`. Update `allplugins.go` to remove giphy and oembed imports. Fix any callers. Add "install giphy as subprocess plugin" to Track E as the only way giphy comes back. Commit.

**A4 — repo rationalization (ops, optional for execution blocker)** — git remote updates, GitHub repo renames. Can happen outside an execution session; it's ops work.

Each sub-track has its own gate. `go build` and `go test` must pass after each. An agent can commit after each and roll back to the last known-good state if a later step fails.

Also: the user's reviewer-context says `shadcn-ui` config typo and broken symlinks should NOT be touched by this audit. Track A.4 does similar repo housekeeping and should be called out as "defer until beta ships" unless it's truly blocking.

## References

- Plan §3 (Track A) — bundled-cleanup section
- Reviewer-context — "prioritize ship blockers" guidance
- Finding 01 in this audit — the UnloadPlugin companion fix that belongs in A1
