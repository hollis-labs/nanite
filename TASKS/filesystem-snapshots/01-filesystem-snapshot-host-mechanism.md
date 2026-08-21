# Filesystem snapshot host mechanism (FilesystemSnapshotProvider + ShadowGit)

**Phase:** 1 — Host mechanism (`TASKS/filesystem-snapshots`)
**Status:** not-started
**Depends on:** conceptually continues the host boundary from `docs/engineering/
architecture/16-agent-host.md`. Does **not** depend on that batch's `wrapper.Wrapper`
session-launch mechanism (currently escalated in `TASKS/agent-host-acp/06` — see that task's
own status note) — capture/diff/preview/restore is an independent capability with no
coupling to session spawn/launch. Safe to build in parallel with `agent-host-acp`'s Phase 2
migration; the Orchestrator should sequence this wherever it fits given the other batch's
current state, not block on it.
**Touches:** new package in `libs/go-agent-wrapper` (e.g. `snapshot/`), alongside
`plant/`/`sandbox/`/`policy/`. Repo: `libs/go-agent-wrapper` (sibling, NOT Nanite).

## Context

`docs/engineering/architecture/18-filesystem-snapshots.md` — an undo/audit primitive for the
filesystem state an agent's granted paths hold: capture a project tree before/after each
step, diff two captures, selectively restore specific paths. **Not** universal process
rollback, **not** a backup system, and explicitly must never be conflated with session/
conversation-state restoration (Glass-4, compaction, handoffs — those stay
`docs/engineering/architecture/06-session-lifecycle-and-recovery.md`'s territory,
zero filesystem awareness today, and this feature must keep it that way).

**Naming discipline, required, not optional**: the doc is explicit that `checkpoint` already
means something specific and different in this codebase (`agentkit/agentruntime/checkpoint`'s
provider-session resume hints; Tether's "resume a logical agent from its checkpoint").
This feature is **"snapshot,"** never "checkpoint." Add a `GLOSSARY.md` entry for this
filesystem sense, explicitly distinguished from both `checkpoint` and Glass-4's own incidental
use of the word "snapshot" (the "agent-self-authored continuity snapshot" entry already in
`GLOSSARY.md`) — three different things reachable by casual use of "snapshot" today; this
task's entry is the one that gets to own the term going forward for filesystem state
specifically.

**The interface, specified directly by the doc — not this task's own design**:

```go
type FilesystemSnapshotProvider interface {
    Capture(ctx context.Context, targets []Target) (SnapshotSet, error)
    Diff(ctx context.Context, from, to SnapshotSet) (Diff, error)
    Preview(ctx context.Context, set SnapshotSet, paths []string) (Preview, error)
    Restore(ctx context.Context, set SnapshotSet, paths []string) error
}
```

with `ShadowGit` as the first (only) concrete implementation. Named around intent
(`snapshot_capture`/`snapshot_diff`/`snapshot_restore`), not mechanism
(`git_shadow_commit`), matching this codebase's existing driver/adapter naming discipline —
leaves room for a non-git implementation later (APFS snapshots, overlayfs/btrfs/ZFS,
copy-on-write workspaces) without needing to build one now.

**Host boundary** (continuing 16-agent-host.md's policy/mechanism split, same shape as
`policy.Engine`/`policy.Store`): the host (this package) owns capture, diff, preview,
restore, target resolution, and shadow-store lifecycle/cleanup mechanism. The product/config
layer (Nanite — task `02`) owns *when* to capture and *how long* to retain.

**Reference implementation to build from — OpenCode's shadow git**, verified by the doc
directly against OpenCode's own current docs:
- A separate internal Git object database in OpenCode's own data directory — never touches
  the real repo's `.git`/index, never creates real commits/branches there.
- Capture happens **per model-step**: immediately before the model call and after each
  cleanly-completed step. Finer-grained than per-turn — a turn with several tool calls gets
  several capture pairs, enabling partial-turn undo. (The actual call-site wiring for *when*
  to invoke `Capture` is Nanite-side, task `02` — this task only needs `Capture` to be safely
  callable at that cadence, i.e. fast and non-blocking.)
- **Exclusions**: ignored files (respect `.gitignore`), files outside the active/session
  scope, changes to Git metadata itself, individual untracked files over 2 MiB.
- Restore is selective and path-scoped, never a whole-tree checkout by default.
- **Explicit disclaimers to carry into this implementation verbatim**: capture is
  best-effort and must never block the actual work (a failed capture doesn't stop a model
  step — this codebase's existing "hints, not control" principle); it is **not a concurrency
  lock** (external edits between capture and restore aren't protected against beyond the
  lightweight hash-check in task `03`); snapshots are **not "secret-free"** — they hold
  complete file contents, so anything sensitive that ever touched a captured file persists
  in the shadow store until cleanup. Document this plainly; don't assume it away.
- **Real field bugs to design around from day one** (found by the doc's own verification
  against OpenCode's issue history): a whole-tree restore touching every file's mtime,
  breaking editor reload state and build-tool caches (argues for selective-only restore,
  never whole-tree-by-default — already the settled design below); a shadow directory not
  initialized before first capture, silently producing zero snapshot data (argues for
  **failing loudly** on a capture/init problem, never a silent no-op); a report of the
  snapshot service corrupting the real repo's git index when invoked from a pre-commit hook
  (argues for being paranoid about `cwd`/working-directory isolation whenever shadow
  operations shell out to `git` — the shadow git object database must never share a working
  directory or index with the real repo's own git state).

**What's already in Nanite this maps onto** (informs `Target`'s shape, even though target
*derivation* is task `02`'s job, not this task's): `projects.repo_path` +
`agent_projects` is exactly a `SnapshotSet`-across-configured-roots — one logical
snapshot ("turn 42") is one shadow-git tree hash *per project root* the agent is scoped to,
not one synthetic repo spanning unrelated roots. Design `Target`/`SnapshotSet` with this
per-root shape in mind, not a single flat path list.

## What to do

1. Define the `FilesystemSnapshotProvider` interface (as specified above — exact Go types for
   `Target`/`SnapshotSet`/`Diff`/`Preview` are this task's own call, but must support the
   per-project-root shape above) in a new `snapshot/` package (or similarly named — check
   `libs/go-agent-wrapper`'s existing package-naming convention) alongside `plant/`,
   `sandbox/`, `policy/`.
2. Implement `ShadowGit`: a separate internal git object database (own data directory, never
   touching any real repo's `.git`), with `Capture`/`Diff`/`Preview`/`Restore` implementing
   the interface. Apply all four exclusion rules from the Context (`.gitignore`, out-of-scope
   paths, git-metadata changes, >2MiB untracked files).
3. Restore must be selective/path-scoped only — no whole-tree-restore code path should exist
   at all, per the mtime/editor-reload field bug above (this isn't a config default to get
   right, it's a capability that shouldn't exist).
4. Fail loudly on any shadow-store init/capture infrastructure problem (e.g. the shadow
   directory itself can't be created/opened) — never silently no-op. This is distinct from
   "capture is best-effort" (a single capture attempt failing mid-turn doesn't block the
   turn) — the two are not the same failure mode and must not be conflated in the
   implementation.
5. Implement a TTL/cleanup mechanism the host exposes per-`Target` or per-`SnapshotSet` (the
   *mechanism* only — actual retention duration is Nanite/config-supplied, task `02`).
6. Be paranoid about working-directory isolation on every `git` shell-out — never let a shadow
   operation run with a `cwd` inside the real repo's own working tree in a way that could
   affect its index, per the pre-commit-hook corruption field bug.
7. Add the `GLOSSARY.md` entry for "snapshot" (filesystem sense), explicitly distinguishing it
   from `checkpoint` and from Glass-4's existing "snapshot" usage — check `GLOSSARY.md` before
   choosing final naming for any exported type/function.
8. Document the security/retention consideration directly (shadow store holds full file
   contents indefinitely until cleanup; needs equivalent access-control treatment to other
   host-owned artifacts like the boot dir and sandbox) in this package's own doc comment.

## Done means

- `FilesystemSnapshotProvider` interface + `ShadowGit` implementation exist in
  `libs/go-agent-wrapper`, matching the specified signature.
- Per-model-step-rate capture is fast enough to be non-blocking in practice (a real
  benchmark/test at realistic file-tree sizes, not just a correctness test).
- All four exclusion rules enforced and tested.
- No whole-tree-restore code path exists.
- Init/capture-infrastructure failures are loud (return a real error, logged); mid-turn
  single-capture failures never propagate as a turn-blocking error (covered by tests for
  both failure modes, not just one).
- Working-directory isolation from any real repo verified by a test that runs shadow-git
  operations inside a directory that also has a real `.git`, confirming zero effect on it.
- `GLOSSARY.md` updated.
- `go build ./...` / `go test ./...` clean in `libs/go-agent-wrapper`.
