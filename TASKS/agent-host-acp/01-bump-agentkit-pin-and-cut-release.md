# Bump go-agent-wrapper's agentkit pin, cut a real release

**Phase:** 1 — Host foundation (`TASKS/agent-host-acp`)
**Status:** reviewed
**Depends on:** none
**Touches:** `libs/go-agent-wrapper/go.mod`, `libs/go-agent-wrapper/go.sum`, `libs/go-agent-wrapper/CHANGELOG.md`. Repo: `libs/go-agent-wrapper` (sibling, NOT Nanite).

## Context

`docs/engineering/architecture/16-agent-host.md` names this as a real gap: go-agent-wrapper's
`go.mod` pins `agentkit v0.1.0`, while every real consumer (Nanite, Torque) runs `v0.3.0` and
Tether runs `v0.2.0`. The doc frames it as "a compat pass, not a rewrite," citing a renamed
symbol (`RenderFrontEnd`→`MissingPolicy`) from `agentkit/CHANGELOG.md` as the thing to check.

**This planning session verified the actual scope directly, and it's smaller than "a compat
pass" implies.** Confirmed facts, not assumed:

- `go-agent-wrapper/go.mod:5` declares `require github.com/hollis-labs/agentkit v0.1.0`, but
  `go.mod:17-24` also carries an **active local `replace` block**:
  ```go
  replace (
      github.com/hollis-labs/agentkit => ../agentkit
      github.com/hollis-labs/go-harness-filters => ../go-harness-filters
      github.com/hollis-labs/go-runtime-events => ../go-runtime-events
  )
  ```
  So the code **on disk today already compiles against `../agentkit`'s HEAD** (`git describe`
  in `libs/agentkit` returns `v0.3.0-1-g5b8aaad` — one docs-only commit past the `v0.3.0`
  tag), not the declared `v0.1.0`. `go build ./...` in `go-agent-wrapper` succeeds cleanly
  right now, via the replace. The `v0.1.0` pin is stale/aspirational, not what's actually
  compiled against.
- **`go-agent-wrapper` only ever imports `agentkit/agentsessions`** — a repo-wide grep for
  `agentlaunch\.|agentsessions\.|agentruntime\.|agentcontext\.|broker\.` found zero hits for
  anything except `agentsessions`. The renamed symbols 16-agent-host.md cites
  (`RenderFrontEnd`→`MissingPolicy`, `FrontEndAutonomous`→`PolicyError`,
  `FrontEndInteractive`→`PolicyCollect`, `RenderRequest.FrontEnd`→`RenderRequest.OnMissing`,
  the `v0.2.0` `PreparedPlantContext` field renames) are all in `agentlaunch` — a package
  `go-agent-wrapper` never touches. A repo-wide grep for every one of those symbol names
  across `go-agent-wrapper` returned zero hits.
- Directly diffed the package that matters: `git diff v0.1.0 v0.3.0 -- agentsessions/` inside
  `libs/agentkit` produces **zero output** — the `agentsessions` package is byte-for-byte
  unchanged across those two tags.
- `go-runner` is a separate, smaller gap 16-agent-host.md also names — **already resolved**:
  go-agent-wrapper's `go.mod:13` and Nanite's own `go.mod:40` both already pin `go-runner
  v0.5.0` (indirect in both). The doc's own text frames the `v0.5.0`→`v0.6.0` gap as
  "shared... by every app in the portfolio, not go-agent-wrapper-specific" — out of scope
  for this task and this batch; no action needed here.

**Net: bumping the declared pin from `v0.1.0` to `v0.3.0` requires zero source changes in
`go-agent-wrapper`.** The real work this task does is making the declared dependency match
reality and giving Nanite (task `03`) something better than a permanent local-checkout
`replace` to depend on — go-agent-wrapper has exactly 2 commits total (`e9a84c3` "Initial
commit: go-agent-wrapper v0.1.0", `a248ab4` "Strengthen headless wrapper core") and exactly
one tag (`v0.1.0`, pointing at the first commit). HEAD is untagged.

## What to do

1. In `libs/go-agent-wrapper/go.mod`: change `require github.com/hollis-labs/agentkit
   v0.1.0` to `v0.3.0`. Confirm `go build ./...` and `go test ./...` stay clean with the
   `replace` block still in place (it will be — this task does not remove the replace,
   see step 3).
2. Update `require github.com/hollis-labs/go-runner v0.5.0` — confirm this is already correct
   (it should be); if the file disagrees with what you find in go.mod, reconcile it, but no
   version change is expected.
3. **Do not remove the local `replace github.com/hollis-labs/agentkit => ../agentkit`
   block yet.** Task `03` (adding go-agent-wrapper as a Nanite dependency) needs go-agent-
   wrapper itself reachable via a local `replace` from Nanite's own `go.mod` too (same
   pattern as the existing `go-modelsdev`/`go-envelopes` precedent) — until go-agent-wrapper
   has a real tagged release Nanite can pin a module-proxy version against, both replaces
   stay. Note this explicitly in your Work Log so task `03`'s worker isn't surprised to find
   the replace still there.
4. Cut a real version tag. `HEAD` (`a248ab4`) is one commit past what's functionally a
   `v0.3.0`-agentkit-compatible state — after this task's go.mod change lands, tag it. Decide
   a version number consistent with the project's stated versioning discipline (this repo's
   own `v0.1.0`/`v0.2.0`/`v0.3.0` progression in `agentkit`'s CHANGELOG is the closest
   precedent) — a reasonable default is `v0.2.0` (one real dependency-version bump since
   `v0.1.0`, no breaking API change to go-agent-wrapper's own exported surface). If genuinely
   unsure whether this warrants `v0.2.0` vs. some other number, that's a judgment call to
   document in your Work Log, not an escalation — no operator sign-off needed for a version
   number on a zero-adopter library.
5. Add a `CHANGELOG.md` entry (create the file if it doesn't exist — check first) recording
   the agentkit pin bump and the version tag, following `agentkit/CHANGELOG.md`'s own format
   as the closest in-repo precedent.

## Done means

- `libs/go-agent-wrapper/go.mod` declares `agentkit v0.3.0`.
- `go build ./...` and `go test ./...` clean in `libs/go-agent-wrapper`, with the `agentkit`
  local `replace` still present (per step 3).
- A real git tag exists past `a248ab4`, reflecting this change.
- `CHANGELOG.md` documents the bump.

## Work log

All work done in the isolated worktree `libs/go-agent-wrapper-wt-task01` on branch
`agent-host-acp/task-01-bump-agentkit-pin`, checked out from `libs/go-agent-wrapper`'s `main`
at `a248ab4`. Nothing touched in `libs/go-agent-wrapper-wt-task02`, the main
`libs/go-agent-wrapper` checkout, or this repo (Nanite) beyond this task file.

**Verified the task file's premises directly before touching anything**, all confirmed as
stated:
- `go.mod`'s `agentkit v0.1.0` require line vs. an active `replace ... => ../agentkit` that
  was already resolving against `libs/agentkit`'s HEAD.
- `libs/agentkit`'s tags are `v0.1.0`/`v0.2.0`/`v0.3.0`, HEAD (`5b8aaad`) is one docs-only
  commit past `v0.3.0`.
- `go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l .` all clean pre-change.

**What I did:**
1. `go.mod`: changed the `agentkit` require from `v0.1.0` to `v0.3.0`. No source changes
   needed anywhere in `go-agent-wrapper` — confirmed clean `go build ./...` / `go vet ./...`
   / `go test ./...` (all 11 packages, same as before) with the `agentkit` local `replace`
   still in place.
2. `go-runner`: already `v0.5.0` (indirect) — matched what the task expected, no change made.
3. Left the `replace github.com/hollis-labs/agentkit => ../agentkit` block in place, per the
   task's explicit instruction. **One thing worth flagging for task `03`'s worker and for the
   Orchestrator**: the pre-existing comment directly above that `replace` block in `go.mod`
   read "Local-development replaces — DO NOT COMMIT to a tagged release... Drop (or comment
   out) before tagging a release" — i.e. the in-repo comment's own guidance is the opposite of
   what this task instructs. Per this project's own decision-vs-rationale rule, the task
   file's instruction is settled (keep the replace, tag anyway) regardless of whether the
   comment's rationale holds up, so I did exactly that — but I also rewrote the comment itself
   (in scope: `go.mod` is on the task's `Touches` list) so it no longer tells the next reader
   to do the opposite of what actually happened. The new comment explains the agentkit replace
   is intentionally staying past this tag for the Nanite-side reason task `03` cites, while
   `go-harness-filters`/`go-runtime-events` keep the original "drop before tagging" discipline
   (nothing depends on those two staying — both have their own `v0.1.0` tags already matching
   their `require` lines). **Confirming explicitly for task `03`: the `agentkit` local replace
   is still present in `go-agent-wrapper`'s `go.mod` on this tag — do not expect it gone.**
4. Cut the tag as an annotated tag: `v0.2.0`. Reasoning: `Config` in `wrapper/wrapper.go`
   gained two new optional fields (`SandboxProfile`, `HeartbeatInterval`) since `v0.1.0` —
   additive, keyed-literal-compatible, not a breaking change to any exported signature — plus
   the dependency bump itself. That's a minor-version bump under the project's own
   `v0.1.0`/`v0.2.0`/`v0.3.0` precedent (`agentkit/CHANGELOG.md`), not a major one. This was a
   judgment call, documented here per the task's own instruction, not escalated — no
   operator sign-off needed for a version number on a zero-adopter library.
5. `CHANGELOG.md` **already existed** (task file said "create it if it doesn't exist — check
   first"; it exists, one prior entry: `v0.1.0 — 2026-05-26`). Added a `v0.2.0` entry above it.
   One deliberate scope expansion beyond "record the agentkit pin bump and the version tag":
   the commit immediately preceding my change, `a248ab4` ("Strengthen headless wrapper core" —
   real feature work: `Config.Filters` now actually wired end-to-end via
   `wrapper/filter_payload.go`/`io_streams.go`, a new `filters.RepairPipeline`, a new
   `translateProviderEvent` provider-event bridge via a new `TypedEventCallback`, new
   `Config.SandboxProfile`/`Config.HeartbeatInterval` fields, a new `JsonRpcRequestHook` wired
   to `agent.permission_requested`/`resolved`, and `policy.ModeApproval` now mapping to its own
   event kind instead of collapsing into `ModeBlock`) — had **never been changelogged**, and it
   ships under this same `v0.2.0` tag. Documenting only the pin bump and leaving that
   substantial, already-committed, untagged work with zero release notes would have made the
   changelog inaccurate about what `v0.2.0` actually contains, so the entry covers both under
   separate "Changed" (pin bump + the `ModeApproval`/turn-bookkeeping behavior changes) and
   "Added" (the new Filters/TypedEventCallback/SandboxProfile/HeartbeatInterval/
   JsonRpcRequestHook surface) headings, with a closing note attributing the "Added" section's
   origin to `a248ab4` rather than to this task's own diff. `README.md`/`ROADMAP.md` still say
   "Status (v0.1.0, 2026-05-26)" and weren't touched — out of this task's `Touches` scope
   (`go.mod`, `go.sum`, `CHANGELOG.md` only); worth a follow-up if anyone reads the README
   looking for current status.
6. `go.sum`: untouched — `git diff` after the `go.mod` edit showed zero `go.sum` changes.
   Expected: the `replace`d module resolves from the local filesystem, which bypasses
   checksum-DB verification for that module entirely.

**Build/test status:** `gofmt -l .` clean, `go build ./...` clean, `go vet ./...` clean,
`go test ./...` clean (11 packages, same set as before: `activity`, `adapters`,
`adapters/claude`, `adapters/codex`, `adapters/opencode`, `classifybridge`, `filters`,
`plant`, `policy`, `sandbox`, `wrapper`) — verified both immediately after the `go.mod` edit
and again at the final tagged commit.

**Final state on branch `agent-host-acp/task-01-bump-agentkit-pin`:**
- Commit `43847bf` ("Bump agentkit pin to v0.3.0, cut v0.2.0"), one commit past `a248ab4`,
  touching only `go.mod` and `CHANGELOG.md`.
- Annotated tag `v0.2.0` on that commit.
- Working tree clean. Not merged to `main`, not pushed — left for the Orchestrator per this
  batch's process notes.

**Orchestrator merge note (2026-08-21):** Independently re-verified this task's claims
directly (git log/diff/tag/build/vet/test in the worktree) before merging — all confirmed.
Merged to `libs/go-agent-wrapper`'s `main` via `git merge --no-ff` as commit `3600d24`, after
task `02`'s independent verification, alongside task `02`. Post-merge `go build`/`go vet`/
`go test`/`go test -race` all clean. Worktree and branch removed after merge.

## Review notes

Fresh Reviewer (no shared context with this task's worker), 2026-08-21 — **PASS**, reviewed
together with task `02` as Phase 1's logical section, against `libs/go-agent-wrapper`'s merge
commit `3600d24`. Independently re-verified: `git diff v0.1.0 v0.3.0 -- agentsessions/` inside
`libs/agentkit` is genuinely byte-empty (confirms "zero source changes needed"); `a248ab4`
genuinely never touched `CHANGELOG.md` (confirms the "previously unchangelogged" claim behind
folding that commit's feature work into the v0.2.0 entry); `go.sum` diff is genuinely empty;
the `replace` block and its rewritten comment are present and accurate; the `v0.2.0` version
judgment call is reasonable (additive fields, dependency bump, no breaking exported-API
change). One non-blocking gap noted: `README.md`/`ROADMAP.md` still say "v0.1.0" — correctly
out of this task's `Touches` scope, already flagged above as a follow-up. Independently ran
`go build ./...`/`go vet ./...`/`go test ./...`/`go test -race ./...` clean. No fixes needed.
