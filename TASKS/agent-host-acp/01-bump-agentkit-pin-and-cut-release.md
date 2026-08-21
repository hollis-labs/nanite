# Bump go-agent-wrapper's agentkit pin, cut a real release

**Phase:** 1 — Host foundation (`TASKS/agent-host-acp`)
**Status:** not-started
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
