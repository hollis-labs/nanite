# Nanite capture policy: target derivation, per-step cadence, retention

**Phase:** 2 — Product policy & wiring (`TASKS/filesystem-snapshots`)
**Status:** not-started
**Depends on:** `01` (host mechanism)
**Touches:** `internal/runtime/agent/sandbox_profile.go` (read, not modified — target
derivation reads from it), a new call site in wherever Nanite's per-turn/per-step execution
loop lives (likely `internal/service/chat_generate.go` or `internal/runtime/agent/agent.go`
— confirm the exact real step boundary at implementation time, don't assume), plus whatever
config surface retention duration needs. Repo: Nanite.

## Context

`docs/engineering/architecture/18-filesystem-snapshots.md`'s explicit mechanism/policy split:
task `01` (the host) owns *how* to capture/diff/preview/restore; Nanite (this task) owns
*when* to capture and *how long* to retain. Two settled design calls this task implements
directly, not open questions:

- **Snapshot targets derive from the sandbox `FS.Write` allowlist, not a separately
  configured list** — "avoids two lists (what an agent may write, what gets snapshotted)
  silently drifting apart over time." The source is `buildSandboxProfile`
  (`internal/runtime/agent/sandbox_profile.go:24-47`, confirmed directly by this project's own
  earlier research into this file for the `agent-host-acp` batch): it appends `opts.Workdir`,
  `workspaceDir`, `bootDir`, and `naniteHomeDir()` (`$HOME/.nanite`) to `p.FS.Write`, deduped.
  Filter this list to **exclude the ephemeral entries** (`bootDir`, `naniteHomeDir()`) before
  deriving `Target`s — those aren't project content, snapshotting them would be pure noise
  (and `bootDir` is torn down per-session anyway, so a snapshot of it is meaningless once the
  session ends).
- **Capture granularity: per model-step** — immediately before the model call and after each
  cleanly-completed step, matching OpenCode's own field-tested choice (task `01`'s Context has
  the full rationale — enables undoing part of a turn, not just the whole thing). Find the
  real step-boundary call site in Nanite's actual turn-execution code before assuming a
  specific file/function — this project's own turn loop may have moved since this doc was
  written; verify against current code, not this task file's own guess at a location.
- **Applies uniformly regardless of worktree isolation.** `internal/worktree.Manager` already
  gives worker sessions a real, disposable `git worktree add`-based checkout — a second,
  independent safety mechanism (whole-branch-discard) operating at a different granularity
  than shadow-git (per-path-per-turn undo). A worktree-isolated session still benefits from
  undoing one bad turn without discarding the whole worktree's other legitimate work — do not
  skip snapshot capture for worktree-isolated sessions on the theory that worktree isolation
  already covers this; verify `internal/worktree.Manager`'s current shape directly before
  wiring, this task file doesn't have a verified citation for it.
- **Capture is best-effort and must never block a turn/step from proceeding** — a failed
  capture call is logged, not surfaced as a turn failure. This is a hard requirement, not a
  style preference: wire the capture call so its own error path can never propagate up into
  the turn/step's own success/failure determination.
- **Retention/cleanup**: this task supplies the actual retention duration (a real, configured
  TTL) consumed by task `01`'s exposed cleanup mechanism — same policy-vs-mechanism split as
  `policy.Store`. Where this config lives (a fixed constant, an env var, a per-project/per-
  agent DB-configurable value) is this task's own call; a fixed sensible default with no
  per-agent override is an acceptable v1 scope unless a concrete reason to make it
  configurable per-agent surfaces during implementation — don't over-build a config surface
  nothing asks for yet.

**Explicitly out of scope for this task** (per the architecture doc's own "What's genuinely
still open"/"What's cut" sections): real conflict/concurrency handling beyond task `03`'s
lightweight hash-check (deferred — becomes more important once Teams' multiple slots can
touch the same project concurrently, revisit then, not now); any GUI/frontend surface
(deferred, frontend concern, matches this project's standing no-frontend-in-any-phase
discipline); portfolio-sharing with Tether/Torque (the doc explicitly leaves this to "the
planner" — this task, and this whole `TASKS/filesystem-snapshots/` folder, scopes Nanite-first
only, matching the same call `TASKS/agent-host-acp/README.md` already made for the host/ACP
batch — whether/when Tether or Torque adopt this is a portfolio-level call made in those
apps' own planning, not blocked by or blocking anything here).

## What to do

1. Write a target-derivation function that reads `buildSandboxProfile`'s resulting
   `FS.Write` list (or the same inputs it derives from) and filters out `bootDir`/
   `naniteHomeDir()` to produce task `01`'s `Target` list, grouped per-project-root per
   `projects.repo_path`/`agent_projects` (per task `01`'s Context on `SnapshotSet` shape).
2. Find the real per-step execution boundary in Nanite's current turn-execution code (verify
   directly, don't assume a file/function from this task's own guess) and wire a `Capture`
   call immediately before each model call and after each cleanly-completed step.
3. Wrap the `Capture` call so any error is logged and never propagates into the turn/step's
   own success/failure path — write a test that forces a capture failure and confirms the
   turn still completes normally.
4. Confirm capture fires the same way for worktree-isolated sessions as non-isolated ones —
   verify `internal/worktree.Manager`'s actual current behavior first, and write a test
   covering a worktree-isolated session's capture path specifically.
5. Supply a real, configured retention duration to task `01`'s cleanup mechanism — a fixed
   sensible default is acceptable v1 scope (see Context).

## Done means

- Snapshot targets are derived from `buildSandboxProfile`'s `FS.Write` allowlist minus
  ephemeral entries, verified by a test comparing the derived target list against a real
  sandbox profile's actual allowlist.
- Capture fires at the correct per-step cadence (before model call, after each cleanly-
  completed step) — verified by a test counting capture calls across a multi-step turn, not
  just confirming it fires at least once.
- A forced capture failure never blocks or fails the turn/step it's attached to — covered by
  an explicit test.
- Worktree-isolated sessions still get capture calls — covered by an explicit test.
- A real, configured retention duration reaches task `01`'s cleanup mechanism.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` clean.
