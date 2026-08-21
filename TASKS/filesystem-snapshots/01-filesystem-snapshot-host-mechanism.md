# Filesystem snapshot host mechanism (FilesystemSnapshotProvider + ShadowGit)

**Phase:** 1 — Host mechanism (`TASKS/filesystem-snapshots`)
**Status:** implemented
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

## Work Log

Implemented entirely in `libs/go-agent-wrapper` (sibling repo), committed directly on top of
`main` at `7c65601` (tag `v0.3.0`) as commit `5c1a343`. New `snapshot/` package alongside
`plant/`/`sandbox/`/`policy/`: `doc.go`, `types.go`, `provider.go`, `shadowgit.go`,
`shadowgit_test.go`, `shadowgit_bench_test.go`.

**`Target`/`SnapshotSet` shape.** `Target{ID, Root, IncludePaths}` — `ID` is caller-supplied
and stable across captures (keys the shadow store via a `sha256(ID)`-derived directory name,
never the raw ID, so arbitrary caller strings can't do path traversal into the shadow-store
tree); `IncludePaths` is optional Root-relative scoping, expected to be handed in from a
sandbox write-allowlist by a later Nanite-side task, not derived here. `SnapshotSet{ID,
CapturedAt, Roots map[string]RootSnapshot}` is keyed by `Target.ID`, one `RootSnapshot`
(`TreeHash`/`CommitHash`/`Skipped`/`Err`) per project root — never a single synthetic tree
across roots, per the doc's `projects.repo_path` + `agent_projects` mapping.
`Diff`/`Preview`/`RestoreChange` follow the same per-Target-keyed-map shape. Since
`Preview`/`Restore`'s signatures are fixed to a flat `paths []string` but need to span
multiple Targets, added `JoinPath(targetID, relPath) string` / `SplitPath` helpers (NUL-byte
internal separator, documented as "always go through JoinPath, never hand-build the string")
as this task's own answer to carrying the per-root shape through the fixed flat parameter.

**Exclusion rules.** `.gitignore` and out-of-scope-path exclusion both ride on git's own
engine for free — captures use `--git-dir=<shadow>/shadow.git --work-tree=<Root>`, so git's
own ignore engine reads `.gitignore` files from the real `Root`, and `IncludePaths` becomes
the `git add`/`ls-files` pathspec directly (no per-file enumeration needed for either rule —
documented as a deliberate choice: enumerating every ignored file in something like
`node_modules` would defeat the point of a fast per-step capture). Git metadata exclusion is
an explicit `:(exclude).git` / `:(exclude,glob)**/.git` pathspec applied to every `add`, on
top of git's own automatic nested-repo-boundary detection. Oversize-untracked (>2 MiB) is a
Go-side check: `git ls-files --others --exclude-standard` scoped to `IncludePaths`, `os.Lstat`
each result, pathspec-exclude (`:(exclude)<path>`) anything over
`DefaultMaxUntrackedFileSize` (2 MiB, overridable via `WithMaxUntrackedFileSize`) — scoped to
*untracked* files only, confirmed by test that a file already captured once keeps being
captured even after growing past the ceiling. `SkippedPath`/`SkipReason` records only the
git-metadata and oversize-untracked exclusions (both cheap/bounded); gitignored and
out-of-scope paths are not enumerated individually, by design (documented in `types.go`).

**Working-directory isolation.** Every git invocation passes `--git-dir`/`--work-tree` as
explicit absolute-path CLI flags (never relies on inherited env), strips every
`GIT_`-prefixed environment variable from the child process's env before layering back only
what this package explicitly sets (author/committer identity for commits), and always sets an
explicit non-zero `cmd.Dir` (the shadow store's own base directory — never inherited from the
calling process, never inside any real repo's working tree). This is deliberately
belt-and-suspenders: manually verified against real `git` (not just assumed) that with a
poisoned `GIT_DIR`/`GIT_WORK_TREE` environment (simulating a real git hook's child-process
env) and *no* explicit flags, git silently resolves to the wrong (real) repo — confirming the
explicit-flags-plus-env-stripping combination is load-bearing, not redundant caution.
`TestWorkingDirectoryIsolation_RealRepoUntouched` runs capture+restore against a `Target.Root`
that has its own real `.git` and diffs the real repo's `HEAD` and `.git/index` bytes
before/after (both unchanged); `TestWorkingDirectoryIsolation_EnvLeakage` additionally poisons
`GIT_DIR`/`GIT_WORK_TREE` in the test process's own environment before calling `Capture` and
confirms both that the capture still lands in the correct shadow store and that the real
repo's `HEAD` is unaffected.

**Two failure modes.** `ErrShadowStoreUnavailable` wraps only shadow-store
init/open/identity-check failures (base dir uncreatable, `git init --bare` failing, missing
git binary, on-disk `meta.json` target-ID mismatch) — always returned as a real top-level
error from `NewShadowGit`/`Capture`, always logged at Error level. Everything after a
successful `ensureShadowRepo` (missing `Target.Root`, a `git add`/`write-tree`/`commit-tree`
failure for one Target) is recorded on that Target's `RootSnapshot.Err`, logged at Warn level,
and never surfaces as `Capture`'s own returned error — a healthy shadow store's per-target
capture hiccup never blocks the batch or the caller's turn. Covered by
`TestCapture_LoudInfrastructureFailure`(+`_MissingGitBinary`) for the loud path and
`TestCapture_SoftPerTargetFailure_NeverBlocksOtherTargets` (one target missing its Root
alongside one healthy target in the same `Capture` call — healthy target still succeeds, top-
level error stays nil) for the soft path.

**Restore selectivity.** No whole-tree restore method or internal helper exists anywhere in
the package — `Restore`/`Preview` always operate over an explicit `paths []string`, and an
empty `paths` restores nothing (tested). Restore skips writing a path whose on-disk content
already matches the snapshot (verified via a real `os.Stat` mtime-unchanged test), to avoid
the OpenCode-reported whole-tree-restore mtime/editor-reload bug even at the single-path
level. A destructive restore (on-disk content differs from the snapshot) is logged via
`slog` rather than silently applied, matching the doc's "warn rather than silently overwrite"
lightweight-conflict-check guidance — `Restore`'s signature only returns `error`, so warning
happens via the configurable `WithLogger` logger rather than a new return value.

**Shadow history shape / cleanup.** Each capture is an independent, parentless git commit
under its own `refs/snapshots/<unixnano>-<nonce>` ref (not chained to the Target's previous
capture) specifically so `Cleanup` can garbage-collect individual old captures
(`update-ref -d` + `git gc --prune=now`) without rewriting a shared linear history — a
parent-chained design was considered and rejected because a single ref pointing at the newest
commit would keep every ancestor reachable forever, making per-capture cleanup structurally
impossible. `Cleanup(ctx, targetID, CleanupPolicy{MaxAge, MaxSnapshotSets})` and
`Purge(ctx, targetID)` (unconditional full removal) are both mechanism-only, per the task
brief — policy values are caller-supplied.

**Benchmark.** `BenchmarkCapture_RealisticTree` (2000 tracked files + 3000 gitignored
`node_modules`-shaped files, 3 files mutated per iteration, matching the "several capture
pairs per turn, small deltas" real cadence): **~52ms/op**, 637 allocs/op, on Apple M2 Pro.
`BenchmarkCapture_FirstCapture` (cold, no prior shadow-repo state, same tree size): ~978ms/op
— reported as a reference point, not the steady-state number the "non-blocking in practice"
criterion is about, since real per-model-step captures hit the incremental path.
`BenchmarkPreview_SinglePath`: ~4.9ms/op.

**Deviations from the task file's own framing.** The task file's "Depends on"/"What's already
in Nanite" sections reference "task `02`"/"task `03`" by number for the Nanite-side
config/retention-policy and hash-check follow-on work; the actual prompt driving this session
described this as the sole task in the batch with no numbered siblings currently tracked.
Treated as pure framing difference, not a scope change — this task's own brief (the
interface, exclusion rules, isolation, loud-vs-best-effort split, GLOSSARY entry) is
unaffected either way, and the lightweight hash-check the doc assigns to "task 03" was in
scope for `Preview`/`Restore` regardless (see "Restore selectivity" above), so it's
implemented here rather than left as a stub.

**Verification.** `go build ./...`, `go vet ./...`, `go test ./... -race`, and `gofmt -l
snapshot/` all clean in `libs/go-agent-wrapper`. 21 tests in `snapshot/`, all passing
(exclusion rules ×4, both failure modes, Diff, Preview, Restore selectivity/symlink/no-op/
unsafe-path rejection, both working-directory-isolation tests, Cleanup age/count, Purge,
JoinPath/SplitPath, NoOpProvider). Whole-repo test suite (`activity`, `adapters`,
`classifybridge`, `filters`, `plant`, `policy`, `sandbox`, `wrapper`) unaffected.
